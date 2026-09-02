package introspect

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"pgsheet/internal/domain"
)

// Result is everything introspection learned about one table.
//
// UniqueIndexes is separate from TableSchema.Constraints because an index is
// not a constraint even though PostgreSQL enforces both identically; the
// validator needs to know which uniqueness rules are partial and therefore
// unverifiable offline.
type Result struct {
	Schema        domain.TableSchema
	UniqueIndexes []UniqueIndex
}

// Table reads the complete structure of one table.
//
// Every session calls this fresh. Nothing is cached between runs: if a
// developer added a column yesterday, this sees it today (spec §5).
func Table(ctx context.Context, pool *pgxpool.Pool, schema, table string) (Result, error) {
	cols, err := Columns(ctx, pool, schema, table)
	if err != nil {
		return Result{}, err
	}
	if len(cols) == 0 {
		return Result{}, fmt.Errorf("%s.%s has no columns, or is not visible to this user", schema, table)
	}

	cons, err := Constraints(ctx, pool, schema, table)
	if err != nil {
		return Result{}, err
	}

	indexes, err := UniqueIndexes(ctx, pool, schema, table)
	if err != nil {
		return Result{}, err
	}

	ts := domain.TableSchema{
		Schema:      schema,
		Table:       table,
		Columns:     cols,
		Constraints: cons,
	}

	for i := range cons {
		if cons[i].Type == "p" {
			ts.PrimaryKey = &cons[i]
			break
		}
	}

	// A single-column primary key may be backed by a sequence, either through
	// a serial default or an identity. That decides which strategies the
	// primary key screen can offer (spec §10).
	if ts.PrimaryKey != nil && len(ts.PrimaryKey.Columns) == 1 {
		seq, err := ColumnSequence(ctx, pool, schema, table, ts.PrimaryKey.Columns[0])
		if err != nil {
			return Result{}, err
		}
		ts.PKSequence = seq
	}

	if err := pool.QueryRow(ctx, `
		SELECT GREATEST(c.reltuples, 0)::bigint
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relname = $2`, schema, table).Scan(&ts.RowCount); err != nil {
		// An unavailable estimate is not a reason to fail introspection; it
		// only affects whether an in-database uniqueness check is offered.
		ts.RowCount = 0
	}

	return Result{Schema: ts, UniqueIndexes: indexes}, nil
}

// UniquenessRules collapses unique constraints and unique indexes into the one
// list the validator works from, without duplicates.
//
// A unique constraint always has an index behind it with the same columns, so
// matching on the column set is what removes the duplicate.
func (r Result) UniquenessRules() []UniquenessRule {
	var out []UniquenessRule
	seen := map[string]bool{}

	add := func(rule UniquenessRule) {
		key := domain.CompositeKey(rule.Columns)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, rule)
	}

	for _, c := range r.Schema.Constraints {
		switch c.Type {
		case "p":
			add(UniquenessRule{Name: c.Name, Columns: c.Columns, Primary: true})
		case "u":
			add(UniquenessRule{Name: c.Name, Columns: c.Columns})
		}
	}
	for _, ix := range r.UniqueIndexes {
		add(UniquenessRule{
			Name:       ix.Name,
			Columns:    ix.Columns,
			Primary:    ix.Primary,
			Partial:    ix.Partial,
			Definition: ix.Definition,
		})
	}
	return out
}

// UniquenessRule is one uniqueness requirement to enforce.
type UniquenessRule struct {
	Name       string   `json:"name"`
	Columns    []string `json:"columns"`
	Primary    bool     `json:"primary"`
	Partial    bool     `json:"partial"`
	Definition string   `json:"definition"`
}
