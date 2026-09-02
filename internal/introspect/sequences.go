package introspect

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"pgsheet/internal/domain"
)

// ColumnSequence resolves the sequence backing a column and reads its state.
//
// It returns nil when the column has no sequence, which is the ordinary case
// for a natural or composite key.
//
// The sequence is never advanced to learn its position: nextval consumes a
// value permanently, and the operator may well cancel this session without
// generating anything. The value shown in the UI is computed from last_value
// and labelled an estimate, because the database assigns the real values when
// the generated file eventually runs (spec §6.5).
func ColumnSequence(ctx context.Context, pool *pgxpool.Pool, schema, table, column string) (*domain.Sequence, error) {
	qualified := schema + "." + table

	var seqName *string
	err := pool.QueryRow(ctx, query("serial_sequence"), schema, table, column).Scan(&seqName)
	if err != nil {
		return nil, fmt.Errorf("resolve sequence for %s.%s: %w", qualified, column, err)
	}
	if seqName == nil || *seqName == "" {
		return nil, nil
	}

	seqSchema, seqRelname := splitQualified(*seqName)

	seq := &domain.Sequence{
		Name:      *seqName,
		OwnedBy:   qualified + "." + column,
		Increment: 1,
	}

	var (
		lastValue *int64
		increment int64
		startVal  *int64
	)
	err = pool.QueryRow(ctx, query("sequence_state"), seqSchema, seqRelname).
		Scan(&lastValue, &increment, &startVal)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// pg_sequences hides sequences the user cannot read. The sequence is
		// real and the strategy still works; only the estimate is unavailable.
		return seq, nil
	case err != nil:
		return nil, fmt.Errorf("read sequence %s: %w", *seqName, err)
	}

	seq.Increment = increment
	if lastValue != nil {
		seq.LastValue = *lastValue
		seq.IsCalled = true
		seq.NextValue = *lastValue + increment
	} else {
		// A sequence that has never been called starts at start_value, and
		// that first value is start_value itself, not start_value + increment.
		if startVal != nil {
			seq.LastValue = *startVal
			seq.NextValue = *startVal
		} else {
			seq.NextValue = 1
		}
	}

	return seq, nil
}

func splitQualified(name string) (schema, relname string) {
	// pg_get_serial_sequence returns a quoted, qualified name such as
	// public.customers_id_seq or "My Schema"."My Seq".
	parts := splitUnquoted(name)
	if len(parts) == 2 {
		return unquoteIdent(parts[0]), unquoteIdent(parts[1])
	}
	return "public", unquoteIdent(name)
}

func splitUnquoted(s string) []string {
	var parts []string
	var cur strings.Builder
	inQuote := false

	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			inQuote = !inQuote
			cur.WriteByte(s[i])
		case '.':
			if inQuote {
				cur.WriteByte(s[i])
				continue
			}
			parts = append(parts, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(s[i])
		}
	}
	parts = append(parts, cur.String())
	return parts
}

func unquoteIdent(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return strings.ReplaceAll(s[1:len(s)-1], `""`, `"`)
	}
	return s
}
