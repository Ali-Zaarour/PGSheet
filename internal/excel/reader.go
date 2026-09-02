package excel

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"

	"pgsheet/internal/domain"
)

// Limits guard against a hostile workbook: a 2MB zip that expands to 40GB is
// a denial of service, not an import.
const (
	MaxCompressionRatio = 50
	MaxUncompressedSize = 2 << 30 // 2GB
	MaxColumns          = 1024
	headerScanRows      = 20
)

// RandomAccessRowLimit is the sheet size above which the checks that need a
// cell by address are skipped.
//
// Any such call makes excelize unmarshal the whole worksheet into structs:
// about a gigabyte on 150,000 rows, and it stays allocated for the session.
// Merged cells and uncalculated formulas are both worth reporting on a
// hand-built sheet of a few hundred rows, and both are rare on a machine
// export of hundreds of thousands. Paying a gigabyte for them there is the
// wrong trade. Measured in memory_test.go.
const RandomAccessRowLimit = 5000

// Workbook is an opened .xlsx file. It holds no sheet contents: every read
// streams.
type Workbook struct {
	f    *excelize.File
	path string

	date1904 bool

	// rows is the sheet size last measured by CountRows, which decides whether
	// the by-address checks are affordable. Zero until something counts.
	rows int

	// Progress, when set, is called during long reads with the number of rows
	// seen so far. Counting a large sheet takes seconds with nothing else to
	// show for it, and silence reads as a hang.
	Progress func(rows int)
}

// Open validates and opens a workbook.
func Open(path string) (*Workbook, error) {
	if err := checkArchiveSafety(path); err != nil {
		return nil, err
	}

	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, fmt.Errorf("open workbook: %w", err)
	}

	wb := &Workbook{f: f, path: path}
	if props, err := f.GetWorkbookProps(); err == nil && props.Date1904 != nil {
		// Workbooks saved by older Mac Excel count days from 1904, so every
		// date in them is otherwise four years and a day off.
		wb.date1904 = *props.Date1904
	}
	return wb, nil
}

// Close releases the workbook.
func (w *Workbook) Close() error {
	if w.f == nil {
		return nil
	}
	return w.f.Close()
}

// Path is the file the workbook was opened from.
func (w *Workbook) Path() string { return w.path }

// Sheets lists the worksheet names in workbook order.
func (w *Workbook) Sheets() []string { return w.f.GetSheetList() }

// checkArchiveSafety rejects zip bombs before excelize decompresses anything.
func checkArchiveSafety(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("open workbook: %w", err)
	}

	r, err := zip.OpenReader(path)
	if err != nil {
		return fmt.Errorf("%q is not a readable .xlsx file: %w", path, err)
	}
	defer r.Close()

	var uncompressed uint64
	for _, f := range r.File {
		uncompressed += f.UncompressedSize64
		if uncompressed > MaxUncompressedSize {
			return fmt.Errorf("workbook expands to more than %dGB and was refused", MaxUncompressedSize>>30)
		}
	}

	if st.Size() > 0 && uncompressed/uint64(st.Size()) > MaxCompressionRatio {
		return fmt.Errorf("workbook expands %dx when decompressed, which exceeds the safety limit of %dx",
			uncompressed/uint64(st.Size()), MaxCompressionRatio)
	}
	return nil
}

// Row is one spreadsheet row with its Excel row number.
type Row struct {
	Number int // 1-based, exactly as Excel shows it
	Cells  []domain.CellValue
}

// Cell returns the cell at an index, or empty when the row is short. Client
// sheets are ragged.
func (r Row) Cell(i int) domain.CellValue {
	if i < 0 || i >= len(r.Cells) {
		return domain.CellValue{Kind: domain.CellEmpty}
	}
	return r.Cells[i]
}

// Scan streams a sheet from startRow, calling fn for each row. Nothing
// accumulates. An error from fn stops the scan and is returned unchanged.
func (w *Workbook) Scan(ctx context.Context, sheet string, startRow int, fn func(Row) error) error {
	dateCols, err := w.dateColumns(sheet, startRow)
	if err != nil {
		return err
	}

	rows, err := w.f.Rows(sheet)
	if err != nil {
		return fmt.Errorf("read sheet %q: %w", sheet, err)
	}
	defer rows.Close()

	rowNum := 0
	for rows.Next() {
		rowNum++
		if rowNum < startRow {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		// RawCellValue keeps the stored value: no number formatting, so a
		// leading-zero code stays text and a large integer keeps its digits.
		raw, err := rows.Columns(excelize.Options{RawCellValue: true})
		if err != nil {
			return fmt.Errorf("read row %d of %q: %w", rowNum, sheet, err)
		}
		if len(raw) > MaxColumns {
			raw = raw[:MaxColumns]
		}

		cells := make([]domain.CellValue, len(raw))
		for i, v := range raw {
			cells[i] = parseCell(v, dateCols[i], w.date1904)
		}

		if err := fn(Row{Number: rowNum, Cells: cells}); err != nil {
			return err
		}
	}
	return rows.Error()
}

// CountRows returns the last row holding data. It streams rather than trust
// the sheet dimension, which many exporters write incorrectly, and ignores
// trailing blanks so the count matches what will be imported.
func (w *Workbook) CountRows(ctx context.Context, sheet string) (total int, lastNonBlank int, err error) {
	rows, err := w.f.Rows(sheet)
	if err != nil {
		return 0, 0, fmt.Errorf("read sheet %q: %w", sheet, err)
	}
	defer rows.Close()

	for rows.Next() {
		total++
		if total%1000 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, 0, err
			}
			if w.Progress != nil {
				w.Progress(total)
			}
		}
		cols, err := rows.Columns(excelize.Options{RawCellValue: true})
		if err != nil {
			return 0, 0, err
		}
		for _, c := range cols {
			if strings.TrimSpace(c) != "" {
				lastNonBlank = total
				break
			}
		}
	}
	w.rows = total
	return total, lastNonBlank, rows.Error()
}

// RandomAccessAffordable reports whether the by-address checks can be run on
// this sheet without materializing it. See RandomAccessRowLimit.
func (w *Workbook) RandomAccessAffordable() bool {
	return w.rows > 0 && w.rows <= RandomAccessRowLimit
}

// Preview reads up to n rows starting at startRow, for the confirmation grid
// on the file screen.
func (w *Workbook) Preview(ctx context.Context, sheet string, startRow, n int) ([]Row, error) {
	var out []Row
	stop := errors.New("preview complete")

	err := w.Scan(ctx, sheet, startRow, func(r Row) error {
		out = append(out, r)
		if len(out) >= n {
			return stop
		}
		return nil
	})
	if err != nil && !errors.Is(err, stop) {
		return nil, err
	}
	return out, nil
}

// dateColumns decides per column whether its numbers are dates.
//
// Excel stores every date as a serial number, and only the cell's number
// format says 45231 is a date rather than a quantity. The obvious way to read
// that format is GetCellStyle, and it is a trap: any random-access call makes
// excelize unmarshal the whole worksheet into structs, which on a 150,000-row
// sheet costs about a gigabyte. Measured in memory_test.go.
//
// So the format is inferred from the stream instead. Two iterators run side by
// side over a sample of rows, one reading stored values and one reading what
// Excel would display. A cell that stores a number and displays a date is a
// date; everything else is what it looks like. Any date-formatted cell in the
// sample marks the column, since a partly-formatted date column is commoner
// than a stray date format on a numeric one.
func (w *Workbook) dateColumns(sheet string, startRow int) (map[int]bool, error) {
	out := map[int]bool{}

	stored, err := w.f.Rows(sheet)
	if err != nil {
		return nil, fmt.Errorf("read sheet %q: %w", sheet, err)
	}
	defer stored.Close()

	displayed, err := w.f.Rows(sheet)
	if err != nil {
		return nil, fmt.Errorf("read sheet %q: %w", sheet, err)
	}
	defer displayed.Close()

	rowNum, sampled := 0, 0
	for stored.Next() && displayed.Next() && sampled < headerScanRows {
		rowNum++
		if rowNum < startRow {
			continue
		}
		sampled++

		rawCells, err := stored.Columns(excelize.Options{RawCellValue: true})
		if err != nil {
			return nil, err
		}
		shownCells, err := displayed.Columns()
		if err != nil {
			return nil, err
		}

		for i, raw := range rawCells {
			if out[i] || i >= len(shownCells) {
				continue
			}
			if isNumericText(raw) && looksTemporal(shownCells[i]) {
				out[i] = true
			}
		}
	}

	return out, stored.Error()
}

func isNumericText(s string) bool {
	s = strings.TrimSpace(s)
	if !looksNumeric(s) {
		return false
	}
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}

// looksTemporal reports whether displayed text reads as a date or a time.
// Number formats such as "#,##0.00" also change the text, so a difference from
// the stored value is not enough on its own.
var temporalText = regexp.MustCompile(
	`\d{1,4}[-/.]\d{1,2}[-/.]\d{1,4}|\d{1,2}:\d{2}|` +
		`(?i)(jan|feb|mar|apr|may|jun|jul|aug|sep|oct|nov|dec)`)

func looksTemporal(shown string) bool {
	return temporalText.MatchString(shown)
}

// MergedRanges reports merged cells. A merge writes its value into the
// top-left cell only, so a merged column silently reads as mostly blank.
//
// Skipped on a large sheet: reading merges costs the whole worksheet model.
// The second return value says whether the check actually ran, so a caller
// never reports "no merged cells" when it did not look.
func (w *Workbook) MergedRanges(sheet string) ([]string, bool, error) {
	if !w.RandomAccessAffordable() {
		return nil, false, nil
	}

	merges, err := w.f.GetMergeCells(sheet)
	if err != nil {
		return nil, false, err
	}
	var out []string
	for _, m := range merges {
		out = append(out, m.GetStartAxis()+":"+m.GetEndAxis())
	}
	return out, true, nil
}
