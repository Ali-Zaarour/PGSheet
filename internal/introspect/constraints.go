package introspect

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"pgsheet/internal/domain"
)

// UniqueIndex is a unique index with no constraint behind it.
//
// CREATE UNIQUE INDEX produces no pg_constraint row but is enforced exactly
// like a unique constraint, so an import that ignores these fails at run time
// for reasons the operator was never shown (spec §6.3).
type UniqueIndex struct {
	Name       string   `json:"name"`
	Columns    []string `json:"columns"`
	Definition string   `json:"definition"`
	Primary    bool     `json:"primary"`
	// Partial indexes carry a WHERE clause. Their uniqueness applies to a
	// subset of rows, which cannot be reproduced offline, so they are reported
	// as verifiable only by the live dry run.
	Partial bool `json:"partial"`
}

// Constraints reads every constraint registered on a table.
func Constraints(ctx context.Context, pool *pgxpool.Pool, schema, table string) ([]domain.Constraint, error) {
	rows, err := pool.Query(ctx, query("constraints"), schema, table)
	if err != nil {
		return nil, fmt.Errorf("read constraints of %s.%s: %w", schema, table, err)
	}
	defer rows.Close()

	var out []domain.Constraint
	for rows.Next() {
		var (
			c          domain.Constraint
			refTable   *string
			refColumns []string
		)
		if err := rows.Scan(&c.Name, &c.Type, &c.Definition, &c.Columns, &refTable, &refColumns); err != nil {
			return nil, fmt.Errorf("read constraints of %s.%s: %w", schema, table, err)
		}
		if refTable != nil {
			c.RefTable = *refTable
		}
		c.RefColumns = refColumns
		out = append(out, c)
	}
	return out, rows.Err()
}

// UniqueIndexes reads the unique indexes on a table.
func UniqueIndexes(ctx context.Context, pool *pgxpool.Pool, schema, table string) ([]UniqueIndex, error) {
	rows, err := pool.Query(ctx, query("unique_indexes"), schema, table)
	if err != nil {
		return nil, fmt.Errorf("read indexes of %s.%s: %w", schema, table, err)
	}
	defer rows.Close()

	var out []UniqueIndex
	for rows.Next() {
		var (
			ix       UniqueIndex
			isUnique bool
		)
		if err := rows.Scan(&ix.Name, &isUnique, &ix.Primary, &ix.Definition, &ix.Columns); err != nil {
			return nil, fmt.Errorf("read indexes of %s.%s: %w", schema, table, err)
		}
		ix.Partial = strings.Contains(strings.ToUpper(ix.Definition), " WHERE ")
		out = append(out, ix)
	}
	return out, rows.Err()
}
