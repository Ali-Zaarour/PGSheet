package validator

import (
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

// The dry run's whole safety property is that it cannot commit. This asserts
// it at the source level, because the invariant is about what the code is
// allowed to contain, not only about what one execution happens to do
// (spec §14).
func TestDryRunNeverCommits(t *testing.T) {
	src, err := os.ReadFile("dryrun.go")
	if err != nil {
		t.Fatalf("read dryrun.go: %v", err)
	}

	// Match a call to Commit, not the word inside a comment.
	commitCall := regexp.MustCompile(`\bCommit\s*\(`)
	if loc := commitCall.FindIndex(src); loc != nil {
		line := 1 + strings.Count(string(src[:loc[0]]), "\n")
		t.Fatalf("dryrun.go calls Commit at line %d — the verification transaction must never commit", line)
	}

	// And the rollback must be deferred before anything can run.
	text := string(src)
	beginAt := strings.Index(text, "Begin(ctx)")
	rollbackAt := strings.Index(text, "tx.Rollback(ctx)")
	execAt := strings.Index(text, "tx.Exec(")

	switch {
	case beginAt < 0:
		t.Fatal("dryrun.go no longer starts a transaction")
	case rollbackAt < 0:
		t.Fatal("dryrun.go no longer defers a rollback")
	case rollbackAt > execAt && execAt > 0:
		t.Fatal("the deferred rollback must be registered before the first statement runs")
	}
}

func TestExcelRef(t *testing.T) {
	tests := []struct {
		col  int
		row  int
		want string
	}{
		{0, 1, "A1"},
		{3, 47, "D47"},
		{25, 2, "Z2"},
		{26, 2, "AA2"},
		{51, 10, "AZ10"},
		{52, 10, "BA10"},
	}
	for _, tt := range tests {
		if got := ExcelRef(tt.col, tt.row); got != tt.want {
			t.Errorf("ExcelRef(%d, %d) = %s, want %s", tt.col, tt.row, got, tt.want)
		}
	}
}

func TestReportSuppressionKeepsCountsHonest(t *testing.T) {
	b := newBuilder(3)
	for i := 0; i < 10; i++ {
		b.add(Issue{Code: "E201", Severity: SevError, Scope: ScopeCell, ExcelRow: i + 2})
	}

	rep := b.finish(10, 0, 0, nowForTest())

	if len(rep.Issues) != 3 {
		t.Errorf("listed %d issues, want the ceiling of 3", len(rep.Issues))
	}
	if rep.ErrorCount != 10 {
		t.Errorf("ErrorCount = %d, want all 10 counted even though only 3 are listed", rep.ErrorCount)
	}
	if !rep.Truncated {
		t.Error("Truncated is false on a report that dropped issues")
	}
}

func nowForTest() time.Time { return time.Now() }
