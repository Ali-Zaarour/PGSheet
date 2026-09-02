// Command setversion propagates internal/version.Version to the files that
// cannot import it.
//
// The release number is decided in one place, in Go. Two files outside the
// compiler's reach repeat it: wails.json, which stamps the Windows executable's
// metadata, and the README, which tells a tester what they are running. This
// rewrites both, and `go test ./internal/version/` fails if either is stale.
//
//	go run ./scripts/setversion
package main

import (
	"fmt"
	"os"
	"regexp"

	"pgsheet/internal/version"
)

func main() {
	changed := 0

	changed += rewrite("wails.json",
		regexp.MustCompile(`("productVersion"\s*:\s*)"[^"]*"`),
		`${1}"`+version.Version+`"`)

	changed += rewrite("README.md",
		regexp.MustCompile(`Version \d+\.\d+\.\d+(?:-[0-9A-Za-z]+)?`),
		"Version "+version.Version)

	if changed == 0 {
		fmt.Printf("everything already reports %s\n", version.Version)
		return
	}
	fmt.Printf("updated %d file(s) to %s\n", changed, version.Version)
}

// rewrite applies one substitution, and reports whether the file changed.
func rewrite(path string, pattern *regexp.Regexp, replacement string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "setversion: %v\n", err)
		os.Exit(1)
	}

	if !pattern.Match(data) {
		// Silence here would mean a file quietly stops being updated, which is
		// the failure this command exists to prevent.
		fmt.Fprintf(os.Stderr, "setversion: no version found in %s; check the pattern\n", path)
		os.Exit(1)
	}

	out := pattern.ReplaceAll(data, []byte(replacement))
	if string(out) == string(data) {
		return 0
	}

	if err := os.WriteFile(path, out, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "setversion: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  %s\n", path)
	return 1
}
