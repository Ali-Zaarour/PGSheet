package validator

import (
	"strings"
	"testing"

	"pgsheet/internal/domain"
)

// A key made of two uuids. Reported as a bare "a, b has no matching row", the
// second id reads as though it had been taken from the wrong column. Each value
// has to be named with the table column and the sheet column it came from.
func TestMissingReferenceNamesEachPart(t *testing.T) {
	fk := domain.Constraint{
		Name:       "orders_account_fk",
		Type:       "f",
		Columns:    []string{"tenant_id", "account_id"},
		RefTable:   "public.accounts",
		RefColumns: []string{"tenant_id", "id"},
	}
	mappingOf := map[string]mappedColumn{
		"tenant_id":  {ExcelColumn: "Tenant", ExcelIndex: 4},
		"account_id": {ExcelColumn: "Account", ExcelIndex: 1},
	}
	occ := keyOccurrence{
		Parts: []string{"11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"},
		Row:   7,
	}

	issue := newMissingReference(fk, mappingOf).issue(occ)

	for _, want := range []string{
		`tenant_id = 11111111-1111-1111-1111-111111111111 (sheet column "Tenant")`,
		`account_id = 22222222-2222-2222-2222-222222222222 (sheet column "Account")`,
		"public.accounts (tenant_id, id)",
		"orders_account_fk",
	} {
		if !strings.Contains(issue.Message, want) {
			t.Errorf("message is missing %q:\n%s", want, issue.Message)
		}
	}
	if issue.ExcelColumn != "Tenant, Account" {
		t.Errorf("sheet columns = %q, want both parts named", issue.ExcelColumn)
	}
	if issue.ExcelRef != "E7" {
		t.Errorf("cell = %q, want E7, the first part of the key", issue.ExcelRef)
	}
	if issue.ExcelRow != 7 || issue.Code != "E305" {
		t.Errorf("row %d code %s, want row 7 E305", issue.ExcelRow, issue.Code)
	}
}

// A single-column key reads the way it always did, with the referenced column
// added.
func TestMissingReferenceSingleColumn(t *testing.T) {
	fk := domain.Constraint{
		Name: "customers_region_fkey", Type: "f",
		Columns: []string{"region"}, RefTable: "public.regions", RefColumns: []string{"code"},
	}
	issue := newMissingReference(fk,
		map[string]mappedColumn{"region": {ExcelColumn: "Region", ExcelIndex: 3}}).
		issue(keyOccurrence{Parts: []string{"NOPE"}, Row: 3})

	if !strings.Contains(issue.Message, `region = NOPE (sheet column "Region") has no matching row in public.regions (code)`) {
		t.Errorf("unexpected message: %s", issue.Message)
	}
	if issue.ExcelColumn != "Region" || issue.ExcelRef != "D3" || issue.Value != "NOPE" {
		t.Errorf("located at %q %q value %q", issue.ExcelColumn, issue.ExcelRef, issue.Value)
	}
}
