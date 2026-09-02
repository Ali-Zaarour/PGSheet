package sqlgen

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"pgsheet/internal/excel"
)

// Mode selects the output form. INSERT is the default because the promise is a
// file a person can review; COPY is roughly ten times faster to load and much
// harder to read.
type Mode string

const (
	ModeInsert Mode = "insert"
	ModeCopy   Mode = "copy"
)

// Options are the output choices from the generate screen.
type Options struct {
	Mode                 Mode
	BatchSize            int
	WrapInTransaction    bool
	IncludeSummaryHeader bool
	SkipBlankRows        bool
	StatementTimeout     string // e.g. "300s"; empty leaves the line commented out
}

// Defaults returns the options the generate screen starts from.
func Defaults() Options {
	return Options{
		Mode:                 ModeInsert,
		BatchSize:            500,
		WrapInTransaction:    true,
		IncludeSummaryHeader: true,
		SkipBlankRows:        true,
	}
}

func (o Options) normalized() Options {
	if o.Mode == "" {
		o.Mode = ModeInsert
	}
	if o.BatchSize < 100 {
		o.BatchSize = 100
	}
	if o.BatchSize > 1000 {
		o.BatchSize = 1000
	}
	return o
}

// Target is where the rows go. Columns is explicit: table column order can
// change between generation and execution.
type Target struct {
	Schema  string
	Table   string
	Columns []string

	// SetvalColumn is the key whose sequence needs resynchronising after
	// explicit values. Empty when the database assigns the key.
	SetvalColumn string
}

// RowCoercer renders one row into SQL literals, in Target.Columns order.
//
// The generator coerces nothing itself; it is handed a function that does.
// That keeps this package independent of the validator while guaranteeing the
// file is written from the same coercion the validator ran. skip drops a row
// without the generator having to know what blank means.
type RowCoercer func(row excel.Row) (literals []string, skip bool, err error)

// Input is one generation run.
type Input struct {
	Workbook  *excel.Workbook
	SheetName string
	DataStart int

	Target  Target
	Coerce  RowCoercer
	Options Options
	Meta    Meta

	Progress func(current, total int)
	Total    int
}

// Result reports what was written.
type Result struct {
	RowsWritten  int    `json:"rowsWritten"`
	RowsSkipped  int    `json:"rowsSkipped"`
	Statements   int    `json:"statements"`
	BytesWritten int64  `json:"bytesWritten"`
	Duration     string `json:"duration"`
}

// Generate streams the workbook to w. Nothing accumulates: rows are read,
// coerced, written and discarded, so a large file costs constant memory.
func Generate(ctx context.Context, w io.Writer, in Input) (Result, error) {
	started := time.Now()
	opts := in.Options.normalized()

	counter := &countingWriter{w: w}
	bw := bufio.NewWriterSize(counter, 64*1024)

	res := Result{}

	if opts.IncludeSummaryHeader {
		if _, err := bw.WriteString(in.Meta.Render()); err != nil {
			return res, err
		}
	}

	if opts.WrapInTransaction {
		if _, err := bw.WriteString("\nBEGIN;\n"); err != nil {
			return res, err
		}
		timeout := "-- SET LOCAL statement_timeout = '300s';\n"
		if opts.StatementTimeout != "" {
			timeout = fmt.Sprintf("SET LOCAL statement_timeout = '%s';\n", sanitizeTimeout(opts.StatementTimeout))
		}
		if _, err := bw.WriteString(timeout); err != nil {
			return res, err
		}
	}

	var err error
	switch opts.Mode {
	case ModeCopy:
		res, err = writeCopy(ctx, bw, in, opts)
	default:
		res, err = writeInserts(ctx, bw, in, opts)
	}
	if err != nil {
		return res, err
	}

	if in.Target.SetvalColumn != "" {
		if _, err := bw.WriteString(setvalStatement(in.Target)); err != nil {
			return res, err
		}
	}

	if opts.WrapInTransaction {
		if _, err := bw.WriteString("\nCOMMIT;\n"); err != nil {
			return res, err
		}
	}

	footer := fmt.Sprintf(
		"\n-- =============================================================\n"+
			"--  End of script: %s rows across %d statement(s)\n"+
			"-- =============================================================\n",
		thousands(res.RowsWritten), res.Statements)
	if _, err := bw.WriteString(footer); err != nil {
		return res, err
	}

	if err := bw.Flush(); err != nil {
		return res, err
	}

	res.BytesWritten = counter.n
	res.Duration = time.Since(started).Round(time.Millisecond).String()
	return res, nil
}

func writeInserts(ctx context.Context, bw *bufio.Writer, in Input, opts Options) (Result, error) {
	res := Result{}

	prefix := fmt.Sprintf("\nINSERT INTO %s\n    (%s)\nVALUES\n",
		QualifiedIdentifier(in.Target.Schema, in.Target.Table),
		strings.Join(quoteAll(in.Target.Columns), ", "))

	var (
		inBatch    int
		batchStart int
		lastRow    int
	)

	closeBatch := func() error {
		if inBatch == 0 {
			return nil
		}
		// The row range comment is what makes a failure locatable: PostgreSQL
		// reports the statement, and the statement says which sheet rows it
		// came from.
		if _, err := fmt.Fprintf(bw, ";   -- rows %d-%d\n", batchStart, lastRow); err != nil {
			return err
		}
		res.Statements++
		inBatch = 0
		return nil
	}

	err := in.Workbook.Scan(ctx, in.SheetName, in.DataStart, func(row excel.Row) error {
		literals, skip, err := in.Coerce(row)
		if err != nil {
			return fmt.Errorf("row %d: %w", row.Number, err)
		}
		if skip {
			res.RowsSkipped++
			return nil
		}

		if inBatch == 0 {
			if _, err := bw.WriteString(prefix); err != nil {
				return err
			}
			batchStart = row.Number
		} else {
			if _, err := bw.WriteString(",\n"); err != nil {
				return err
			}
		}

		if _, err := bw.WriteString("    (" + strings.Join(literals, ", ") + ")"); err != nil {
			return err
		}

		inBatch++
		lastRow = row.Number
		res.RowsWritten++

		if in.Progress != nil && res.RowsWritten%1000 == 0 {
			in.Progress(res.RowsWritten, in.Total)
		}

		if inBatch >= opts.BatchSize {
			return closeBatch()
		}
		return nil
	})
	if err != nil {
		return res, err
	}

	if err := closeBatch(); err != nil {
		return res, err
	}
	if in.Progress != nil {
		in.Progress(res.RowsWritten, in.Total)
	}
	return res, nil
}

// setvalStatement resynchronises the sequence after explicit key values. It is
// the most commonly forgotten step in a manual bulk import, and the one that
// breaks the live application: without it the next ordinary insert collides
// with a row this file added.
//
// pg_get_serial_sequence rather than a hardcoded name, so a renamed sequence
// does not turn this into a silent no-op. COALESCE handles an empty table.
func setvalStatement(t Target) string {
	qualified := t.Schema + "." + t.Table
	return fmt.Sprintf(`
-- Resynchronise the sequence: this file supplied explicit key values, so the
-- database's counter must be moved past them or the next insert will collide.
SELECT setval(
    pg_get_serial_sequence(%s, %s),
    (SELECT COALESCE(MAX(%s), 1) FROM %s),
    true
);
`,
		MustQuoteLiteral(qualified, true),
		MustQuoteLiteral(t.SetvalColumn, true),
		QuoteIdentifier(t.SetvalColumn),
		QualifiedIdentifier(t.Schema, t.Table))
}

func quoteAll(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = QuoteIdentifier(n)
	}
	return out
}

// sanitizeTimeout keeps the value to digits and a unit, so operator input
// cannot become anything else.
func sanitizeTimeout(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= '0' && r <= '9') || r == 's' || r == 'm' || r == 'h' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "300s"
	}
	return b.String()
}

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}
