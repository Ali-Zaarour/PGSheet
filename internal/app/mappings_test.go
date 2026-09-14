package app

import (
	"testing"

	"pgsheet/internal/domain"
)

// The mapping screen pushes back whatever mappings it holds, including ones
// from a configuration loaded against an older layout. The session must keep
// the positions of the sheet that is open, not the ones it was sent.
func TestSetMappingsResolvesPositionsByName(t *testing.T) {
	a := New("test")
	a.session.introspection = peopleTable()
	a.session.sheet = domain.SheetInfo{Headers: []string{"Last Name", "First Name"}}

	status, err := a.SetMappings([]domain.ColumnMapping{
		{ExcelColumn: "First Name", ExcelIndex: 0, DBColumn: "first_name", Enabled: true},
		{ExcelColumn: "Last Name", ExcelIndex: 1, DBColumn: "last_name", Enabled: true},
	})
	if err != nil {
		t.Fatalf("SetMappings: %v", err)
	}
	if status.Blocking {
		t.Fatalf("a reordered sheet with every name present should not block: %+v", status.Problems)
	}

	want := map[string]int{"first_name": 1, "last_name": 0}
	for _, m := range a.session.mappings {
		if m.ExcelIndex != want[m.DBColumn] {
			t.Errorf("session reads %s from sheet column %d, want %d", m.DBColumn, m.ExcelIndex, want[m.DBColumn])
		}
	}
}
