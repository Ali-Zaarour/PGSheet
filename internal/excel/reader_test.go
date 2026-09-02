package excel

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"

	"pgsheet/internal/domain"
)

// hostileWorkbook builds a file with the quirks from spec §7 that silently
// corrupt data when a reader trusts Excel's display instead of its storage.
func hostileWorkbook(t *testing.T) string {
	t.Helper()

	f := excelize.NewFile()
	defer f.Close()

	const sheet = "Sheet1"

	// A title row above the headers, which is how client exports usually
	// arrive, so row 1 is not the header row.
	must(t, f.SetCellStr(sheet, "A1", "Customer export — March 2026"))

	headers := []string{"Client Name", "Code", "Signup Date", "Big Number", "Active", "Notes"}
	for i, h := range headers {
		cell, err := excelize.CoordinatesToCellName(i+1, 2)
		must(t, err)
		must(t, f.SetCellStr(sheet, cell, h))
	}

	dateStyle, err := f.NewStyle(&excelize.Style{NumFmt: 14}) // m/d/yy
	must(t, err)

	// Row 3: the ordinary case, with a real date and a leading-zero code.
	must(t, f.SetCellStr(sheet, "A3", "Acme SARL"))
	must(t, f.SetCellStr(sheet, "B3", "00123"))
	must(t, f.SetCellValue(sheet, "C3", time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC)))
	must(t, f.SetCellStyle(sheet, "C3", "C3", dateStyle))
	must(t, f.SetCellValue(sheet, "D3", "1234567890123456"))
	must(t, f.SetCellStr(sheet, "E3", "Oui"))
	must(t, f.SetCellStr(sheet, "F3", "A value​"))

	// Row 4: an Excel error cell and a blank.
	must(t, f.SetCellStr(sheet, "A4", "Beta Ltd"))
	must(t, f.SetCellStr(sheet, "B4", "00456"))
	must(t, f.SetCellStr(sheet, "C4", "#N/A"))
	must(t, f.SetCellValue(sheet, "D4", 42))
	must(t, f.SetCellStr(sheet, "E4", "FALSE"))

	// Rows 5 and 6: trailing blanks, which real files carry by the thousand.
	must(t, f.SetCellStr(sheet, "A5", ""))
	must(t, f.SetCellStr(sheet, "A6", ""))

	path := filepath.Join(t.TempDir(), "hostile.xlsx")
	must(t, f.SaveAs(path))
	return path
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestReadsRawValuesNotFormattedText(t *testing.T) {
	wb, err := Open(hostileWorkbook(t))
	must(t, err)
	defer wb.Close()

	rows, err := wb.Preview(context.Background(), "Sheet1", 3, 2)
	must(t, err)
	if len(rows) < 2 {
		t.Fatalf("read %d rows, want 2", len(rows))
	}

	first := rows[0]

	// "00123" is a code, not the number 123. Reading the formatted value would
	// lose the leading zeros for good.
	if code := first.Cell(1); code.Str != "00123" {
		t.Errorf("code = %q (kind %v), want 00123 preserved as text", code.Str, code.Kind)
	}

	// A date-formatted serial must come back as a date, not as 45366.
	signup := first.Cell(2)
	if signup.Kind != domain.CellDate {
		t.Errorf("signup date kind = %v, want a date; raw was %q", signup.Kind, signup.RawText)
	} else if signup.Time.Format("2006-01-02") != "2024-03-15" {
		t.Errorf("signup date = %s, want 2024-03-15", signup.Time.Format("2006-01-02"))
	}

	// A sixteen-digit integer must keep every digit: the float64 route would
	// render it as 1.23456789012346E+15.
	if big := first.Cell(3); big.Num.String() != "1234567890123456" {
		t.Errorf("big number = %s, want every digit intact", big.Num.String())
	}

	// Invisible characters are normalized on read, or exact matching silently
	// fails later on values that look identical.
	if notes := first.Cell(5); notes.Str != "A value" {
		t.Errorf("notes = %q, want the non-breaking space and zero-width space normalized away", notes.Str)
	}
}

func TestErrorCellsAreNotData(t *testing.T) {
	wb, err := Open(hostileWorkbook(t))
	must(t, err)
	defer wb.Close()

	rows, err := wb.Preview(context.Background(), "Sheet1", 4, 1)
	must(t, err)

	if got := rows[0].Cell(2); got.Kind != domain.CellError {
		t.Errorf("#N/A read as kind %v, want CellError", got.Kind)
	}
}

func TestHeaderDetectionSkipsATitleRow(t *testing.T) {
	wb, err := Open(hostileWorkbook(t))
	must(t, err)
	defer wb.Close()

	guess, err := wb.DetectHeaderRow(context.Background(), "Sheet1")
	must(t, err)

	if guess.Row != 2 {
		t.Errorf("header row = %d, want 2 (row 1 is a title)", guess.Row)
	}
}

func TestDescribeBuildsSheetInfo(t *testing.T) {
	wb, err := Open(hostileWorkbook(t))
	must(t, err)
	defer wb.Close()

	info, err := wb.Describe(context.Background(), "Sheet1", 2)
	must(t, err)

	if len(info.Headers) != 6 {
		t.Errorf("headers = %v, want 6", info.Headers)
	}
	if info.DataStart != 3 {
		t.Errorf("data start = %d, want 3", info.DataStart)
	}
	// Trailing blank rows are not data: the count the operator sees must match
	// what will actually be imported.
	if info.TotalRows != 2 {
		t.Errorf("total rows = %d, want 2 (the trailing blanks do not count)", info.TotalRows)
	}
	if info.Fingerprint == "" {
		t.Error("fingerprint is empty")
	}
}

func TestFingerprintIgnoresCosmeticDifferences(t *testing.T) {
	a := Fingerprint([]string{"Client Name", "Email"})
	b := Fingerprint([]string{"  client   name ", "EMAIL"})
	if a != b {
		t.Error("case and whitespace should not change the fingerprint")
	}

	if Fingerprint([]string{"Client Name", "Email"}) == Fingerprint([]string{"Email", "Client Name"}) {
		t.Error("reordered headers must produce a different fingerprint — that is the whole point")
	}
}

func TestCompareHeadersNamesWhatChanged(t *testing.T) {
	saved := []string{"Client Name", "Email", "City"}
	current := []string{"Client Name", "City", "Phone"}

	diff := CompareHeaders(saved, current)

	if diff.Match {
		t.Fatal("these header sets differ")
	}
	if len(diff.Added) != 1 || diff.Added[0] != "Phone" {
		t.Errorf("added = %v, want [Phone]", diff.Added)
	}
	if len(diff.Removed) != 1 || diff.Removed[0] != "Email" {
		t.Errorf("removed = %v, want [Email]", diff.Removed)
	}
	if len(diff.Reordered) == 0 {
		t.Error("City moved from index 2 to index 1 and should be reported as reordered")
	}
}

func TestHeaderNamesFillBlanksAndDeduplicate(t *testing.T) {
	cells := []domain.CellValue{
		{Kind: domain.CellString, Str: "Amount"},
		{Kind: domain.CellEmpty},
		{Kind: domain.CellString, Str: "Amount"},
	}

	got := HeaderNames(cells)

	if got[0] != "Amount" {
		t.Errorf("got %q, want Amount", got[0])
	}
	if got[1] != "(column B)" {
		t.Errorf("blank header = %q, want a positional placeholder", got[1])
	}
	if got[2] == "Amount" {
		t.Error("a duplicate header must be distinguishable, or every map keyed by name collapses it")
	}
}

func TestScoreHeaderCandidateRejectsATitleRow(t *testing.T) {
	title := headerCandidate{
		RowIndex: 1,
		Cells:    []domain.CellValue{{Kind: domain.CellString, Str: "Customer export"}, {}, {}, {}},
		Next: []domain.CellValue{
			{Kind: domain.CellString, Str: "Name"},
			{Kind: domain.CellString, Str: "Code"},
			{Kind: domain.CellString, Str: "Date"},
			{Kind: domain.CellString, Str: "Amount"},
		},
	}
	header := headerCandidate{
		RowIndex: 2,
		Cells: []domain.CellValue{
			{Kind: domain.CellString, Str: "Name"},
			{Kind: domain.CellString, Str: "Code"},
			{Kind: domain.CellString, Str: "Date"},
			{Kind: domain.CellString, Str: "Amount"},
		},
		Next: []domain.CellValue{
			{Kind: domain.CellString, Str: "Acme"},
			{Kind: domain.CellString, Str: "00123"},
			{Kind: domain.CellDate},
			{Kind: domain.CellNumber},
		},
	}

	if scoreHeaderCandidate(header) <= scoreHeaderCandidate(title) {
		t.Errorf("the header row scored %.3f and the title row %.3f",
			scoreHeaderCandidate(header), scoreHeaderCandidate(title))
	}
}

func TestZipBombIsRefused(t *testing.T) {
	// A file that is not a zip at all must be refused with a message about the
	// file, not a panic from deep inside the reader.
	path := filepath.Join(t.TempDir(), "not-a-workbook.xlsx")
	must(t, writeFile(path, []byte("this is not a zip archive")))

	if _, err := Open(path); err == nil {
		t.Error("a non-xlsx file was accepted")
	}
}

func writeFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o600)
}
