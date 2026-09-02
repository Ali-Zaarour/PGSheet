package validator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"pgsheet/internal/excel"
	"pgsheet/internal/mapper"
	"pgsheet/internal/sqlgen"
)

// Direct execution is the one operation in PGSheet that writes to the
// database. It is the dry run with a single difference — it commits — so it
// lives beside it rather than duplicating the batching, and the Commit is here
// rather than in dryrun.go, where a test forbids it (spec §14, §20).
//
// Everything about the way this is reached is deliberately awkward: it is off
// by default, the caller has to prove the operator typed the table name, and
// the tool still recommends generating a file and running that instead. The
// product's main safety property is that it writes files, not data.

// ExecuteInput is one direct execution.
type ExecuteInput struct {
	Pool      *pgxpool.Pool
	Workbook  *excel.Workbook
	SheetName string
	DataStart int
	Plan      mapper.Plan
	Opts      Options
	SkipBlank bool
	BatchSize int

	// SetvalColumn resynchronises the sequence after explicit key values, in
	// the same transaction as the inserts.
	SetvalColumn string

	Progress ProgressFunc
}

// ExecResult reports what was written.
type ExecResult struct {
	RowsInserted int    `json:"rowsInserted"`
	RowsSkipped  int    `json:"rowsSkipped"`
	Statements   int    `json:"statements"`
	Duration     string `json:"duration"`
	Committed    bool   `json:"committed"`
}

// Execute inserts the rows and commits.
//
// The transaction is all-or-nothing: any failure rolls the whole thing back,
// so the table is never left half-imported. That is the same promise the
// generated file makes with its BEGIN/COMMIT wrapper.
func Execute(ctx context.Context, in ExecuteInput) (ExecResult, Report, error) {
	started := time.Now()
	b := newBuilder(1000)
	res := ExecResult{}

	if in.Pool == nil {
		return res, Report{}, errors.New("direct execution needs a database connection")
	}

	columns := PlanColumnNames(in.Plan)
	if len(columns) == 0 {
		return res, Report{}, errors.New("nothing is mapped, so there is nothing to insert")
	}

	batchSize := in.BatchSize
	if batchSize < 1 || batchSize > 1000 {
		batchSize = 500
	}

	tx, err := in.Pool.Begin(ctx)
	if err != nil {
		return res, Report{}, fmt.Errorf("start transaction: %w", err)
	}
	// Registered before any statement: every path that does not reach the
	// explicit Commit below leaves the table untouched.
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	coerce := RowLiterals(in.Plan, in.Opts, in.SkipBlank)
	prefix := fmt.Sprintf("INSERT INTO %s (%s) VALUES\n",
		sqlgen.QualifiedIdentifier(in.Plan.Schema.Schema, in.Plan.Schema.Table),
		strings.Join(quoteAll(columns), ", "))

	var (
		batch     []string
		batchRows []int
		failure   error
	)

	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		defer func() { batch, batchRows = batch[:0], batchRows[:0] }()

		if _, err := tx.Exec(ctx, prefix+strings.Join(batch, ",\n")); err != nil {
			// Report the failure against the first row of the batch that
			// failed, then abort: a partial import is exactly what this tool
			// exists to prevent, so there is nothing to gain by continuing.
			row := 0
			if len(batchRows) > 0 {
				row = batchRows[0]
			}
			b.add(pgErrorToIssue(err, row, in.Plan))
			return err
		}

		res.Statements++
		res.RowsInserted += len(batch)
		return nil
	}

	scanErr := in.Workbook.Scan(ctx, in.SheetName, in.DataStart, func(row excel.Row) error {
		literals, skip, err := coerce(row)
		if err != nil {
			return err
		}
		if skip {
			res.RowsSkipped++
			return nil
		}

		batch = append(batch, "    ("+strings.Join(literals, ", ")+")")
		batchRows = append(batchRows, row.Number)

		if len(batch) >= batchSize {
			if err := flush(); err != nil {
				failure = err
				return err
			}
			report(in.Progress, "inserting", res.RowsInserted, 0)
		}
		return nil
	})

	if scanErr != nil {
		if failure == nil {
			failure = scanErr
		}
		return res, b.finish(res.RowsInserted, res.RowsInserted, res.RowsSkipped, started), failure
	}

	if err := flush(); err != nil {
		return res, b.finish(res.RowsInserted, res.RowsInserted, res.RowsSkipped, started), err
	}

	if in.SetvalColumn != "" {
		// Without this the application that owns the database fails on its
		// next insert, which is the single most common consequence of a manual
		// bulk import (spec §10).
		if _, err := tx.Exec(ctx, setvalSQL(in.Plan.Schema.Schema, in.Plan.Schema.Table, in.SetvalColumn)); err != nil {
			return res, b.finish(res.RowsInserted, res.RowsInserted, res.RowsSkipped, started),
				fmt.Errorf("resynchronise the sequence: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return res, b.finish(res.RowsInserted, res.RowsInserted, res.RowsSkipped, started),
			fmt.Errorf("commit: %w", err)
	}
	committed = true

	res.Committed = true
	res.Duration = time.Since(started).Round(time.Millisecond).String()
	report(in.Progress, "inserting", res.RowsInserted, res.RowsInserted)

	return res, b.finish(res.RowsInserted, res.RowsInserted, res.RowsSkipped, started), nil
}

func setvalSQL(schema, table, column string) string {
	return fmt.Sprintf(
		`SELECT setval(pg_get_serial_sequence(%s, %s), (SELECT COALESCE(MAX(%s), 1) FROM %s), true)`,
		sqlgen.MustQuoteLiteral(schema+"."+table, true),
		sqlgen.MustQuoteLiteral(column, true),
		sqlgen.QuoteIdentifier(column),
		sqlgen.QualifiedIdentifier(schema, table),
	)
}
