package validator

import (
	"errors"
	"testing"

	"github.com/shopspring/decimal"
)

// The definitions below are pg_get_constraintdef output as PostgreSQL actually
// renders it — with the casts, the doubled parentheses and the ARRAY form —
// rather than the tidy SQL a person would have typed.
func TestParseCheckAcceptedForms(t *testing.T) {
	tests := []struct {
		name       string
		definition string
		values     map[string]Coerced
		wantPass   bool
	}{
		{
			name:       "IN list",
			definition: `CHECK (((status)::text = ANY ((ARRAY['active'::character varying, 'inactive'::character varying])::text[])))`,
			values:     map[string]Coerced{"status": {Text: "active"}},
			wantPass:   true,
		},
		{
			name:       "IN list rejects an unlisted value",
			definition: `CHECK (((status)::text = ANY ((ARRAY['active'::character varying, 'inactive'::character varying])::text[])))`,
			values:     map[string]Coerced{"status": {Text: "pending"}},
			wantPass:   false,
		},
		{
			name:       "numeric comparison",
			definition: `CHECK ((credit_limit >= (0)::numeric))`,
			values:     map[string]Coerced{"credit_limit": {Num: decimal.NewFromInt(500), HasNum: true}},
			wantPass:   true,
		},
		{
			name:       "numeric comparison fails",
			definition: `CHECK ((credit_limit >= (0)::numeric))`,
			values:     map[string]Coerced{"credit_limit": {Num: decimal.NewFromInt(-1), HasNum: true}},
			wantPass:   false,
		},
		{
			name:       "BETWEEN",
			definition: `CHECK ((score BETWEEN 0 AND 100))`,
			values:     map[string]Coerced{"score": {Num: decimal.NewFromInt(55), HasNum: true}},
			wantPass:   true,
		},
		{
			name:       "BETWEEN out of range",
			definition: `CHECK ((score BETWEEN 0 AND 100))`,
			values:     map[string]Coerced{"score": {Num: decimal.NewFromInt(101), HasNum: true}},
			wantPass:   false,
		},
		{
			name:       "length",
			definition: `CHECK ((char_length((code)::text) = 3))`,
			values:     map[string]Coerced{"code": {Text: "LBN"}},
			wantPass:   true,
		},
		{
			name:       "length fails",
			definition: `CHECK ((char_length((code)::text) = 3))`,
			values:     map[string]Coerced{"code": {Text: "LB"}},
			wantPass:   false,
		},
		{
			name:       "regex",
			definition: `CHECK (((email)::text ~ '^[^@]+@[^@]+$'::text))`,
			values:     map[string]Coerced{"email": {Text: "contact@acme.lb"}},
			wantPass:   true,
		},
		{
			name:       "regex fails",
			definition: `CHECK (((email)::text ~ '^[^@]+@[^@]+$'::text))`,
			values:     map[string]Coerced{"email": {Text: "not-an-email"}},
			wantPass:   false,
		},
		{
			name:       "IS NOT NULL",
			definition: `CHECK ((name IS NOT NULL))`,
			values:     map[string]Coerced{"name": {Text: "Acme"}},
			wantPass:   true,
		},
		{
			name:       "conjunction",
			definition: `CHECK (((score >= 0) AND (score <= 100)))`,
			values:     map[string]Coerced{"score": {Num: decimal.NewFromInt(50), HasNum: true}},
			wantPass:   true,
		},
		{
			name:       "conjunction fails on the second part",
			definition: `CHECK (((score >= 0) AND (score <= 100)))`,
			values:     map[string]Coerced{"score": {Num: decimal.NewFromInt(500), HasNum: true}},
			wantPass:   false,
		},
		{
			// NULL does not violate a CHECK in PostgreSQL: the expression
			// evaluates to unknown, not false.
			name:       "NULL satisfies a membership check",
			definition: `CHECK (((status)::text = ANY ((ARRAY['active'::character varying])::text[])))`,
			values:     map[string]Coerced{"status": {IsNull: true}},
			wantPass:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expr, err := ParseCheck("c", tt.definition)
			if err != nil {
				t.Fatalf("ParseCheck(%s): %v", tt.definition, err)
			}
			failed := expr.Eval(tt.values)
			if tt.wantPass && failed != nil {
				t.Errorf("expected the row to pass, but %s failed", failed.Describe())
			}
			if !tt.wantPass && failed == nil {
				t.Errorf("expected the row to fail, but every predicate passed")
			}
		})
	}
}

// Anything outside the subset must be refused rather than half-understood: a
// parser that guesses at a constraint is worse than one that defers to the
// live verification (spec §9).
func TestParseCheckRefusesWhatItCannotEvaluate(t *testing.T) {
	unparseable := []string{
		`CHECK ((EXISTS ( SELECT 1 FROM other o WHERE (o.id = customer_id))))`,
		`CHECK ((lower((email)::text) = (email)::text))`,
		`CHECK (((start_date < end_date)))`,
		`CHECK (((a > 1) OR (b < 2)))`,
		`CHECK ((my_function(x) > 0))`,
	}

	for _, def := range unparseable {
		if _, err := ParseCheck("c", def); !errors.Is(err, ErrUnparseable) {
			t.Errorf("ParseCheck(%s) = %v, want ErrUnparseable", def, err)
		}
	}
}

func TestParseCheckCrossColumnConjunction(t *testing.T) {
	// Each conjunct is about one column, so this is parseable even though the
	// constraint names two columns.
	expr, err := ParseCheck("c", `CHECK (((score >= 0) AND (name IS NOT NULL)))`)
	if err != nil {
		t.Fatalf("ParseCheck: %v", err)
	}
	if len(expr.Predicates) != 2 {
		t.Fatalf("got %d predicates, want 2", len(expr.Predicates))
	}

	failed := expr.Eval(map[string]Coerced{
		"score": {Num: decimal.NewFromInt(10), HasNum: true},
		"name":  {IsNull: true},
	})
	if failed == nil {
		t.Fatal("a NULL name should fail the IS NOT NULL predicate")
	}
}
