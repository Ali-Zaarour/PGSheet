package validator

import (
	"fmt"

	"pgsheet/internal/excel"
	"pgsheet/internal/mapper"
	"pgsheet/internal/sqlgen"
)

// RowLiterals builds the function the generator uses to turn a source row into
// SQL literals.
//
// It runs the same transforms and the same Coerce calls the validation ran, so
// the file cannot contain a value the report did not judge. The generator is
// handed this function rather than importing the validator, which is what
// keeps the dependency pointing one way (spec §11).
func RowLiterals(plan mapper.Plan, opts Options, skipBlank bool) sqlgen.RowCoercer {
	cols := mappedColumns(plan)

	return func(row excel.Row) ([]string, bool, error) {
		blank := true
		for _, c := range cols {
			if row.Cell(c.ExcelIndex).Kind != 0 { // 0 is domain.CellEmpty
				blank = false
				break
			}
		}
		if blank && skipBlank {
			return nil, true, nil
		}

		out := make([]string, len(cols))
		for i, c := range cols {
			applied := mapper.Apply(row.Cell(c.ExcelIndex), c.Transform, c.Family)
			coerced, cerr := Coerce(applied.Value, c.Column, opts)
			if cerr != nil {
				// Validation passed, so reaching this means the file changed
				// under us or a mapping was altered after validation. Failing
				// loudly is right: a partially correct .sql file is worse than
				// none.
				return nil, false, fmt.Errorf("column %s (%s): %s",
					c.Column.Name, c.ExcelColumn, cerr.Message)
			}
			if coerced.IsNull {
				out[i] = "NULL"
				continue
			}
			out[i] = coerced.Literal
		}
		return out, false, nil
	}
}

// PlanColumnNames is the explicit column list for the INSERT statement, in
// table order.
func PlanColumnNames(plan mapper.Plan) []string {
	out := make([]string, 0, len(plan.Columns))
	for _, c := range plan.Columns {
		out = append(out, c.Column.Name)
	}
	return out
}
