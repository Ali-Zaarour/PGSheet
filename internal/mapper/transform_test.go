package mapper

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"pgsheet/internal/domain"
)

func str(s string) domain.CellValue {
	return domain.CellValue{Kind: domain.CellString, Str: s, RawText: s}
}

func num(s string) domain.CellValue {
	d, _ := decimal.NewFromString(s)
	return domain.CellValue{Kind: domain.CellNumber, Num: d, Str: s, RawText: s}
}

func empty() domain.CellValue { return domain.CellValue{Kind: domain.CellEmpty} }

func TestApplyTrim(t *testing.T) {
	got := Apply(str("  Acme SARL  "), domain.Transform{Trim: true}, domain.FamilyText)
	if got.Value.Str != "Acme SARL" {
		t.Errorf("got %q, want %q", got.Value.Str, "Acme SARL")
	}
	if got.Original.Str != "  Acme SARL  " {
		t.Error("the original must be kept so error messages can quote what the operator sees")
	}
}

func TestApplyTrimHandlesInvisibleWhitespace(t *testing.T) {
	// A cell holding a non-breaking space looks empty and is not.
	got := Apply(str(" ​Acme "), domain.Transform{Trim: true}, domain.FamilyText)
	if got.Value.Str != "Acme" {
		t.Errorf("got %q, want %q", got.Value.Str, "Acme")
	}
}

func TestApplyBlankAsNull(t *testing.T) {
	got := Apply(str("   "), domain.Transform{Trim: true, BlankAsNull: true}, domain.FamilyText)
	if !got.IsNull {
		t.Error("a whitespace-only cell with BlankAsNull should become NULL")
	}

	got = Apply(str("   "), domain.Transform{Trim: true}, domain.FamilyText)
	if got.IsNull {
		t.Error("without BlankAsNull the cell must stay an empty string, not become NULL")
	}
}

// The order in spec §8 is not a preference: these two cases give different
// answers under any other arrangement, which is why the order is pinned.
func TestTransformOrderIsFixed(t *testing.T) {
	t.Run("BlankAsNull wins over DefaultOnBlank", func(t *testing.T) {
		got := Apply(str("  "), domain.Transform{
			Trim:           true,
			BlankAsNull:    true,
			DefaultOnBlank: "unknown",
		}, domain.FamilyText)

		if !got.IsNull {
			t.Fatalf("got %q, want NULL: BlankAsNull is applied before DefaultOnBlank", got.Value.Str)
		}
	})

	t.Run("StripNonDigits runs before Trim", func(t *testing.T) {
		// Stripping first turns "+961 3 123 456" into "+9613123456"; trimming
		// first would leave the internal spaces for the stripper anyway, but
		// the reverse order on a value like " 12 " would produce "12" either
		// way — the observable difference is that stripping never has to cope
		// with a value the trim already changed.
		got := Apply(str(" +961 3 123-456 "), domain.Transform{
			StripNonDigits: true,
			Trim:           true,
		}, domain.FamilyText)

		if got.Value.Str != "+9613123456" {
			t.Errorf("got %q, want %q", got.Value.Str, "+9613123456")
		}
	})

	t.Run("case folding runs before ValueMap", func(t *testing.T) {
		// The map is written in the folded form, so folding must happen first
		// or the lookup misses.
		got := Apply(str("Actif"), domain.Transform{
			Trim:      true,
			LowerCase: true,
			ValueMap:  map[string]string{"actif": "active"},
		}, domain.FamilyText)

		if got.Value.Str != "active" {
			t.Errorf("got %q, want %q", got.Value.Str, "active")
		}
	})
}

func TestApplyValueMap(t *testing.T) {
	tr := domain.Transform{Trim: true, ValueMap: map[string]string{"Actif": "active", "Inactif": "inactive"}}

	if got := Apply(str("Actif"), tr, domain.FamilyText); got.Value.Str != "active" {
		t.Errorf("got %q, want active", got.Value.Str)
	}
	// An unmapped value passes through: the value map is a translation table,
	// not a whitelist. Membership is the enum check's job.
	if got := Apply(str("Suspendu"), tr, domain.FamilyText); got.Value.Str != "Suspendu" {
		t.Errorf("got %q, want the value unchanged", got.Value.Str)
	}
}

func TestApplyBoolMap(t *testing.T) {
	got := Apply(str("Oui"), domain.Transform{Trim: true}, domain.FamilyBool)
	if got.Value.Kind != domain.CellBool || !got.Value.Bool {
		t.Errorf("Oui should map to true through the default word list, got %+v", got.Value)
	}

	// A configured map replaces the defaults: if the operator listed the words
	// their data uses, an unlisted word is a data problem.
	custom := domain.Transform{Trim: true, BoolMap: map[string]bool{"1": true, "2": false}}
	got = Apply(str("yes"), custom, domain.FamilyBool)
	if got.Value.Kind == domain.CellBool {
		t.Error("a configured BoolMap must not fall back to the English defaults")
	}
	got = Apply(str("2"), custom, domain.FamilyBool)
	if got.Value.Kind != domain.CellBool || got.Value.Bool {
		t.Errorf("2 should map to false through the configured map, got %+v", got.Value)
	}
}

func TestApplyDateFormat(t *testing.T) {
	tr := domain.Transform{Trim: true, DateFormat: "02/01/2006"}

	got := Apply(str("15/03/2024"), tr, domain.FamilyDate)
	if got.Value.Kind != domain.CellDate {
		t.Fatalf("expected a date, got %+v", got.Value)
	}
	want := time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC)
	if !got.Value.Time.Equal(want) {
		t.Errorf("got %v, want %v", got.Value.Time, want)
	}

	// A value that does not match the declared layout is reported, not guessed
	// at with a different layout.
	got = Apply(str("March 2024"), tr, domain.FamilyDate)
	if !got.DateParseFailed {
		t.Error("a value that does not match the configured layout must be flagged")
	}
}

func TestApplyLeavesExcelDatesAlone(t *testing.T) {
	when := time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC)
	cell := domain.CellValue{Kind: domain.CellDate, Time: when, RawText: "45366"}

	got := Apply(cell, domain.Transform{Trim: true, DateFormat: "02/01/2006"}, domain.FamilyDate)
	if got.Value.Kind != domain.CellDate || !got.Value.Time.Equal(when) {
		t.Errorf("a cell Excel already stored as a date must not be reparsed: got %+v", got.Value)
	}
}

func TestApplyKeepsNumericPrecision(t *testing.T) {
	// The raw text is what carries the precision; a round trip through float64
	// is what loses it.
	got := Apply(num("1234567890123456789"), domain.Transform{Trim: true}, domain.FamilyNumeric)
	if got.Value.Str != "1234567890123456789" {
		t.Errorf("got %q, want the digits intact", got.Value.Str)
	}
	if got.Value.Kind != domain.CellNumber {
		t.Error("an untouched number must stay a number so coercion never re-parses it")
	}
}

func TestApplyPreservesLeadingZeros(t *testing.T) {
	// "00123" is a code, not the number 123, and the raw value is what says so.
	cell := domain.CellValue{Kind: domain.CellString, Str: "00123", RawText: "00123"}
	got := Apply(cell, domain.Transform{Trim: true}, domain.FamilyText)
	if got.Value.Str != "00123" {
		t.Errorf("got %q, want 00123", got.Value.Str)
	}
}

func TestApplyEmptyCell(t *testing.T) {
	got := Apply(empty(), domain.Transform{Trim: true, BlankAsNull: true}, domain.FamilyText)
	if !got.IsNull {
		t.Error("an empty cell with BlankAsNull should be NULL")
	}

	got = Apply(empty(), domain.Transform{Trim: true, DefaultOnBlank: "N/A"}, domain.FamilyText)
	if got.Value.Str != "N/A" {
		t.Errorf("got %q, want the configured default", got.Value.Str)
	}
}

func TestApplyErrorCellIsUntouched(t *testing.T) {
	cell := domain.CellValue{Kind: domain.CellError, Str: "#N/A", RawText: "#N/A"}
	got := Apply(cell, domain.Transform{Trim: true, BlankAsNull: true}, domain.FamilyText)
	if got.Value.Kind != domain.CellError {
		t.Error("an Excel error cell must reach coercion as an error, not be transformed into data")
	}
}
