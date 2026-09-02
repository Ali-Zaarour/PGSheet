package introspect

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"pgsheet/internal/domain"
)

// ListTables returns the tables the connected user can read.
func ListTables(ctx context.Context, pool *pgxpool.Pool) ([]domain.TableRef, error) {
	rows, err := pool.Query(ctx, query("list_tables"))
	if err != nil {
		return nil, fmt.Errorf("list tables: %w", err)
	}
	defer rows.Close()

	var out []domain.TableRef
	for rows.Next() {
		var (
			t       domain.TableRef
			comment *string
		)
		if err := rows.Scan(&t.Schema, &t.Table, &t.EstRows, &comment); err != nil {
			return nil, fmt.Errorf("list tables: %w", err)
		}
		if comment != nil {
			t.Comment = *comment
		}
		// reltuples is -1 on a table that has never been analysed, which would
		// read as nonsense in the picker.
		if t.EstRows < 0 {
			t.EstRows = 0
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Columns reads one table's columns.
//
// The interpretation of identity and generated flags happens here, once, so
// every other package works from the same understanding of what a column will
// accept (spec §6.2).
func Columns(ctx context.Context, pool *pgxpool.Pool, schema, table string) ([]domain.Column, error) {
	rows, err := pool.Query(ctx, query("columns"), schema, table)
	if err != nil {
		return nil, fmt.Errorf("read columns of %s.%s: %w", schema, table, err)
	}
	defer rows.Close()

	var out []domain.Column
	for rows.Next() {
		var (
			c             domain.Column
			defaultExpr   *string
			identityKind  string
			generatedKind string
			maxLength     *int
			numPrecision  *int
			numScale      *int
			typeCategory  string
			typeSchema    string
			comment       *string
			arrayElemType *string
		)

		if err := rows.Scan(
			&c.OrdinalPosition, &c.Name, &c.DataType, &c.FormattedType, &c.Nullable,
			&defaultExpr, &c.HasDefault, &identityKind, &generatedKind,
			&maxLength, &numPrecision, &numScale, &typeCategory, &typeSchema, &comment, &arrayElemType,
		); err != nil {
			return nil, fmt.Errorf("read columns of %s.%s: %w", schema, table, err)
		}

		if defaultExpr != nil {
			c.DefaultExpr = *defaultExpr
		}
		c.MaxLength = maxLength
		c.NumericPrecision = numPrecision
		c.NumericScale = numScale
		if comment != nil {
			c.Comment = *comment
		}
		if arrayElemType != nil {
			c.ArrayElemType = *arrayElemType
		}

		switch identityKind {
		case "a":
			// GENERATED ALWAYS AS IDENTITY: an explicit value is rejected
			// unless the statement says OVERRIDING SYSTEM VALUE, so mapping to
			// it is refused outright (E103).
			c.IsIdentity = true
			c.IdentityKind = "ALWAYS"
		case "d":
			c.IsIdentity = true
			c.IdentityKind = "BY DEFAULT"
		}

		c.IsGenerated = generatedKind == "s"

		if typeCategory == "e" {
			labels, err := EnumValues(ctx, pool, c.DataType, typeSchema)
			if err != nil {
				return nil, err
			}
			c.EnumValues = labels
			// The cast emitted in generated SQL is schema-qualified, so the
			// file does not depend on the search_path of whoever runs it.
			c.EnumSchema = typeSchema
		}

		out = append(out, c)
	}
	return out, rows.Err()
}

// EnumValues reads the labels of an enum type, in declaration order.
func EnumValues(ctx context.Context, pool *pgxpool.Pool, typeName, typeSchema string) ([]string, error) {
	rows, err := pool.Query(ctx, query("enum_values"), typeName, typeSchema)
	if err != nil {
		return nil, fmt.Errorf("read enum %q: %w", typeName, err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var label string
		if err := rows.Scan(&label); err != nil {
			return nil, fmt.Errorf("read enum %q: %w", typeName, err)
		}
		out = append(out, label)
	}
	return out, rows.Err()
}
