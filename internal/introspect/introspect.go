package introspect

import (
	_ "embed"
	"fmt"
	"strings"
)

//go:embed queries.sql
var queriesSQL string

// queries holds the statements from queries.sql, split on their "-- name:"
// markers. Keeping the SQL in a file rather than in string literals means it
// can be run and explained directly against a database while debugging, and it
// makes "is anything interpolated into this?" a one-file review.
var queries = parseQueries(queriesSQL)

func parseQueries(src string) map[string]string {
	out := map[string]string{}

	var name string
	var body strings.Builder

	flush := func() {
		if name != "" {
			out[name] = strings.TrimSpace(body.String())
		}
		body.Reset()
	}

	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "-- name:") {
			flush()
			name = strings.TrimSpace(strings.TrimPrefix(trimmed, "-- name:"))
			continue
		}
		if name == "" {
			continue // file header comments
		}
		body.WriteString(line)
		body.WriteByte('\n')
	}
	flush()

	return out
}

// query returns a named statement, panicking if it is missing.
//
// A missing name is a build-time mistake in this package, not a runtime
// condition: the file is embedded, so if it parses at all the names are fixed.
func query(name string) string {
	q, ok := queries[name]
	if !ok {
		panic(fmt.Sprintf("introspect: no query named %q in queries.sql", name))
	}
	return q
}
