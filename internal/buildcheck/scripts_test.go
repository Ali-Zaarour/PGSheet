// Package buildcheck holds tests about the build machinery rather than the
// application: things that break a release build without breaking a test run.
package buildcheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// Windows PowerShell 5.1 reads a .ps1 file in the system codepage, not UTF-8,
// and NSIS reads .nsi/.nsh the same way. One em dash in a comment is therefore
// not a typographic detail: the file stops parsing and the installer cannot be
// built at all. It cost a build to learn, so it is a test now.
//
// GitHub's runner uses pwsh, which does read UTF-8, so CI would not have caught
// this on its own.
func TestBuildScriptsAreASCII(t *testing.T) {
	root := filepath.Join("..", "..", "scripts")

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".ps1", ".nsi", ".nsh", ".cmd", ".bat":
		default:
			return nil
		}

		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(body), "\n") {
			for _, r := range line {
				if r > utf8.RuneSelf {
					t.Errorf("%s:%d holds %q (U+%04X). The shell that runs this file reads it as the system codepage, so it will not parse.",
						filepath.ToSlash(path), i+1, r, r)
					break
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
