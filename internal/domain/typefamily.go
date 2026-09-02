package domain

import "strings"

// TypeFamily groups PostgreSQL types into the families that behave alike for
// coercion, transform applicability and literal rendering.
//
// Classification is done once, from the introspected udt_name, and then drives
// mapper (which transforms apply), validator (which coercion function runs) and
// sqlgen (which cast is emitted). Those three must never classify separately.
type TypeFamily int

const (
	FamilyUnsupported TypeFamily = iota
	FamilyText
	FamilyInteger
	FamilyNumeric
	FamilyFloat
	FamilyBool
	FamilyDate
	FamilyTimestamp
	FamilyTimestampTZ
	FamilyTime
	FamilyUUID
	FamilyJSON
	FamilyEnum
	FamilyNetwork
	FamilyArray
)

var familyNames = map[TypeFamily]string{
	FamilyUnsupported: "unsupported",
	FamilyText:        "text",
	FamilyInteger:     "integer",
	FamilyNumeric:     "numeric",
	FamilyFloat:       "float",
	FamilyBool:        "boolean",
	FamilyDate:        "date",
	FamilyTimestamp:   "timestamp",
	FamilyTimestampTZ: "timestamptz",
	FamilyTime:        "time",
	FamilyUUID:        "uuid",
	FamilyJSON:        "json",
	FamilyEnum:        "enum",
	FamilyNetwork:     "network",
	FamilyArray:       "array",
}

func (f TypeFamily) String() string { return familyNames[f] }

// IntegerWidths maps the integer types to their inclusive value range.
var IntegerWidths = map[string]struct{ Min, Max int64 }{
	"int2": {Min: -32768, Max: 32767},
	"int4": {Min: -2147483648, Max: 2147483647},
	"int8": {Min: -9223372036854775808, Max: 9223372036854775807},
}

// Family classifies a column by its introspected udt_name.
//
// Enum detection cannot come from the name — an enum type is named by whoever
// created it — so it comes from EnumValues being populated, which introspect
// does when pg_type.typtype is 'e'.
func (c Column) Family() TypeFamily {
	if len(c.EnumValues) > 0 {
		return FamilyEnum
	}
	if c.ArrayElemType != "" || strings.HasPrefix(c.DataType, "_") {
		return FamilyArray
	}
	return familyOf(c.DataType)
}

func familyOf(udt string) TypeFamily {
	switch udt {
	case "text", "varchar", "bpchar", "char", "name", "citext":
		return FamilyText
	case "int2", "int4", "int8":
		return FamilyInteger
	case "numeric", "decimal", "money":
		return FamilyNumeric
	case "float4", "float8":
		return FamilyFloat
	case "bool":
		return FamilyBool
	case "date":
		return FamilyDate
	case "timestamp":
		return FamilyTimestamp
	case "timestamptz":
		return FamilyTimestampTZ
	case "time", "timetz":
		return FamilyTime
	case "uuid":
		return FamilyUUID
	case "json", "jsonb":
		return FamilyJSON
	case "inet", "cidr", "macaddr", "macaddr8":
		return FamilyNetwork
	default:
		// bytea, composite, range, interval, xml, tsvector and anything else
		// are out of scope for v1 and reported as E106.
		return FamilyUnsupported
	}
}

// AcceptsValue reports whether a mapping to this column can ever produce a
// value the database will take.
//
// GENERATED ALWAYS AS IDENTITY rejects an explicit value unless the statement
// uses OVERRIDING SYSTEM VALUE, and a stored generated column rejects one
// outright. Mapping either is refused at Phase A with E103 rather than
// producing a file that fails on its first row (spec §6.2, §9).
func (c Column) AcceptsValue() bool {
	return !c.IsGenerated && c.IdentityKind != "ALWAYS"
}

// Required reports whether the column must receive a value: NOT NULL, with no
// default and no identity to supply one.
func (c Column) Required() bool {
	return !c.Nullable && !c.HasDefault && !c.IsIdentity && !c.IsGenerated
}
