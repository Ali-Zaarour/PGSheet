package validator

import (
	"encoding/json"
	"fmt"
	"math"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/shopspring/decimal"

	"pgsheet/internal/domain"
	"pgsheet/internal/sqlgen"
)

// Coerced is one cell resolved into everything downstream needs, produced
// once so the same cell is not parsed in three packages.
type Coerced struct {
	IsNull  bool
	Literal string // SQL literal, cast included
	Text    string // normalized text, for unique keys and CHECK comparison
	Num     decimal.Decimal
	HasNum  bool
	Time    time.Time
	HasTime bool
}

// CellError is a coercion failure without row context; the pipeline adds the
// location.
type CellError struct {
	Code    string
	Message string
	Hint    string
}

func cellErr(code, format string, args ...any) *CellError {
	return &CellError{Code: code, Message: fmt.Sprintf(format, args...)}
}

func (e *CellError) withHint(hint string) *CellError {
	e.Hint = hint
	return e
}

// Options are the choices that change what coercion accepts. Recorded in the
// configuration so a rerun is deterministic.
type Options struct {
	// SourceTimezone resolves naive spreadsheet times for timestamptz. Without
	// it the literal means different instants on different servers.
	SourceTimezone *time.Location

	// AllowNumericRounding rounds instead of rejecting. Off by default:
	// silently rounding money is worse than a failed import.
	AllowNumericRounding bool

	// EnumCaseInsensitive matches labels ignoring case. Off by default: enums
	// are case-sensitive, and folding hides real data problems.
	EnumCaseInsensitive bool

	// StandardConformingStrings comes from the server probe, never assumed.
	StandardConformingStrings bool
}

// PostgreSQL's date limits: outside them is E209, rather than a database
// error on a file that already looked valid.
var (
	pgMinDate = time.Date(-4713, 11, 24, 0, 0, 0, 0, time.UTC)
	pgMaxDate = time.Date(294276, 12, 31, 23, 59, 59, 0, time.UTC)
)

var (
	integerText = regexp.MustCompile(`^[+-]?\d+$`)
	uuidText    = regexp.MustCompile(`^[0-9a-fA-F]{8}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{12}$`)
)

// Coerce turns one transformed cell into a literal for one column. Every
// failure carries a code and quotes the offending value: "invalid input" with
// no value is not actionable.
func Coerce(cell domain.CellValue, col domain.Column, opts Options) (Coerced, *CellError) {
	if cell.Kind == domain.CellError {
		// Two things arrive as an error cell: a formula result such as #N/A,
		// and a value Excel can display but no calendar can hold. They need
		// different words, because they need different fixes.
		if strings.HasPrefix(cell.Str, "#") {
			return Coerced{}, cellErr("E210", "the cell contains the Excel error %s", cell.Str).
				withHint("fix the formula in the source workbook, or replace the cell with a value")
		}
		detail := cell.Str
		if detail == "" {
			detail = truncate(cell.RawText)
		}
		return Coerced{}, cellErr("E210", "the cell holds %s", detail).
			withHint("correct the value in the source workbook")
	}

	if cell.Kind == domain.CellEmpty {
		if col.Nullable {
			return Coerced{IsNull: true, Literal: "NULL"}, nil
		}

		// A default or an identity does not rescue a blank here. Both fill the
		// column when it is left out of the insert; neither applies once the
		// column is in the column list and handed an explicit NULL.
		if col.HasDefault || col.IsIdentity {
			return Coerced{}, cellErr("E202",
				"%s is NOT NULL and the cell is empty", col.Name).
				withHint("the column has a default, but a default only applies when the column is left out of the insert entirely: unmap it and the database fills every row, or set a fixed value for blanks")
		}

		return Coerced{}, cellErr("E202",
			"%s is NOT NULL with no default and the cell is empty", col.Name).
			withHint("supply a value in the sheet, or set a fixed value for blanks on this column")
	}

	switch col.Family() {
	case domain.FamilyInteger:
		return coerceInteger(cell, col)
	case domain.FamilyNumeric:
		return coerceNumeric(cell, col, opts)
	case domain.FamilyFloat:
		return coerceFloat(cell, col)
	case domain.FamilyBool:
		return coerceBool(cell)
	case domain.FamilyText:
		return coerceText(cell, col, opts)
	case domain.FamilyEnum:
		return coerceEnum(cell, col, opts)
	case domain.FamilyDate:
		return coerceDate(cell, opts)
	case domain.FamilyTimestamp:
		return coerceTimestamp(cell, opts)
	case domain.FamilyTimestampTZ:
		return coerceTimestampTZ(cell, opts)
	case domain.FamilyTime:
		return coerceTime(cell, opts)
	case domain.FamilyUUID:
		return coerceUUID(cell, opts)
	case domain.FamilyJSON:
		return coerceJSON(cell, col, opts)
	case domain.FamilyNetwork:
		return coerceNetwork(cell, col, opts)
	default:
		return Coerced{}, cellErr("E106", "%s has type %s, which this version cannot write",
			col.Name, col.FormattedType)
	}
}

func coerceInteger(cell domain.CellValue, col domain.Column) (Coerced, *CellError) {
	var d decimal.Decimal

	switch cell.Kind {
	case domain.CellNumber:
		d = cell.Num
	case domain.CellBool:
		if cell.Bool {
			d = decimal.NewFromInt(1)
		} else {
			d = decimal.NewFromInt(0)
		}
	default:
		s := strings.TrimSpace(domain.NormalizeKey(text(cell)))
		if !integerText.MatchString(s) {
			// Accept "12.00" — a whole number that Excel wrote with a scale —
			// but not "12.5", which loses data silently.
			parsed, err := decimal.NewFromString(s)
			if err != nil {
				return Coerced{}, cellErr("E201", "%q is not a whole number", truncate(text(cell))).
					withHint("integer columns take digits only; check for stray text or separators")
			}
			d = parsed
		} else {
			parsed, err := decimal.NewFromString(s)
			if err != nil {
				return Coerced{}, cellErr("E201", "%q is not a whole number", truncate(text(cell)))
			}
			d = parsed
		}
	}

	if !d.Equal(d.Truncate(0)) {
		return Coerced{}, cellErr("E201", "%s has a fractional part and %s takes whole numbers only",
			d.String(), col.FormattedType).
			withHint("round the value in the sheet, or map this column to a numeric column instead")
	}
	d = d.Truncate(0)

	if w, ok := domain.IntegerWidths[col.DataType]; ok {
		if d.LessThan(decimal.NewFromInt(w.Min)) || d.GreaterThan(decimal.NewFromInt(w.Max)) {
			return Coerced{}, cellErr("E205", "%s is outside the range of %s (%d to %d)",
				d.String(), col.FormattedType, w.Min, w.Max).
				withHint("a wider integer type is needed for this value")
		}
	}

	return Coerced{Literal: d.String(), Text: d.String(), Num: d, HasNum: true}, nil
}

func coerceNumeric(cell domain.CellValue, col domain.Column, opts Options) (Coerced, *CellError) {
	var d decimal.Decimal

	switch cell.Kind {
	case domain.CellNumber:
		d = cell.Num
	case domain.CellBool:
		return Coerced{}, cellErr("E201", "a boolean cannot be written to %s", col.FormattedType)
	default:
		s := strings.TrimSpace(domain.NormalizeKey(text(cell)))
		// Thousands separators are the single most common reason a money
		// column fails, and stripping them cannot change the value.
		s = strings.ReplaceAll(s, ",", "")
		s = strings.ReplaceAll(s, " ", "")
		parsed, err := decimal.NewFromString(s)
		if err != nil {
			return Coerced{}, cellErr("E201", "%q is not a number", truncate(text(cell))).
				withHint("check for currency symbols, text such as N/A, or a stray unit")
		}
		d = parsed
	}

	if col.NumericScale != nil {
		scale := int32(*col.NumericScale)
		if -d.Exponent() > scale {
			if !opts.AllowNumericRounding {
				return Coerced{}, cellErr("E204", "%s has more than %d decimal places, which %s cannot store",
					d.String(), scale, col.FormattedType).
					withHint("round the value in the sheet, or enable rounding for this column")
			}
			d = d.Round(scale)
		}
	}

	if col.NumericPrecision != nil && col.NumericScale != nil {
		maxIntegerDigits := *col.NumericPrecision - *col.NumericScale
		if digitsBeforePoint(d) > maxIntegerDigits {
			return Coerced{}, cellErr("E204", "%s has more than %d digits before the decimal point, which %s cannot store",
				d.String(), maxIntegerDigits, col.FormattedType).
				withHint("the value is too large for this column's precision")
		}
	}

	// Emit at the declared scale. Same value to PostgreSQL, but easier for a
	// person to check against the source, and it keeps the text form canonical
	// so "1.50" and "1.5" are not two values to the duplicate check.
	out := d.String()
	if col.NumericScale != nil && *col.NumericScale > 0 {
		out = d.StringFixed(int32(*col.NumericScale))
	}

	return Coerced{Literal: out, Text: out, Num: d, HasNum: true}, nil
}

func digitsBeforePoint(d decimal.Decimal) int {
	s := d.Abs().Truncate(0).String()
	if s == "0" {
		return 0
	}
	return len(s)
}

func coerceFloat(cell domain.CellValue, col domain.Column) (Coerced, *CellError) {
	var f float64

	switch cell.Kind {
	case domain.CellNumber:
		f, _ = cell.Num.Float64()
	default:
		s := strings.TrimSpace(domain.NormalizeKey(text(cell)))
		parsed, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return Coerced{}, cellErr("E201", "%q is not a number", truncate(text(cell)))
		}
		f = parsed
	}

	if math.IsNaN(f) || math.IsInf(f, 0) {
		return Coerced{}, cellErr("E201", "%q is not a finite number", truncate(text(cell))).
			withHint("NaN and infinity cannot be written to " + col.FormattedType)
	}

	s := strconv.FormatFloat(f, 'g', -1, 64)
	return Coerced{Literal: s, Text: s, Num: decimal.NewFromFloat(f), HasNum: true}, nil
}

func coerceBool(cell domain.CellValue) (Coerced, *CellError) {
	var b bool

	switch cell.Kind {
	case domain.CellBool:
		b = cell.Bool
	case domain.CellNumber:
		switch {
		case cell.Num.IsZero():
			b = false
		case cell.Num.Equal(decimal.NewFromInt(1)):
			b = true
		default:
			return Coerced{}, cellErr("E201", "%s is not a boolean", cell.Num.String()).
				withHint("only 0 and 1 are accepted from numbers; set a boolean word map for anything else")
		}
	default:
		key := strings.ToLower(domain.NormalizeKey(text(cell)))
		v, ok := defaultBoolWords[key]
		if !ok {
			return Coerced{}, cellErr("E201", "%q is not a boolean", truncate(text(cell))).
				withHint("set a boolean word map on this column so the sheet's wording is understood")
		}
		b = v
	}

	if b {
		return Coerced{Literal: "TRUE", Text: "true"}, nil
	}
	return Coerced{Literal: "FALSE", Text: "false"}, nil
}

// defaultBoolWords mirrors mapper.DefaultBoolMap for the case where coercion
// is reached without a configured map. Kept here rather than imported so
// validator does not depend on mapper for a constant.
var defaultBoolWords = map[string]bool{
	"true": true, "false": false,
	"t": true, "f": false,
	"yes": true, "no": false,
	"y": true, "n": false,
	"1": true, "0": false,
	"oui": true, "non": false,
	"vrai": true, "faux": false,
}

func coerceText(cell domain.CellValue, col domain.Column, opts Options) (Coerced, *CellError) {
	s := text(cell)

	if strings.ContainsRune(s, 0) {
		return Coerced{}, cellErr("E211", "the value contains a NUL byte, which PostgreSQL cannot store in text").
			withHint("the cell probably came from a binary paste; clean it in the source workbook")
	}
	if !utf8.ValidString(s) {
		return Coerced{}, cellErr("E212", "the value is not valid UTF-8 text").
			withHint("a UTF-8 database would reject these bytes; retype the cell in the source workbook")
	}

	// Length is counted in runes, not bytes: a 200-character Arabic name is
	// 200 characters to PostgreSQL, not 400 (spec §9).
	if col.MaxLength != nil && *col.MaxLength > 0 {
		if n := utf8.RuneCountInString(s); n > *col.MaxLength {
			return Coerced{}, cellErr("E203", "the value is %d characters and %s allows %d",
				n, col.FormattedType, *col.MaxLength).
				withHint("shorten the value in the sheet, or widen the column")
		}
	}

	lit, err := sqlgen.QuoteLiteral(s, opts.StandardConformingStrings)
	if err != nil {
		return Coerced{}, cellErr("E211", "%v", err)
	}
	return Coerced{Literal: lit, Text: s}, nil
}

func coerceEnum(cell domain.CellValue, col domain.Column, opts Options) (Coerced, *CellError) {
	s := domain.NormalizeKey(text(cell))

	match := ""
	for _, label := range col.EnumValues {
		if label == s || (opts.EnumCaseInsensitive && strings.EqualFold(label, s)) {
			match = label
			break
		}
	}
	if match == "" {
		return Coerced{}, cellErr("E206", "%q is not one of the values %s accepts", truncate(s), col.FormattedType).
			withHint("allowed values: " + strings.Join(col.EnumValues, ", ")).
			asEnumHint(col)
	}

	lit, err := sqlgen.QuoteLiteral(match, opts.StandardConformingStrings)
	if err != nil {
		return Coerced{}, cellErr("E211", "%v", err)
	}
	// The cast is schema-qualified so the value cannot resolve to a different
	// type through someone else's search_path.
	return Coerced{
		Literal: lit + sqlgen.EnumCast(col.EnumSchema, col.DataType),
		Text:    match,
	}, nil
}

// asEnumHint keeps the allowed-value list short enough to read. An enum with
// forty labels helps nobody as a wall of text.
func (e *CellError) asEnumHint(col domain.Column) *CellError {
	const maxLabels = 12
	if len(col.EnumValues) > maxLabels {
		e.Hint = fmt.Sprintf("allowed values include: %s, and %d more",
			strings.Join(col.EnumValues[:maxLabels], ", "), len(col.EnumValues)-maxLabels)
	}
	return e
}

func coerceDate(cell domain.CellValue, opts Options) (Coerced, *CellError) {
	t, err := asTime(cell)
	if err != nil {
		return Coerced{}, err
	}
	if outOfRange(t) {
		return Coerced{}, cellErr("E209", "%s is outside the range PostgreSQL can store", t.Format("2006-01-02"))
	}
	s := t.Format("2006-01-02")
	return Coerced{
		Literal: sqlgen.MustQuoteLiteral(s, opts.StandardConformingStrings) + "::date",
		Text:    s,
		Time:    t,
		HasTime: true,
	}, nil
}

func coerceTimestamp(cell domain.CellValue, opts Options) (Coerced, *CellError) {
	t, err := asTime(cell)
	if err != nil {
		return Coerced{}, err
	}
	if outOfRange(t) {
		return Coerced{}, cellErr("E209", "%s is outside the range PostgreSQL can store", t.Format(time.RFC3339))
	}
	s := t.Format("2006-01-02 15:04:05.999999")
	return Coerced{
		Literal: sqlgen.MustQuoteLiteral(s, opts.StandardConformingStrings) + "::timestamp",
		Text:    s,
		Time:    t,
		HasTime: true,
	}, nil
}

func coerceTimestampTZ(cell domain.CellValue, opts Options) (Coerced, *CellError) {
	t, err := asTime(cell)
	if err != nil {
		return Coerced{}, err
	}
	if outOfRange(t) {
		return Coerced{}, cellErr("E209", "%s is outside the range PostgreSQL can store", t.Format(time.RFC3339))
	}

	// A spreadsheet time carries no zone. Attaching the configured source
	// timezone here, and emitting an explicit offset below, is what makes the
	// generated file mean the same instant wherever it is run (spec §11, §20).
	loc := opts.SourceTimezone
	if loc == nil {
		loc = time.Local
	}
	withZone := time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), loc)

	s := withZone.Format("2006-01-02 15:04:05.999999-07:00")
	return Coerced{
		Literal: sqlgen.MustQuoteLiteral(s, opts.StandardConformingStrings) + "::timestamptz",
		Text:    s,
		Time:    withZone,
		HasTime: true,
	}, nil
}

func coerceTime(cell domain.CellValue, opts Options) (Coerced, *CellError) {
	t, err := asTime(cell)
	if err != nil {
		return Coerced{}, err
	}
	s := t.Format("15:04:05.999999")
	return Coerced{
		Literal: sqlgen.MustQuoteLiteral(s, opts.StandardConformingStrings) + "::time",
		Text:    s,
		Time:    t,
		HasTime: true,
	}, nil
}

func coerceUUID(cell domain.CellValue, opts Options) (Coerced, *CellError) {
	s := domain.NormalizeKey(text(cell))
	if !uuidText.MatchString(s) {
		return Coerced{}, cellErr("E207", "%q is not a UUID", truncate(s)).
			withHint("expected 32 hexadecimal digits, usually written 8-4-4-4-12")
	}

	// Normalize to the lowercase hyphenated form so two spellings of the same
	// UUID are one value for duplicate detection.
	hex := strings.ToLower(strings.ReplaceAll(s, "-", ""))
	norm := fmt.Sprintf("%s-%s-%s-%s-%s", hex[0:8], hex[8:12], hex[12:16], hex[16:20], hex[20:32])

	return Coerced{
		Literal: sqlgen.MustQuoteLiteral(norm, opts.StandardConformingStrings) + "::uuid",
		Text:    norm,
	}, nil
}

func coerceJSON(cell domain.CellValue, col domain.Column, opts Options) (Coerced, *CellError) {
	s := text(cell)
	if !json.Valid([]byte(s)) {
		return Coerced{}, cellErr("E208", "the value is not valid JSON").
			withHint("check for smart quotes, trailing commas, or a value that is plain text")
	}
	if strings.ContainsRune(s, 0) {
		return Coerced{}, cellErr("E211", "the value contains a NUL byte, which PostgreSQL cannot store")
	}

	out := s
	if col.DataType == "jsonb" {
		// jsonb discards insignificant whitespace and key order anyway;
		// normalizing here means the file and the database agree on the value,
		// which matters when a unique index covers the column.
		var v any
		if err := json.Unmarshal([]byte(s), &v); err == nil {
			if b, err := json.Marshal(v); err == nil {
				out = string(b)
			}
		}
	}

	lit, err := sqlgen.QuoteLiteral(out, opts.StandardConformingStrings)
	if err != nil {
		return Coerced{}, cellErr("E211", "%v", err)
	}
	return Coerced{Literal: lit + "::" + col.DataType, Text: out}, nil
}

func coerceNetwork(cell domain.CellValue, col domain.Column, opts Options) (Coerced, *CellError) {
	s := domain.NormalizeKey(text(cell))

	switch col.DataType {
	case "inet":
		if _, err := netip.ParseAddr(s); err != nil {
			if _, err2 := netip.ParsePrefix(s); err2 != nil {
				return Coerced{}, cellErr("E201", "%q is not an IP address", truncate(s))
			}
		}
	case "cidr":
		if _, err := netip.ParsePrefix(s); err != nil {
			return Coerced{}, cellErr("E201", "%q is not a network in CIDR notation", truncate(s)).
				withHint("expected a form such as 192.168.1.0/24")
		}
	default: // macaddr, macaddr8 — left to the server, which parses more forms
		if s == "" {
			return Coerced{}, cellErr("E201", "empty value for %s", col.FormattedType)
		}
	}

	lit, err := sqlgen.QuoteLiteral(s, opts.StandardConformingStrings)
	if err != nil {
		return Coerced{}, cellErr("E211", "%v", err)
	}
	return Coerced{Literal: lit + "::" + col.DataType, Text: s}, nil
}

// asTime resolves any cell kind to a time. Excel serials were already
// converted on read, so a CellDate is authoritative; a string is parsed with
// the layouts the transform layer could not resolve.
func asTime(cell domain.CellValue) (time.Time, *CellError) {
	switch cell.Kind {
	case domain.CellDate:
		return cell.Time, nil
	case domain.CellNumber:
		// A bare number reaching a date column means the reader could not tell
		// it was a date — the cell had no date formatting. Converting it
		// silently would invent a date from an order quantity.
		return time.Time{}, cellErr("E201", "%s is a number, not a date", cell.Num.String()).
			withHint("the cell is not formatted as a date in Excel; format it, or set a date format on this column")
	default:
		s := domain.NormalizeKey(text(cell))
		if s == "" {
			return time.Time{}, cellErr("E201", "the value is empty")
		}
		for _, layout := range []string{
			"2006-01-02", "2006-01-02 15:04:05", "2006-01-02T15:04:05",
			"2006-01-02T15:04:05Z07:00", "15:04:05", "15:04",
		} {
			if t, err := time.Parse(layout, s); err == nil {
				return t, nil
			}
		}
		return time.Time{}, cellErr("E201", "%q is not a date", truncate(s)).
			withHint("set the date format for this column so the sheet's wording is understood")
	}
}

func outOfRange(t time.Time) bool {
	return t.Before(pgMinDate) || t.After(pgMaxDate)
}

func text(c domain.CellValue) string {
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
	default:
		return c.RawText
	}
}

// truncate keeps a quoted value short enough for a report row. 100 characters
// is enough to recognise the cell and short enough to scan a thousand of them.
func truncate(s string) string {
	const limit = 100
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	r := []rune(s)
	return string(r[:limit]) + "…"
}
