package validator

import (
	"fmt"

	"pgsheet/internal/domain"
	"pgsheet/internal/excel"
	"pgsheet/internal/mapper"
)

// mappedColumn is one resolved mapping, flattened so the hot loop touches no
// maps and no pointers it does not need.
type mappedColumn struct {
	Column      domain.Column
	ExcelColumn string
	ExcelIndex  int
	Transform   domain.Transform
	Family      domain.TypeFamily
}

func mappedColumns(plan mapper.Plan) []mappedColumn {
	out := make([]mappedColumn, 0, len(plan.Columns))
	for _, pc := range plan.Columns {
		out = append(out, mappedColumn{
			Column:      pc.Column,
			ExcelColumn: pc.Mapping.ExcelColumn,
			ExcelIndex:  pc.ExcelIndex,
			Transform:   pc.Mapping.Transform,
			Family:      pc.Family,
		})
	}
	return out
}

// rowOutcome is everything one row produced. Values is kept because the
// generator consumes exactly the same coercion the validator performed — a
// second coercion pass could disagree with the first, and then the file would
// not match the report.
type rowOutcome struct {
	Row     int
	Blank   bool
	Issues  []Issue
	Values  map[string]Coerced
	Ordered []Coerced // in plan column order, for the generator
}

// evaluateRow runs transforms, coercion and the parsed CHECK constraints over
// one row.
//
// It is pure: same row in, same result out, no shared state. That is what lets
// the pipeline run it on N workers without a mutex anywhere in the hot path.
func evaluateRow(row excel.Row, cols []mappedColumn, checks []CheckExpr, opts Options) rowOutcome {
	out := rowOutcome{
		Row:     row.Number,
		Values:  make(map[string]Coerced, len(cols)),
		Ordered: make([]Coerced, len(cols)),
	}

	blank := true
	for _, c := range cols {
		if row.Cell(c.ExcelIndex).Kind != domain.CellEmpty {
			blank = false
			break
		}
	}
	if blank {
		out.Blank = true
		return out
	}

	for i, c := range cols {
		cell := row.Cell(c.ExcelIndex)
		applied := mapper.Apply(cell, c.Transform, c.Family)

		if applied.DateParseFailed {
			out.Issues = append(out.Issues, Issue{
				Code:        "E201",
				Severity:    SevError,
				Scope:       ScopeCell,
				ExcelRow:    row.Number,
				ExcelColumn: c.ExcelColumn,
				ExcelRef:    ExcelRef(c.ExcelIndex, row.Number),
				DBColumn:    c.Column.Name,
				Value:       truncate(applied.Original.RawText),
				Message:     fmt.Sprintf("%q does not match the date format %s set for this column", truncate(text(applied.Original)), c.Transform.DateFormat),
				Hint:        "correct the value, or change the date format on this column",
			})
			continue
		}

		coerced, cerr := Coerce(applied.Value, c.Column, opts)
		if cerr != nil {
			out.Issues = append(out.Issues, Issue{
				Code:        cerr.Code,
				Severity:    SevError,
				Scope:       ScopeCell,
				ExcelRow:    row.Number,
				ExcelColumn: c.ExcelColumn,
				ExcelRef:    ExcelRef(c.ExcelIndex, row.Number),
				DBColumn:    c.Column.Name,
				Value:       truncate(text(applied.Original)),
				Message:     cerr.Message,
				Hint:        cerr.Hint,
			})
			continue
		}

		out.Values[c.Column.Name] = coerced
		out.Ordered[i] = coerced
	}

	// CHECK constraints run last: they compare coerced values, so a row with a
	// coercion failure has nothing meaningful to check.
	if len(out.Issues) == 0 {
		for _, chk := range checks {
			failed := chk.Eval(out.Values)
			if failed == nil {
				continue
			}
			col := failed.Column()
			m := findColumn(cols, col)
			out.Issues = append(out.Issues, Issue{
				Code:        "E306",
				Severity:    SevError,
				Scope:       ScopeCell,
				ExcelRow:    row.Number,
				ExcelColumn: m.ExcelColumn,
				ExcelRef:    ExcelRef(m.ExcelIndex, row.Number),
				DBColumn:    col,
				Value:       truncate(out.Values[col].Text),
				Message:     fmt.Sprintf("constraint %s is not satisfied: %s", chk.Name, failed.Describe()),
				Hint:        chk.Definition,
			})
		}
	}

	return out
}

func findColumn(cols []mappedColumn, dbColumn string) mappedColumn {
	for _, c := range cols {
		if c.Column.Name == dbColumn {
			return c
		}
	}
	return mappedColumn{ExcelIndex: -1}
}
