package config

import (
	"time"

	"pgsheet/internal/domain"
)

// CurrentVersion is the configuration format this build writes.
//
// A file from an older version is migrated on load; a file from a newer one is
// refused with a message that says so, because guessing at a format we do not
// know is how a mapping silently changes meaning (spec §12).
const CurrentVersion = 1

// Config is a saved spreadsheet layout: everything needed to repeat an import
// except the connection.
//
// There are deliberately no connection details in this struct — not the host,
// not the database, and certainly not a password. These files get emailed and
// committed to repositories, so they must be safe to share.
type Config struct {
	ConfigVersion int       `json:"configVersion" validate:"required,min=1"`
	Name          string    `json:"name"`
	CreatedAt     time.Time `json:"createdAt"`
	CreatedBy     string    `json:"createdBy"`

	Target     Target                 `json:"target" validate:"required"`
	Source     Source                 `json:"source" validate:"required"`
	Mappings   []domain.ColumnMapping `json:"mappings" validate:"required,min=1,dive"`
	PrimaryKey PrimaryKey             `json:"primaryKey"`
	Validation Validation             `json:"validation"`
	Output     Output                 `json:"output"`
}

// Target is the table the configuration was built against. It is compared with
// the live database on load: a column that no longer exists is reported rather
// than discovered at insert time.
type Target struct {
	Schema string `json:"schema" validate:"required"`
	Table  string `json:"table" validate:"required"`
}

// Source describes the sheet layout, including the fingerprint that detects a
// changed client template.
type Source struct {
	SheetName         string   `json:"sheetName"`
	SheetIndex        int      `json:"sheetIndex"`
	HeaderRow         int      `json:"headerRow" validate:"min=1"`
	DataStartRow      int      `json:"dataStartRow" validate:"min=1"`
	HeaderFingerprint string   `json:"headerFingerprint"`
	Headers           []string `json:"headers"`
}

// PrimaryKey records the strategy decision so a rerun behaves identically.
type PrimaryKey struct {
	Strategy   domain.PKStrategy `json:"strategy"`
	Columns    []string          `json:"columns"`
	EmitSetval bool              `json:"emitSetval"`
}

// Validation holds the checking choices, including the two that change results
// rather than only speed: the source timezone and the misalignment threshold.
type Validation struct {
	CheckUniqueAgainstDB    bool    `json:"checkUniqueAgainstDb"`
	CheckForeignKeys        bool    `json:"checkForeignKeys"`
	ColumnMisalignThreshold float64 `json:"columnMisalignThreshold" validate:"min=0,max=1"`
	MaxIssues               int     `json:"maxIssues" validate:"min=0"`
	AllowNumericRounding    bool    `json:"allowNumericRounding"`
	EnumCaseInsensitive     bool    `json:"enumCaseInsensitive"`

	// SourceTimezone resolves naive spreadsheet times for timestamptz columns.
	// Recorded here because without it a rerun on a machine in another zone
	// would produce different instants from the same file (spec §20).
	SourceTimezone string `json:"sourceTimezone"`
}

// Output holds the generation choices.
type Output struct {
	Mode                 string `json:"mode" validate:"omitempty,oneof=insert copy"`
	BatchSize            int    `json:"batchSize" validate:"omitempty,min=100,max=1000"`
	WrapInTransaction    bool   `json:"wrapInTransaction"`
	IncludeSummaryHeader bool   `json:"includeSummaryHeader"`
	SkipBlankRows        bool   `json:"skipBlankRows"`
}

// Default returns a configuration with the defaults the UI starts from.
func Default() Config {
	return Config{
		ConfigVersion: CurrentVersion,
		CreatedAt:     time.Now(),
		Validation: Validation{
			ColumnMisalignThreshold: 0.30,
			MaxIssues:               10000,
			CheckUniqueAgainstDB:    true,
		},
		Output: Output{
			Mode:                 "insert",
			BatchSize:            500,
			WrapInTransaction:    true,
			IncludeSummaryHeader: true,
			SkipBlankRows:        true,
		},
	}
}
