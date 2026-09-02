package excel

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/xuri/excelize/v2"
)

// Spec §13 puts the ceiling at 400MB for a 100,000-row file. This measures it,
// because until it was measured the number was an intention rather than a fact.
//
//	go test ./internal/excel/ -run TestMemory -v
func TestMemoryOnALargeSheet(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a large fixture")
	}

	const rows = 150000
	path := largeSheet(t, rows)

	ctx := context.Background()

	before := heapMB()
	wb, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer wb.Close()
	afterOpen := heapMB()

	info, err := wb.Describe(ctx, "Sheet1", 1)
	if err != nil {
		t.Fatal(err)
	}
	afterDescribe := heapMB()

	seen := 0
	if err := wb.Scan(ctx, "Sheet1", info.DataStart, func(Row) error {
		seen++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	afterScan := heapMB()

	// The other two calls that reach into the sheet by address rather than
	// streaming it. Both are cheap to look at and expensive to be wrong about.
	if _, checked, err := wb.MergedRanges("Sheet1"); err != nil {
		t.Fatal(err)
	} else if checked {
		t.Error("the merged-cell check should be skipped on a sheet this large")
	}
	afterMerged := heapMB()

	if _, err := wb.ScanFormulaGaps(ctx, "Sheet1", info.DataStart, 50, len(info.Headers)); err != nil {
		t.Fatal(err)
	}
	afterFormula := heapMB()

	t.Logf("rows read      : %d (sheet reports %d)", seen, info.TotalRows)
	t.Logf("heap at start  : %d MB", before)
	t.Logf("after Open     : %d MB", afterOpen)
	t.Logf("after Describe : %d MB", afterDescribe)
	t.Logf("after Scan     : %d MB", afterScan)
	t.Logf("after Merged   : %d MB", afterMerged)
	t.Logf("after Formula  : %d MB", afterFormula)
	t.Logf("peak observed  : %d MB", maxOf(afterOpen, afterDescribe, afterScan, afterMerged, afterFormula))

	if peak := maxOf(afterOpen, afterDescribe, afterScan, afterMerged, afterFormula); peak > 600 {
		t.Errorf("peak heap %d MB on %d rows: the target is under 400MB at 100k rows", peak, rows)
	}
}

// largeSheet writes a wide-ish sheet the size of a real client export.
func largeSheet(tb testing.TB, rows int) string {
	tb.Helper()

	f := excelize.NewFile()
	defer f.Close()

	sw, err := f.NewStreamWriter("Sheet1")
	if err != nil {
		tb.Fatal(err)
	}

	if err := sw.SetRow("A1", []any{
		"Name", "Email", "Phone", "City", "Status", "Amount", "Signup", "Notes", "Ref", "Code",
	}); err != nil {
		tb.Fatal(err)
	}

	for i := 0; i < rows; i++ {
		cell, err := excelize.CoordinatesToCellName(1, i+2)
		if err != nil {
			tb.Fatal(err)
		}
		row := []any{
			fmt.Sprintf("Client %d", i),
			fmt.Sprintf("client%d@example.com", i),
			fmt.Sprintf("+9611%06d", i%1000000),
			"Beirut",
			"active",
			fmt.Sprintf("%d.%02d", 100+i%9000, i%100),
			"2024-03-15",
			"A note that is long enough to be realistic for a free text column",
			fmt.Sprintf("REF-%08d", i),
			fmt.Sprintf("%05d", i%100000),
		}
		if err := sw.SetRow(cell, row); err != nil {
			tb.Fatal(err)
		}
	}
	if err := sw.Flush(); err != nil {
		tb.Fatal(err)
	}

	path := filepath.Join(tb.TempDir(), "large.xlsx")
	if err := f.SaveAs(path); err != nil {
		tb.Fatal(err)
	}
	return path
}

func heapMB() int {
	var m runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m)
	return int(m.HeapAlloc / (1 << 20))
}

func maxOf(values ...int) int {
	out := 0
	for _, v := range values {
		if v > out {
			out = v
		}
	}
	return out
}
