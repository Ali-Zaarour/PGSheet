package excel

import (
	"context"
	"fmt"

	"github.com/xuri/excelize/v2"
)

// A formula cell carries a cached result written by whatever last saved the
// workbook. Reading that cache is correct and cheap — but a file saved by a
// tool that does not compute formulas has cells with a formula and no cached
// value, and those read as empty. Silently importing them as NULL is the
// failure this looks for (spec §7).
//
// The scan is bounded: reading a style or a formula is a random-access call,
// which is affordable over a sample and not over a hundred thousand rows. One
// example is enough to tell the operator the workbook needs recalculating.

// FormulaGap is a cell whose formula has no cached result.
type FormulaGap struct {
	Ref     string `json:"ref"`     // A1 notation
	Row     int    `json:"row"`     // as shown in Excel
	Column  int    `json:"column"`  // zero-based
	Formula string `json:"formula"` // truncated for display
}

// ScanFormulaGaps checks the first rows of the data range for formula cells
// with no cached value.
func (w *Workbook) ScanFormulaGaps(ctx context.Context, sheet string, startRow, maxRows, columns int) ([]FormulaGap, error) {
	if columns <= 0 || maxRows <= 0 {
		return nil, nil
	}
	// Reading a cell by address materializes the worksheet. Not worth a
	// gigabyte on a large export; see RandomAccessRowLimit.
	if !w.RandomAccessAffordable() {
		return nil, nil
	}
	if columns > MaxColumns {
		columns = MaxColumns
	}

	var gaps []FormulaGap

	for r := 0; r < maxRows; r++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		row := startRow + r

		for c := 1; c <= columns; c++ {
			ref, err := excelize.CoordinatesToCellName(c, row)
			if err != nil {
				break
			}

			value, err := w.f.GetCellValue(sheet, ref, excelize.Options{RawCellValue: true})
			if err != nil || value != "" {
				continue
			}

			formula, err := w.f.GetCellFormula(sheet, ref)
			if err != nil || formula == "" {
				continue
			}

			gaps = append(gaps, FormulaGap{
				Ref:     ref,
				Row:     row,
				Column:  c - 1,
				Formula: truncateFormula(formula),
			})
		}
	}

	return gaps, nil
}

func truncateFormula(s string) string {
	const limit = 60
	r := []rune(s)
	if len(r) <= limit {
		return "=" + s
	}
	return fmt.Sprintf("=%s…", string(r[:limit]))
}
