package validator

import (
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"pgsheet/internal/domain"
)

func intPtr(v int) *int { return &v }

func col(name, udt string, opts ...func(*domain.Column)) domain.Column {
	c := domain.Column{
		Name:          name,
		DataType:      udt,
		FormattedType: udt,
		Nullable:      true,
	}
	for _, o := range opts {
		o(&c)
	}
	return c
}

func notNull(c *domain.Column) { c.Nullable = false }
func maxLen(n int) func(*domain.Column) {
	return func(c *domain.Column) { c.MaxLength = intPtr(n) }
}
func numeric(p, s int) func(*domain.Column) {
	return func(c *domain.Column) { c.NumericPrecision = intPtr(p); c.NumericScale = intPtr(s) }
}
func enumOf(values ...string) func(*domain.Column) {
	return func(c *domain.Column) { c.EnumValues = values }
}

func num(s string) domain.CellValue {
	d, err := decimal.NewFromString(s)
	if err != nil {
		panic(err)
	}
	return domain.CellValue{Kind: domain.CellNumber, Num: d, Str: s, RawText: s}
}

func str(s string) domain.CellValue {
	return domain.CellValue{Kind: domain.CellString, Str: s, RawText: s}
}

func empty() domain.CellValue { return domain.CellValue{Kind: domain.CellEmpty} }

var stdOpts = Options{StandardConformingStrings: true, SourceTimezone: time.UTC}

func TestCoerceInteger(t *testing.T) {
	tests := []struct {
		name     string
		cell     domain.CellValue
		column   domain.Column
		want     string
		wantCode string
	}{
		{name: "plain", cell: num("42"), column: col("n", "int4"), want: "42"},
		{name: "negative", cell: num("-7"), column: col("n", "int4"), want: "-7"},
		{name: "text digits", cell: str(" 128 "), column: col("n", "int4"), want: "128"},
		{name: "whole number written with a scale", cell: num("12.00"), column: col("n", "int4"), want: "12"},
		{name: "fractional is rejected", cell: num("12.5"), column: col("n", "int4"), wantCode: "E201"},
		{name: "text is rejected", cell: str("N/A"), column: col("n", "int4"), wantCode: "E201"},
		{name: "int2 upper bound", cell: num("32767"), column: col("n", "int2"), want: "32767"},
		{name: "int2 overflow", cell: num("32768"), column: col("n", "int2"), wantCode: "E205"},
		{name: "int2 underflow", cell: num("-32769"), column: col("n", "int2"), wantCode: "E205"},
		{name: "int4 overflow", cell: num("2147483648"), column: col("n", "int4"), wantCode: "E205"},
		{name: "int8 holds what int4 cannot", cell: num("2147483648"), column: col("n", "int8"), want: "2147483648"},
		{name: "empty into nullable", cell: empty(), column: col("n", "int4"), want: "NULL"},
		{name: "empty into NOT NULL", cell: empty(), column: col("n", "int4", notNull), wantCode: "E202"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Coerce(tt.cell, tt.column, stdOpts)
			assertCoercion(t, got, err, tt.want, tt.wantCode)
		})
	}
}

func TestCoerceNumeric(t *testing.T) {
	tests := []struct {
		name     string
		cell     domain.CellValue
		column   domain.Column
		opts     Options
		want     string
		wantCode string
	}{
		{name: "exact decimal survives", cell: num("15000.00"), column: col("amount", "numeric", numeric(10, 2)), want: "15000.00"},
		{
			// The float64 route would make this 0.30000000000000004.
			name:   "float imprecision does not happen",
			cell:   num("0.30000000000000004"),
			column: col("amount", "numeric"), // unconstrained numeric: no scale to pad to
			want:   "0.30000000000000004",
		},
		{
			// A declared scale is honoured in the output, so a money column
			// shows its cents in the file a human reviews.
			name:   "declared scale is padded",
			cell:   num("15000"),
			column: col("amount", "numeric", numeric(10, 2)),
			want:   "15000.00",
		},
		{name: "thousands separators are stripped", cell: str("1,250.75"), column: col("amount", "numeric", numeric(10, 2)), want: "1250.75"},
		{name: "scale overflow is rejected by default", cell: num("1.005"), column: col("amount", "numeric", numeric(10, 2)), wantCode: "E204"},
		{
			name:   "scale overflow rounds when allowed",
			cell:   num("1.005"),
			column: col("amount", "numeric", numeric(10, 2)),
			opts:   Options{StandardConformingStrings: true, AllowNumericRounding: true},
			want:   "1.01",
		},
		{name: "too many digits before the point", cell: num("123456789"), column: col("amount", "numeric", numeric(10, 2)), wantCode: "E204"},
		{name: "currency symbol is a data problem", cell: str("$15.00"), column: col("amount", "numeric", numeric(10, 2)), wantCode: "E201"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := tt.opts
			if opts == (Options{}) {
				opts = stdOpts
			}
			got, err := Coerce(tt.cell, tt.column, opts)
			assertCoercion(t, got, err, tt.want, tt.wantCode)
		})
	}
}

func TestCoerceTextLengthIsCountedInRunes(t *testing.T) {
	// Ten Arabic characters are ten characters to PostgreSQL, not twenty bytes.
	arabic := strings.Repeat("ب", 10)

	if _, err := Coerce(str(arabic), col("name", "varchar", maxLen(10)), stdOpts); err != nil {
		t.Fatalf("10 runes into varchar(10) was rejected: %v", err.Message)
	}

	_, err := Coerce(str(arabic+"ب"), col("name", "varchar", maxLen(10)), stdOpts)
	if err == nil || err.Code != "E203" {
		t.Fatalf("11 runes into varchar(10): got %v, want E203", err)
	}
}

func TestCoerceTextRejectsNulByte(t *testing.T) {
	_, err := Coerce(str("bad\x00value"), col("notes", "text"), stdOpts)
	if err == nil || err.Code != "E211" {
		t.Fatalf("NUL byte: got %v, want E211", err)
	}
}

func TestCoerceTextEscapesQuotes(t *testing.T) {
	got, err := Coerce(str("O'Brien"), col("name", "text"), stdOpts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err.Message)
	}
	if got.Literal != `'O''Brien'` {
		t.Errorf("literal = %s, want 'O''Brien'", got.Literal)
	}
	if got.Text != "O'Brien" {
		t.Errorf("text = %q, want the original for comparison purposes", got.Text)
	}
}

func TestCoerceEnum(t *testing.T) {
	status := col("status", "customer_status", enumOf("active", "inactive", "suspended"))

	if _, err := Coerce(str("active"), status, stdOpts); err != nil {
		t.Fatalf("exact label rejected: %v", err.Message)
	}

	// Case sensitivity is the default: PostgreSQL enums are case-sensitive and
	// folding would hide a real data problem (spec §20).
	if _, err := Coerce(str("Active"), status, stdOpts); err == nil || err.Code != "E206" {
		t.Fatalf("wrong-case label: got %v, want E206", err)
	}

	insensitive := stdOpts
	insensitive.EnumCaseInsensitive = true
	got, err := Coerce(str("Active"), status, insensitive)
	if err != nil {
		t.Fatalf("case-insensitive match rejected: %v", err.Message)
	}
	if got.Text != "active" {
		t.Errorf("normalized to %q, want the label as the database spells it", got.Text)
	}

	// An invisible character must not silently defeat membership.
	if _, err := Coerce(str("active​"), status, stdOpts); err != nil {
		t.Errorf("zero-width space defeated enum matching: %v", err.Message)
	}
}

func TestCoerceBool(t *testing.T) {
	b := col("flag", "bool")
	for _, tt := range []struct {
		in   domain.CellValue
		want string
	}{
		{domain.CellValue{Kind: domain.CellBool, Bool: true}, "TRUE"},
		{domain.CellValue{Kind: domain.CellBool, Bool: false}, "FALSE"},
		{num("1"), "TRUE"},
		{num("0"), "FALSE"},
		{str("yes"), "TRUE"},
		{str("Oui"), "TRUE"},
		{str("NON"), "FALSE"},
	} {
		got, err := Coerce(tt.in, b, stdOpts)
		if err != nil {
			t.Errorf("%v: unexpected error %s", tt.in, err.Message)
			continue
		}
		if got.Literal != tt.want {
			t.Errorf("%v -> %s, want %s", tt.in, got.Literal, tt.want)
		}
	}

	if _, err := Coerce(num("2"), b, stdOpts); err == nil {
		t.Error("2 was accepted as a boolean")
	}
	if _, err := Coerce(str("maybe"), b, stdOpts); err == nil {
		t.Error("\"maybe\" was accepted as a boolean")
	}
}

func TestCoerceDateAndTimestamps(t *testing.T) {
	when := time.Date(2024, 3, 15, 14, 22, 0, 0, time.UTC)
	dateCell := domain.CellValue{Kind: domain.CellDate, Time: when, RawText: "45366"}

	got, err := Coerce(dateCell, col("d", "date"), stdOpts)
	if err != nil {
		t.Fatalf("date: %v", err.Message)
	}
	if got.Literal != `'2024-03-15'::date` {
		t.Errorf("date literal = %s", got.Literal)
	}

	got, err = Coerce(dateCell, col("ts", "timestamp"), stdOpts)
	if err != nil {
		t.Fatalf("timestamp: %v", err.Message)
	}
	if !strings.HasSuffix(got.Literal, "::timestamp") || !strings.Contains(got.Literal, "2024-03-15 14:22:00") {
		t.Errorf("timestamp literal = %s", got.Literal)
	}

	// A timestamptz literal must carry an explicit offset, or the file means
	// different instants on servers with different TimeZone settings.
	beirut, err2 := time.LoadLocation("Asia/Beirut")
	if err2 != nil {
		t.Skip("timezone database unavailable")
	}
	tzOpts := stdOpts
	tzOpts.SourceTimezone = beirut
	got, err = Coerce(dateCell, col("tstz", "timestamptz"), tzOpts)
	if err != nil {
		t.Fatalf("timestamptz: %v", err.Message)
	}
	if !strings.HasSuffix(got.Literal, "::timestamptz") {
		t.Fatalf("timestamptz literal has no cast: %s", got.Literal)
	}
	if !strings.Contains(got.Literal, "+02:00") && !strings.Contains(got.Literal, "+03:00") {
		t.Errorf("timestamptz literal has no explicit offset: %s", got.Literal)
	}
}

func TestCoerceRejectsBareNumberIntoDate(t *testing.T) {
	// A number reaching a date column means the cell was not formatted as a
	// date. Converting it silently would invent a date from a quantity.
	_, err := Coerce(num("45366"), col("d", "date"), stdOpts)
	if err == nil || err.Code != "E201" {
		t.Fatalf("bare number into date: got %v, want E201", err)
	}
}

func TestCoerceUUID(t *testing.T) {
	got, err := Coerce(str("3F2504E0-4F89-11D3-9A0C-0305E82C3301"), col("id", "uuid"), stdOpts)
	if err != nil {
		t.Fatalf("uuid: %v", err.Message)
	}
	if got.Text != "3f2504e0-4f89-11d3-9a0c-0305e82c3301" {
		t.Errorf("uuid not normalized: %s", got.Text)
	}

	if _, err := Coerce(str("not-a-uuid"), col("id", "uuid"), stdOpts); err == nil || err.Code != "E207" {
		t.Fatalf("invalid uuid: got %v, want E207", err)
	}
}

func TestCoerceJSON(t *testing.T) {
	if _, err := Coerce(str(`{"a":1}`), col("doc", "jsonb"), stdOpts); err != nil {
		t.Fatalf("valid json rejected: %v", err.Message)
	}
	if _, err := Coerce(str(`{"a":1,}`), col("doc", "jsonb"), stdOpts); err == nil || err.Code != "E208" {
		t.Fatalf("trailing comma: got %v, want E208", err)
	}
	if _, err := Coerce(str("plain text"), col("doc", "json"), stdOpts); err == nil || err.Code != "E208" {
		t.Fatalf("plain text into json: got %v, want E208", err)
	}
}

func TestCoerceErrorCell(t *testing.T) {
	cell := domain.CellValue{Kind: domain.CellError, Str: "#N/A", RawText: "#N/A"}
	_, err := Coerce(cell, col("n", "int4"), stdOpts)
	if err == nil || err.Code != "E210" {
		t.Fatalf("error cell: got %v, want E210", err)
	}
}

func TestCoerceUnsupportedType(t *testing.T) {
	_, err := Coerce(str("x"), col("blob", "bytea"), stdOpts)
	if err == nil || err.Code != "E106" {
		t.Fatalf("bytea: got %v, want E106", err)
	}
}

func assertCoercion(t *testing.T, got Coerced, err *CellError, want, wantCode string) {
	t.Helper()

	if wantCode != "" {
		if err == nil {
			t.Fatalf("got literal %q, want error %s", got.Literal, wantCode)
		}
		if err.Code != wantCode {
			t.Fatalf("got %s (%s), want %s", err.Code, err.Message, wantCode)
		}
		return
	}

	if err != nil {
		t.Fatalf("unexpected %s: %s", err.Code, err.Message)
	}
	if want == "NULL" {
		if !got.IsNull {
			t.Fatalf("got %q, want NULL", got.Literal)
		}
		return
	}
	if got.Literal != want {
		t.Fatalf("got %q, want %q", got.Literal, want)
	}
}

func TestCoerceBlankIntoNotNullWithADefault(t *testing.T) {
	// The database default cannot rescue this: it applies when the column is
	// left out of the insert, and this column is in the column list.
	withDefault := col("status", "text", notNull)
	withDefault.HasDefault = true
	withDefault.DefaultExpr = "'active'"

	_, err := Coerce(empty(), withDefault, stdOpts)
	if err == nil {
		t.Fatal("a blank into a NOT NULL column with a default was accepted as NULL")
	}
	if err.Code != "E202" {
		t.Fatalf("got %s, want E202", err.Code)
	}
	if !strings.Contains(err.Hint, "left out of the insert") {
		t.Errorf("the hint does not explain why the default does not apply: %q", err.Hint)
	}
}
