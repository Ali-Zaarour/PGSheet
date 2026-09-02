// Package introspect reads a table's real structure from PostgreSQL: columns,
// constraints, unique indexes, enum labels and sequences.
//
// Nothing is cached between sessions and nothing is inferred. All SQL lives in
// queries.sql with bound parameters only.
//
// nextval is never called. It would consume an id permanently, even if the
// operator cancels; the displayed next value is computed from pg_sequences.
package introspect
