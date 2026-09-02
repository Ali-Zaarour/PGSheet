package sqlgen

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// ErrNulByte means a NUL reached the escaper. PostgreSQL cannot store one at
// all, so validation rejects it (E211) where the operator gets a row and
// column; reaching here is a caller bug, not bad input.
type ErrNulByte struct{ Context string }

func (e *ErrNulByte) Error() string {
	return "NUL byte in " + e.Context + ": must be rejected by validation (E211), not escaped"
}

// ErrInvalidUTF8 means the value is not valid UTF-8.
//
// Ranging over a Go string decodes it, and an invalid byte decodes to U+FFFD.
// Writing that out would store a different value than the sheet held, quietly,
// which is worse than refusing it: a UTF-8 database would reject the byte
// anyway, and the operator would never learn the value had been altered.
type ErrInvalidUTF8 struct{ Context string }

func (e *ErrInvalidUTF8) Error() string {
	return "invalid UTF-8 in " + e.Context + ": must be rejected by validation (E212), not escaped"
}

// QuoteLiteral renders s as a PostgreSQL string literal, mirroring libpq's
// PQescapeLiteral. This is the one place a bug becomes SQL injection in a file
// someone runs against production, so it is simple and exhaustively fuzzed.
//
// standardConformingStrings comes from the server probe, never an assumption:
// when off, a backslash escapes inside a plain literal and the string needs
// the E form with backslashes doubled.
func QuoteLiteral(s string, standardConformingStrings bool) (string, error) {
	if strings.ContainsRune(s, 0) {
		return "", &ErrNulByte{Context: "string literal"}
	}
	// Ranging over the string below decodes it, and an invalid byte decodes to
	// U+FFFD. Writing that out would store a different value than the sheet
	// held, without saying so.
	if !utf8.ValidString(s) {
		return "", &ErrInvalidUTF8{Context: "string literal"}
	}

	hasBackslash := strings.ContainsRune(s, '\\')

	var b strings.Builder
	// Worst case every rune doubles, plus the quotes and a possible E prefix.
	b.Grow(len(s)*2 + 3)

	if hasBackslash && !standardConformingStrings {
		// A leading space keeps the E literal from fusing with a preceding
		// identifier or operator, exactly as PQescapeLiteral does.
		b.WriteString(" E")
	}

	b.WriteByte('\'')
	for _, r := range s {
		switch r {
		case '\'':
			b.WriteString("''")
		case '\\':
			if standardConformingStrings {
				b.WriteRune(r)
			} else {
				b.WriteString(`\\`)
			}
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('\'')

	return b.String(), nil
}

// MustQuoteLiteral is QuoteLiteral for validated data. It panics rather than
// silently emitting something wrong.
func MustQuoteLiteral(s string, standardConformingStrings bool) string {
	out, err := QuoteLiteral(s, standardConformingStrings)
	if err != nil {
		panic(err)
	}
	return out
}

// QuoteIdentifier quotes an identifier: always quote, double any embedded
// quote. Same rule as pgx.Identifier.Sanitize, without needing the driver.
func QuoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// QualifiedIdentifier renders schema.name with both parts quoted.
func QualifiedIdentifier(schema, name string) string {
	if schema == "" {
		return QuoteIdentifier(name)
	}
	return QuoteIdentifier(schema) + "." + QuoteIdentifier(name)
}

// QuoteComment makes operator text safe inside the header comment. A newline
// in a file name would end the comment and turn the rest of the line into
// executable SQL. Everything else stays readable; the header is meant to be
// read.
func QuoteComment(s string) string {
	replacer := strings.NewReplacer(
		"\r\n", " ",
		"\n", " ",
		"\r", " ",
		"\x00", "",
	)
	out := replacer.Replace(s)
	if !utf8.ValidString(out) {
		out = strings.ToValidUTF8(out, "?")
	}
	return out
}

// copyEscaper handles COPY text format: no quotes, backslash escapes for the
// delimiters, \N for NULL. Beside QuoteLiteral so the difference is visible,
// but a separate path with its own tests.
var copyEscaper = strings.NewReplacer(
	`\`, `\\`,
	"\t", `\t`,
	"\n", `\n`,
	"\r", `\r`,
)

// CopyNull is the text-format representation of NULL in a COPY stream.
const CopyNull = `\N`

// EscapeCopyField renders one field of a COPY text-format line.
func EscapeCopyField(s string) (string, error) {
	if strings.ContainsRune(s, 0) {
		return "", &ErrNulByte{Context: "COPY field"}
	}
	if !utf8.ValidString(s) {
		return "", &ErrInvalidUTF8{Context: "COPY field"}
	}
	return copyEscaper.Replace(s), nil
}

// EnumCast renders an enum cast, qualified by schema. Casts are not optional:
// without them an unqualified enum resolves through the runner's search_path
// and a bare date through their DateStyle.
func EnumCast(schema, typeName string) string {
	if schema == "" {
		return "::" + QuoteIdentifier(typeName)
	}
	return fmt.Sprintf("::%s.%s", QuoteIdentifier(schema), QuoteIdentifier(typeName))
}
