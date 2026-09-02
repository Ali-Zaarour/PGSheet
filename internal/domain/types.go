// Package domain holds the types shared by every package. It depends on
// nothing but the standard library and decimal, and never imports Wails. The
// JSON tags are what the frontend sees, so changing one breaks it.
package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

// ---------- Schema ----------

// Column is one column of the target table, read live from PostgreSQL.
type Column struct {
	Name             string   `json:"name"`
	OrdinalPosition  int      `json:"ordinalPosition"`
	DataType         string   `json:"dataType"`      // udt_name: int4, varchar, numeric
	FormattedType    string   `json:"formattedType"` // human: character varying(255)
	Nullable         bool     `json:"nullable"`
	HasDefault       bool     `json:"hasDefault"`
	DefaultExpr      string   `json:"defaultExpr"`
	MaxLength        *int     `json:"maxLength"`
	NumericPrecision *int     `json:"numericPrecision"`
	NumericScale     *int     `json:"numericScale"`
	IsIdentity       bool     `json:"isIdentity"`
	IdentityKind     string   `json:"identityKind"` // ALWAYS | BY DEFAULT | ""
	IsGenerated      bool     `json:"isGenerated"`  // GENERATED ALWAYS AS (expr)
	EnumValues       []string `json:"enumValues"`
	EnumSchema       string   `json:"enumSchema"` // schema of the enum type, for the cast
	ArrayElemType    string   `json:"arrayElemType"`
	Comment          string   `json:"comment"`
}

// Constraint is any constraint registered on the table.
// Type follows pg_constraint.contype.
type Constraint struct {
	Name       string   `json:"name"`
	Type       string   `json:"type"` // p | u | c | f | x
	Columns    []string `json:"columns"`
	Definition string   `json:"definition"` // pg_get_constraintdef
	RefTable   string   `json:"refTable"`   // FK only
	RefColumns []string `json:"refColumns"`
}

// Sequence describes the sequence behind a generated key. NextValue is
// computed for display, never read with nextval, which would consume an id
// permanently even if the operator cancels.
type Sequence struct {
	Name      string `json:"name"` // fully qualified
	LastValue int64  `json:"lastValue"`
	IsCalled  bool   `json:"isCalled"`
	NextValue int64  `json:"nextValue"` // computed, display only, an estimate
	Increment int64  `json:"increment"`
	OwnedBy   string `json:"ownedBy"`
}

// TableSchema is the introspected picture of one target table.
type TableSchema struct {
	Schema      string       `json:"schema"`
	Table       string       `json:"table"`
	Columns     []Column     `json:"columns"`
	Constraints []Constraint `json:"constraints"`
	PrimaryKey  *Constraint  `json:"primaryKey"`
	PKSequence  *Sequence    `json:"pkSequence"`
	RowCount    int64        `json:"rowCount"` // estimate from pg_class.reltuples
}

// TableRef is a table as listed in the picker, before full introspection.
type TableRef struct {
	Schema  string `json:"schema"`
	Table   string `json:"table"`
	EstRows int64  `json:"estRows"`
	Comment string `json:"comment"`
}

// ---------- Source ----------

// CellKind is what a cell holds, which is not what Excel displays.
type CellKind int

const (
	CellEmpty CellKind = iota
	CellString
	CellNumber
	CellBool
	CellDate  // Excel serial resolved to time.Time
	CellError // #N/A, #REF!, #DIV/0!
)

// CellValue is a normalized cell. Numbers come from the raw stored text via
// decimal, never float64, which silently corrupts them. RawText keeps the
// original so an error can quote what the operator sees.
type CellValue struct {
	Kind    CellKind
	Str     string
	Num     decimal.Decimal
	Bool    bool
	Time    time.Time
	RawText string
}

// SheetInfo describes the chosen worksheet. Fingerprint hashes the header row;
// a mismatch on reuse is the main defence against mis-mapping a changed
// template.
type SheetInfo struct {
	Name        string   `json:"name"`
	Headers     []string `json:"headers"`
	HeaderRow   int      `json:"headerRow"`
	DataStart   int      `json:"dataStart"`
	TotalRows   int      `json:"totalRows"`
	Fingerprint string   `json:"fingerprint"` // sha256 of normalized headers
}

// ---------- Mapping ----------

// Transform is the per-column adjustments. The order they apply in is fixed,
// because the results differ: strip digits, trim, blank-as-null,
// default-on-blank, case, value map, boolean map, date format.
type Transform struct {
	Trim           bool              `json:"trim"`
	BlankAsNull    bool              `json:"blankAsNull"`
	UpperCase      bool              `json:"upperCase"`
	LowerCase      bool              `json:"lowerCase"`
	DateFormat     string            `json:"dateFormat"` // Go layout, for string dates
	BoolMap        map[string]bool   `json:"boolMap"`
	ValueMap       map[string]string `json:"valueMap"` // "Actif" -> "active"
	DefaultOnBlank string            `json:"defaultOnBlank"`
	StripNonDigits bool              `json:"stripNonDigits"` // phone numbers
}

// ColumnMapping links one sheet column to one table column, one-to-one.
type ColumnMapping struct {
	ExcelColumn string    `json:"excelColumn"`
	ExcelIndex  int       `json:"excelIndex"`
	DBColumn    string    `json:"dbColumn"`
	Transform   Transform `json:"transform"`
	Enabled     bool      `json:"enabled"`
}

// PKStrategy is how the primary key is supplied (spec §10).
type PKStrategy string

const (
	PKSequence PKStrategy = "sequence" // omit column, DB assigns at run time
	PKMapped   PKStrategy = "mapped"   // values from Excel + setval at end of file
	PKNone     PKStrategy = "none"     // table has no PK
)
