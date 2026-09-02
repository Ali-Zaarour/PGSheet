package domain

import "strings"

// Invisible and look-alike whitespace characters that arrive in spreadsheets
// and break exact matching in ways nobody can see: a value that looks like
// "active" fails enum membership, two "identical" keys are not detected as
// duplicates, a visually empty cell is not empty. Copy-paste from web pages,
// PDFs and Word is the usual source (spec §7).
//
// Written as escapes on purpose: a literal NBSP in this source would be
// invisible in review, which is the whole problem being solved here.
const (
	NoBreakSpace     rune = 0x00A0 // NO-BREAK SPACE
	NarrowNoBreak    rune = 0x202F // NARROW NO-BREAK SPACE
	FigureSpace      rune = 0x2007 // FIGURE SPACE
	ThinSpace        rune = 0x2009 // THIN SPACE
	IdeographicSpace rune = 0x3000 // IDEOGRAPHIC SPACE
	LineSeparator    rune = 0x2028 // LINE SEPARATOR
	ParaSeparator    rune = 0x2029 // PARAGRAPH SEPARATOR

	ZeroWidthSpace   rune = 0x200B // ZERO WIDTH SPACE
	ZeroWidthNonJoin rune = 0x200C // ZERO WIDTH NON-JOINER
	ZeroWidthJoiner  rune = 0x200D // ZERO WIDTH JOINER
	ByteOrderMark    rune = 0xFEFF // ZERO WIDTH NO-BREAK SPACE / BOM
)

// NormalizeInvisible replaces invisible whitespace variants with a plain space
// and removes zero-width characters entirely.
//
// It deliberately does not trim. Trimming is a per-column transform the
// operator controls; this runs unconditionally on read. The distinction
// matters: a cell holding three non-breaking spaces becomes three ordinary
// spaces here, and becomes empty only if the operator asked for Trim.
func NormalizeInvisible(s string) string {
	if !strings.ContainsFunc(s, isInvisible) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case NoBreakSpace, NarrowNoBreak, FigureSpace, ThinSpace,
			IdeographicSpace, LineSeparator, ParaSeparator:
			b.WriteRune(' ')
		case ZeroWidthSpace, ZeroWidthNonJoin, ZeroWidthJoiner, ByteOrderMark:
			// removed entirely
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isInvisible(r rune) bool {
	switch r {
	case NoBreakSpace, NarrowNoBreak, FigureSpace, ThinSpace,
		IdeographicSpace, LineSeparator, ParaSeparator,
		ZeroWidthSpace, ZeroWidthNonJoin, ZeroWidthJoiner, ByteOrderMark:
		return true
	}
	return false
}

// NormalizeKey renders a value for identity comparison: enum membership,
// duplicate detection, value-map lookup. Invisible characters go, the result is
// trimmed, and case is preserved — PostgreSQL compares case-sensitively and
// folding it here would hide real data problems (spec §20).
func NormalizeKey(s string) string {
	return strings.TrimSpace(NormalizeInvisible(s))
}

// NormalizeHeader renders a header for fingerprinting and name matching:
// lowercased, invisible characters removed, internal whitespace collapsed.
func NormalizeHeader(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(NormalizeInvisible(s)), " "))
}

// CompositeKey joins the parts of a multi-column key with a separator that
// cannot appear in normalized cell text, so ("a","bc") and ("ab","c") stay
// distinct.
func CompositeKey(parts []string) string {
	return strings.Join(parts, "\x1f")
}
