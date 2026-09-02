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
