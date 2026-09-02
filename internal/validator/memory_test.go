package validator_test

import (
	"context"
	"runtime"
	"testing"
	"time"

	"pgsheet/internal/domain"
	"pgsheet/internal/excel"
	"pgsheet/internal/introspect"
	"pgsheet/internal/mapper"
	"pgsheet/internal/validator"
)

// What a whole validation costs on a file the size of a real client export.
//
//	go test ./internal/validator/ -run TestMemory -v
func TestMemoryOnALargeFile(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a large fixture")
	}

	const rows = 150000
	path := benchWorkbook(t, rows)

	ctx := context.Background()
	schema := benchSchema()

	// A unique constraint, because the in-file duplicate check is the one
	// structure that has to remember something about every row.
	unique := domain.Constraint{Name: "clients_email_key", Type: "u", Columns: []string{"email"}}
	schema.Constraints = append(schema.Constraints, unique)

	wb, err := excel.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer wb.Close()

	sheet, err := wb.Describe(ctx, "Sheet1", 1)
	if err != nil {
		t.Fatal(err)
	}

	before := heapMB()
	mappings := benchMappings()

	// Sampled while it runs. Measuring after Run returns would only show what
	// survived the collector, which is nothing: the interesting number is what
	// the run holds at once.
	peak := 0
	sample := func(string, int, int) {
		if h := liveHeapMB(); h > peak {
			peak = h
		}
	}

	rep, err := validator.Run(ctx, validator.Input{
		Progress:      sample,
		Workbook:      wb,
		Sheet:         sheet,
		Mappings:      mappings,
		Plan:          mapper.BuildPlan(mappings, schema, domain.PKNone),
		Introspection: introspect.Result{Schema: schema},
		Opts:          validator.Options{StandardConformingStrings: true, SourceTimezone: time.UTC},
		Settings: validator.Settings{
			MaxIssues:               10000,
			ColumnMisalignThreshold: 0.30,
			SkipBlankRows:           true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	after := heapMB()

	t.Logf("rows validated : %d", rep.RowsTotal)
	t.Logf("errors         : %d", rep.ErrorCount)
	t.Logf("heap before    : %d MB", before)
	t.Logf("peak during    : %d MB", peak)
	t.Logf("heap retained  : %d MB", after)
	t.Logf("peak per row   : %d bytes", peak*(1<<20)/max(rows, 1))

	// Spec §13 allows 400MB at 100,000 rows.
	if peak > 400 {
		t.Errorf("peak heap %d MB validating %d rows, target is under 400MB", peak, rows)
	}
}

// liveHeapMB reads the heap without forcing a collection: during a run, what
// matters is how much is held at once, not how little survives a GC.
func liveHeapMB() int {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return int(m.HeapAlloc / (1 << 20))
}

func heapMB() int {
	var m runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m)
	return int(m.HeapAlloc / (1 << 20))
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
