package sqlgen

import (
	"bufio"
	"context"
	"fmt"
	"strings"

	"pgsheet/internal/excel"
)

// COPY output is a separate code path, not a variation on the INSERT writer.
//
// Its escaping rules have nothing in common with literal quoting: no
// surrounding quotes, backslash escapes for tab, newline, carriage return and
// backslash, and \N for NULL. Sharing an escaper between the two would be the
// single easiest way to corrupt data silently, so they do not share one
// (spec §11).

// CopyLiteralToField converts an INSERT literal back to a COPY field.
//
// The coercion layer produces SQL literals because that is what the INSERT
// path needs. Rather than run a second coercion for COPY — which could
// disagree with the first, and then the two output modes would mean different
// things — the literal is unwrapped here: NULL becomes the NULL marker, quoted
// strings are unquoted and re-escaped, and bare literals such as numbers,
// TRUE and FALSE pass through.
func CopyLiteralToField(literal string) (string, error) {
	trimmed := strings.TrimSpace(literal)

	if trimmed == "NULL" {
		return CopyNull, nil
	}

	// Strip a trailing cast: '2024-03-15'::date
	value := trimmed
	if i := strings.LastIndex(value, "'::"); i >= 0 {
		value = value[:i+1]
	}

	escapeForm := false
	if strings.HasPrefix(value, " E'") {
		escapeForm = true
		value = strings.TrimPrefix(value, " E")
	}

	if !strings.HasPrefix(value, "'") || !strings.HasSuffix(value, "'") || len(value) < 2 {
		// A bare literal: a number, TRUE, FALSE. COPY takes these as text.
		return EscapeCopyField(trimmed)
	}

	inner := value[1 : len(value)-1]

	var b strings.Builder
	b.Grow(len(inner))
	for i := 0; i < len(inner); i++ {
		switch {
		case inner[i] == '\'' && i+1 < len(inner) && inner[i+1] == '\'':
			b.WriteByte('\'')
			i++
		case escapeForm && inner[i] == '\\' && i+1 < len(inner) && inner[i+1] == '\\':
			b.WriteByte('\\')
			i++
		default:
			b.WriteByte(inner[i])
		}
	}

	return EscapeCopyField(b.String())
}

func writeCopy(ctx context.Context, bw *bufio.Writer, in Input, opts Options) (Result, error) {
	res := Result{}

	header := fmt.Sprintf("\nCOPY %s (%s) FROM stdin;\n",
		QualifiedIdentifier(in.Target.Schema, in.Target.Table),
		strings.Join(quoteAll(in.Target.Columns), ", "))
	if _, err := bw.WriteString(header); err != nil {
		return res, err
	}
	res.Statements = 1

	err := in.Workbook.Scan(ctx, in.SheetName, in.DataStart, func(row excel.Row) error {
		literals, skip, err := in.Coerce(row)
		if err != nil {
			return fmt.Errorf("row %d: %w", row.Number, err)
		}
		if skip {
			res.RowsSkipped++
			return nil
		}

		fields := make([]string, len(literals))
		for i, lit := range literals {
			f, err := CopyLiteralToField(lit)
			if err != nil {
				return fmt.Errorf("row %d: %w", row.Number, err)
			}
			fields[i] = f
		}

		if _, err := bw.WriteString(strings.Join(fields, "\t") + "\n"); err != nil {
			return err
		}
		res.RowsWritten++

		if in.Progress != nil && res.RowsWritten%1000 == 0 {
			in.Progress(res.RowsWritten, in.Total)
		}
		return nil
	})
	if err != nil {
		return res, err
	}

	// The terminator must be exactly this, on its own line, or the server
	// keeps reading the rest of the file as data.
	if _, err := bw.WriteString("\\.\n"); err != nil {
		return res, err
	}

	if in.Progress != nil {
		in.Progress(res.RowsWritten, in.Total)
	}
	return res, nil
}
