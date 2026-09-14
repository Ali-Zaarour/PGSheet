package mapper

import (
	"testing"

	"pgsheet/internal/domain"
)

// A configuration saved when first_name was column B meets a sheet where the
// client swapped it with last_name. Both names still exist, so nothing is
// reported missing, and both columns are text, so nothing fails to coerce.
// Reading by the saved positions would import each into the other.
func TestResolveIndexesFollowsReorderedColumns(t *testing.T) {
	saved := []domain.ColumnMapping{
		{ExcelColumn: "Id", ExcelIndex: 0, DBColumn: "id", Enabled: true},
		{ExcelColumn: "First Name", ExcelIndex: 1, DBColumn: "first_name", Enabled: true},
		{ExcelColumn: "Last Name", ExcelIndex: 2, DBColumn: "last_name", Enabled: true},
	}
	reordered := []string{"Id", "Last Name", "First Name"}

	got := ResolveIndexes(saved, reordered)

	want := map[string]int{"id": 0, "first_name": 2, "last_name": 1}
	for _, m := range got {
		if m.ExcelIndex != want[m.DBColumn] {
			t.Errorf("%s reads sheet column %d, want %d (%q)",
				m.DBColumn, m.ExcelIndex, want[m.DBColumn], reordered[want[m.DBColumn]])
		}
	}

	// The saved configuration is the caller's; resolving must not rewrite it.
	if saved[1].ExcelIndex != 1 || saved[2].ExcelIndex != 2 {
		t.Error("ResolveIndexes modified the slice it was given")
	}

	// And the plan the validator and generator read from follows the names.
	schema := domain.TableSchema{Columns: []domain.Column{column("id"), column("first_name"), column("last_name")}}
	for _, pc := range BuildPlan(got, schema, domain.PKNone).Columns {
		if pc.ExcelIndex != want[pc.Column.Name] {
			t.Errorf("plan reads %s from sheet column %d, want %d", pc.Column.Name, pc.ExcelIndex, want[pc.Column.Name])
		}
	}
}

func TestResolveIndexes(t *testing.T) {
	tests := []struct {
		name       string
		mapping    domain.ColumnMapping
		headers    []string
		wantIndex  int
		wantHeader string // a found column takes this sheet's spelling
	}{
		{
			name:       "a column inserted before it",
			mapping:    domain.ColumnMapping{ExcelColumn: "Email", ExcelIndex: 1},
			headers:    []string{"Id", "Phone", "Email"},
			wantIndex:  2,
			wantHeader: "Email",
		},
		{
			name:       "header differs only in case and spacing",
			mapping:    domain.ColumnMapping{ExcelColumn: "First Name", ExcelIndex: 0},
			headers:    []string{"Id", "first  name"},
			wantIndex:  1,
			wantHeader: "first  name",
		},
		{
			name:       "exact text wins over a normalized match",
			mapping:    domain.ColumnMapping{ExcelColumn: "Amount", ExcelIndex: 0},
			headers:    []string{"amount ", "Amount"},
			wantIndex:  1,
			wantHeader: "Amount",
		},
		{
			name:       "ambiguous normalized match keeps the recorded position",
			mapping:    domain.ColumnMapping{ExcelColumn: "total", ExcelIndex: 1},
			headers:    []string{"Total", "TOTAL"},
			wantIndex:  1,
			wantHeader: "TOTAL",
		},
		{
			// Check reports it as E101 and blocks; nothing is guessed, and the
			// name stays so the error can say which column went away.
			name:       "name no longer in the sheet",
			mapping:    domain.ColumnMapping{ExcelColumn: "Fax", ExcelIndex: 3},
			headers:    []string{"Id", "Phone"},
			wantIndex:  3,
			wantHeader: "Fax",
		},
		{
			// A configuration loaded before any workbook keeps its positions
			// until there is a sheet to resolve against.
			name:       "no sheet open yet",
			mapping:    domain.ColumnMapping{ExcelColumn: "Email", ExcelIndex: 4},
			headers:    nil,
			wantIndex:  4,
			wantHeader: "Email",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveIndexes([]domain.ColumnMapping{tt.mapping}, tt.headers)[0]
			if got.ExcelIndex != tt.wantIndex || got.ExcelColumn != tt.wantHeader {
				t.Errorf("got %q at %d, want %q at %d", got.ExcelColumn, got.ExcelIndex, tt.wantHeader, tt.wantIndex)
			}
		})
	}
}
