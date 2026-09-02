package sqlgen_test

import (
	"bytes"
	"context"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"

	"pgsheet/internal/domain"
	"pgsheet/internal/excel"
	"pgsheet/internal/mapper"
	"pgsheet/internal/sqlgen"
	"pgsheet/internal/validator"
)

// update rewrites the golden files. Generated SQL is compared against checked-in
// output so that any change to the generator has to be a deliberate golden
// update, reviewed in the diff (spec §16).
var update = flag.Bool("update", false, "rewrite the golden files")

func fixtureWorkbook(t *testing.T) string {
	t.Helper()

	f := excelize.NewFile()
	defer f.Close()

	const sheet = "Sheet1"
	headers := []string{"Client Name", "Email", "Mobile", "Status", "Credit Limit", "Signup Date", "Notes"}
	for i, h := range headers {
		cell, err := excelize.CoordinatesToCellName(i+1, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.SetCellStr(sheet, cell, h); err != nil {
			t.Fatal(err)
		}
	}

	dateStyle, err := f.NewStyle(&excelize.Style{NumFmt: 14})
	if err != nil {
		t.Fatal(err)
	}

	rows := []struct {
		name, email, phone, status, credit, notes string
		signup                                    time.Time
	}{
		{"Acme SARL", "contact@acme.lb", "+961 1 234 567", "Actif", "15000.00", "", time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC)},
		{"O'Brien & Sons", "hello@obrien.ie", "+353 1 555 0100", "Actif", "2500.5", "Pays in EUR", time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC)},
		{"Zeta \\ Partners", "z@zeta.example", "0300 000 000", "Inactif", "0", "Backslash in the name", time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC)},
	}

	for i, r := range rows {
		row := i + 2
		set := func(colIdx int, v any) {
			cell, err := excelize.CoordinatesToCellName(colIdx, row)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.SetCellValue(sheet, cell, v); err != nil {
				t.Fatal(err)
			}
		}
		setStr := func(colIdx int, v string) {
			cell, err := excelize.CoordinatesToCellName(colIdx, row)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.SetCellStr(sheet, cell, v); err != nil {
				t.Fatal(err)
			}
		}

		setStr(1, r.name)
		setStr(2, r.email)
		setStr(3, r.phone)
		setStr(4, r.status)
		setStr(5, r.credit)
		set(6, r.signup)
		setStr(7, r.notes)

		cell, _ := excelize.CoordinatesToCellName(6, row)
		if err := f.SetCellStyle(sheet, cell, cell, dateStyle); err != nil {
			t.Fatal(err)
		}
	}

	path := filepath.Join(t.TempDir(), "customers.xlsx")
	if err := f.SaveAs(path); err != nil {
		t.Fatal(err)
	}
	return path
}

func intPtr(v int) *int { return &v }

// fixtureSchema mirrors the kind of table this tool exists for: an identity
// key, a required name, a unique email, an enum status, a money column and a
// nullable free-text column.
func fixtureSchema() domain.TableSchema {
	pk := domain.Constraint{Name: "customers_pkey", Type: "p", Columns: []string{"id"}}
	uniqueEmail := domain.Constraint{Name: "customers_email_key", Type: "u", Columns: []string{"email"}}

	return domain.TableSchema{
		Schema: "public",
		Table:  "customers",
		Columns: []domain.Column{
			{Name: "id", DataType: "int4", FormattedType: "integer", IsIdentity: true, IdentityKind: "BY DEFAULT", HasDefault: true},
			{Name: "name", DataType: "varchar", FormattedType: "character varying(255)", MaxLength: intPtr(255)},
			{Name: "email", DataType: "varchar", FormattedType: "character varying(255)", MaxLength: intPtr(255)},
			{Name: "phone", DataType: "varchar", FormattedType: "character varying(32)", MaxLength: intPtr(32), Nullable: true},
			{Name: "status", DataType: "customer_status", FormattedType: "customer_status", EnumValues: []string{"active", "inactive"}, EnumSchema: "public"},
			{Name: "credit_limit", DataType: "numeric", FormattedType: "numeric(12,2)", NumericPrecision: intPtr(12), NumericScale: intPtr(2), Nullable: true},
			{Name: "signup_date", DataType: "date", FormattedType: "date", Nullable: true},
			{Name: "notes", DataType: "text", FormattedType: "text", Nullable: true},
		},
		Constraints: []domain.Constraint{pk, uniqueEmail},
		PrimaryKey:  &pk,
	}
}

func fixtureMappings() []domain.ColumnMapping {
	trim := domain.Transform{Trim: true, BlankAsNull: true}
	return []domain.ColumnMapping{
		{ExcelColumn: "Client Name", ExcelIndex: 0, DBColumn: "name", Enabled: true, Transform: domain.Transform{Trim: true}},
		{ExcelColumn: "Email", ExcelIndex: 1, DBColumn: "email", Enabled: true, Transform: domain.Transform{Trim: true, LowerCase: true}},
		{ExcelColumn: "Mobile", ExcelIndex: 2, DBColumn: "phone", Enabled: true, Transform: domain.Transform{Trim: true, StripNonDigits: true, BlankAsNull: true}},
		{ExcelColumn: "Status", ExcelIndex: 3, DBColumn: "status", Enabled: true, Transform: domain.Transform{
			Trim:      true,
			LowerCase: true,
			ValueMap:  map[string]string{"actif": "active", "inactif": "inactive"},
		}},
		{ExcelColumn: "Credit Limit", ExcelIndex: 4, DBColumn: "credit_limit", Enabled: true, Transform: trim},
		{ExcelColumn: "Signup Date", ExcelIndex: 5, DBColumn: "signup_date", Enabled: true, Transform: trim},
		{ExcelColumn: "Notes", ExcelIndex: 6, DBColumn: "notes", Enabled: true, Transform: trim},
	}
}

func TestGenerateGolden(t *testing.T) {
	ctx := context.Background()

	wb, err := excel.Open(fixtureWorkbook(t))
	if err != nil {
		t.Fatal(err)
	}
	defer wb.Close()

	sheet, err := wb.Describe(ctx, "Sheet1", 1)
	if err != nil {
		t.Fatal(err)
	}

	schema := fixtureSchema()
	plan := mapper.BuildPlan(fixtureMappings(), schema, domain.PKSequence)
	opts := validator.Options{StandardConformingStrings: true, SourceTimezone: time.UTC}

	var buf bytes.Buffer
	res, err := sqlgen.Generate(ctx, &buf, sqlgen.Input{
		Workbook:  wb,
		SheetName: sheet.Name,
		DataStart: sheet.DataStart,
		Target: sqlgen.Target{
			Schema:  schema.Schema,
			Table:   schema.Table,
			Columns: validator.PlanColumnNames(plan),
		},
		Coerce: validator.RowLiterals(plan, opts, true),
		Options: sqlgen.Options{
			Mode:                 sqlgen.ModeInsert,
			BatchSize:            500,
			WrapInTransaction:    true,
			IncludeSummaryHeader: true,
			SkipBlankRows:        true,
		},
		Meta: sqlgen.Meta{
			Version: "1.0.0-test",
			// Fixed so the golden file is stable.
			GeneratedAt:         time.Date(2026, 9, 1, 14, 22, 7, 0, time.UTC),
			SourceFile:          "customers.xlsx",
			SheetName:           "Sheet1",
			HeaderRow:           1,
			FirstRow:            2,
			LastRow:             4,
			Fingerprint:         "sha256:fixed-for-the-golden-file",
			TargetSchema:        "public",
			TargetTable:         "customers",
			ServerInfo:          "PostgreSQL 16.2",
			RowsToInsert:        3,
			ColumnsMapped:       7,
			ColumnsDefault:      []string{"id"},
			PrimaryKey:          "id  (identity)",
			PrimaryKeyNote:      "Values assigned by the database at run time.",
			Validated:           "offline (full)",
			PersonalDataColumns: sqlgen.FlagPersonalData(validator.PlanColumnNames(plan)),
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if res.RowsWritten != 3 {
		t.Errorf("wrote %d rows, want 3", res.RowsWritten)
	}

	compareGolden(t, "testdata/customers_insert.sql", buf.Bytes())
}

func TestGenerateCopyGolden(t *testing.T) {
	ctx := context.Background()

	wb, err := excel.Open(fixtureWorkbook(t))
	if err != nil {
		t.Fatal(err)
	}
	defer wb.Close()

	sheet, err := wb.Describe(ctx, "Sheet1", 1)
	if err != nil {
		t.Fatal(err)
	}

	schema := fixtureSchema()
	plan := mapper.BuildPlan(fixtureMappings(), schema, domain.PKSequence)
	opts := validator.Options{StandardConformingStrings: true, SourceTimezone: time.UTC}

	var buf bytes.Buffer
	_, err = sqlgen.Generate(ctx, &buf, sqlgen.Input{
		Workbook:  wb,
		SheetName: sheet.Name,
		DataStart: sheet.DataStart,
		Target: sqlgen.Target{
			Schema:  schema.Schema,
			Table:   schema.Table,
			Columns: validator.PlanColumnNames(plan),
		},
		Coerce: validator.RowLiterals(plan, opts, true),
		Options: sqlgen.Options{
			Mode:              sqlgen.ModeCopy,
			WrapInTransaction: true,
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	compareGolden(t, "testdata/customers_copy.sql", buf.Bytes())
}

// A setval must appear whenever the file supplies explicit key values, and
// must not when the database assigns them. This is the step whose absence
// breaks the live application after a manual import (spec §10).
func TestSetvalOnlyWhenKeysAreExplicit(t *testing.T) {
	ctx := context.Background()

	wb, err := excel.Open(fixtureWorkbook(t))
	if err != nil {
		t.Fatal(err)
	}
	defer wb.Close()

	sheet, err := wb.Describe(ctx, "Sheet1", 1)
	if err != nil {
		t.Fatal(err)
	}

	schema := fixtureSchema()
	plan := mapper.BuildPlan(fixtureMappings(), schema, domain.PKSequence)
	opts := validator.Options{StandardConformingStrings: true, SourceTimezone: time.UTC}

	build := func(setvalColumn string) string {
		var buf bytes.Buffer
		_, err := sqlgen.Generate(ctx, &buf, sqlgen.Input{
			Workbook:  wb,
			SheetName: sheet.Name,
			DataStart: sheet.DataStart,
			Target: sqlgen.Target{
				Schema:       schema.Schema,
				Table:        schema.Table,
				Columns:      validator.PlanColumnNames(plan),
				SetvalColumn: setvalColumn,
			},
			Coerce:  validator.RowLiterals(plan, opts, true),
			Options: sqlgen.Options{Mode: sqlgen.ModeInsert, BatchSize: 500, WrapInTransaction: true},
		})
		if err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}

	if got := build(""); bytes.Contains([]byte(got), []byte("setval")) {
		t.Error("a file whose keys the database assigns must not contain setval")
	}

	withSetval := build("id")
	if !bytes.Contains([]byte(withSetval), []byte("pg_get_serial_sequence")) {
		t.Error("a file with explicit keys must resynchronise the sequence")
	}
	// Inside the transaction, or a failed import leaves the sequence moved.
	setvalAt := bytes.Index([]byte(withSetval), []byte("setval"))
	commitAt := bytes.LastIndex([]byte(withSetval), []byte("COMMIT;"))
	if setvalAt > commitAt {
		t.Error("setval must run inside the transaction, before COMMIT")
	}
}

func TestTransactionWrapsEverything(t *testing.T) {
	ctx := context.Background()

	wb, err := excel.Open(fixtureWorkbook(t))
	if err != nil {
		t.Fatal(err)
	}
	defer wb.Close()

	sheet, err := wb.Describe(ctx, "Sheet1", 1)
	if err != nil {
		t.Fatal(err)
	}

	schema := fixtureSchema()
	plan := mapper.BuildPlan(fixtureMappings(), schema, domain.PKSequence)
	opts := validator.Options{StandardConformingStrings: true, SourceTimezone: time.UTC}

	var buf bytes.Buffer
	if _, err := sqlgen.Generate(ctx, &buf, sqlgen.Input{
		Workbook:  wb,
		SheetName: sheet.Name,
		DataStart: sheet.DataStart,
		Target: sqlgen.Target{
			Schema:  schema.Schema,
			Table:   schema.Table,
			Columns: validator.PlanColumnNames(plan),
		},
		Coerce:  validator.RowLiterals(plan, opts, true),
		Options: sqlgen.Options{Mode: sqlgen.ModeInsert, BatchSize: 500, WrapInTransaction: true},
	}); err != nil {
		t.Fatal(err)
	}

	out := buf.String()
	begin := bytes.Index([]byte(out), []byte("BEGIN;"))
	insert := bytes.Index([]byte(out), []byte("INSERT INTO"))
	commit := bytes.LastIndex([]byte(out), []byte("COMMIT;"))

	if begin < 0 || insert < 0 || commit < 0 {
		t.Fatalf("missing transaction structure: begin=%d insert=%d commit=%d", begin, insert, commit)
	}
	if !(begin < insert && insert < commit) {
		t.Error("every statement must sit between BEGIN and COMMIT: all or nothing")
	}
}

func compareGolden(t *testing.T, path string, got []byte) {
	t.Helper()

	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("updated %s", path)
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden file: %v (run: go test ./internal/sqlgen/ -update)", err)
	}

	if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), got) {
		t.Errorf("output differs from %s\n--- got ---\n%s", path, got)
	}
}
