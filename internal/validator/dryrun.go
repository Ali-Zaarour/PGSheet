package validator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"pgsheet/internal/excel"
	"pgsheet/internal/mapper"
	"pgsheet/internal/sqlgen"
)

// The live verification is the definitive check: it runs the real statements
// against the real table, inside a transaction that is always rolled back.
//
// The one invariant this file has: there is no Commit. The deferred Rollback
// is registered immediately after Begin, before any statement executes, and no
// code path reaches a commit. A test asserts that by scanning this file
// (spec §14).

const (
	dryRunBatchSize = 200
	dryRunTimeout   = "120s"
)

// DryRunInput is one live verification.
type DryRunInput struct {
	Pool      *pgxpool.Pool
	Workbook  *excel.Workbook
	SheetName string
	DataStart int
	Plan      mapper.Plan
	Opts      Options
	MaxIssues int
	SkipBlank bool
	Progress  ProgressFunc
}

// DryRun executes the import against the database and rolls it back.
//
// Two things the operator must know, and which the UI states before this runs:
// triggers on the table do fire during the dry run, and any sequence value
// consumed inside the transaction is not given back by the rollback.
func DryRun(ctx context.Context, in DryRunInput) (Report, error) {
	started := time.Now()
	b := newBuilder(in.MaxIssues)

	if in.Pool == nil {
		return Report{}, errors.New("live verification needs a database connection")
	}

	tx, err := in.Pool.Begin(ctx)
	if err != nil {
		return Report{}, fmt.Errorf("start verification transaction: %w", err)
	}
	// Registered before any statement runs, so every return path below —
	// including a panic — leaves the database untouched.
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, "SET LOCAL statement_timeout = '"+dryRunTimeout+"'"); err != nil {
		return Report{}, fmt.Errorf("configure verification transaction: %w", err)
	}

	columns := PlanColumnNames(in.Plan)
	if len(columns) == 0 {
		return Report{}, errors.New("nothing is mapped, so there is nothing to verify")
	}

	coerce := RowLiterals(in.Plan, in.Opts, in.SkipBlank)
	prefix := fmt.Sprintf("INSERT INTO %s (%s) VALUES\n",
		sqlgen.QualifiedIdentifier(in.Plan.Schema.Schema, in.Plan.Schema.Table),
		strings.Join(quoteAll(columns), ", "))

	var (
		batch     []string
		batchRows []int
		rowsTotal int
		rowsOK    int
		skipped   int
	)

	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		defer func() { batch, batchRows = batch[:0], batchRows[:0] }()

		// A savepoint per batch is what lets verification continue past the
		// first failure. Without it the transaction aborts and every later
		// statement fails with "current transaction is aborted", which would
		// report one real problem and hide the rest (spec §9).
		if _, err := tx.Exec(ctx, "SAVEPOINT pgsheet_batch"); err != nil {
			return err
		}

		sql := prefix + strings.Join(batch, ",\n")
		if _, err := tx.Exec(ctx, sql); err != nil {
			if _, rerr := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT pgsheet_batch"); rerr != nil {
				return rerr
			}
			return isolateFailure(ctx, tx, prefix, batch, batchRows, in.Plan, b)
		}

		if _, err := tx.Exec(ctx, "RELEASE SAVEPOINT pgsheet_batch"); err != nil {
			return err
		}
		rowsOK += len(batch)
		return nil
	}

	scanErr := in.Workbook.Scan(ctx, in.SheetName, in.DataStart, func(row excel.Row) error {
		literals, skip, err := coerce(row)
		if err != nil {
			return err
		}
		if skip {
			skipped++
			return nil
		}
		rowsTotal++

		batch = append(batch, "    ("+strings.Join(literals, ", ")+")")
		batchRows = append(batchRows, row.Number)

		if len(batch) >= dryRunBatchSize {
			if err := flush(); err != nil {
				return err
			}
			report(in.Progress, "verifying", rowsTotal, 0)
		}
		if b.full() {
			return errCeiling
		}
		return nil
	})
	if scanErr != nil && !errors.Is(scanErr, errCeiling) {
		return Report{}, scanErr
	}
	if scanErr == nil {
		if err := flush(); err != nil {
			return Report{}, err
		}
	}

	report(in.Progress, "verifying", rowsTotal, rowsTotal)

	rep := b.finish(rowsTotal, rowsOK, skipped, started)
	return rep, nil
}

// isolateFailure re-runs a failed batch one row at a time.
//
// A multi-row INSERT reports the first violation and nothing else, so the
// batch is replayed row by row, each in its own savepoint, to name every row
// that actually fails. It costs one round trip per row but only for batches
// that already failed.
func isolateFailure(
	ctx context.Context,
	tx pgx.Tx,
	prefix string,
	batch []string,
	rows []int,
	plan mapper.Plan,
	b *builder,
) error {
	for i, values := range batch {
		rowNum := 0
		if i < len(rows) {
			rowNum = rows[i]
		}

		if _, err := tx.Exec(ctx, "SAVEPOINT pgsheet_row"); err != nil {
			return err
		}

		_, err := tx.Exec(ctx, prefix+values)
		if err == nil {
			if _, err := tx.Exec(ctx, "RELEASE SAVEPOINT pgsheet_row"); err != nil {
				return err
			}
			continue
		}

		if _, rerr := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT pgsheet_row"); rerr != nil {
			return rerr
		}

		b.add(pgErrorToIssue(err, rowNum, plan))
		if b.full() {
			return nil
		}
	}
	return nil
}

// pgErrorToIssue turns a PostgreSQL error into a located issue.
//
// pgconn.PgError carries the constraint name, the column name and a Detail
// that usually contains the offending key values — far more than the generic
// message, and exactly what the operator needs.
func pgErrorToIssue(err error, row int, plan mapper.Plan) Issue {
	issue := Issue{
		Severity: SevError,
		Scope:    ScopeCell,
		ExcelRow: row,
		Code:     "E306",
		Message:  err.Error(),
	}

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return issue
	}

	issue.Message = pgErr.Message
	issue.Hint = strings.TrimSpace(pgErr.Detail + " " + pgErr.Hint)
	issue.DBColumn = pgErr.ColumnName

	switch pgErr.Code {
	case "23505": // unique_violation
		issue.Code = "E304"
	case "23503": // foreign_key_violation
		issue.Code = "E305"
	case "23502": // not_null_violation
		issue.Code = "E202"
	case "23514": // check_violation
		issue.Code = "E306"
	case "22001": // string_data_right_truncation
		issue.Code = "E203"
	case "22003": // numeric_value_out_of_range
		issue.Code = "E205"
	case "22P02": // invalid_text_representation
		issue.Code = "E201"
	}

	if issue.DBColumn == "" && pgErr.ConstraintName != "" {
		issue.DBColumn = constraintColumns(plan, pgErr.ConstraintName)
	}

	// Map the table column back to the sheet column the operator recognises.
	if issue.DBColumn != "" {
		for _, pc := range plan.Columns {
			if pc.Column.Name == issue.DBColumn {
				issue.ExcelColumn = pc.Mapping.ExcelColumn
				issue.ExcelRef = ExcelRef(pc.ExcelIndex, row)
				break
			}
		}
	}

	return issue
}

func constraintColumns(plan mapper.Plan, name string) string {
	for _, c := range plan.Schema.Constraints {
		if c.Name == name {
			return strings.Join(c.Columns, ", ")
		}
	}
	return ""
}

func quoteAll(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = sqlgen.QuoteIdentifier(n)
	}
	return out
}
