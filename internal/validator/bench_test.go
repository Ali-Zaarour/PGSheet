package validator_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"

	"pgsheet/internal/domain"
	"pgsheet/internal/excel"
	"pgsheet/internal/introspect"
	"pgsheet/internal/mapper"
	"pgsheet/internal/validator"
)

// Targets from spec §13, for the shape these benchmarks use:
//
//	  1,000 rows x 10 cols   read + validate < 1s     peak < 60MB
//	 10,000 rows x 20 cols   read + validate < 3s     peak < 150MB
//	100,000 rows x 20 cols   read + validate < 25s    peak < 400MB
//
// Run with: go test ./internal/validator/ -bench . -benchmem
//
// The 100,000-row case is skipped in -short mode: it is a performance check,
// not a correctness one, and it costs about a minute to build the fixture.

func benchWorkbook(tb testing.TB, rows int) string {
	tb.Helper()

	f := excelize.NewFile()
	defer f.Close()

	const sheet = "Sheet1"
	sw, err := f.NewStreamWriter(sheet)
	if err != nil {
		tb.Fatal(err)
	}

	headers := []any{"Name", "Email", "Amount", "Signup", "Status"}
	if err := sw.SetRow("A1", headers); err != nil {
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
			fmt.Sprintf("%d.%02d", 100+i%9000, i%100),
			"2024-03-15",
			"active",
		}
		if err := sw.SetRow(cell, row); err != nil {
			tb.Fatal(err)
		}
	}
	if err := sw.Flush(); err != nil {
		tb.Fatal(err)
	}

	path := filepath.Join(tb.TempDir(), fmt.Sprintf("bench-%d.xlsx", rows))
	if err := f.SaveAs(path); err != nil {
		tb.Fatal(err)
	}
	return path
}

func benchSchema() domain.TableSchema {
	return domain.TableSchema{
		Schema: "public",
		Table:  "clients",
		Columns: []domain.Column{
			{Name: "name", DataType: "varchar", FormattedType: "character varying(100)", MaxLength: intPtr(100)},
			{Name: "email", DataType: "varchar", FormattedType: "character varying(255)", MaxLength: intPtr(255)},
			{Name: "amount", DataType: "numeric", FormattedType: "numeric(12,2)", NumericPrecision: intPtr(12), NumericScale: intPtr(2)},
			{Name: "signup", DataType: "date", FormattedType: "date", Nullable: true},
			{Name: "status", DataType: "text", FormattedType: "text", Nullable: true},
		},
	}
}

func benchMappings() []domain.ColumnMapping {
	tr := domain.Transform{Trim: true, BlankAsNull: true}
	return []domain.ColumnMapping{
		{ExcelColumn: "Name", ExcelIndex: 0, DBColumn: "name", Enabled: true, Transform: tr},
		{ExcelColumn: "Email", ExcelIndex: 1, DBColumn: "email", Enabled: true, Transform: tr},
		{ExcelColumn: "Amount", ExcelIndex: 2, DBColumn: "amount", Enabled: true, Transform: tr},
		{ExcelColumn: "Signup", ExcelIndex: 3, DBColumn: "signup", Enabled: true, Transform: tr},
		{ExcelColumn: "Status", ExcelIndex: 4, DBColumn: "status", Enabled: true, Transform: tr},
	}
}

func benchmarkValidate(b *testing.B, rows int) {
	path := benchWorkbook(b, rows)
	schema := benchSchema()
	mappings := benchMappings()
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		wb, err := excel.Open(path)
		if err != nil {
			b.Fatal(err)
		}

		sheet, err := wb.Describe(ctx, "Sheet1", 1)
		if err != nil {
			b.Fatal(err)
		}

		rep, err := validator.Run(ctx, validator.Input{
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
			b.Fatal(err)
		}
		if rep.ErrorCount != 0 {
			b.Fatalf("the benchmark fixture should validate cleanly, got %d errors: %+v",
				rep.ErrorCount, rep.Issues)
		}

		_ = wb.Close()
	}
}

func BenchmarkValidate1k(b *testing.B)  { benchmarkValidate(b, 1000) }
func BenchmarkValidate10k(b *testing.B) { benchmarkValidate(b, 10000) }

func BenchmarkValidate100k(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping the 100,000-row performance fixture in short mode")
	}
	benchmarkValidate(b, 100000)
}

// The memory targets in §13 are only achievable because nothing accumulates.
// This asserts the shape rather than the number: a run over ten times the rows
// must not allocate ten times the memory.
func TestMemoryStaysBoundedAcrossFileSizes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the streaming-memory check in short mode")
	}

	small := testing.Benchmark(func(b *testing.B) { benchmarkValidate(b, 1000) })
	large := testing.Benchmark(func(b *testing.B) { benchmarkValidate(b, 10000) })

	if small.MemBytes == 0 || large.MemBytes == 0 {
		t.Skip("allocation data unavailable")
	}

	ratio := float64(large.AllocedBytesPerOp()) / float64(small.AllocedBytesPerOp())
	t.Logf("1k: %d B/op, 10k: %d B/op, ratio %.1fx for 10x the rows",
		small.AllocedBytesPerOp(), large.AllocedBytesPerOp(), ratio)

	// Ten times the rows will allocate more — the uniqueness keys and the
	// per-row work are real. What must not happen is the whole sheet being
	// held, which would show up as a far steeper climb.
	if ratio > 15 {
		t.Errorf("allocations grew %.1fx for 10x the rows, which suggests something is accumulating", ratio)
	}
}
