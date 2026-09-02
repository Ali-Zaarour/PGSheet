package sqlgen

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Meta is the summary block at the top of a generated file.
//
// It exists so that a file found on disk months later can still answer: what
// produced this, from which source, against which table, on which server, and
// what was checked before it was written (spec §11).
type Meta struct {
	Version     string
	GeneratedAt time.Time

	SourceFile  string
	SheetName   string
	HeaderRow   int
	FirstRow    int
	LastRow     int
	Fingerprint string
	ConfigName  string

	TargetSchema string
	TargetTable  string
	ServerInfo   string

	RowsToInsert   int
	RowsSkipped    int
	ColumnsMapped  int
	ColumnsDefault []string

	PrimaryKey     string
	PrimaryKeyNote string

	Warnings  int
	Validated string

	// PersonalDataColumns lists mapped columns whose names suggest personal
	// data. The warning is in the file itself because the file is the thing
	// that gets emailed around (spec §14).
	PersonalDataColumns []string
}

// Render produces the comment block.
//
// Every operator-supplied string goes through QuoteComment: a newline in a
// file name would otherwise end the comment and turn the rest of the line into
// executable SQL.
func (m Meta) Render() string {
	var b strings.Builder

	line := "-- =============================================================\n"
	b.WriteString(line)
	b.WriteString("--  PGSheet " + QuoteComment(m.Version) + ": generated import script\n")
	b.WriteString(line)

	at := m.GeneratedAt
	if at.IsZero() {
		at = time.Now()
	}
	field(&b, "Generated at", at.Format("2006-01-02 15:04:05 -07:00"))
	field(&b, "Source file", QuoteComment(m.SourceFile))

	sheet := QuoteComment(m.SheetName)
	if m.HeaderRow > 0 {
		sheet += fmt.Sprintf(" (header row %d, data rows %d to %d)", m.HeaderRow, m.FirstRow, m.LastRow)
	}
	field(&b, "Sheet", sheet)
	field(&b, "Fingerprint", m.Fingerprint)
	if m.ConfigName != "" {
		field(&b, "Config", QuoteComment(m.ConfigName))
	}

	b.WriteString("--\n")
	field(&b, "Target", m.TargetSchema+"."+m.TargetTable)
	if m.ServerInfo != "" {
		field(&b, "Server", QuoteComment(m.ServerInfo))
	}

	b.WriteString("--\n")
	field(&b, "Rows to insert", thousands(m.RowsToInsert))
	if m.RowsSkipped > 0 {
		field(&b, "Rows skipped", fmt.Sprintf("%d  (fully blank)", m.RowsSkipped))
	}
	field(&b, "Columns mapped", strconv.Itoa(m.ColumnsMapped))
	if len(m.ColumnsDefault) > 0 {
		field(&b, "Columns defaulted", fmt.Sprintf("%d  (%s)",
			len(m.ColumnsDefault), strings.Join(m.ColumnsDefault, ", ")))
	}

	if m.PrimaryKey != "" {
		b.WriteString("--\n")
		field(&b, "Primary key", QuoteComment(m.PrimaryKey))
		if m.PrimaryKeyNote != "" {
			b.WriteString("--                 " + QuoteComment(m.PrimaryKeyNote) + "\n")
		}
	}

	if m.Warnings > 0 {
		b.WriteString("--\n")
		field(&b, "Warnings", strconv.Itoa(m.Warnings))
	}
	if m.Validated != "" {
		field(&b, "Validated", QuoteComment(m.Validated))
	}

	if len(m.PersonalDataColumns) > 0 {
		b.WriteString("--\n")
		b.WriteString("--  NOTE: this file contains personal data in the columns " +
			QuoteComment(strings.Join(m.PersonalDataColumns, ", ")) + ".\n")
		b.WriteString("--        Treat it as you would the database itself.\n")
	}

	b.WriteString(line)
	return b.String()
}

func field(b *strings.Builder, label, value string) {
	// Wide enough for the longest label, so the block stays a readable column
	// of values rather than a ragged list.
	fmt.Fprintf(b, "--  %-17s: %s\n", label, value)
}

// personalDataHints are column-name fragments that suggest the generated file
// will contain personal data. Deliberately name-based and deliberately broad:
// the cost of an unnecessary note in a comment is nil, and the cost of a file
// full of personal data being mailed around unmarked is not.
var personalDataHints = []string{
	"email", "mail", "phone", "tel", "mobile", "name", "address", "addr",
	"national", "nid", "ssn", "passport", "birth", "dob", "iban", "card",
	"salary", "gender", "religion",
}

// FlagPersonalData reports which of the mapped columns look like personal data.
func FlagPersonalData(columns []string) []string {
	var out []string
	for _, c := range columns {
		lower := strings.ToLower(c)
		for _, hint := range personalDataHints {
			if strings.Contains(lower, hint) {
				out = append(out, c)
				break
			}
		}
	}
	return out
}

// thousands renders a count with separators, because the header is read by
// people and 1,247 is easier to trust at a glance than 1247.
func thousands(n int) string {
	s := strconv.Itoa(n)
	if n < 0 {
		return "-" + thousands(-n)
	}
	if len(s) <= 3 {
		return s
	}

	var b strings.Builder
	pre := len(s) % 3
	if pre > 0 {
		b.WriteString(s[:pre])
	}
	for i := pre; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}
