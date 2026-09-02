package excel

import (
	"testing"

	"github.com/shopspring/decimal"
)

// looksNumeric is a fast path in front of the parser, so the only thing that
// can go wrong is refusing a value the parser would have accepted: a numeric
// cell would silently become text, and its column would then fail to map.
// Everything it lets through is still decided by the parser, so accepting too
// much is free.
func FuzzLooksNumericNeverRefusesANumber(f *testing.F) {
	for _, s := range []string{
		"0", "1", "-1", "+1", "1.5", ".5", "5.", "1e10", "1E-10", "-1.2e+34",
		"00123", "9999999999999999999999999999", "0.000000000000000000001",
		"", " ", "abc", "1,234", "12:30", "TRUE", "١٢٣",
		// Found by this fuzzer against a stricter first attempt: decimal
		// accepts a sign that is neither leading nor part of an exponent.
		".+0", "--1", "1-2", "2024-03-15",
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, s string) {
		if looksNumeric(s) {
			return
		}
		if _, err := decimal.NewFromString(s); err == nil {
			t.Fatalf("looksNumeric refused %q, which decimal parses as a number", s)
		}
	})
}

func TestLooksNumericRefusesTheCommonNonNumbers(t *testing.T) {
	refused := []string{"", "abc", "client1@example.com", "1,234", "12:30", "active", "N/A"}
	for _, s := range refused {
		if looksNumeric(s) {
			t.Errorf("looksNumeric(%q) = true, wanted the parser to be skipped", s)
		}
	}

	// Not a number, but the parser gets to say so: the alphabet is wider than
	// the grammar on purpose.
	for _, s := range []string{"1.2.3", "1e", ".", "2024-03-15"} {
		if !looksNumeric(s) {
			t.Errorf("looksNumeric(%q) = false, wanted the parser to decide", s)
		}
		if _, err := decimal.NewFromString(s); err == nil {
			t.Errorf("decimal parsed %q, so the fixture is wrong", s)
		}
	}
}
