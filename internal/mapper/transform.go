package mapper

import (
	"strings"
	"time"
	"unicode"

	"pgsheet/internal/domain"
)

// Applied is one cell after its column's transforms. Original is kept so an
// error can quote the cell as the operator sees it, not the transformed form.
type Applied struct {
	Value    domain.CellValue
	Original domain.CellValue
	IsNull   bool
	// DateParseFailed reports that DateFormat was configured and did not match.
	// Coercion turns this into E201 with the layout in the hint; the transform
	// layer does not decide what is an error.
	DateParseFailed bool
}

// DefaultBoolMap covers the words that turn up in client sheets. A configured
// BoolMap replaces it rather than extending it, so an operator can make "1"
// mean false if their data says so. Matched case-insensitively.
var DefaultBoolMap = map[string]bool{
	"true": true, "false": false,
	"t": true, "f": false,
	"yes": true, "no": false,
	"y": true, "n": false,
	"1": true, "0": false,
	"oui": true, "non": false,
	"vrai": true, "faux": false,
	"actif": true, "inactif": false,
	"نعم": true, "لا": false,
	"on": true, "off": false,
}

// commonDateLayouts are tried in order when a string reaches a date target
// with no DateFormat set. ISO first because it is unambiguous, then day-first.
// Anything genuinely ambiguous takes the first layout that fits, which is why
// the UI pushes for an explicit format on dates-as-text.
var commonDateLayouts = []string{
	"2006-01-02",
	"2006/01/02",
	"02-01-2006",
	"02/01/2006",
	"01/02/2006",
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05",
	"02/01/2006 15:04",
	"02/01/2006 15:04:05",
	"2 January 2006",
	"2 Jan 2006",
	"January 2, 2006",
	"Jan 2, 2006",
}

// Apply runs a column's transforms over one cell. The order is not a
// preference: StripNonDigits before Trim, and BlankAsNull before
// DefaultOnBlank, give different answers. Asserted in the tests.
func Apply(cell domain.CellValue, t domain.Transform, target domain.TypeFamily) Applied {
	out := Applied{Value: cell, Original: cell}

	// Non-string kinds pass through untouched unless a transform that can
	// apply to them is configured. A date cell reaching a date column needs no
	// transform at all, and touching it could only lose precision.
	needsString := t.StripNonDigits || t.Trim || t.UpperCase || t.LowerCase ||
		len(t.ValueMap) > 0 || t.BlankAsNull || t.DefaultOnBlank != "" ||
		(target == domain.FamilyBool) ||
		(t.DateFormat != "" && isTemporal(target))

	if cell.Kind == domain.CellError {
		return out // E210; nothing to transform
	}

	// A cell Excel already stored as a date carries no ambiguity to resolve,
	// and rendering it to text so a transform can run over it would introduce
	// some. Transforms exist for text and numbers.
	if cell.Kind == domain.CellDate {
		return out
	}
	if !needsString && cell.Kind != domain.CellEmpty {
		return out
	}

	s := stringView(cell)

	// 1. StripNonDigits
	if t.StripNonDigits {
		s = stripNonDigits(s)
	}

	// 2. Trim — invisible characters were already normalized on read, so this
	//    is the ordinary trim the operator asked for.
	if t.Trim {
		s = strings.TrimSpace(domain.NormalizeInvisible(s))
	}

	// 3. BlankAsNull
	blank := strings.TrimSpace(s) == ""
	if blank && t.BlankAsNull {
		out.Value = domain.CellValue{Kind: domain.CellEmpty, RawText: cell.RawText}
		out.IsNull = true
		return out
	}

	// 4. DefaultOnBlank — only reachable when the blank was not turned into
	//    NULL above, which is why the two are ordered and not exclusive.
	if blank && t.DefaultOnBlank != "" {
		s = t.DefaultOnBlank
		blank = false
	}

	// 5. Case
	if t.UpperCase {
		s = strings.ToUpper(s)
	}
	if t.LowerCase {
		s = strings.ToLower(s)
	}

	// 6. ValueMap — "Actif" to "active". Matched on the normalized key so an
	//    invisible character in the sheet cannot defeat it.
	if len(t.ValueMap) > 0 {
		if mapped, ok := lookupFold(t.ValueMap, domain.NormalizeKey(s)); ok {
			s = mapped
		}
	}

	// 7. BoolMap — boolean targets only.
	if target == domain.FamilyBool {
		if b, ok := lookupBool(t.BoolMap, s); ok {
			out.Value = domain.CellValue{Kind: domain.CellBool, Bool: b, RawText: cell.RawText}
			return out
		}
		if cell.Kind == domain.CellBool {
			return out // already boolean, no mapping needed
		}
	}

	// 8. DateFormat — date and timestamp targets, string input only. A cell
	//    Excel already stored as a date is left alone: it has no ambiguity to
	//    resolve and reparsing its text would introduce some.
	if isTemporal(target) && cell.Kind != domain.CellDate {
		if parsed, ok, attempted := parseDate(s, t.DateFormat); attempted {
			if ok {
				out.Value = domain.CellValue{Kind: domain.CellDate, Time: parsed, RawText: cell.RawText}
				return out
			}
			out.DateParseFailed = true
		}
	}

	if blank && cell.Kind == domain.CellEmpty {
		out.Value = domain.CellValue{Kind: domain.CellEmpty, RawText: cell.RawText}
		out.IsNull = true
		return out
	}

	// A number that had digits stripped is no longer that number, so it does
	// not go back to CellNumber.
	out.Value = domain.CellValue{Kind: domain.CellString, Str: s, RawText: cell.RawText}
	if cell.Kind == domain.CellNumber && !t.StripNonDigits {
		// Untouched numerically: keep the decimal so coercion never has to
		// round-trip through text.
		out.Value.Kind = domain.CellNumber
		out.Value.Num = cell.Num
		out.Value.Str = s
	}
	return out
}

// stringView renders a cell as the text a transform operates on: for numbers
// the raw stored text, so "00123" stays "00123" and a long integer keeps
// every digit.
func stringView(c domain.CellValue) string {
	switch c.Kind {
	case domain.CellString:
		return c.Str
	case domain.CellNumber:
		if c.Str != "" {
			return c.Str
		}
		return c.Num.String()
	case domain.CellBool:
		if c.Bool {
			return "TRUE"
		}
		return "FALSE"
	case domain.CellDate:
		return c.Time.Format(time.RFC3339)
	case domain.CellError:
		return c.RawText
	default:
		return ""
	}
}

// stripNonDigits removes everything that is not a digit, keeping a leading
// plus so an international dialling prefix survives. Used for phone columns,
// where the sheet is full of spaces, dashes and parentheses.
func stripNonDigits(s string) string {
	var b strings.Builder
	b.Grow(len(s))

	// The dialling prefix is kept when it leads the value. "Leading" means
	// before any digit rather than at index zero, because this runs before the
	// trim and the cell often starts with a space.
	seenDigit := false
	for _, r := range s {
		switch {
		case unicode.IsDigit(r):
			seenDigit = true
			b.WriteRune(r)
		case r == '+' && !seenDigit && b.Len() == 0:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func lookupFold(m map[string]string, key string) (string, bool) {
	if v, ok := m[key]; ok {
		return v, true
	}
	lower := strings.ToLower(key)
	for k, v := range m {
		if strings.ToLower(k) == lower {
			return v, true
		}
	}
	return "", false
}

func lookupBool(custom map[string]bool, s string) (bool, bool) {
	key := strings.ToLower(domain.NormalizeKey(s))
	if len(custom) > 0 {
		for k, v := range custom {
			if strings.ToLower(domain.NormalizeKey(k)) == key {
				return v, true
			}
		}
		// A configured BoolMap replaces the default: if the operator listed the
		// words their data uses, an unlisted word is a data problem, not a
		// reason to fall back to English defaults.
		return false, false
	}
	v, ok := DefaultBoolMap[key]
	return v, ok
}

// parseDate reports the time, whether it parsed, and whether a parse was
// attempted at all: "not a date column" and "failed to parse" are different
// errors to the operator.
func parseDate(s string, layout string) (t time.Time, ok bool, attempted bool) {
	s = strings.TrimSpace(domain.NormalizeInvisible(s))
	if s == "" {
		return time.Time{}, false, false
	}
	if layout != "" {
		parsed, err := time.Parse(layout, s)
		return parsed, err == nil, true
	}
	for _, l := range commonDateLayouts {
		if parsed, err := time.Parse(l, s); err == nil {
			return parsed, true, true
		}
	}
	return time.Time{}, false, true
}

func isTemporal(f domain.TypeFamily) bool {
	switch f {
	case domain.FamilyDate, domain.FamilyTimestamp, domain.FamilyTimestampTZ:
		return true
	}
	return false
}
