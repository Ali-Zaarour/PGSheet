package validator

import (
	"strings"
	"testing"

	"pgsheet/internal/introspect"
)

// The existence check is the one query built from schema names rather than
// written out in queries.sql, so its shape is worth pinning: values as bound
// arrays, identifiers quoted, and the comparison done in the column's own type.
func TestBuildExistenceQuery(t *testing.T) {
	t.Run("single column, known type", func(t *testing.T) {
		got := buildExistenceQuery("public", "customers",
			introspect.UniquenessRule{Name: "customers_email_key", Columns: []string{"email"}},
			map[string]string{"email": "varchar"})

		for _, want := range []string{
			`unnest($1::text[]) AS k0`,
			`JOIN "public"."customers" t`,
			`t."email" = v.k0::"varchar"`,
		} {
			if !strings.Contains(got, want) {
				t.Errorf("query is missing %q:\n%s", want, got)
			}
		}
	})

	t.Run("composite key", func(t *testing.T) {
		got := buildExistenceQuery("public", "orders",
			introspect.UniquenessRule{Name: "orders_pkey", Columns: []string{"customer_id", "line_no"}},
			map[string]string{"customer_id": "int4", "line_no": "int4"})

		// Both parts have to be in the join, or the check reports a collision
		// on the first column alone.
		if !strings.Contains(got, `t."customer_id" = v.k0::"int4" AND t."line_no" = v.k1::"int4"`) {
			t.Errorf("composite join is wrong:\n%s", got)
		}
		if !strings.Contains(got, "unnest($1::text[]) AS k0, unnest($2::text[]) AS k1") {
			t.Errorf("expected one array parameter per key column:\n%s", got)
		}
	})

	t.Run("unknown type falls back to text", func(t *testing.T) {
		// Foreign key checks pass no type map: the referenced column's type is
		// not introspected, so both sides are compared as text.
		got := buildExistenceQuery("public", "customers",
			introspect.UniquenessRule{Name: "fk", Columns: []string{"id"}}, nil)

		if !strings.Contains(got, `t."id"::text = v.k0`) {
			t.Errorf("expected a text comparison:\n%s", got)
		}
	})

	t.Run("no value is ever interpolated", func(t *testing.T) {
		got := buildExistenceQuery("public", "t",
			introspect.UniquenessRule{Name: "r", Columns: []string{"a"}}, nil)

		// Every value reaches the server as a parameter. A literal quote in the
		// generated SQL would mean something was pasted in.
		if strings.Contains(got, "'") {
			t.Errorf("the query contains a literal, so a value was interpolated:\n%s", got)
		}
	})
}
