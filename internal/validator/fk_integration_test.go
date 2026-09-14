//go:build integration

package validator_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"pgsheet/internal/dbconn"
	"pgsheet/internal/domain"
	"pgsheet/internal/excel"
	"pgsheet/internal/introspect"
	"pgsheet/internal/mapper"
	"pgsheet/internal/validator"
)

// Two uuid foreign keys on one table: a plain one, and a composite one whose
// column order differs from the referenced table's own column order. That
// second shape is where an unordered list of referenced columns pairs each
// value with the other column.
const uuidFKDDL = `
CREATE TABLE pgsheet_rt.owners (
    id uuid PRIMARY KEY
);
CREATE TABLE pgsheet_rt.accounts (
    label  text,
    tenant uuid NOT NULL,
    id     uuid NOT NULL,
    UNIQUE (id, tenant)
);
CREATE TABLE pgsheet_rt.orders (
    owner_id   uuid REFERENCES pgsheet_rt.owners(id),
    tenant_id  uuid NOT NULL,
    account_id uuid NOT NULL,
    note       text,
    CONSTRAINT orders_account_fk FOREIGN KEY (account_id, tenant_id)
        REFERENCES pgsheet_rt.accounts (id, tenant)
);

INSERT INTO pgsheet_rt.owners VALUES ('aaaaaaaa-0000-0000-0000-000000000001');
INSERT INTO pgsheet_rt.accounts VALUES
    ('main', 'bbbbbbbb-0000-0000-0000-000000000001', 'cccccccc-0000-0000-0000-000000000001');
`

const (
	owner   = "aaaaaaaa-0000-0000-0000-000000000001"
	tenant  = "bbbbbbbb-0000-0000-0000-000000000001"
	account = "cccccccc-0000-0000-0000-000000000001"
	missing = "dddddddd-0000-0000-0000-000000000009"
)

// ordersTable creates the uuid foreign key tables and reads orders back.
func ordersTable(t *testing.T) (*pgxpool.Pool, dbconn.ServerInfo, introspect.Result) {
	t.Helper()
	pool, info := integrationPool(t)
	if _, err := pool.Exec(context.Background(), uuidFKDDL); err != nil {
		t.Fatalf("create tables: %v", err)
	}
	res, err := introspect.Table(context.Background(), pool, "pgsheet_rt", "orders")
	if err != nil {
		t.Fatal(err)
	}
	return pool, info, res
}

func TestForeignKeyReferencedColumnsKeepTheirOrder(t *testing.T) {
	_, _, res := ordersTable(t)
	fk := constraintNamed(t, res.Schema, "orders_account_fk")

	// columns[i] references ref_columns[i]. The referenced table stores tenant
	// before id, so anything but the declared order reads [tenant id].
	if strings.Join(fk.Columns, ",") != "account_id,tenant_id" {
		t.Errorf("columns = %v, want [account_id tenant_id]", fk.Columns)
	}
	if strings.Join(fk.RefColumns, ",") != "id,tenant" {
		t.Errorf("referenced columns = %v, want [id tenant]: each value would be compared against the other column", fk.RefColumns)
	}
}

// Every value in row 2 exists, so row 2 must not be reported. Row 3 has an
// account that does not exist, row 4 an owner that does not exist; each must be
// reported with its own value and nothing from the neighbouring column.
func TestForeignKeyCheckWithTwoUUIDColumns(t *testing.T) {
	ctx := context.Background()
	pool, info, res := ordersTable(t)

	// Sheet order deliberately unlike table order.
	headers := []string{"Note", "Account", "Owner", "Tenant"}
	path := buildSheet(t, headers, [][]string{
		{"all present", account, owner, tenant},
		{"missing account", missing, owner, tenant},
		{"missing owner", account, missing, tenant},
	})

	wb, err := excel.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer wb.Close()
	sheet, err := wb.Describe(ctx, "Sheet1", 1)
	if err != nil {
		t.Fatal(err)
	}

	tr := domain.Transform{Trim: true, BlankAsNull: true}
	target := map[string]string{"Note": "note", "Account": "account_id", "Owner": "owner_id", "Tenant": "tenant_id"}
	mappings := make([]domain.ColumnMapping, 0, len(headers))
	for i, h := range sheet.Headers {
		mappings = append(mappings, domain.ColumnMapping{
			ExcelColumn: h, ExcelIndex: i, DBColumn: target[h], Enabled: true, Transform: tr,
		})
	}

	rep, err := validator.Run(ctx, validator.Input{
		Workbook:      wb,
		Sheet:         sheet,
		Mappings:      mappings,
		Plan:          mapper.BuildPlan(mappings, res.Schema, domain.PKNone),
		Introspection: res,
		Pool:          pool,
		Opts: validator.Options{
			StandardConformingStrings: info.StandardConformingStrings,
			SourceTimezone:            time.UTC,
		},
		Settings: validator.Settings{
			MaxIssues:               1000,
			ColumnMisalignThreshold: 0.3,
			CheckForeignKeys:        true,
			SkipBlankRows:           true,
		},
	})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}

	byRow := map[int][]validator.Issue{}
	for _, i := range rep.Issues {
		if i.Code == "E305" {
			byRow[i.ExcelRow] = append(byRow[i.ExcelRow], i)
		}
	}

	if got := byRow[2]; len(got) > 0 {
		t.Errorf("row 2 references rows that exist, but was reported: %+v", got)
	}

	if got := byRow[3]; len(got) != 1 {
		t.Errorf("row 3: want one E305 for the account, got %+v", got)
	} else if !strings.Contains(got[0].Message, "account_id = "+missing) ||
		!strings.Contains(got[0].Message, "tenant_id = "+tenant) {
		t.Errorf("row 3 pairs the wrong values with its columns: %s", got[0].Message)
	}

	if got := byRow[4]; len(got) != 1 {
		t.Errorf("row 4: want one E305 for the owner, got %+v", got)
	} else if got[0].DBColumn != "owner_id" || got[0].Value != missing {
		t.Errorf("row 4 reported %s = %s, want owner_id = %s", got[0].DBColumn, got[0].Value, missing)
	}
}

func constraintNamed(t *testing.T, schema domain.TableSchema, name string) domain.Constraint {
	t.Helper()
	for _, c := range schema.Constraints {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("constraint %s was not read", name)
	return domain.Constraint{}
}
