package config

import (
	"fmt"

	"pgsheet/internal/domain"
)

// migration upgrades a configuration by exactly one format version.
type migration struct {
	from int
	to   int
	// note explains, in the operator's terms, what the upgrade assumed. A
	// migration that silently guesses is how a reused mapping starts meaning
	// something else, so anything assumed is surfaced as a warning.
	note string
	fn   func(*Config) error
}

// migrations are applied in order. There are none yet: version 1 is the first
// released format. The machinery exists now so that the first format change
// does not have to invent it under time pressure, and so that the refusal to
// read a newer file is symmetrical with the ability to read an older one.
var migrations = []migration{}

// Migrate brings a configuration up to CurrentVersion.
func Migrate(c *Config) ([]Warning, error) {
	if c.ConfigVersion == 0 {
		// Files written before the field existed cannot exist — version 1 is
		// the first release — so a zero here means the file was hand-edited.
		return nil, fmt.Errorf("configuration has no configVersion; it may have been edited by hand")
	}
	if c.ConfigVersion > CurrentVersion {
		return nil, fmt.Errorf(
			"configuration format %d is newer than this build understands (%d)",
			c.ConfigVersion, CurrentVersion)
	}

	var warnings []Warning
	for _, m := range migrations {
		if c.ConfigVersion != m.from {
			continue
		}
		if err := m.fn(c); err != nil {
			return nil, fmt.Errorf("upgrade configuration from format %d to %d: %w", m.from, m.to, err)
		}
		c.ConfigVersion = m.to
		if m.note != "" {
			warnings = append(warnings, Warning{
				Kind:    "version",
				Message: fmt.Sprintf("configuration upgraded from format %d to %d", m.from, m.to),
				Detail:  m.note,
			})
		}
	}

	applyDefaults(c)
	return warnings, nil
}

// applyDefaults fills fields that an older or hand-written file may omit.
//
// Every default here is the same value the UI starts from, so a configuration
// missing a field behaves like a fresh one rather than like zero.
func applyDefaults(c *Config) {
	if c.Validation.ColumnMisalignThreshold <= 0 {
		c.Validation.ColumnMisalignThreshold = 0.30
	}
	if c.Validation.MaxIssues <= 0 {
		c.Validation.MaxIssues = 10000
	}
	if c.Output.Mode == "" {
		c.Output.Mode = "insert"
	}
	if c.Output.BatchSize == 0 {
		c.Output.BatchSize = 500
	}
	if c.PrimaryKey.Strategy == "" {
		c.PrimaryKey.Strategy = domain.PKNone
	}
	if c.Source.DataStartRow == 0 && c.Source.HeaderRow > 0 {
		c.Source.DataStartRow = c.Source.HeaderRow + 1
	}
	if c.CreatedBy == "" {
		c.CreatedBy = "pgsheet"
	}
}
