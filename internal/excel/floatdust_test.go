package excel

import (
	"testing"

	"pgsheet/internal/domain"
)

// Excel stores doubles and writes their full binary expansion to the XML,
// while its UI shows at most 15 significant digits. These raw strings are what
// excelize hands over with RawCellValue for a sheet that visibly reads
// 2222.76511, 2225.6987452 and so on.
func TestFloatDustIsTrimmed(t *testing.T) {
	tests := []struct {
		name string
		raw  string // exactly what the .xlsx contains
		want string // what the operator typed, and means
	}{
		{"dust below - the reported failure", "2222.76510999999982232", "2222.76511"},
		{"dust in the step 3 grid", "2225.6987451999998", "2225.6987452"},
		{"dust above", "1234.56780000000003383", "1234.5678"},
		{"formula result", "0.30000000000000004", "0.3"},
		{"repeating dust", "8.699999999999999289", "8.7"},
		{"same value, dusty scale", "2222.7651100000000000", "2222.76511"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseCell(tc.raw, false, false)
			if got.Kind != domain.CellNumber {
				t.Fatalf("kind = %v, want CellNumber", got.Kind)
			}
			if got.Num.String() != tc.want {
				t.Errorf("Num = %s, want %s", got.Num.String(), tc.want)
			}
			// The scale must come down too. String hides trailing zeros but
			// the validator's scale check reads Exponent, so a value that
			// prints correctly can still be rejected as too precise.
			if places := -got.Num.Exponent(); int(places) > len(tc.want) {
				t.Errorf("Exponent = %d, too precise for %s", got.Num.Exponent(), tc.want)
			}
			// Str feeds the step 3 confirmation grid and text-mapped columns.
			if got.Str != tc.want {
				t.Errorf("Str = %s, want %s", got.Str, tc.want)
			}
			// RawText is the file's own bytes and never changes.
			if got.RawText != tc.raw {
				t.Errorf("RawText = %q, want %q", got.RawText, tc.raw)
			}
		})
	}
}

// Cells Excel could represent are left exactly as the file wrote them: the
// trimming must not become a general reformatter.
func TestUntouchedCellsKeepTheirText(t *testing.T) {
	tests := []struct{ name, raw string }{
		{"leading zeros", "00123"},
		{"a declared scale", "1.50"},
		{"exactly 10 decimal places", "0.1234567890"},
		{"15 significant digits", "123456789012345"},
		{"small real precision", "0.0000000001"},
		{"a clean value", "2225.6987452"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseCell(tc.raw, false, false)
			if got.Str != tc.raw {
				t.Errorf("Str = %q, want %q — untouched cells keep their text", got.Str, tc.raw)
			}
			if got.RawText != tc.raw {
				t.Errorf("RawText = %q, want %q", got.RawText, tc.raw)
			}
		})
	}
}

// A long identifier is not a measurement. Excel cannot hold it precisely
// either, but rounding it here would corrupt an account number.
func TestLongIdentifierIsNotRounded(t *testing.T) {
	const id = "12345678901234567890"
	got := parseCell(id, false, false)
	if got.Str != id || got.RawText != id {
		t.Fatalf("text form lost: Str=%q RawText=%q", got.Str, got.RawText)
	}
	if got.Num.String() != id {
		t.Errorf("Num = %s, want %s — integers are never trimmed", got.Num.String(), id)
	}
}
