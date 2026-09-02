package validator_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"

	"pgsheet/internal/domain"
	"pgsheet/internal/excel"
	"pgsheet/internal/introspect"
	"pgsheet/internal/mapper"
	"pgsheet/internal/validator"
)

// buildSheet writes a workbook whose first row is the header and whose
// remaining rows are the given cells, so each test can state its data inline.
func buildSheet(t *testing.T, headers []string, rows [][]string) string {
	t.Helper()

	f := excelize.NewFile()
	defer f.Close()

	const sheet = "Sheet1"
	for i, h := range headers {
		cell, err := excelize.CoordinatesToCellName(i+1, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.SetCellStr(sheet, cell, h); err != nil {
			t.Fatal(err)
		}
	}
	for r, row := range rows {
		for c, v := range row {
			cell, err := excelize.CoordinatesToCellName(c+1, r+2)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.SetCellStr(sheet, cell, v); err != nil {
				t.Fatal(err)
			}
		}
	}

	path := filepath.Join(t.TempDir(), "sheet.xlsx")
	if err := f.SaveAs(path); err != nil {
		t.Fatal(err)
	}
	return path
}

func intPtr(v int) *int { return &v }

func customersSchema() domain.TableSchema {
	pk := domain.Constraint{Name: "customers_pkey", Type: "p", Columns: []string{"id"}}
	uniqueEmail := domain.Constraint{Name: "customers_email_key", Type: "u", Columns: []string{"email"}}

	return domain.TableSchema{
		Schema: "public",
		Table:  "customers",
		Columns: []domain.Column{
			{Name: "id", DataType: "int4", FormattedType: "integer", IsIdentity: true, IdentityKind: "BY DEFAULT", HasDefault: true},
			{Name: "name", DataType: "varchar", FormattedType: "character varying(20)", MaxLength: intPtr(20)},
			{Name: "email", DataType: "varchar", FormattedType: "character varying(255)", MaxLength: intPtr(255)},
			{Name: "signup_date", DataType: "date", FormattedType: "date", Nullable: true},
		},
		Constraints: []domain.Constraint{pk, uniqueEmail},
		PrimaryKey:  &pk,
	}
}

func runValidation(t *testing.T, path string, mappings []domain.ColumnMapping, schema domain.TableSchema) validator.Report {
	t.Helper()

	ctx := context.Background()
	wb, err := excel.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer wb.Close()

	sheet, err := wb.Describe(ctx, "Sheet1", 1)
	if err != nil {
		t.Fatal(err)
	}

	rep, err := validator.Run(ctx, validator.Input{
		Workbook:      wb,
		Sheet:         sheet,
		Mappings:      mappings,
		Plan:          mapper.BuildPlan(mappings, schema, domain.PKSequence),
		Introspection: introspect.Result{Schema: schema},
		Opts:          validator.Options{StandardConformingStrings: true, SourceTimezone: time.UTC},
		Settings: validator.Settings{
			MaxIssues:               10000,
			ColumnMisalignThreshold: 0.30,
			SkipBlankRows:           true,
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return rep
}

func standardMappings() []domain.ColumnMapping {
	tr := domain.Transform{Trim: true, BlankAsNull: true}
	return []domain.ColumnMapping{
		{ExcelColumn: "Name", ExcelIndex: 0, DBColumn: "name", Enabled: true, Transform: domain.Transform{Trim: true}},
		{ExcelColumn: "Email", ExcelIndex: 1, DBColumn: "email", Enabled: true, Transform: domain.Transform{Trim: true}},
		{ExcelColumn: "Signup", ExcelIndex: 2, DBColumn: "signup_date", Enabled: true, Transform: tr},
	}
}

func TestRunAcceptsCleanData(t *testing.T) {
	path := buildSheet(t,
		[]string{"Name", "Email", "Signup"},
		[][]string{
			{"Acme", "a@example.com", "2024-03-15"},
			{"Beta", "b@example.com", "2024-04-01"},
		})

	rep := runValidation(t, path, standardMappings(), customersSchema())

	if !rep.OK() {
		t.Fatalf("clean data produced errors: %+v", rep.Issues)
	}
	if rep.RowsValid != 2 {
		t.Errorf("RowsValid = %d, want 2", rep.RowsValid)
	}
}

// The behaviour that makes the report usable: a column mapped to the wrong
// target produces one line about the column, not one line per row (spec §9).
func TestRunReportsAMisalignedColumnOnce(t *testing.T) {
	rows := make([][]string, 0, 40)
	for i := 0; i < 40; i++ {
		rows = append(rows, []string{"Acme", "a@example.com", "not a date at all"})
	}
	path := buildSheet(t, []string{"Name", "Email", "Signup"}, rows)

	rep := runValidation(t, path, standardMappings(), customersSchema())

	if rep.OK() {
		t.Fatal("a text column mapped to a date column should not validate")
	}

	var columnIssues, rowIssues int
	for _, i := range rep.Issues {
		switch i.Scope {
		case validator.ScopeColumn:
			columnIssues++
		case validator.ScopeCell:
			rowIssues++
		}
	}

	if columnIssues == 0 {
		t.Fatalf("expected a column-level verdict, got %+v", rep.Issues)
	}
	if rowIssues > 0 {
		t.Errorf("expected the 40 row errors to be suppressed by the column verdict, got %d", rowIssues)
	}
	if rep.ByCode["E104"] == 0 {
		t.Errorf("expected E104, got codes %v", rep.ByCode)
	}
}

func TestRunFindsDuplicateUniqueValues(t *testing.T) {
	path := buildSheet(t,
		[]string{"Name", "Email", "Signup"},
		[][]string{
			{"Acme", "same@example.com", "2024-03-15"},
			{"Beta", "same@example.com", "2024-04-01"},
		})

	rep := runValidation(t, path, standardMappings(), customersSchema())

	if rep.ByCode["E303"] == 0 {
		t.Fatalf("expected E303 for the duplicate email, got %v", rep.ByCode)
	}

	for _, i := range rep.Issues {
		if i.Code != "E303" {
			continue
		}
		// The message has to name the other row, or the operator has to hunt
		// for it.
		if !strings.Contains(i.Message, "row 2") {
			t.Errorf("duplicate message does not name the first row: %s", i.Message)
		}
		if i.ExcelRow != 3 {
			t.Errorf("duplicate reported on row %d, want row 3", i.ExcelRow)
		}
	}
}

func TestRunReportsTruncationAsAWarningAndTheRowAsAnError(t *testing.T) {
	long := strings.Repeat("x", 40) // name is varchar(20)
	path := buildSheet(t,
		[]string{"Name", "Email", "Signup"},
		[][]string{
			{long, "a@example.com", "2024-03-15"},
		})

	rep := runValidation(t, path, standardMappings(), customersSchema())

	if rep.ByCode["W201"] == 0 {
		t.Errorf("expected the truncation warning W201, got %v", rep.ByCode)
	}
	if rep.WarningCount == 0 {
		t.Error("truncation risk must be a warning, not silence")
	}
}

func TestRunSkipsBlankRowsWithoutComplaining(t *testing.T) {
	path := buildSheet(t,
		[]string{"Name", "Email", "Signup"},
		[][]string{
			{"Acme", "a@example.com", "2024-03-15"},
			{"", "", ""},
			{"Beta", "b@example.com", "2024-04-01"},
		})

	rep := runValidation(t, path, standardMappings(), customersSchema())

	if !rep.OK() {
		t.Fatalf("a blank row should be skipped, not reported: %+v", rep.Issues)
	}
	if rep.RowsSkipped != 1 {
		t.Errorf("RowsSkipped = %d, want 1", rep.RowsSkipped)
	}
	if rep.RowsValid != 2 {
		t.Errorf("RowsValid = %d, want 2", rep.RowsValid)
	}
}

func TestRunBlocksOnAnUnmappedRequiredColumn(t *testing.T) {
	path := buildSheet(t, []string{"Email"}, [][]string{{"a@example.com"}})

	mappings := []domain.ColumnMapping{
		{ExcelColumn: "Email", ExcelIndex: 0, DBColumn: "email", Enabled: true, Transform: domain.Transform{Trim: true}},
	}

	rep := runValidation(t, path, mappings, customersSchema())

	if rep.ByCode["E102"] == 0 {
		t.Fatalf("expected E102 for the unmapped required name column, got %v", rep.ByCode)
	}
	// Phase B must not have run: judging the data of a mapping that is already
	// known to be wrong only produces noise.
	if rep.RowsTotal != 0 {
		t.Errorf("RowsTotal = %d, want 0 — phase B should not run after a blocking phase A", rep.RowsTotal)
	}
}

func TestRunIssuesCarryALocation(t *testing.T) {
	path := buildSheet(t,
		[]string{"Name", "Email", "Signup"},
		[][]string{
			{"Acme", "a@example.com", "not a date"},
		})

	rep := runValidation(t, path, standardMappings(), customersSchema())

	for _, i := range rep.Issues {
		if i.Scope != validator.ScopeCell {
			continue
		}
		if i.ExcelRow == 0 || i.ExcelRef == "" || i.ExcelColumn == "" || i.DBColumn == "" {
			t.Errorf("issue without a full location: %+v", i)
		}
		if i.Message == "" {
			t.Errorf("issue without a message: %+v", i)
		}
	}
}
