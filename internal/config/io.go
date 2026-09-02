package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"

	"pgsheet/internal/domain"
	"pgsheet/internal/excel"
)

// Extension is the conventional suffix for a saved configuration.
const Extension = ".pgsheet.json"

var structValidator = validator.New(validator.WithRequiredStructEnabled())

// Warning is a non-fatal finding from loading a configuration against a live
// database and an opened workbook.
//
// These are the whole point of the reuse workflow: a configuration that loads
// silently against a changed template is how a monthly import quietly writes
// the wrong columns (spec §12).
type Warning struct {
	Kind    string `json:"kind"` // "headers" | "schema" | "version"
	Message string `json:"message"`
	Detail  string `json:"detail"`
}

// Save writes a configuration to disk.
func Save(path string, c Config) error {
	c.ConfigVersion = CurrentVersion
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now()
	}

	if err := Validate(c); err != nil {
		return err
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("encode configuration: %w", err)
	}
	data = append(data, '\n')

	// Write to a temporary file in the same directory and rename, so an
	// interrupted save cannot leave a half-written configuration where a
	// working one used to be.
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".pgsheet-*.tmp")
	if err != nil {
		return fmt.Errorf("write configuration: %w", err)
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("write configuration: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("write configuration: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("write configuration: %w", err)
	}
	return nil
}

// Load reads and validates a configuration file.
func Load(path string) (Config, []Warning, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, nil, fmt.Errorf("read configuration: %w", err)
	}

	var probe struct {
		ConfigVersion int `json:"configVersion"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return Config{}, nil, fmt.Errorf("%s is not a valid PGSheet configuration: %w", filepath.Base(path), err)
	}

	if probe.ConfigVersion > CurrentVersion {
		return Config{}, nil, fmt.Errorf(
			"%s was written by a newer version of PGSheet (format %d, this build understands %d). Update PGSheet to use it",
			filepath.Base(path), probe.ConfigVersion, CurrentVersion)
	}

	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return Config{}, nil, fmt.Errorf("%s is not a valid PGSheet configuration: %w", filepath.Base(path), err)
	}

	warnings, err := Migrate(&c)
	if err != nil {
		return Config{}, nil, err
	}

	if err := Validate(c); err != nil {
		return Config{}, nil, err
	}

	return c, warnings, nil
}

// Validate checks the struct rules and the cross-field rules the tags cannot
// express.
func Validate(c Config) error {
	if err := structValidator.Struct(c); err != nil {
		var ve validator.ValidationErrors
		if ok := asValidationErrors(err, &ve); ok {
			return fmt.Errorf("configuration is not usable: %s", describeValidationErrors(ve))
		}
		return fmt.Errorf("configuration is not usable: %w", err)
	}

	if c.Source.DataStartRow <= c.Source.HeaderRow {
		return fmt.Errorf("configuration is not usable: data starts at row %d, which is not below the header row %d",
			c.Source.DataStartRow, c.Source.HeaderRow)
	}

	seenDB := map[string]bool{}
	for _, m := range c.Mappings {
		if !m.Enabled {
			continue
		}
		if seenDB[m.DBColumn] {
			return fmt.Errorf("configuration is not usable: %s is mapped more than once", m.DBColumn)
		}
		seenDB[m.DBColumn] = true
	}

	if c.PrimaryKey.Strategy == domain.PKMapped && len(c.PrimaryKey.Columns) == 0 {
		return fmt.Errorf("configuration is not usable: the primary key strategy is 'mapped' but no key columns are named")
	}

	return nil
}

func asValidationErrors(err error, out *validator.ValidationErrors) bool {
	ve, ok := err.(validator.ValidationErrors)
	if ok {
		*out = ve
	}
	return ok
}

func describeValidationErrors(ve validator.ValidationErrors) string {
	parts := make([]string, 0, len(ve))
	for _, e := range ve {
		parts = append(parts, fmt.Sprintf("%s fails rule %q", e.Namespace(), e.Tag()))
	}
	return strings.Join(parts, "; ")
}

// CheckWorkbook compares a configuration against an opened sheet.
//
// A fingerprint mismatch is a blocking warning, not a silent adjustment: the
// operator is shown exactly which headers were added, removed or reordered and
// has to confirm before the mapping is reused.
func CheckWorkbook(c Config, sheet domain.SheetInfo) []Warning {
	if c.Source.HeaderFingerprint == "" || c.Source.HeaderFingerprint == sheet.Fingerprint {
		return nil
	}

	diff := excel.CompareHeaders(c.Source.Headers, sheet.Headers)

	var detail []string
	if len(diff.Added) > 0 {
		detail = append(detail, "added: "+strings.Join(diff.Added, ", "))
	}
	if len(diff.Removed) > 0 {
		detail = append(detail, "removed: "+strings.Join(diff.Removed, ", "))
	}
	if len(diff.Reordered) > 0 {
		detail = append(detail, "moved: "+strings.Join(diff.Reordered, ", "))
	}
	if len(detail) == 0 {
		detail = append(detail, "the header text differs from the saved layout")
	}

	return []Warning{{
		Kind:    "headers",
		Message: "this workbook's headers do not match the layout this configuration was saved against",
		Detail:  strings.Join(detail, "; "),
	}}
}

// CheckSchema compares a configuration against the live table.
func CheckSchema(c Config, schema domain.TableSchema) []Warning {
	var out []Warning

	if c.Target.Schema != schema.Schema || c.Target.Table != schema.Table {
		out = append(out, Warning{
			Kind:    "schema",
			Message: "this configuration was saved for a different table",
			Detail: fmt.Sprintf("saved for %s.%s, currently pointed at %s.%s",
				c.Target.Schema, c.Target.Table, schema.Schema, schema.Table),
		})
	}

	existing := make(map[string]domain.Column, len(schema.Columns))
	for _, col := range schema.Columns {
		existing[col.Name] = col
	}

	var missing, unwritable []string
	for _, m := range c.Mappings {
		if !m.Enabled {
			continue
		}
		col, ok := existing[m.DBColumn]
		if !ok {
			missing = append(missing, m.DBColumn)
			continue
		}
		if !col.AcceptsValue() {
			unwritable = append(unwritable, m.DBColumn)
		}
	}

	if len(missing) > 0 {
		out = append(out, Warning{
			Kind:    "schema",
			Message: "the table no longer has some of the columns this configuration maps",
			Detail:  strings.Join(missing, ", "),
		})
	}
	if len(unwritable) > 0 {
		out = append(out, Warning{
			Kind:    "schema",
			Message: "some mapped columns no longer accept values",
			Detail:  strings.Join(unwritable, ", ") + " (the database now supplies these)",
		})
	}

	return out
}

// SuggestFilename proposes a file name for a configuration.
func SuggestFilename(c Config) string {
	base := c.Name
	if base == "" {
		base = c.Target.Table
	}
	if base == "" {
		base = "import"
	}

	var b strings.Builder
	for _, r := range strings.ToLower(base) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteRune('_')
		}
	}
	return strings.Trim(b.String(), "_") + Extension
}
