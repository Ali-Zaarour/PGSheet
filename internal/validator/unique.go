package validator

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"pgsheet/internal/domain"
	"pgsheet/internal/introspect"
	"pgsheet/internal/sqlgen"
)

// dbBatchSize is how many keys go into one existence query: enough to make
// round trips negligible, small enough for the protocol to be comfortable.
const dbBatchSize = 1000

// keyOccurrence records where a key came from, so a duplicate names both rows.
type keyOccurrence struct {
	Key   string
	Parts []string
	Row   int
}

// uniqueTracker detects duplicates within the file, streaming. Roughly sixty
// bytes per row per rule, which is the ceiling for keeping this in memory.
type uniqueTracker struct {
	rule   introspect.UniquenessRule
	first  map[string]int // key -> first Excel row that used it
	occKey string         // the bucket in-database checks read, built once
}

func newUniqueTracker(rule introspect.UniquenessRule, expectedRows int) *uniqueTracker {
	size := expectedRows
	if size > 100000 {
		size = 100000
	}
	return &uniqueTracker{
		rule:   rule,
		first:  make(map[string]int, size),
		occKey: "u:" + rule.Name,
	}
}

// observe records one row's key. It returns the earlier row when this key has
// already been seen, and zero otherwise.
// observe returns the row that first used this key, or zero, along with the
// key itself: the caller needs it too, and joining twice per row is a cost the
// file's size multiplies.
func (t *uniqueTracker) observe(row int, parts []string) (string, int) {
	key := domain.CompositeKey(parts)
	if prev, ok := t.first[key]; ok {
		return key, prev
	}
	t.first[key] = row
	return key, 0
}

// duplicateIssue renders the in-file duplicate. The primary key gets its own
// code: a duplicate unique value is a data problem, a duplicate key usually
// means the wrong column was mapped to it.
func (t *uniqueTracker) duplicateIssue(row, firstRow int, parts []string, excelColumn, dbColumn string, colIndex int) Issue {
	code := "E303"
	what := fmt.Sprintf("unique constraint %s", t.rule.Name)
	if t.rule.Primary {
		code = "E301"
		what = "primary key"
	}

	value := strings.Join(parts, ", ")
	return Issue{
		Code:        code,
		Severity:    SevError,
		Scope:       ScopeCell,
		ExcelRow:    row,
		ExcelColumn: excelColumn,
		ExcelRef:    ExcelRef(colIndex, row),
		DBColumn:    dbColumn,
		Value:       truncate(value),
		Message:     fmt.Sprintf("%s (%s) is already used by row %d in this file", what, value, firstRow),
		Hint:        "each row must have its own value; remove or correct one of the two rows",
	}
}

// checkExistingInDB reports keys already in the target table. Sent in batches,
// fully parameterised: values travel as an array parameter, never as text.
func checkExistingInDB(
	ctx context.Context,
	pool *pgxpool.Pool,
	schema, table string,
	rule introspect.UniquenessRule,
	columnTypes map[string]string,
	occurrences []keyOccurrence,
	mappingOf map[string]mappedColumn,
) ([]Issue, error) {
	if pool == nil || len(occurrences) == 0 {
		return nil, nil
	}
	if rule.Partial {
		// A partial index applies to a subset of rows defined by a WHERE
		// clause we deliberately do not evaluate. Claiming to have checked it
		// would be worse than saying it needs the live verification.
		return nil, nil
	}

	existing, err := selectExistingKeys(ctx, pool, schema, table, rule, columnTypes, occurrences)
	if err != nil {
		return nil, err
	}
	if len(existing) == 0 {
		return nil, nil
	}

	code := "E304"
	what := fmt.Sprintf("unique constraint %s", rule.Name)
	if rule.Primary {
		code = "E302"
		what = "primary key"
	}

	var issues []Issue
	for _, occ := range occurrences {
		if !existing[occ.Key] {
			continue
		}
		first := rule.Columns[0]
		m := mappingOf[first]
		issues = append(issues, Issue{
			Code:        code,
			Severity:    SevError,
			Scope:       ScopeCell,
			ExcelRow:    occ.Row,
			ExcelColumn: m.ExcelColumn,
			ExcelRef:    ExcelRef(m.ExcelIndex, occ.Row),
			DBColumn:    strings.Join(rule.Columns, ", "),
			Value:       truncate(strings.Join(occ.Parts, ", ")),
			Message:     fmt.Sprintf("%s (%s) already exists in %s.%s", what, strings.Join(occ.Parts, ", "), schema, table),
			Hint:        "this row is already in the table; remove it from the sheet or correct the value",
		})
	}
	return issues, nil
}

// selectExistingKeys runs the batched existence queries and returns the set of
// composite keys already present.
func selectExistingKeys(
	ctx context.Context,
	pool *pgxpool.Pool,
	schema, table string,
	rule introspect.UniquenessRule,
	columnTypes map[string]string,
	occurrences []keyOccurrence,
) (map[string]bool, error) {
	found := map[string]bool{}

	for start := 0; start < len(occurrences); start += dbBatchSize {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		end := start + dbBatchSize
		if end > len(occurrences) {
			end = len(occurrences)
		}
		batch := occurrences[start:end]

		// One text array per key column. Values are compared after casting the
		// table's column to text, so the comparison matches the normalized
		// text form the file will actually write.
		args := make([]any, len(rule.Columns))
		for ci := range rule.Columns {
			col := make([]string, len(batch))
			for bi, occ := range batch {
				if ci < len(occ.Parts) {
					col[bi] = occ.Parts[ci]
				}
			}
			args[ci] = col
		}

		sql := buildExistenceQuery(schema, table, rule, columnTypes)

		rows, err := pool.Query(ctx, sql, args...)
		if err != nil {
			return nil, fmt.Errorf("check existing values for %s: %w", rule.Name, err)
		}

		for rows.Next() {
			parts := make([]*string, len(rule.Columns))
			scanArgs := make([]any, len(rule.Columns))
			for i := range parts {
				scanArgs[i] = &parts[i]
			}
			if err := rows.Scan(scanArgs...); err != nil {
				rows.Close()
				return nil, fmt.Errorf("check existing values for %s: %w", rule.Name, err)
			}
			key := make([]string, len(parts))
			for i, p := range parts {
				if p != nil {
					key[i] = *p
				}
			}
			found[domain.CompositeKey(key)] = true
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("check existing values for %s: %w", rule.Name, err)
		}
	}

	return found, nil
}

// buildExistenceQuery asks which of a batch of keys already exist.
//
// The values never appear in the SQL: they arrive as text arrays, one per key
// column, and are unnested into a relation the target is joined against. Where
// the column's type is known the parameter is cast to it rather than the column
// cast to text, because "1.50" and "1.5" are one numeric and two strings.
func buildExistenceQuery(
	schema, table string,
	rule introspect.UniquenessRule,
	columnTypes map[string]string,
) string {
	var selectList, joinList, unnestList []string

	for ci, colName := range rule.Columns {
		alias := fmt.Sprintf("k%d", ci)
		ident := sqlgen.QuoteIdentifier(colName)

		unnestList = append(unnestList, fmt.Sprintf("unnest($%d::text[]) AS %s", ci+1, alias))
		selectList = append(selectList, "v."+alias)

		if castTo := columnTypes[colName]; castTo == "" {
			joinList = append(joinList, fmt.Sprintf("t.%s::text = v.%s", ident, alias))
		} else {
			joinList = append(joinList,
				fmt.Sprintf("t.%s = v.%s::%s", ident, alias, sqlgen.QuoteIdentifier(castTo)))
		}
	}

	return fmt.Sprintf(
		"SELECT DISTINCT %s FROM (SELECT %s) v JOIN %s t ON %s",
		strings.Join(selectList, ", "),
		strings.Join(unnestList, ", "),
		sqlgen.QualifiedIdentifier(schema, table),
		strings.Join(joinList, " AND "),
	)
}

// checkForeignKeys reports mapped values with no matching row in the
// referenced table. Opt-in: it costs a query per constraint per batch.
func checkForeignKeys(
	ctx context.Context,
	pool *pgxpool.Pool,
	fk domain.Constraint,
	occurrences []keyOccurrence,
	mappingOf map[string]mappedColumn,
) ([]Issue, error) {
	if pool == nil || len(occurrences) == 0 || len(fk.RefColumns) == 0 {
		return nil, nil
	}

	refSchema, refTable := "public", fk.RefTable
	if i := strings.Index(fk.RefTable, "."); i >= 0 {
		refSchema, refTable = fk.RefTable[:i], fk.RefTable[i+1:]
	}

	rule := introspect.UniquenessRule{Name: fk.Name, Columns: fk.RefColumns}
	present, err := selectExistingKeys(ctx, pool, refSchema, refTable, rule, nil, occurrences)
	if err != nil {
		return nil, err
	}

	var issues []Issue
	for _, occ := range occurrences {
		if present[occ.Key] {
			continue
		}
		m := mappingOf[fk.Columns[0]]
		issues = append(issues, Issue{
			Code:        "E305",
			Severity:    SevError,
			Scope:       ScopeCell,
			ExcelRow:    occ.Row,
			ExcelColumn: m.ExcelColumn,
			ExcelRef:    ExcelRef(m.ExcelIndex, occ.Row),
			DBColumn:    strings.Join(fk.Columns, ", "),
			Value:       truncate(strings.Join(occ.Parts, ", ")),
			Message: fmt.Sprintf("%s has no matching row in %s (constraint %s)",
				strings.Join(occ.Parts, ", "), fk.RefTable, fk.Name),
			Hint: "the referenced row must exist before this import runs",
		})
	}
	return issues, nil
}
