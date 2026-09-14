package excel

import (
	"errors"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"github.com/xuri/excelize/v2"

	"pgsheet/internal/domain"
)

// Number format ids that mean the value is a date or time. Excel stores dates
// as serial numbers; only the cell's format makes 45231 a date.
var builtinDateFormats = map[int]bool{
	14: true, 15: true, 16: true, 17: true, 18: true, 19: true,
	20: true, 21: true, 22: true,
	45: true, 46: true, 47: true,
}

// excelErrorValues reach us as text and must never be treated as data.
var excelErrorValues = map[string]bool{
	"#N/A":             true,
	"#REF!":            true,
	"#DIV/0!":          true,
	"#VALUE!":          true,
	"#NAME?":           true,
	"#NULL!":           true,
	"#NUM!":            true,
	"#GETTING_DATA":    true,
	"#SPILL!":          true,
	"#CALC!":           true,
	"#CONNECT!":        true,
	"#BLOCKED!":        true,
	"#UNKNOWN!":        true,
	"#FIELD!":          true,
	"#EXTERNAL_ERROR!": true,
}

// The 1900 leap-year bug: serial 60 is 29 February 1900, a date that never
// existed, so every serial below it maps one day earlier in excelize than the
// date Excel displays. The library does not compensate, so the reader does.
// Found by the regression test on 1900-02-28 and 1900-03-01.
const (
	phantomSerial      = 60
	phantomDateMessage = "29 February 1900, a date Excel has and the calendar does not"
)

// correctLeapBug shifts pre-1 March 1900 serials forward by the day excelize
// does not account for, so the value matches what the operator sees in Excel.
func correctLeapBug(t time.Time, serial float64, date1904 bool) time.Time {
	if date1904 || serial >= phantomSerial {
		return t
	}
	return t.AddDate(0, 0, 1)
}

// isDateNumberFormat reports whether a format means a date or time. Custom
// formats have no id, so the code is inspected: y, d or h is temporal, while m
// alone is ambiguous (it is minutes in "h:mm").
func isDateNumberFormat(numFmtID int, code string) bool {
	if builtinDateFormats[numFmtID] {
		return true
	}
	if code == "" {
		return false
	}

	// Strip quoted literals and colour/condition sections so text like
	// "Day" or [Red] cannot be mistaken for format markers.
	cleaned := stripFormatLiterals(code)
	lower := strings.ToLower(cleaned)

	hasY := strings.Contains(lower, "y")
	hasD := strings.Contains(lower, "d")
	hasH := strings.Contains(lower, "h")
	hasS := strings.Contains(lower, "s")
	hasM := strings.Contains(lower, "m")

	if hasY || hasD || hasH {
		return true
	}
	// "mm:ss" is a duration format — still temporal.
	return hasM && hasS
}

func stripFormatLiterals(code string) string {
	var b strings.Builder
	inQuote, inBracket := false, false
	for i := 0; i < len(code); i++ {
		c := code[i]
		switch {
		case c == '"':
			inQuote = !inQuote
		case c == '[':
			inBracket = true
		case c == ']':
			inBracket = false
		case c == '\\' && i+1 < len(code):
			i++ // escaped literal character
		case !inQuote && !inBracket:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// parseCell normalizes one raw cell. raw is what the workbook stores, not what
// Excel displays: "00123" stays "00123" and a long integer keeps every digit.
func parseCell(raw string, isDateColumn bool, date1904 bool) domain.CellValue {
	normalized := domain.NormalizeInvisible(raw)
	trimmed := strings.TrimSpace(normalized)

	if trimmed == "" {
		return domain.CellValue{Kind: domain.CellEmpty, RawText: raw}
	}

	if excelErrorValues[trimmed] {
		return domain.CellValue{Kind: domain.CellError, Str: normalized, RawText: raw}
	}

	// Booleans are stored as 1/0 with a boolean cell type; excelize renders
	// them as TRUE/FALSE even in raw mode.
	switch normalized {
	case "TRUE":
		return domain.CellValue{Kind: domain.CellBool, Bool: true, RawText: raw}
	case "FALSE":
		return domain.CellValue{Kind: domain.CellBool, Bool: false, RawText: raw}
	}

	if d, err := parseNumber(trimmed); err == nil {
		if isDateColumn {
			// The serial only means a date because the cell's format says so.
			f, _ := d.Float64()

			if !date1904 && f >= phantomSerial && f < phantomSerial+1 {
				// Serial 60 is 29 February 1900 — a day Excel has and the
				// calendar does not. It cannot be stored, and quietly shifting
				// it to the 28th or the 1st would invent a date.
				return domain.CellValue{
					Kind:    domain.CellError,
					Str:     phantomDateMessage,
					Num:     d,
					RawText: raw,
				}
			}

			if t, err := excelize.ExcelDateToTime(f, date1904); err == nil {
				return domain.CellValue{
					Kind:    domain.CellDate,
					Time:    correctLeapBug(t, f, date1904),
					Num:     d,
					Str:     normalized,
					RawText: raw,
				}
			}
		}
		// Trim the binary expansion Excel wrote on save. When trimming changes
		// the text, that text *was* the expansion, so the normalized form is
		// replaced with it: the confirmation grid and any text-mapped column
		// must show the typed value, not the artefact. Cells that are left
		// alone keep their text exactly, so "00123" keeps its leading zeros.
		// RawText is always the file's own bytes, whichever branch is taken.
		// Compare the scale too, not just the value: String reports 2222.76511
		// for a cell written as 2222.7651100000000000, but the decimal keeps
		// exponent -16, and the scale check downstream counts the exponent.
		if t := excelPrecision(d); t.Exponent() != d.Exponent() || !t.Equal(d) {
			d = t
			normalized = t.String()
		}
		return domain.CellValue{Kind: domain.CellNumber, Num: d, Str: normalized, RawText: raw}
	}

	return domain.CellValue{Kind: domain.CellString, Str: normalized, RawText: raw}
}

// parseNumber parses a number, but only after a cheap look at the characters.
//
// Every cell of every text column used to reach decimal.NewFromString, and a
// failed parse there allocates an error that is thrown away: three objects per
// cell, several million on a real file. looksNumeric costs one pass over the
// bytes and no allocation.
func parseNumber(s string) (decimal.Decimal, error) {
	if !looksNumeric(s) {
		return decimal.Decimal{}, errNotNumeric
	}
	return decimal.NewFromString(s)
}

var errNotNumeric = errors.New("not a number")

// excelSignificantDigits is the precision Excel actually guarantees. It stores
// numbers as IEEE-754 doubles and its UI renders at most 15 significant
// digits, but it writes the full binary expansion into the XML. A sheet
// showing 2222.76511 saves as 2222.76510999999982232, and RawCellValue hands
// that to us verbatim — correct, and not what the operator typed. Digits past
// the 15th are an artefact of binary representation, never source data.
const excelSignificantDigits = 15

// excelPrecision recovers the value the operator actually entered.
//
// It is not a rounding policy. Excel cannot hold more than 15 significant
// digits of intent, so any digit past the 15th was manufactured by the binary
// expansion when the file was saved and was never data. Discarding those
// digits returns the typed value exactly: 2222.76510999999982232 is the double
// nearest 2222.76511, and 2222.76511 is what comes back.
//
// Two kinds of cell are left strictly alone:
//
//   - anything with no fractional part, so a long account number keeps every
//     digit even when it exceeds what Excel could represent;
//   - anything already at or under 15 significant digits, which is the typed
//     value with nothing to recover.
func excelPrecision(d decimal.Decimal) decimal.Decimal {
	if d.Exponent() >= 0 {
		return d // an integer: not a measurement, never trimmed
	}
	if d.NumDigits() <= excelSignificantDigits {
		return d // already exactly what was typed
	}

	// Digits to keep after the point, so that 15 significant digits remain in
	// total. Clamped at zero: a value whose integer part alone is longer than
	// 15 digits keeps that part whole rather than having it rounded away.
	integerDigits := d.NumDigits() + int(d.Exponent())
	places := excelSignificantDigits - integerDigits
	if places < 0 {
		places = 0
	}

	return stripTrailingZeros(d.Round(int32(places)))
}

// stripTrailingZeros removes the padding Round leaves behind. It matters:
// Round(11) on 2222.76510999999982232 yields 2222.76511000000, whose exponent
// is still -11, so a numeric(15,10) column would keep rejecting it.
func stripTrailingZeros(d decimal.Decimal) decimal.Decimal {
	s := d.String()
	if !strings.Contains(s, ".") {
		return d
	}
	s = strings.TrimRight(s, "0")
	s = strings.TrimSuffix(s, ".")
	out, err := decimal.NewFromString(s)
	if err != nil {
		return d
	}
	return out
}

// looksNumeric reports whether s is made only of characters a number can be
// made of. It accepts far more than the parser does ("1.2.3", "--1", "e"), and
// that is the point: anything it lets through is still decided by the parser,
// while refusing a value the parser would have accepted would misread a
// numeric cell as text. The alphabet stays wider than the grammar.
//
// An earlier version also required signs to be leading or after an exponent,
// which reads well and is wrong: decimal accepts ".+0". The fuzz test found it.
func looksNumeric(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c >= '0' && c <= '9',
			c == '.', c == 'e', c == 'E', c == '+', c == '-':
		default:
			return false
		}
	}
	return true
}
