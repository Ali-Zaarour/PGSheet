package mapper

import (
	"fmt"
	"sort"

	"pgsheet/internal/domain"
)

// Problem is a structural fault in the mapping itself — one that can be
// decided from the schema and the header list alone, with no cell data.
//
// The mapping screen shows these live as the operator works, and the
// validator's Phase A reuses exactly this function rather than reimplementing
// the rules, so the two can never disagree.
type Problem struct {
	Code        string `json:"code"`
	Severity    string `json:"severity"` // "error" | "warning"
	Message     string `json:"message"`
	Hint        string `json:"hint"`
	DBColumn    string `json:"dbColumn"`
	ExcelColumn string `json:"excelColumn"`
}

// Status is what the mapping screen renders: counters plus the live problem
// list. Blocking is true when the operator cannot proceed to validation.
type Status struct {
	// SheetPending is true when no workbook has been opened yet, so the checks
	// that need the sheet's headers have not run. The UI says so rather than
	// presenting a partial result as a complete one.
	SheetPending bool `json:"sheetPending"`

	Mapped         int       `json:"mapped"`
	UnmappedSheet  int       `json:"unmappedSheet"`
	DefaultedCols  int       `json:"defaultedCols"`
	Problems       []Problem `json:"problems"`
	Blocking       bool      `json:"blocking"`
	MappedDBColumn []string  `json:"mappedDbColumns"`
}

// Check validates a mapping set against the sheet headers and the table schema.
//
// A nil headers slice means no workbook is open yet. The checks that need the
// sheet are then skipped rather than failed: a configuration loaded before the
// spreadsheet would otherwise report every one of its mappings as a missing
// column, which is noise, and noise in red teaches the operator to ignore the
// panel that matters.
//
// pkStrategy matters because it changes what "required" means: under
// PKSequence the primary key column is supplied by the database and must not
// be reported as an unmapped required column, while under PKMapped every part
// of the key must be present.
func Check(
	mappings []domain.ColumnMapping,
	schema domain.TableSchema,
	headers []string,
	pkStrategy domain.PKStrategy,
) Status {
	st := Status{SheetPending: headers == nil}

	colByName := make(map[string]domain.Column, len(schema.Columns))
	for _, c := range schema.Columns {
		colByName[c.Name] = c
	}
	headerSet := make(map[string]bool, len(headers))
	for _, h := range headers {
		headerSet[domain.NormalizeHeader(h)] = true
	}

	pkColumns := map[string]bool{}
	if schema.PrimaryKey != nil {
		for _, c := range schema.PrimaryKey.Columns {
			pkColumns[c] = true
		}
	}

	mappedDB := map[string][]string{} // db column -> excel columns targeting it
	mappedExcel := map[int]bool{}

	for _, m := range mappings {
		if !m.Enabled {
			continue
		}
		st.Mapped++
		mappedDB[m.DBColumn] = append(mappedDB[m.DBColumn], m.ExcelColumn)
		mappedExcel[m.ExcelIndex] = true

		// E101 — the sheet no longer has the column this mapping names. The
		// usual cause is a saved configuration meeting a changed template.
		// Skipped entirely when there is no sheet to compare against.
		if !st.SheetPending && !headerSet[domain.NormalizeHeader(m.ExcelColumn)] {
			st.Problems = append(st.Problems, Problem{
				Code:        "E101",
				Severity:    "error",
				ExcelColumn: m.ExcelColumn,
				DBColumn:    m.DBColumn,
				Message:     fmt.Sprintf("the sheet has no column named %q", m.ExcelColumn),
				Hint:        "the workbook layout has changed since this mapping was saved; remap this column",
			})
			continue
		}

		col, ok := colByName[m.DBColumn]
		if !ok {
			st.Problems = append(st.Problems, Problem{
				Code:        "E101",
				Severity:    "error",
				ExcelColumn: m.ExcelColumn,
				DBColumn:    m.DBColumn,
				Message:     fmt.Sprintf("the table has no column named %q", m.DBColumn),
				Hint:        "the table has changed since this mapping was saved; remap this column",
			})
			continue
		}

		// E103 — the database will refuse a value for this column outright.
		if !col.AcceptsValue() {
			reason := "it is a generated column"
			if col.IdentityKind == "ALWAYS" {
				reason = "it is GENERATED ALWAYS AS IDENTITY"
			}
			st.Problems = append(st.Problems, Problem{
				Code:        "E103",
				Severity:    "error",
				ExcelColumn: m.ExcelColumn,
				DBColumn:    m.DBColumn,
				Message:     fmt.Sprintf("%s cannot receive a value: %s", m.DBColumn, reason),
				Hint:        "remove this mapping; the database supplies the value",
			})
		}

		// E106 — target type out of scope for v1.
		switch col.Family() {
		case domain.FamilyUnsupported, domain.FamilyArray:
			st.Problems = append(st.Problems, Problem{
				Code:        "E106",
				Severity:    "error",
				ExcelColumn: m.ExcelColumn,
				DBColumn:    m.DBColumn,
				Message:     fmt.Sprintf("%s has type %s, which this version cannot write", m.DBColumn, col.FormattedType),
				Hint:        "leave this column unmapped and populate it separately",
			})
		}
	}

	// E105 — two sheet columns aimed at one table column.
	dupNames := make([]string, 0)
	for db := range mappedDB {
		if len(mappedDB[db]) > 1 {
			dupNames = append(dupNames, db)
		}
	}
	sort.Strings(dupNames)
	for _, db := range dupNames {
		st.Problems = append(st.Problems, Problem{
			Code:     "E105",
			Severity: "error",
			DBColumn: db,
			Message: fmt.Sprintf("%d sheet columns are mapped to %s: %v",
				len(mappedDB[db]), db, mappedDB[db]),
			Hint: "a table column can take values from only one sheet column",
		})
	}

	// E102 — a column the database requires, with nothing to fill it.
	for _, col := range schema.Columns {
		if len(mappedDB[col.Name]) > 0 {
			continue
		}
		if col.HasDefault || col.IsIdentity || col.IsGenerated {
			st.DefaultedCols++
			continue
		}
		if pkColumns[col.Name] {
			// A key the database generates is not missing; a key it does not
			// generate is, and under PKMapped every part must be mapped.
			if pkStrategy == domain.PKSequence {
				continue
			}
		}
		if col.Nullable {
			continue
		}
		st.Problems = append(st.Problems, Problem{
			Code:     "E102",
			Severity: "error",
			DBColumn: col.Name,
			Message: fmt.Sprintf("%s is required (%s NOT NULL, no default) and nothing is mapped to it",
				col.Name, col.FormattedType),
			Hint: "map a sheet column to it, or set a fixed value with DefaultOnBlank",
		})
	}

	for i := range headers {
		if !mappedExcel[i] {
			st.UnmappedSheet++
		}
	}
	for db := range mappedDB {
		st.MappedDBColumn = append(st.MappedDBColumn, db)
	}
	sort.Strings(st.MappedDBColumn)

	for _, p := range st.Problems {
		if p.Severity == "error" {
			st.Blocking = true
			break
		}
	}
	return st
}

// Plan is the resolved, ordered mapping the validator and generator both work
// from: one entry per table column that will appear in the INSERT column list,
// in table order so generated files are stable and diffable.
type Plan struct {
	Columns  []PlanColumn
	Schema   domain.TableSchema
	Strategy domain.PKStrategy
}

// PlanColumn ties a table column to the sheet column feeding it.
type PlanColumn struct {
	Column     domain.Column
	Mapping    domain.ColumnMapping
	ExcelIndex int
	Family     domain.TypeFamily
}

// BuildPlan resolves mappings into table order and drops anything the strategy
// says the database will supply.
func BuildPlan(mappings []domain.ColumnMapping, schema domain.TableSchema, strategy domain.PKStrategy) Plan {
	byDB := make(map[string]domain.ColumnMapping, len(mappings))
	for _, m := range mappings {
		if m.Enabled {
			byDB[m.DBColumn] = m
		}
	}

	pkColumns := map[string]bool{}
	if schema.PrimaryKey != nil {
		for _, c := range schema.PrimaryKey.Columns {
			pkColumns[c] = true
		}
	}

	plan := Plan{Schema: schema, Strategy: strategy}
	for _, col := range schema.Columns {
		m, ok := byDB[col.Name]
		if !ok {
			continue
		}
		// Under PKSequence the key column is omitted from the file entirely,
		// even if a mapping exists, so the database assigns values when the
		// file actually runs rather than when it was written (spec §10).
		if strategy == domain.PKSequence && pkColumns[col.Name] {
			continue
		}
		plan.Columns = append(plan.Columns, PlanColumn{
			Column:     col,
			Mapping:    m,
			ExcelIndex: m.ExcelIndex,
			Family:     col.Family(),
		})
	}
	return plan
}
