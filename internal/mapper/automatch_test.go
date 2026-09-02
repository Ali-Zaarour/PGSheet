package mapper

import (
	"testing"

	"pgsheet/internal/domain"
)

func column(name string, opts ...func(*domain.Column)) domain.Column {
	c := domain.Column{Name: name, DataType: "text", FormattedType: "text", Nullable: true}
	for _, o := range opts {
		o(&c)
	}
	return c
}

func identityAlways(c *domain.Column) { c.IsIdentity = true; c.IdentityKind = "ALWAYS" }
func required(c *domain.Column)       { c.Nullable = false }

func acceptedFor(suggestions []Suggestion, excelColumn string) string {
	for _, s := range suggestions {
		if s.ExcelColumn == excelColumn && s.Accepted {
			return s.DBColumn
		}
	}
	return ""
}

func TestAutoMatch(t *testing.T) {
	headers := []string{"Client Name", "Email", "Mobile", "City", "Status", "Credit Limit", "Random Notes Column"}
	columns := []domain.Column{
		column("id", identityAlways),
		column("name"),
		column("email"),
		column("phone"),
		column("city"),
		column("status"),
		column("credit_limit"),
	}

	got := AutoMatch(headers, columns)

	want := map[string]string{
		"Client Name":  "name",         // containment after normalization
		"Email":        "email",        // exact
		"Mobile":       "phone",        // synonym
		"City":         "city",         // exact
		"Status":       "status",       // exact
		"Credit Limit": "credit_limit", // separators ignored
	}

	for header, dbColumn := range want {
		if got := acceptedFor(got, header); got != dbColumn {
			t.Errorf("%q matched %q, want %q", header, got, dbColumn)
		}
	}

	if db := acceptedFor(got, "Random Notes Column"); db != "" {
		t.Errorf("%q should not have matched anything, got %q", "Random Notes Column", db)
	}
}

func TestAutoMatchNeverProposesGeneratedAlwaysColumns(t *testing.T) {
	// Mapping to a GENERATED ALWAYS identity is refused at validation (E103),
	// so proposing it would only manufacture an error for the operator to
	// undo.
	got := AutoMatch([]string{"id"}, []domain.Column{column("id", identityAlways)})
	for _, s := range got {
		if s.DBColumn == "id" {
			t.Fatalf("proposed a mapping to a GENERATED ALWAYS column: %+v", s)
		}
	}
}

func TestAutoMatchIsOneToOne(t *testing.T) {
	// Two headers that both look like "name": only one can win, and the other
	// must be left for the operator rather than doubling up.
	headers := []string{"Name", "Client Name"}
	columns := []domain.Column{column("name")}

	got := AutoMatch(headers, columns)

	accepted := 0
	for _, s := range got {
		if s.Accepted {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatalf("%d accepted mappings to one column, want 1", accepted)
	}
	if acceptedFor(got, "Name") != "name" {
		t.Error("the exact match should win over the containment match")
	}
}

func TestScoreNames(t *testing.T) {
	tests := []struct {
		header, column string
		wantAtLeast    float64
		wantBelow      float64
	}{
		{"Email", "email", 1.0, 1.01},
		{"Client Name", "client_name", 0.95, 0.96},
		{"CLIENT_NAME", "client name", 0.95, 1.01},
		{"Mobile", "phone", 0.70, 0.71},
		{"Credit Limit", "credit_limit", 0.95, 0.96},
		{"Adress", "address", 0.60, 0.81}, // a typo, scored as similar spelling
		{"Colour", "quantity", 0, 0.5},
		{"", "email", 0, 0.01},
	}

	for _, tt := range tests {
		got, _ := scoreNames(tt.header, tt.column)
		if got < tt.wantAtLeast || got >= tt.wantBelow {
			t.Errorf("scoreNames(%q, %q) = %.3f, want [%.2f, %.2f)",
				tt.header, tt.column, got, tt.wantAtLeast, tt.wantBelow)
		}
	}
}

func TestDefaultTransform(t *testing.T) {
	// A nullable column treats blanks as NULL; a required one leaves the blank
	// to fail coercion, where the message names the real problem.
	if tr := DefaultTransform(column("notes")); !tr.BlankAsNull {
		t.Error("a nullable column should default to BlankAsNull")
	}
	if tr := DefaultTransform(column("name", required)); tr.BlankAsNull {
		t.Error("a required column should not silently turn blanks into NULL")
	}
	if tr := DefaultTransform(column("notes")); !tr.Trim {
		t.Error("Trim should be on by default")
	}
}

func TestCheckReportsStructuralProblems(t *testing.T) {
	schema := domain.TableSchema{
		Schema: "public",
		Table:  "customers",
		Columns: []domain.Column{
			column("id", identityAlways),
			column("name", required),
			column("email"),
		},
	}
	headers := []string{"Name", "Email", "Extra"}

	t.Run("required column with nothing mapped", func(t *testing.T) {
		st := Check([]domain.ColumnMapping{
			{ExcelColumn: "Email", ExcelIndex: 1, DBColumn: "email", Enabled: true},
		}, schema, headers, domain.PKNone)

		if !hasCode(st.Problems, "E102") {
			t.Errorf("expected E102 for the unmapped required column, got %+v", st.Problems)
		}
		if !st.Blocking {
			t.Error("an unmapped required column must block")
		}
	})

	t.Run("mapping to a generated always column", func(t *testing.T) {
		st := Check([]domain.ColumnMapping{
			{ExcelColumn: "Name", ExcelIndex: 0, DBColumn: "name", Enabled: true},
			{ExcelColumn: "Extra", ExcelIndex: 2, DBColumn: "id", Enabled: true},
		}, schema, headers, domain.PKNone)

		if !hasCode(st.Problems, "E103") {
			t.Errorf("expected E103 for the identity column, got %+v", st.Problems)
		}
	})

	t.Run("two sheet columns to one table column", func(t *testing.T) {
		st := Check([]domain.ColumnMapping{
			{ExcelColumn: "Name", ExcelIndex: 0, DBColumn: "name", Enabled: true},
			{ExcelColumn: "Extra", ExcelIndex: 2, DBColumn: "name", Enabled: true},
		}, schema, headers, domain.PKNone)

		if !hasCode(st.Problems, "E105") {
			t.Errorf("expected E105 for the duplicate mapping, got %+v", st.Problems)
		}
	})

	t.Run("mapping a column the sheet does not have", func(t *testing.T) {
		st := Check([]domain.ColumnMapping{
			{ExcelColumn: "Client Name", ExcelIndex: 0, DBColumn: "name", Enabled: true},
		}, schema, headers, domain.PKNone)

		if !hasCode(st.Problems, "E101") {
			t.Errorf("expected E101 for the missing header, got %+v", st.Problems)
		}
	})
}

func TestBuildPlanOmitsSequenceKey(t *testing.T) {
	pk := domain.Constraint{Name: "customers_pkey", Type: "p", Columns: []string{"id"}}
	schema := domain.TableSchema{
		Schema:      "public",
		Table:       "customers",
		Columns:     []domain.Column{column("id"), column("name")},
		Constraints: []domain.Constraint{pk},
		PrimaryKey:  &pk,
	}

	mappings := []domain.ColumnMapping{
		{ExcelColumn: "Id", ExcelIndex: 0, DBColumn: "id", Enabled: true},
		{ExcelColumn: "Name", ExcelIndex: 1, DBColumn: "name", Enabled: true},
	}

	// Under PKSequence the key is left out of the file entirely, even though a
	// mapping exists, so the database assigns values when the file runs.
	plan := BuildPlan(mappings, schema, domain.PKSequence)
	for _, c := range plan.Columns {
		if c.Column.Name == "id" {
			t.Fatal("the key column must not appear in the file under the sequence strategy")
		}
	}

	plan = BuildPlan(mappings, schema, domain.PKMapped)
	found := false
	for _, c := range plan.Columns {
		if c.Column.Name == "id" {
			found = true
		}
	}
	if !found {
		t.Error("the key column must appear in the file under the mapped strategy")
	}
}

func hasCode(problems []Problem, code string) bool {
	for _, p := range problems {
		if p.Code == code {
			return true
		}
	}
	return false
}

// A column default applies when the column is left out of the insert, not when
// it is handed an explicit NULL. Treating a default as permission to write
// NULL produces a file that validates and then fails on a NOT NULL column.
func TestDefaultTransformDoesNotTreatADefaultAsNullable(t *testing.T) {
	notNullWithDefault := domain.Column{
		Name: "status", DataType: "text", Nullable: false, HasDefault: true, DefaultExpr: "'active'",
	}
	if DefaultTransform(notNullWithDefault).BlankAsNull {
		t.Error("a NOT NULL column with a default must not turn blanks into NULL")
	}

	identity := domain.Column{
		Name: "id", DataType: "int4", Nullable: false, IsIdentity: true, HasDefault: true,
	}
	if DefaultTransform(identity).BlankAsNull {
		t.Error("an identity column must not turn blanks into NULL")
	}

	nullable := domain.Column{Name: "notes", DataType: "text", Nullable: true}
	if !DefaultTransform(nullable).BlankAsNull {
		t.Error("a nullable column should treat blanks as NULL")
	}
}
