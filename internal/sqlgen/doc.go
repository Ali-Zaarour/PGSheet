// Package sqlgen writes the .sql output: the summary header, batched INSERTs,
// the COPY variant, and the setval that keeps a sequence in step.
//
// This is where a bug becomes SQL injection in a file someone runs against
// production, so: every literal through QuoteLiteral, every identifier through
// QuoteIdentifier, explicit casts on everything non-trivial, numbers from
// decimal.String() and never a float64.
//
// copygen.go is a separate path, not a flag. COPY escaping has nothing in
// common with literal quoting.
package sqlgen
