package excel

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/xuri/excelize/v2"

	"pgsheet/internal/domain"
)

// Excel's serial numbering contains a day that never existed: 1900-02-29. Every
// date before 1 March 1900 is therefore off by one unless the reader accounts
// for it, which is why the spec asks for exactly these two dates as a
// regression test (spec §7).
func TestExcel1900LeapYearBug(t *testing.T) {
	f := excelize.NewFile()
	defer f.Close()

	const sheet = "Sheet1"
	dateStyle, err := f.NewStyle(&excelize.Style{NumFmt: 14})
	must(t, err)

	// Serials written directly, as a real workbook stores them.
	cases := []struct {
		ref    string
		serial int
		want   string
	}{
		{"A1", 1, "1900-01-01"},   // the first serial Excel defines
		{"A2", 59, "1900-02-28"},  // the last real day before the phantom one
		{"A3", 61, "1900-03-01"},  // the first day after it
		{"A4", 367, "1901-01-01"}, // well clear of the bug
		{"A5", 45366, "2024-03-15"},
	}

	for _, c := range cases {
		must(t, f.SetCellInt(sheet, c.ref, c.serial))
		must(t, f.SetCellStyle(sheet, c.ref, c.ref, dateStyle))
	}

	path := filepath.Join(t.TempDir(), "leap.xlsx")
	must(t, f.SaveAs(path))

	wb, err := Open(path)
	must(t, err)
	defer wb.Close()

	rows, err := wb.Preview(context.Background(), sheet, 1, len(cases))
	must(t, err)

	for i, c := range cases {
		cell := rows[i].Cell(0)
		if cell.Kind != domain.CellDate {
			t.Errorf("serial %d read as kind %v, want a date", c.serial, cell.Kind)
			continue
		}
		if got := cell.Time.Format("2006-01-02"); got != c.want {
			t.Errorf("serial %d = %s, want %s", c.serial, got, c.want)
		}
	}
}

// Serial 60 is 29 February 1900. Excel shows it; the calendar does not have
// it; PostgreSQL cannot store it. Shifting it silently to the 28th or the 1st
// would invent a date, so it is reported instead.
func TestPhantomLeapDayIsReported(t *testing.T) {
	f := excelize.NewFile()
	defer f.Close()

	const sheet = "Sheet1"
	dateStyle, err := f.NewStyle(&excelize.Style{NumFmt: 14})
	must(t, err)
	must(t, f.SetCellInt(sheet, "A1", 60))
	must(t, f.SetCellStyle(sheet, "A1", "A1", dateStyle))

	path := filepath.Join(t.TempDir(), "phantom.xlsx")
	must(t, f.SaveAs(path))

	wb, err := Open(path)
	must(t, err)
	defer wb.Close()

	rows, err := wb.Preview(context.Background(), sheet, 1, 1)
	must(t, err)

	cell := rows[0].Cell(0)
	if cell.Kind != domain.CellError {
		t.Fatalf("serial 60 read as kind %v (%s), want an error the operator can see",
			cell.Kind, cell.Time.Format("2006-01-02"))
	}
}

// A workbook saved by Mac Excel counts days from 1904, so every date in it is
// four years and a day off unless the epoch is honoured.
func TestDate1904Workbooks(t *testing.T) {
	f := excelize.NewFile()
	defer f.Close()

	const sheet = "Sheet1"
	date1904 := true
	must(t, f.SetWorkbookProps(&excelize.WorkbookPropsOptions{Date1904: &date1904}))

	dateStyle, err := f.NewStyle(&excelize.Style{NumFmt: 14})
	must(t, err)
	must(t, f.SetCellInt(sheet, "A1", 0))
	must(t, f.SetCellStyle(sheet, "A1", "A1", dateStyle))

	path := filepath.Join(t.TempDir(), "mac.xlsx")
	must(t, f.SaveAs(path))

	wb, err := Open(path)
	must(t, err)
	defer wb.Close()

	if !wb.date1904 {
		t.Fatal("the 1904 epoch flag was not read from the workbook")
	}

	rows, err := wb.Preview(context.Background(), sheet, 1, 1)
	must(t, err)

	cell := rows[0].Cell(0)
	if cell.Kind != domain.CellDate {
		t.Fatalf("serial 0 read as kind %v, want a date", cell.Kind)
	}
	if got := cell.Time.Format("2006-01-02"); got != "1904-01-01" {
		t.Errorf("serial 0 in a 1904 workbook = %s, want 1904-01-01", got)
	}
}
