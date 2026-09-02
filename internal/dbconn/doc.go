// Package dbconn owns the connection: DSN construction, the pgx pool, and the
// post-connect probe.
//
// The DSN is never built by concatenating operator input; only an allowlisted
// SSL mode reaches the connection string, and the rest are set as fields.
//
// The password lives in memory only, and is redacted from every error before
// it can reach a log or the frontend.
package dbconn
