package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"pgsheet/internal/config"
	"pgsheet/internal/domain"
	"pgsheet/internal/introspect"
)

// writeWorkbook saves a one-sheet workbook with a header row and text rows.
func writeWorkbook(t *testing.T, path string, headers []string, rows [][]string) {
	t.Helper()

	f := excelize.NewFile()
	defer f.Close()
	for c, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(c+1, 1)
		if err := f.SetCellStr("Sheet1", cell, h); err != nil {
			t.Fatal(err)
		}
	}
	for r, row := range rows {
		for c, v := range row {
			cell, _ := excelize.CoordinatesToCellName(c+1, r+2)
			if err := f.SetCellStr("Sheet1", cell, v); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := f.SaveAs(path); err != nil {
		t.Fatal(err)
	}
}

// peopleTable is three text columns: nothing about their types can catch a
// value landing in the wrong one.
func peopleTable() *introspect.Result {
	text := func(name string) domain.Column {
		return domain.Column{Name: name, DataType: "text", FormattedType: "text", Nullable: true}
	}
	return &introspect.Result{Schema: domain.TableSchema{
		Schema:  "public",
		Table:   "people",
		Columns: []domain.Column{text("first_name"), text("last_name"), text("email")},
	}}
}

// saveConfigFrom maps the original layout by auto-match and exports it, the
// way an operator builds the configuration they will reuse.
func saveConfigFrom(t *testing.T, dir string) string {
	t.Helper()

	original := filepath.Join(dir, "original.xlsx")
	writeWorkbook(t, original,
		[]string{"First Name", "Last Name", "Email"},
		[][]string{{"Grace", "Hopper", "grace@navy.example"}})

	a := New("test")
	a.session.introspection = peopleTable()
	if _, err := a.OpenWorkbookAt(original); err != nil {
		t.Fatalf("open original: %v", err)
	}
	if _, err := a.SelectSheet("Sheet1", 1); err != nil {
		t.Fatalf("select original: %v", err)
	}
	res, err := a.AutoMatch()
	if err != nil {
		t.Fatalf("auto-match: %v", err)
	}
	if len(res.Mappings) != 3 || res.Status.Blocking {
		t.Fatalf("auto-match on the original layout mapped %d columns, blocking=%v", len(res.Mappings), res.Status.Blocking)
	}

	a.mu.Lock()
	c, err := a.configLocked()
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "people.pgsheet.json")
	if err := config.Save(path, c); err != nil {
		t.Fatal(err)
	}
	return path
}

// The client's next file has the name columns swapped and the email header
// recased. Every name is still present, every column is text, so nothing but
// reading by name keeps the values in their columns.
func reorderedWorkbook(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "reordered.xlsx")
	writeWorkbook(t, path,
		[]string{"Last Name", "First Name", "EMAIL"},
		[][]string{
			{"Lovelace", "Ada", "ada@analytical.example"},
			{"Turing", "Alan", "alan@bletchley.example"},
		})
	return path
}

// generateSQL validates and writes the file, and returns its text.
func generateSQL(t *testing.T, a *App, dir string) string {
	t.Helper()

	rep, err := a.Validate()
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !rep.OK() {
		t.Fatalf("validation found %d errors on a file with every mapped column present", rep.ErrorCount)
	}

	out := filepath.Join(dir, "out.sql")
	if _, genErr := a.Generate(GenerateOptions{
		Mode:              "insert",
		BatchSize:         500,
		WrapInTransaction: true,
		SkipBlankRows:     true,
		Path:              out,
	}); genErr != nil {
		t.Fatalf("generate: %v", genErr)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func assertRowsInTheirColumns(t *testing.T, sql string) {
	t.Helper()

	if !strings.Contains(sql, `("first_name", "last_name", "email")`) {
		t.Fatalf("unexpected column list:\n%s", sql)
	}
	for _, want := range []string{
		`('Ada', 'Lovelace', 'ada@analytical.example')`,
		`('Alan', 'Turing', 'alan@bletchley.example')`,
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("the file does not contain %s; values went to the wrong columns:\n%s", want, sql)
		}
	}
}

func assertResolved(t *testing.T, mappings []domain.ColumnMapping) {
	t.Helper()

	want := map[string]struct {
		header string
		index  int
	}{
		"first_name": {"First Name", 1},
		"last_name":  {"Last Name", 0},
		"email":      {"EMAIL", 2},
	}
	if len(mappings) != len(want) {
		t.Fatalf("got %d mappings, want %d", len(mappings), len(want))
	}
	for _, m := range mappings {
		w := want[m.DBColumn]
		if m.ExcelIndex != w.index || m.ExcelColumn != w.header {
			t.Errorf("%s maps to %q at %d, want %q at %d", m.DBColumn, m.ExcelColumn, m.ExcelIndex, w.header, w.index)
		}
	}
}

// reorderFixture is a fresh session on the people table, a configuration saved
// from the original layout, and the reordered workbook it will meet.
func reorderFixture(t *testing.T) (a *App, dir, cfg, file string) {
	t.Helper()
	dir = t.TempDir()
	cfg = saveConfigFrom(t, dir)
	file = reorderedWorkbook(t, dir)

	a = New("test")
	a.session.introspection = peopleTable()
	return a, dir, cfg, file
}

// The usual order: the configuration is loaded on the first screen, before
// any workbook is open, and the sheet arrives later.
func TestSavedConfigOnReorderedSheet_ConfigFirst(t *testing.T) {
	a, dir, cfg, file := reorderFixture(t)

	if _, err := a.ImportConfigAt(cfg); err != nil {
		t.Fatalf("load config: %v", err)
	}
	if _, err := a.OpenWorkbookAt(file); err != nil {
		t.Fatalf("open: %v", err)
	}
	sel, err := a.SelectSheet("Sheet1", 1)
	if err != nil {
		t.Fatalf("select: %v", err)
	}

	// The changed template is still announced.
	if !strings.Contains(strings.Join(sel.Warnings, "\n"), "do not match") {
		t.Errorf("no header warning for a changed template: %v", sel.Warnings)
	}
	if sel.Status == nil || sel.Status.Blocking {
		t.Fatalf("the reordered sheet should not block: %+v", sel.Status)
	}
	// What the screen receives is what the engine reads.
	assertResolved(t, sel.Mappings)

	// The screen pushes its copy back before validating; that must hold too.
	if _, err := a.SetMappings(sel.Mappings); err != nil {
		t.Fatal(err)
	}
	assertRowsInTheirColumns(t, generateSQL(t, a, dir))
}

// The other order: the workbook is open when the configuration is loaded.
func TestSavedConfigOnReorderedSheet_WorkbookFirst(t *testing.T) {
	a, dir, cfg, file := reorderFixture(t)

	if _, err := a.OpenWorkbookAt(file); err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := a.SelectSheet("Sheet1", 1); err != nil {
		t.Fatalf("select: %v", err)
	}
	res, err := a.ImportConfigAt(cfg)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if res.Status.Blocking {
		t.Fatalf("the reordered sheet should not block: %+v", res.Status.Problems)
	}
	assertResolved(t, res.Config.Mappings)

	assertRowsInTheirColumns(t, generateSQL(t, a, dir))
}
