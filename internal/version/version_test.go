package version

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// wails.json holds its own copy of the version for the Windows executable's
// metadata, because a JSON file cannot import a Go constant. This is what
// keeps the copy honest: change Version and this test names the file that has
// not caught up.
func TestWailsJSONMatchesVersion(t *testing.T) {
	path := filepath.Join("..", "..", "wails.json")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read wails.json: %v", err)
	}

	var cfg struct {
		Info struct {
			ProductVersion string `json:"productVersion"`
		} `json:"info"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse wails.json: %v", err)
	}

	if cfg.Info.ProductVersion != Version {
		t.Errorf("wails.json productVersion is %q but version.Version is %q.\n"+
			"Update wails.json, or run: go run ./scripts/setversion",
			cfg.Info.ProductVersion, Version)
	}
}

// The README states the version to the reader. A stale number there tells a
// tester they are running something they are not.
func TestReadmeMatchesVersion(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Skipf("README.md not readable: %v", err)
	}

	// Matched as a whole version token: a lazy match would stop at the first
	// period and compare "1" against "1.0.0-beta".
	stated := regexp.MustCompile(`Version (\d+\.\d+\.\d+(?:-[0-9A-Za-z]+)?)`).FindSubmatch(data)
	if stated == nil {
		t.Skip("the README does not state a version")
	}

	if got := string(stated[1]); got != Version {
		t.Errorf("README says version %q but version.Version is %q", got, Version)
	}
}

func TestStringIncludesTheBuildStampWhenPresent(t *testing.T) {
	original := build
	t.Cleanup(func() { build = original })

	build = ""
	if got := String(); got != Version {
		t.Errorf("String() = %q, want the bare version when no build is stamped", got)
	}

	build = "a1b2c3d"
	if got := String(); got != Version+"+a1b2c3d" {
		t.Errorf("String() = %q, want the version with the build stamp", got)
	}

	// Whitespace from a shell substitution that produced nothing must not
	// become a meaningless "+" suffix.
	build = "  "
	if got := String(); got != Version {
		t.Errorf("String() = %q, want the bare version for a blank stamp", got)
	}
}

func TestVersionLooksLikeAVersion(t *testing.T) {
	if !regexp.MustCompile(`^\d+\.\d+\.\d+(-[0-9A-Za-z.]+)?$`).MatchString(Version) {
		t.Errorf("Version %q is not major.minor.patch with an optional pre-release suffix", Version)
	}
	if strings.HasPrefix(Version, "v") {
		t.Error("Version should not carry a leading v; tags add that, the constant does not")
	}
}

func TestIsPrerelease(t *testing.T) {
	if !IsPrerelease() && strings.Contains(Version, "-") {
		t.Error("a version with a pre-release suffix must report as a pre-release")
	}
}
