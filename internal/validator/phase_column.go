package validator

import (
	"context"
	"fmt"
	"math/rand"
	"unicode/utf8"

	"pgsheet/internal/domain"
	"pgsheet/internal/excel"
	"pgsheet/internal/mapper"
)

// Phase A sample shape, from spec §9: the first rows catch a header offset or
// a units row, the last rows catch a totals row or a change of format halfway
// down a client's export, and the random middle catches everything else.
const (
	sampleHead   = 200
	sampleTail   = 200
	sampleRandom = 600
	sampleTotal  = sampleHead + sampleTail + sampleRandom

	// Formula cells are checked by random access, which is affordable over a
	// few rows and not over a whole file. One example is enough to say the
	// workbook needs recalculating.
	formulaScanRows = 50
)

// columnStats accumulates what Phase A learns about one mapped column.
type columnStats struct {
	col       mappedColumn
	sampled   int
	blank     int
	failures  int
	truncated int
	firstErr  *CellError
	firstRow  int
	firstVal  string
}

// runPhaseA judges the mapping before judging the data.
//
// The distinction is the whole point: if a column mapped to a date column
// contains text in most of its cells, the answer is not to fix nine hundred
// rows, it is that the wrong column was mapped. Saying that once, and
// suppressing the nine hundred row-level errors that follow from it, is what
// makes the report usable (spec §9).
func runPhaseA(
	ctx context.Context,
	in Input,
	cols []mappedColumn,
	b *builder,
) ([]ColumnVerdict, error) {
	// Structural problems first: they need no data at all, and a mapping that
	// is structurally wrong makes every sampled value meaningless.
	status := mapper.Check(in.Mappings, in.Plan.Schema, in.Sheet.Headers, in.Plan.Strategy)
	for _, p := range status.Problems {
		sev := SevError
		if p.Severity == "warning" {
			sev = SevWarning
		}
		b.add(Issue{
			Code:        p.Code,
			Severity:    sev,
			Scope:       ScopeColumn,
			ExcelColumn: p.ExcelColumn,
			DBColumn:    p.DBColumn,
			Message:     p.Message,
			Hint:        p.Hint,
		})
	}
	if status.Blocking {
		return nil, nil
	}

	stats := make([]columnStats, len(cols))
	for i, c := range cols {
		stats[i] = columnStats{col: c}
	}

	wanted := sampleRows(in.Sheet.DataStart, in.Sheet.DataStart+in.Sheet.TotalRows-1)

	// Phase A's time goes on the row scan, not on judging the columns
	// afterwards, so that is what progress reports.
	scanned := 0
	err := in.Workbook.Scan(ctx, in.Sheet.Name, in.Sheet.DataStart, func(row excel.Row) error {
		scanned++
		if scanned%1000 == 0 {
			report(in.Progress, "checking columns", scanned, in.Sheet.TotalRows)
		}
		if wanted != nil && !wanted[row.Number] {
			return nil
		}
		for i := range stats {
			sampleCell(&stats[i], row, in.Opts)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// A workbook saved without recalculating has formula cells whose cached
	// result is missing; those read as empty and would import as NULL.
	if gaps, err := in.Workbook.ScanFormulaGaps(ctx, in.Sheet.Name, in.Sheet.DataStart, formulaScanRows, len(in.Sheet.Headers)); err == nil && len(gaps) > 0 {
		first := gaps[0]
		column := ""
		if first.Column < len(in.Sheet.Headers) {
			column = in.Sheet.Headers[first.Column]
		}
		b.add(Issue{
			Code:        "W204",
			Severity:    SevWarning,
			Scope:       ScopeColumn,
			ExcelColumn: column,
			ExcelRef:    first.Ref,
			Message: fmt.Sprintf("%d formula cell(s) in the first %d rows have no calculated value, starting at %s",
				len(gaps), formulaScanRows, first.Ref),
			Hint: "open the workbook, let Excel recalculate, and save it again. These cells would import as empty",
		})
	}

	threshold := in.Settings.ColumnMisalignThreshold
	if threshold <= 0 {
		threshold = 0.30
	}

	verdicts := make([]ColumnVerdict, 0, len(stats))
	for _, s := range stats {
		v := ColumnVerdict{
			ExcelColumn: s.col.ExcelColumn,
			DBColumn:    s.col.Column.Name,
			Sampled:     s.sampled,
			Failures:    s.failures,
		}
		if s.sampled > 0 {
			v.FailureRate = float64(s.failures) / float64(s.sampled)
		}

		switch {
		// E107 — a column that is entirely blank cannot fill a NOT NULL target.
		case s.sampled > 0 && s.blank == s.sampled && s.col.Column.Required():
			v.Blocked = true
			v.Reason = "every sampled cell is empty and the target column is required"
			b.add(Issue{
				Code:        "E107",
				Severity:    SevError,
				Scope:       ScopeColumn,
				ExcelColumn: s.col.ExcelColumn,
				DBColumn:    s.col.Column.Name,
				Message: fmt.Sprintf("%q is empty in every sampled row, but %s is required",
					s.col.ExcelColumn, s.col.Column.Name),
				Hint: "map a different sheet column, or set a fixed value for blanks",
			})

		// E104 — the column does not hold what the target type needs. This is
		// the check that produces one honest line instead of a flood.
		case v.FailureRate > threshold:
			v.Blocked = true
			v.Reason = fmt.Sprintf("%.0f%% of sampled values cannot be written to %s",
				v.FailureRate*100, s.col.Column.FormattedType)
			b.suppress(s.col.ExcelColumn)

			msg := fmt.Sprintf("%q does not match %s: %d of %d sampled values failed",
				s.col.ExcelColumn, s.col.Column.FormattedType, s.failures, s.sampled)
			hint := "this usually means the wrong sheet column is mapped here"
			if s.firstErr != nil {
				hint = fmt.Sprintf("row %d has %q: %s", s.firstRow, truncate(s.firstVal), s.firstErr.Message)
			}
			b.add(Issue{
				Code:        "E104",
				Severity:    SevError,
				Scope:       ScopeColumn,
				ExcelColumn: s.col.ExcelColumn,
				DBColumn:    s.col.Column.Name,
				Message:     msg,
				Hint:        hint,
			})
		}

		// W201 — values that the database would silently cut short.
		if s.truncated > 0 && s.col.Column.MaxLength != nil {
			b.add(Issue{
				Code:        "W201",
				Severity:    SevWarning,
				Scope:       ScopeColumn,
				ExcelColumn: s.col.ExcelColumn,
				DBColumn:    s.col.Column.Name,
				Message: fmt.Sprintf("%d sampled values are longer than the %d characters %s allows",
					s.truncated, *s.col.Column.MaxLength, s.col.Column.Name),
				Hint: "these rows will fail; shorten the values or widen the column",
			})
		}

		// W202 — a mostly empty column is usually a mapping mistake, but it is
		// legitimate often enough that it cannot block.
		if s.sampled > 0 && !s.col.Column.Required() {
			if rate := float64(s.blank) / float64(s.sampled); rate > 0.5 && s.blank != s.sampled {
				b.add(Issue{
					Code:        "W202",
					Severity:    SevWarning,
					Scope:       ScopeColumn,
					ExcelColumn: s.col.ExcelColumn,
					DBColumn:    s.col.Column.Name,
					Message: fmt.Sprintf("%.0f%% of sampled values in %q are empty",
						rate*100, s.col.ExcelColumn),
					Hint: "confirm this column is the one you meant to map",
				})
			}
		}

		verdicts = append(verdicts, v)
	}

	return verdicts, nil
}

func sampleCell(s *columnStats, row excel.Row, opts Options) {
	cell := row.Cell(s.col.ExcelIndex)
	s.sampled++

	if cell.Kind == domain.CellEmpty {
		s.blank++
		return
	}

	applied := mapper.Apply(cell, s.col.Transform, s.col.Family)
	if applied.IsNull {
		s.blank++
		return
	}

	if s.col.Column.MaxLength != nil && *s.col.Column.MaxLength > 0 {
		if utf8.RuneCountInString(text(applied.Value)) > *s.col.Column.MaxLength {
			s.truncated++
		}
	}

	if applied.DateParseFailed {
		s.failures++
		return
	}

	if _, err := Coerce(applied.Value, s.col.Column, opts); err != nil {
		s.failures++
		if s.firstErr == nil {
			s.firstErr = err
			s.firstRow = row.Number
			s.firstVal = text(cell)
		}
	}
}

// sampleRows picks which rows Phase A looks at.
//
// Returning nil means "every row": for a file small enough that sampling saves
// nothing, sampling would only make the verdict less reliable.
func sampleRows(first, last int) map[int]bool {
	total := last - first + 1
	if total <= 0 || total <= sampleTotal {
		return nil
	}

	want := make(map[int]bool, sampleTotal)
	for i := 0; i < sampleHead; i++ {
		want[first+i] = true
	}
	for i := 0; i < sampleTail; i++ {
		want[last-i] = true
	}

	// Deterministic: the same file sampled twice gives the same verdict, so an
	// operator who reruns validation after changing nothing sees no change.
	rng := rand.New(rand.NewSource(int64(total)))
	middleFirst := first + sampleHead
	middleLast := last - sampleTail
	span := middleLast - middleFirst + 1
	if span > 0 {
		for i := 0; i < sampleRandom; i++ {
			want[middleFirst+rng.Intn(span)] = true
		}
	}
	return want
}
