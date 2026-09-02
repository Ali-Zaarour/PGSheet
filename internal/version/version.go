// Package version is the single place the release number is written.
//
// It was four places before: main.go, cmd/pgsheet-cli, wails.json and the
// packaging script. Four copies of a number that has to agree is three copies
// too many — the version ends up in the .sql header of every generated file
// and in the configuration files operators keep, so a build that reports the
// wrong one makes those artifacts untraceable.
//
// wails.json cannot import Go, so it keeps its own copy for the Windows
// executable metadata. A test in this package reads that file and fails if the
// two have drifted, which is what makes the duplication safe.
package version

import "strings"

// Version is the release. Change it here and nowhere else, then run
// `go test ./internal/version/` to find anything left behind.
const Version = "1.0.1"

// build is optional provenance stamped at link time, so a binary handed to
// someone can be traced back to a commit:
//
//	go build -ldflags "-X pgsheet/internal/version.build=$(git rev-parse --short HEAD)"
//
// It is deliberately not part of Version: the release number is a decision,
// while this is a fact about one build of it.
var build = ""

// String is what the application reports: the release, plus the build stamp
// when there is one.
func String() string {
	if b := strings.TrimSpace(build); b != "" {
		return Version + "+" + b
	}
	return Version
}

// IsPrerelease reports whether this is a pre-release build.
//
// The UI uses it to say so plainly. A tester who does not know they are on a
// beta reports its rough edges as if they were the product.
func IsPrerelease() bool {
	return strings.ContainsAny(Version, "-")
}
