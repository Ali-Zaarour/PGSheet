package sqlgen

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestQuoteLiteral(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		scs     bool // standard_conforming_strings
		want    string
		wantErr bool
	}{
		{name: "empty", in: "", scs: true, want: `''`},
		{name: "plain", in: "Acme SARL", scs: true, want: `'Acme SARL'`},
		{name: "apostrophe", in: "O'Brien", scs: true, want: `'O''Brien'`},
		{name: "two apostrophes", in: "''", scs: true, want: `''''''`},
		{name: "classic injection attempt", in: "'; DROP TABLE customers; --", scs: true,
			want: `'''; DROP TABLE customers; --'`},
		{name: "backslash with scs on", in: `C:\temp`, scs: true, want: `'C:\temp'`},
		{name: "backslash with scs off", in: `C:\temp`, scs: false, want: ` E'C:\\temp'`},
		{name: "backslash quote with scs off", in: `\'`, scs: false, want: ` E'\\'''`},
		{name: "no backslash keeps plain form with scs off", in: "plain", scs: false, want: `'plain'`},
		{name: "newline is data, not structure", in: "line1\nline2", scs: true, want: "'line1\nline2'"},
		{name: "unicode passes through", in: "Ahmad — بيروت", scs: true, want: `'Ahmad — بيروت'`},
		{name: "nul rejected", in: "bad\x00value", scs: true, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := QuoteLiteral(tt.in, tt.scs)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("QuoteLiteral(%q) = %q, want error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("QuoteLiteral(%q): %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("QuoteLiteral(%q, scs=%v)\n got %q\nwant %q", tt.in, tt.scs, got, tt.want)
			}
		})
	}
}

// unquote reverses QuoteLiteral the way PostgreSQL's lexer would, so a round
// trip can be asserted without a database. The integration suite runs the same
// property against a real server (spec §16); this keeps it enforced in the
// unit suite, where it runs on every commit.
func unquote(t *testing.T, quoted string) string {
	t.Helper()

	s := quoted
	escapeForm := false
	if strings.HasPrefix(s, " E'") {
		escapeForm = true
		s = strings.TrimPrefix(s, " E")
	}
	if !strings.HasPrefix(s, "'") || !strings.HasSuffix(s, "'") || len(s) < 2 {
		t.Fatalf("not a quoted literal: %q", quoted)
	}
	s = s[1 : len(s)-1]

	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '\'' && i+1 < len(s) && s[i+1] == '\'':
			b.WriteByte('\'')
			i++
		case escapeForm && s[i] == '\\' && i+1 < len(s) && s[i+1] == '\\':
			b.WriteByte('\\')
			i++
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

func TestQuoteLiteralRoundTrip(t *testing.T) {
	inputs := []string{
		"", "simple", "O'Brien", `back\slash`, `both'\ kinds`,
		"'; DELETE FROM t; --", `\'; DELETE FROM t; --`,
		"tab\there", "newline\nhere", "quote\"here",
		"بيروت", "日本語", "emoji 🙂", strings.Repeat("'", 50),
	}
	for _, scs := range []bool{true, false} {
		for _, in := range inputs {
			quoted, err := QuoteLiteral(in, scs)
			if err != nil {
				t.Fatalf("QuoteLiteral(%q): %v", in, err)
			}
			if got := unquote(t, quoted); got != in {
				t.Errorf("round trip scs=%v: %q -> %q -> %q", scs, in, quoted, got)
			}
		}
	}
}

// FuzzQuoteLiteral is the highest-value test in the suite (spec §16): whatever
// goes in must come back out unchanged, and the result must always be a single
// balanced literal.
func FuzzQuoteLiteral(f *testing.F) {
	for _, seed := range []string{"", "a", "'", `\`, `'\''`, "'; DROP TABLE t; --", "بيروت"} {
		f.Add(seed, true)
		f.Add(seed, false)
	}

	f.Fuzz(func(t *testing.T, in string, scs bool) {
		// Two things PostgreSQL cannot hold in a text value, both of which have
		// to be refused rather than altered on the way out.
		storable := !strings.ContainsRune(in, 0) && utf8.ValidString(in)

		quoted, err := QuoteLiteral(in, scs)
		if err != nil {
			if storable {
				t.Fatalf("QuoteLiteral(%q) refused a storable value: %v", in, err)
			}
			return
		}
		if !storable {
			t.Fatalf("QuoteLiteral accepted a value PostgreSQL cannot store: %q", in)
		}
		if got := unquote(t, quoted); got != in {
			t.Fatalf("round trip: %q -> %q -> %q", in, quoted, got)
		}
	})
}

func TestEscapeCopyField(t *testing.T) {
	tests := []struct{ in, want string }{
		{"plain", "plain"},
		{"tab\there", `tab\there`},
		{"nl\nhere", `nl\nhere`},
		{"cr\rhere", `cr\rhere`},
		{`back\slash`, `back\\slash`},
		{`\N`, `\\N`},          // a literal backslash-N must not become the NULL marker
		{"O'Brien", "O'Brien"}, // quotes are not special in COPY text format
	}
	for _, tt := range tests {
		got, err := EscapeCopyField(tt.in)
		if err != nil {
			t.Fatalf("EscapeCopyField(%q): %v", tt.in, err)
		}
		if got != tt.want {
			t.Errorf("EscapeCopyField(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}

	if _, err := EscapeCopyField("bad\x00value"); err == nil {
		t.Error("EscapeCopyField accepted a NUL byte")
	}
}

func TestQuoteIdentifier(t *testing.T) {
	tests := []struct{ in, want string }{
		{"customers", `"customers"`},
		{"Mixed Case", `"Mixed Case"`},
		{`we"ird`, `"we""ird"`},
		{"customers; DROP TABLE t", `"customers; DROP TABLE t"`},
	}
	for _, tt := range tests {
		if got := QuoteIdentifier(tt.in); got != tt.want {
			t.Errorf("QuoteIdentifier(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestQuoteCommentNeutralisesNewlines(t *testing.T) {
	// A file name carrying a newline must not be able to end the comment and
	// start a new line of SQL.
	in := "evil.xlsx\nDROP TABLE customers;"
	got := QuoteComment(in)
	if strings.ContainsAny(got, "\r\n") {
		t.Errorf("QuoteComment left a line break: %q", got)
	}
	if !strings.Contains(got, "DROP TABLE customers;") {
		t.Errorf("QuoteComment lost content: %q", got)
	}
}
