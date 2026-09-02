package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pgsheet/internal/domain"
)

func sample() Config {
	c := Default()
	c.Name = "Monthly customer import"
	c.Target = Target{Schema: "public", Table: "customers"}
	c.Source = Source{
		SheetName:         "Sheet1",
		HeaderRow:         1,
		DataStartRow:      2,
		HeaderFingerprint: "sha256:abc",
		Headers:           []string{"Client Name", "Email"},
	}
	c.Mappings = []domain.ColumnMapping{
		{ExcelColumn: "Client Name", ExcelIndex: 0, DBColumn: "name", Enabled: true,
			Transform: domain.Transform{Trim: true}},
		{ExcelColumn: "Email", ExcelIndex: 1, DBColumn: "email", Enabled: true,
			Transform: domain.Transform{Trim: true, LowerCase: true}},
	}
	c.PrimaryKey = PrimaryKey{Strategy: domain.PKSequence, Columns: []string{"id"}}
	return c
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "customers"+Extension)

	want := sample()
	if err := Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, warnings, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %+v", warnings)
	}

	if got.Target != want.Target {
		t.Errorf("target = %+v, want %+v", got.Target, want.Target)
	}
	if len(got.Mappings) != len(want.Mappings) {
		t.Fatalf("got %d mappings, want %d", len(got.Mappings), len(want.Mappings))
	}
	if !got.Mappings[1].Transform.LowerCase {
		t.Error("a transform flag was lost in the round trip")
	}
	if got.PrimaryKey.Strategy != domain.PKSequence {
		t.Errorf("pk strategy = %q, want sequence", got.PrimaryKey.Strategy)
	}
}

// The file gets emailed and committed to repositories. Anything that could
// identify or reach the database must not be in it (spec §12).
func TestSavedFileContainsNoConnectionDetails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c"+Extension)
	if err := Save(path, sample()); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// Matched as JSON keys, not as substrings: a configuration named "Monthly
	// customer import" legitimately contains the letters of "port".
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}

	forbidden := map[string]bool{
		"host": true, "port": true, "database": true, "user": true, "username": true,
		"password": true, "sslmode": true, "dsn": true, "connection": true, "cacertpath": true,
	}

	var walk func(prefix string, raw json.RawMessage)
	walk = func(prefix string, raw json.RawMessage) {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(raw, &obj); err != nil {
			return
		}
		for key, value := range obj {
			if forbidden[strings.ToLower(key)] {
				t.Errorf("the saved configuration carries a connection field: %s%s", prefix, key)
			}
			walk(prefix+key+".", value)
		}
	}
	for key, value := range fields {
		if forbidden[strings.ToLower(key)] {
			t.Errorf("the saved configuration carries a connection field: %s", key)
		}
		walk(key+".", value)
	}
}

func TestLoadRefusesANewerFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "future"+Extension)

	c := sample()
	raw, _ := json.Marshal(c)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	m["configVersion"] = CurrentVersion + 1
	future, _ := json.MarshalIndent(m, "", "  ")

	if err := os.WriteFile(path, future, 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, err := Load(path)
	if err == nil {
		t.Fatal("a configuration from a newer version was accepted")
	}
	// The message has to say what to do, not only that something is wrong.
	if !strings.Contains(err.Error(), "newer version") {
		t.Errorf("unhelpful message: %v", err)
	}
}

func TestValidateRejectsUnusableConfigurations(t *testing.T) {
	t.Run("data starting above the header", func(t *testing.T) {
		c := sample()
		c.Source.DataStartRow = 1
		if err := Validate(c); err == nil {
			t.Error("data starting at or above the header row was accepted")
		}
	})

	t.Run("one column mapped twice", func(t *testing.T) {
		c := sample()
		c.Mappings[1].DBColumn = "name"
		if err := Validate(c); err == nil {
			t.Error("two sheet columns mapped to one table column was accepted")
		}
	})

	t.Run("mapped key strategy with no key columns", func(t *testing.T) {
		c := sample()
		c.PrimaryKey = PrimaryKey{Strategy: domain.PKMapped}
		if err := Validate(c); err == nil {
			t.Error("the mapped strategy was accepted without key columns")
		}
	})

	t.Run("no mappings", func(t *testing.T) {
		c := sample()
		c.Mappings = nil
		if err := Validate(c); err == nil {
			t.Error("a configuration with no mappings was accepted")
		}
	})
}

// The fingerprint comparison is the main defence in the reuse workflow: a
// client who changes their template must not silently import into the wrong
// columns (spec §12).
func TestCheckWorkbookNamesWhatChanged(t *testing.T) {
	c := sample()

	same := domain.SheetInfo{Fingerprint: c.Source.HeaderFingerprint}
	if w := CheckWorkbook(c, same); len(w) != 0 {
		t.Errorf("a matching fingerprint produced warnings: %+v", w)
	}

	changed := domain.SheetInfo{
		Fingerprint: "sha256:different",
		Headers:     []string{"Client Name", "Mobile"},
	}
	warnings := CheckWorkbook(c, changed)
	if len(warnings) == 0 {
		t.Fatal("a changed layout produced no warning")
	}
	if !strings.Contains(warnings[0].Detail, "Mobile") || !strings.Contains(warnings[0].Detail, "Email") {
		t.Errorf("the warning does not say what changed: %q", warnings[0].Detail)
	}
}

func TestCheckSchemaReportsVanishedColumns(t *testing.T) {
	c := sample()

	schema := domain.TableSchema{
		Schema: "public",
		Table:  "customers",
		Columns: []domain.Column{
			{Name: "name", DataType: "text"},
			// email is gone, and id no longer accepts a value.
			{Name: "id", DataType: "int4", IsGenerated: true},
		},
	}

	warnings := CheckSchema(c, schema)
	if len(warnings) == 0 {
		t.Fatal("a table missing a mapped column produced no warning")
	}

	joined := ""
	for _, w := range warnings {
		joined += w.Message + " " + w.Detail + "\n"
	}
	if !strings.Contains(joined, "email") {
		t.Errorf("the vanished column is not named:\n%s", joined)
	}
}

func TestMigrateFillsDefaults(t *testing.T) {
	c := Config{
		ConfigVersion: 1,
		Source:        Source{HeaderRow: 3},
	}

	if _, err := Migrate(&c); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	if c.Validation.ColumnMisalignThreshold != 0.30 {
		t.Errorf("threshold = %v, want the default 0.30", c.Validation.ColumnMisalignThreshold)
	}
	if c.Validation.MaxIssues != 10000 {
		t.Errorf("maxIssues = %d, want the default 10000", c.Validation.MaxIssues)
	}
	if c.Output.BatchSize != 500 {
		t.Errorf("batchSize = %d, want the default 500", c.Output.BatchSize)
	}
	if c.Source.DataStartRow != 4 {
		t.Errorf("dataStartRow = %d, want the row below the header", c.Source.DataStartRow)
	}
}

func TestSuggestFilename(t *testing.T) {
	c := sample()
	got := SuggestFilename(c)
	if !strings.HasSuffix(got, Extension) {
		t.Errorf("%q does not end in %s", got, Extension)
	}
	if strings.ContainsAny(got, ` /\:*?"<>|`) {
		t.Errorf("%q contains characters a filename cannot hold", got)
	}
}

func TestSaveDoesNotDestroyAnExistingFileOnFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c"+Extension)

	if err := Save(path, sample()); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// An invalid configuration must be refused before anything is written.
	bad := sample()
	bad.Mappings = nil
	if err := Save(path, bad); err == nil {
		t.Fatal("an invalid configuration was saved")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("a failed save damaged the existing file")
	}
}
