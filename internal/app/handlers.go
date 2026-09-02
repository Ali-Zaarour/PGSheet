package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"pgsheet/internal/config"
	"pgsheet/internal/dbconn"
	"pgsheet/internal/domain"
	"pgsheet/internal/excel"
	"pgsheet/internal/introspect"
	"pgsheet/internal/mapper"
	"pgsheet/internal/sqlgen"
	"pgsheet/internal/validator"
)

// The bound method surface from spec §15. Every method returns (T, error);
// Wails turns the error into a rejected promise. Errors that could carry the
// password go through a.redact first.

// ---------- Step 1: connect ----------

// ConnectResult is what the connection screen shows after a successful test.
type ConnectResult struct {
	Server   dbconn.ServerInfo `json:"server"`
	SSLModes []string          `json:"sslModes"`
}

// Connect opens the pool. Credentials are held in memory only.
func (a *App) Connect(in dbconn.ConnectInput) (ConnectResult, error) {
	ctx, done := a.operation()
	defer done()

	pool, err := dbconn.Connect(ctx, in)
	if err != nil {
		return ConnectResult{}, dbconn.Redact(err, in.Password)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	a.closeSessionLocked()
	a.session.pool = pool
	a.session.password = in.Password

	a.log.Info("connected",
		"database", pool.Info.Database,
		"user", pool.Info.User,
		"serverVersion", pool.Info.VersionNum)

	return ConnectResult{Server: pool.Info, SSLModes: dbconn.SSLModes}, nil
}

// Disconnect closes the pool and forgets the credentials.
func (a *App) Disconnect() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closeSessionLocked()
	return nil
}

// ---------- Step 2: table ----------

// ListTables returns the tables this user can read.
func (a *App) ListTables() ([]domain.TableRef, error) {
	ctx, done := a.operation()
	defer done()

	pool, err := a.requirePool()
	if err != nil {
		return nil, err
	}

	tables, err := introspect.ListTables(ctx, pool.Pool)
	return tables, a.redact(err)
}

// TableDetail is the full picture of a table plus what this user may do to it.
type TableDetail struct {
	Schema        domain.TableSchema          `json:"schema"`
	UniqueIndexes []introspect.UniqueIndex    `json:"uniqueIndexes"`
	Rules         []introspect.UniquenessRule `json:"rules"`
	Privileges    dbconn.Privileges           `json:"privileges"`
	PKOptions     []PKOption                  `json:"pkOptions"`
	Unverifiable  []validator.Unverifiable    `json:"unverifiable"`
}

// DescribeTable reads a table's real structure and selects it for this session.
func (a *App) DescribeTable(schema, table string) (TableDetail, error) {
	ctx, done := a.operation()
	defer done()

	pool, err := a.requirePool()
	if err != nil {
		return TableDetail{}, err
	}

	res, err := introspect.Table(ctx, pool.Pool, schema, table)
	if err != nil {
		return TableDetail{}, a.redact(err)
	}

	priv, err := dbconn.CheckPrivileges(ctx, pool.Pool, schema, table)
	if err != nil {
		return TableDetail{}, a.redact(err)
	}

	_, unverifiable := validator.ParseConstraints(res.Schema.Constraints)

	a.mu.Lock()
	a.session.introspection = &res
	a.session.privileges = priv
	a.session.pk = defaultPKChoice(res.Schema)
	a.session.lastReport = nil
	a.session.dryRunOK = false
	a.mu.Unlock()

	return TableDetail{
		Schema:        res.Schema,
		UniqueIndexes: res.UniqueIndexes,
		Rules:         res.UniquenessRules(),
		Privileges:    priv,
		PKOptions:     pkOptions(res.Schema),
		Unverifiable:  unverifiable,
	}, nil
}

// ---------- Step 3: workbook ----------

// WorkbookInfo describes an opened file.
type WorkbookInfo struct {
	Path        string            `json:"path"`
	FileName    string            `json:"fileName"`
	Sheets      []string          `json:"sheets"`
	HeaderGuess excel.HeaderGuess `json:"headerGuess"`
	Merged      []string          `json:"merged"`
}

// ChooseWorkbook shows the native dialog and returns the chosen path, without
// opening anything.
//
// Split from OpenWorkbookAt on purpose. Opening a workbook means a zip safety
// scan and parsing the file, which is seconds on a large one; doing that in
// the same call as the dialog leaves the operator watching an idle window with
// no way to know the work has started. Two calls let the UI show what it is
// doing before the slow part begins.
//
// The path has to come from the native dialog: an HTML file input cannot give
// a real one.
func (a *App) ChooseWorkbook() (string, error) {
	return a.openFileDialog("Open spreadsheet", "Excel workbook (*.xlsx)", "*.xlsx")
}

// OpenWorkbookAt opens a specific path, so tests need no dialog.
func (a *App) OpenWorkbookAt(path string) (WorkbookInfo, error) {
	ctx, done := a.operation()
	defer done()

	wb, err := excel.Open(path)
	if err != nil {
		return WorkbookInfo{}, err
	}

	sheets := wb.Sheets()
	if len(sheets) == 0 {
		_ = wb.Close()
		return WorkbookInfo{}, fmt.Errorf("%s has no worksheets", filepath.Base(path))
	}

	guess, err := wb.DetectHeaderRow(ctx, sheets[0])
	if err != nil {
		_ = wb.Close()
		return WorkbookInfo{}, err
	}

	// Not run until the sheet has been counted, and skipped on a large one.
	merged, _, _ := wb.MergedRanges(sheets[0])

	a.mu.Lock()
	if a.session.workbook != nil {
		_ = a.session.workbook.Close()
	}
	a.session.workbook = wb
	a.session.lastReport = nil
	a.session.dryRunOK = false
	a.mu.Unlock()

	return WorkbookInfo{
		Path:        path,
		FileName:    filepath.Base(path),
		Sheets:      sheets,
		HeaderGuess: guess,
		Merged:      merged,
	}, nil
}

// SheetSelection is the confirmed sheet, a preview of its first rows, and the
// mapping re-checked against it. A configuration can be loaded before the
// workbook, so this is where its mappings first mean anything.
type SheetSelection struct {
	Info     domain.SheetInfo `json:"info"`
	Preview  [][]PreviewCell  `json:"preview"`
	Warnings []string         `json:"warnings"`
	Status   *mapper.Status   `json:"status"`
}

// PreviewCell is one cell for the confirmation grid. The kind is shown so the
// operator can see that a date really was read as a date.
type PreviewCell struct {
	Text string `json:"text"`
	Kind string `json:"kind"`
}

// SelectSheet confirms the sheet and header row.
func (a *App) SelectSheet(name string, headerRow int) (SheetSelection, error) {
	ctx, done := a.operation()
	defer done()

	a.mu.Lock()
	wb := a.session.workbook
	a.mu.Unlock()
	if wb == nil {
		return SheetSelection{}, errors.New("open a workbook first")
	}

	// Describe walks the whole sheet to count its rows, which is seconds on a
	// large file. Report as it goes, or the window looks hung.
	wb.Progress = func(rows int) { a.emitProgress("reading the sheet", rows, 0, "") }
	defer func() { wb.Progress = nil }()

	info, err := wb.Describe(ctx, name, headerRow)
	if err != nil {
		return SheetSelection{}, err
	}

	rows, err := wb.Preview(ctx, name, info.DataStart, 10)
	if err != nil {
		return SheetSelection{}, err
	}

	preview := make([][]PreviewCell, 0, len(rows))
	for _, r := range rows {
		row := make([]PreviewCell, len(info.Headers))
		for i := range info.Headers {
			c := r.Cell(i)
			row[i] = PreviewCell{Text: previewText(c), Kind: kindName(c.Kind)}
		}
		preview = append(preview, row)
	}

	var warnings []string
	merged, mergeChecked, _ := wb.MergedRanges(name)
	switch {
	case !mergeChecked:
		// Saying nothing would read as "no merged cells", which is the one
		// thing this must not be mistaken for.
		warnings = append(warnings,
			"this sheet is too large to check for merged cells without loading all of it into memory, so that check was skipped; a merged cell holds its value only in its top-left cell and the rest read as empty")
	case len(merged) > 0:
		warnings = append(warnings, fmt.Sprintf(
			"this sheet has %d merged ranges; a merged cell holds its value only in the top-left cell, so the rest read as empty",
			len(merged)))
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	a.session.sheet = info

	// The fingerprint comparison belongs here, not at load time: this is the
	// first moment there is a workbook to compare against. It is the main
	// guard against silently mis-mapping a changed template.
	var status *mapper.Status
	if c := a.session.loadedConfig; c != nil {
		for _, w := range config.CheckWorkbook(*c, info) {
			warnings = append(warnings, w.Message+": "+w.Detail)
		}
	}

	if a.session.introspection != nil && len(a.session.mappings) > 0 {
		checked := mapper.Check(a.session.mappings, a.session.introspection.Schema,
			info.Headers, a.session.pk.Strategy)
		status = &checked
	}

	return SheetSelection{Info: info, Preview: preview, Warnings: warnings, Status: status}, nil
}

// ---------- Step 4: mapping ----------

// AutoMatchResult is the proposal plus the mappings it produced.
type AutoMatchResult struct {
	Suggestions []mapper.Suggestion    `json:"suggestions"`
	Mappings    []domain.ColumnMapping `json:"mappings"`
	Status      mapper.Status          `json:"status"`
}

// AutoMatch proposes mappings by name similarity.
func (a *App) AutoMatch() (AutoMatchResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.session.introspection == nil {
		return AutoMatchResult{}, errors.New("choose a table first")
	}
	if len(a.session.sheet.Headers) == 0 {
		return AutoMatchResult{}, errors.New("choose a sheet first")
	}

	schema := a.session.introspection.Schema
	suggestions := mapper.AutoMatch(a.session.sheet.Headers, schema.Columns)
	mappings := mapper.ToMappings(suggestions, schema.Columns)

	a.session.mappings = mappings
	status := mapper.Check(mappings, schema, a.session.sheet.Headers, a.session.pk.Strategy)

	return AutoMatchResult{Suggestions: suggestions, Mappings: mappings, Status: status}, nil
}

// TransformFor is the default set of adjustments for a target column.
//
// The mapping screen asks for this when the operator picks a target, rather
// than deciding it in TypeScript. Whether a blank may become NULL depends on
// the column's nullability, and two copies of that rule is one too many.
func (a *App) TransformFor(dbColumn string) (domain.Transform, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.session.introspection == nil {
		return domain.Transform{}, errors.New("choose a table first")
	}

	for _, c := range a.session.introspection.Schema.Columns {
		if c.Name == dbColumn {
			return mapper.DefaultTransform(c), nil
		}
	}
	return domain.Transform{}, fmt.Errorf("the table has no column named %q", dbColumn)
}

// SetMappings records the operator's mapping and returns the live problem list.
func (a *App) SetMappings(mappings []domain.ColumnMapping) (mapper.Status, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.session.introspection == nil {
		return mapper.Status{}, errors.New("choose a table first")
	}

	a.session.mappings = mappings
	a.session.lastReport = nil
	a.session.dryRunOK = false

	return mapper.Check(mappings, a.session.introspection.Schema, a.session.sheet.Headers, a.session.pk.Strategy), nil
}

// ---------- Step 5: primary key ----------

// PKChoice is the strategy decision.
type PKChoice struct {
	Strategy   domain.PKStrategy `json:"strategy"`
	Columns    []string          `json:"columns"`
	EmitSetval bool              `json:"emitSetval"`
}

// PKOption is one offered strategy, with its consequence spelled out.
type PKOption struct {
	Strategy    domain.PKStrategy `json:"strategy"`
	Label       string            `json:"label"`
	Description string            `json:"description"`
	Recommended bool              `json:"recommended"`
	Available   bool              `json:"available"`
	Reason      string            `json:"reason"`
}

// SetPKStrategy records the primary key decision.
func (a *App) SetPKStrategy(choice PKChoice) (mapper.Status, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.session.introspection == nil {
		return mapper.Status{}, errors.New("choose a table first")
	}

	a.session.pk = choice
	a.session.lastReport = nil
	a.session.dryRunOK = false

	return mapper.Check(a.session.mappings, a.session.introspection.Schema,
		a.session.sheet.Headers, choice.Strategy), nil
}

// ---------- Step 6: validation ----------

// SetValidationOptions records the checking choices.
func (a *App) SetValidationOptions(s validator.Settings, sourceTimezone string, allowRounding, enumCaseInsensitive bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.session.settings = s
	a.session.opts.AllowNumericRounding = allowRounding
	a.session.opts.EnumCaseInsensitive = enumCaseInsensitive

	if sourceTimezone != "" {
		loc, err := time.LoadLocation(sourceTimezone)
		if err != nil {
			return fmt.Errorf("unknown timezone %q", sourceTimezone)
		}
		a.session.opts.SourceTimezone = loc
	}
	return nil
}

// Validate checks the whole file against the table's real rules.
func (a *App) Validate() (validator.Report, error) {
	ctx, done := a.operation()
	defer done()

	in, err := a.validatorInput()
	if err != nil {
		return validator.Report{}, err
	}
	in.Progress = a.progressFunc("validating")

	rep, err := validator.Run(ctx, in)
	if err != nil {
		return validator.Report{}, a.redact(err)
	}

	a.mu.Lock()
	a.session.lastReport = &rep
	a.mu.Unlock()

	return rep, nil
}

// DryRun runs the real statements in a transaction that is always rolled back.
// Nothing is written, but triggers fire and consumed sequence values are not
// given back.
func (a *App) DryRun() (validator.Report, error) {
	ctx, done := a.operation()
	defer done()

	a.mu.Lock()
	pool := a.session.pool
	priv := a.session.privileges
	wb := a.session.workbook
	sheet := a.session.sheet
	opts := a.session.opts
	settings := a.session.settings
	plan, planErr := a.planLocked()
	a.mu.Unlock()

	if planErr != nil {
		return validator.Report{}, planErr
	}
	if pool == nil {
		return validator.Report{}, errors.New("live verification needs a database connection")
	}
	if !priv.CanInsert {
		return validator.Report{}, fmt.Errorf(
			"live verification needs INSERT privilege on %s, which this user does not have", priv.Table)
	}

	rep, err := validator.DryRun(ctx, validator.DryRunInput{
		Pool:      pool.Pool,
		Workbook:  wb,
		SheetName: sheet.Name,
		DataStart: sheet.DataStart,
		Plan:      plan,
		Opts:      opts,
		MaxIssues: settings.MaxIssues,
		SkipBlank: settings.SkipBlankRows,
		Progress:  a.progressFunc("verifying"),
	})
	if err != nil {
		return validator.Report{}, a.redact(err)
	}

	a.mu.Lock()
	a.session.dryRunOK = rep.OK()
	a.mu.Unlock()

	return rep, nil
}

// ExportIssues writes the issue list for whoever supplied the data.
func (a *App) ExportIssues() (string, error) {
	a.mu.Lock()
	rep := a.session.lastReport
	a.mu.Unlock()

	if rep == nil {
		return "", errors.New("run validation first")
	}

	path, err := a.saveFileDialog("Export issues", "issues.xlsx",
		"Excel workbook (*.xlsx)", "*.xlsx;*.csv")
	if err != nil || path == "" {
		return "", err
	}

	// The recipient of this file works in Excel, so a workbook is the default.
	// A .csv name still produces a CSV, for anyone piping it somewhere.
	if strings.EqualFold(filepath.Ext(path), ".csv") {
		f, err := os.Create(path)
		if err != nil {
			return "", err
		}
		defer f.Close()

		if err := rep.WriteCSV(f); err != nil {
			return "", err
		}
		return path, nil
	}

	if filepath.Ext(path) == "" {
		path += ".xlsx"
	}
	if err := rep.WriteXLSX(path); err != nil {
		return "", err
	}
	return path, nil
}

// ExecuteDirect inserts the rows and commits: the only operation that writes.
// Deliberately hard to reach — the table name has to be typed to confirm,
// INSERT privilege is required, and validation has to have passed.
func (a *App) ExecuteDirect(opts GenerateOptions, confirmTableName string) (validator.ExecResult, error) {
	ctx, done := a.operation()
	defer done()

	a.mu.Lock()
	rep := a.session.lastReport
	pool := a.session.pool
	priv := a.session.privileges
	wb := a.session.workbook
	sheet := a.session.sheet
	coerceOpts := a.session.opts
	pk := a.session.pk
	schemaPtr := a.session.introspection
	plan, planErr := a.planLocked()
	a.mu.Unlock()

	if planErr != nil {
		return validator.ExecResult{}, planErr
	}
	if rep == nil {
		return validator.ExecResult{}, errors.New("run validation first")
	}
	if !rep.OK() {
		return validator.ExecResult{}, fmt.Errorf(
			"validation found %d errors; direct execution is not available until they are fixed", rep.ErrorCount)
	}
	if pool == nil {
		return validator.ExecResult{}, errors.New("connect to a database first")
	}
	if !priv.CanInsert {
		return validator.ExecResult{}, fmt.Errorf(
			"this user has no INSERT privilege on %s", priv.Table)
	}

	schema := schemaPtr.Schema
	// Matched case-insensitively so what the operator is asked to type is what
	// is accepted: the field shows the name in lower case, and a table called
	// Customers must not silently reject "customers".
	if !strings.EqualFold(strings.TrimSpace(confirmTableName), schema.Table) {
		return validator.ExecResult{}, fmt.Errorf(
			"type %q to confirm writing to %s.%s",
			strings.ToLower(schema.Table), schema.Schema, schema.Table)
	}

	coerceOpts.StandardConformingStrings = pool.Info.StandardConformingStrings

	setvalColumn := ""
	if pk.Strategy == domain.PKMapped && pk.EmitSetval && schema.PKSequence != nil &&
		schema.PrimaryKey != nil && len(schema.PrimaryKey.Columns) == 1 {
		setvalColumn = schema.PrimaryKey.Columns[0]
	}

	a.log.Info("direct execution starting",
		"table", schema.Schema+"."+schema.Table, "rows", rep.RowsValid)

	res, execReport, err := validator.Execute(ctx, validator.ExecuteInput{
		Pool:         pool.Pool,
		Workbook:     wb,
		SheetName:    sheet.Name,
		DataStart:    sheet.DataStart,
		Plan:         plan,
		Opts:         coerceOpts,
		SkipBlank:    opts.SkipBlankRows,
		BatchSize:    opts.BatchSize,
		SetvalColumn: setvalColumn,
		Progress:     a.progressFunc("inserting"),
	})
	if err != nil {
		a.log.Error("direct execution rolled back", "error", err.Error())
		if len(execReport.Issues) > 0 {
			i := execReport.Issues[0]
			return res, fmt.Errorf("nothing was written. Row %d: %s", i.ExcelRow, i.Message)
		}
		return res, a.redact(err)
	}

	a.log.Info("direct execution committed", "rows", res.RowsInserted)
	return res, nil
}

// ---------- Step 7: generate ----------

// GenerateOptions are the output choices from the generate screen.
type GenerateOptions struct {
	Mode                 string `json:"mode"`
	BatchSize            int    `json:"batchSize"`
	WrapInTransaction    bool   `json:"wrapInTransaction"`
	IncludeSummaryHeader bool   `json:"includeSummaryHeader"`
	SkipBlankRows        bool   `json:"skipBlankRows"`
	StatementTimeout     string `json:"statementTimeout"`
	Path                 string `json:"path"`
}

// GenerateResult reports what was written and where.
type GenerateResult struct {
	Path         string   `json:"path"`
	RowsWritten  int      `json:"rowsWritten"`
	RowsSkipped  int      `json:"rowsSkipped"`
	Statements   int      `json:"statements"`
	BytesWritten int64    `json:"bytesWritten"`
	Duration     string   `json:"duration"`
	PersonalData []string `json:"personalData"`
	Preview      []string `json:"preview"`
}

// Generate writes the .sql file.
func (a *App) Generate(opts GenerateOptions) (GenerateResult, error) {
	ctx, done := a.operation()
	defer done()

	a.mu.Lock()
	rep := a.session.lastReport
	a.mu.Unlock()

	if rep == nil {
		return GenerateResult{}, errors.New("run validation first")
	}
	if !rep.OK() {
		return GenerateResult{}, fmt.Errorf("validation found %d errors; fix them before generating", rep.ErrorCount)
	}

	path := opts.Path
	if path == "" {
		var err error
		path, err = a.saveFileDialog("Save import script", a.suggestedSQLName(), "SQL (*.sql)", "*.sql")
		if err != nil || path == "" {
			return GenerateResult{}, err
		}
	}

	in, err := a.generateInput(opts)
	if err != nil {
		return GenerateResult{}, err
	}

	f, err := os.Create(path)
	if err != nil {
		return GenerateResult{}, err
	}

	res, genErr := sqlgen.Generate(ctx, f, in)
	closeErr := f.Close()

	if genErr != nil {
		// A partial .sql file is worse than none: it looks runnable and is not.
		os.Remove(path)
		return GenerateResult{}, genErr
	}
	if closeErr != nil {
		os.Remove(path)
		return GenerateResult{}, closeErr
	}

	preview, _ := headLines(path, 50)

	return GenerateResult{
		Path:         path,
		RowsWritten:  res.RowsWritten,
		RowsSkipped:  res.RowsSkipped,
		Statements:   res.Statements,
		BytesWritten: res.BytesWritten,
		Duration:     res.Duration,
		PersonalData: in.Meta.PersonalDataColumns,
		Preview:      preview,
	}, nil
}

// ---------- Configuration ----------

// ExportConfig saves the mapping and settings for reuse.
func (a *App) ExportConfig() (string, error) {
	a.mu.Lock()
	c, err := a.configLocked()
	a.mu.Unlock()
	if err != nil {
		return "", err
	}

	path, err := a.saveFileDialog("Export configuration", config.SuggestFilename(c),
		"PGSheet configuration (*.json)", "*.json")
	if err != nil || path == "" {
		return "", err
	}

	if err := config.Save(path, c); err != nil {
		return "", err
	}
	return path, nil
}

// ConfigLoadResult carries the configuration and whatever did not line up
// with the current table and workbook. Table is the target it names, selected
// as part of loading: the file already knows which table it is for.
type ConfigLoadResult struct {
	Config   config.Config    `json:"config"`
	Warnings []config.Warning `json:"warnings"`
	Status   mapper.Status    `json:"status"`
	Table    *TableDetail     `json:"table"`
}

// ImportConfig loads a saved configuration and applies it to this session.
func (a *App) ImportConfig() (ConfigLoadResult, error) {
	path, err := a.openFileDialog("Load configuration", "PGSheet configuration (*.json)", "*.json")
	if err != nil || path == "" {
		return ConfigLoadResult{}, err
	}
	return a.ImportConfigAt(path)
}

// ImportConfigAt loads a configuration from a specific path.
func (a *App) ImportConfigAt(path string) (ConfigLoadResult, error) {
	c, warnings, err := config.Load(path)
	if err != nil {
		return ConfigLoadResult{}, err
	}

	// Select the table the configuration names, if we are connected and it is
	// not already selected. The I/O happens outside the lock.
	a.mu.Lock()
	connected := a.session.pool != nil
	alreadySelected := a.session.introspection != nil &&
		a.session.introspection.Schema.Schema == c.Target.Schema &&
		a.session.introspection.Schema.Table == c.Target.Table
	a.mu.Unlock()

	var table *TableDetail
	if connected && !alreadySelected {
		detail, err := a.DescribeTable(c.Target.Schema, c.Target.Table)
		if err != nil {
			// Not fatal: the operator can pick the table by hand. Saying which
			// table the file wanted is more useful than the raw error alone.
			warnings = append(warnings, config.Warning{
				Kind:    "schema",
				Message: fmt.Sprintf("could not open %s.%s, the table this configuration was saved for", c.Target.Schema, c.Target.Table),
				Detail:  err.Error(),
			})
		} else {
			table = &detail
		}
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if table == nil && a.session.introspection != nil {
		detail := TableDetail{
			Schema:        a.session.introspection.Schema,
			UniqueIndexes: a.session.introspection.UniqueIndexes,
			Rules:         a.session.introspection.UniquenessRules(),
			Privileges:    a.session.privileges,
			PKOptions:     pkOptions(a.session.introspection.Schema),
		}
		table = &detail
	}

	if a.session.introspection != nil {
		warnings = append(warnings, config.CheckSchema(c, a.session.introspection.Schema)...)
	}
	// Only meaningful once a workbook is open; otherwise SelectSheet does it.
	if a.session.sheet.Fingerprint != "" {
		warnings = append(warnings, config.CheckWorkbook(c, a.session.sheet)...)
	}
	a.session.loadedConfig = &c

	a.session.mappings = c.Mappings
	a.session.pk = PKChoice{
		Strategy:   c.PrimaryKey.Strategy,
		Columns:    c.PrimaryKey.Columns,
		EmitSetval: c.PrimaryKey.EmitSetval,
	}
	a.session.settings = validator.Settings{
		MaxIssues:               c.Validation.MaxIssues,
		ColumnMisalignThreshold: c.Validation.ColumnMisalignThreshold,
		CheckUniqueAgainstDB:    c.Validation.CheckUniqueAgainstDB,
		CheckForeignKeys:        c.Validation.CheckForeignKeys,
		SkipBlankRows:           c.Output.SkipBlankRows,
	}
	a.session.opts.AllowNumericRounding = c.Validation.AllowNumericRounding
	a.session.opts.EnumCaseInsensitive = c.Validation.EnumCaseInsensitive
	if c.Validation.SourceTimezone != "" {
		if loc, err := time.LoadLocation(c.Validation.SourceTimezone); err == nil {
			a.session.opts.SourceTimezone = loc
		} else {
			warnings = append(warnings, config.Warning{
				Kind:    "version",
				Message: fmt.Sprintf("the saved timezone %q is not known on this machine", c.Validation.SourceTimezone),
				Detail:  "times will be interpreted in the local timezone instead",
			})
		}
	}
	a.session.configName = filepath.Base(path)
	a.session.lastReport = nil
	a.session.dryRunOK = false

	// Applying a configuration replaces the mapping wholesale, so anything the
	// operator had opened is no longer described by it.

	var status mapper.Status
	if a.session.introspection != nil {
		status = mapper.Check(c.Mappings, a.session.introspection.Schema,
			a.session.sheet.Headers, c.PrimaryKey.Strategy)
	}

	return ConfigLoadResult{Config: c, Warnings: warnings, Status: status, Table: table}, nil
}

// ---------- helpers ----------

func (a *App) requirePool() (*dbconn.Pool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.session.pool == nil {
		return nil, errors.New("connect to a database first")
	}
	return a.session.pool, nil
}

// planLocked resolves the mapping into the plan the validator and generator
// both work from. The caller holds a.mu.
func (a *App) planLocked() (mapper.Plan, error) {
	if a.session.introspection == nil {
		return mapper.Plan{}, errors.New("choose a table first")
	}
	if a.session.workbook == nil || a.session.sheet.Name == "" {
		return mapper.Plan{}, errors.New("choose a sheet first")
	}
	if len(a.session.mappings) == 0 {
		return mapper.Plan{}, errors.New("map at least one column first")
	}
	return mapper.BuildPlan(a.session.mappings, a.session.introspection.Schema, a.session.pk.Strategy), nil
}

func (a *App) validatorInput() (validator.Input, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	plan, err := a.planLocked()
	if err != nil {
		return validator.Input{}, err
	}

	opts := a.session.opts
	if a.session.pool != nil {
		opts.StandardConformingStrings = a.session.pool.Info.StandardConformingStrings
	} else {
		// Without a connection the safe assumption is the default every
		// supported server ships with; the generated file is then correct on
		// any server that has not been reconfigured.
		opts.StandardConformingStrings = true
	}

	in := validator.Input{
		Workbook:      a.session.workbook,
		Sheet:         a.session.sheet,
		Mappings:      a.session.mappings,
		Plan:          plan,
		Introspection: *a.session.introspection,
		Opts:          opts,
		Settings:      a.session.settings,
	}
	if a.session.pool != nil {
		in.Pool = a.session.pool.Pool
	}
	return in, nil
}

func (a *App) generateInput(opts GenerateOptions) (sqlgen.Input, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	plan, err := a.planLocked()
	if err != nil {
		return sqlgen.Input{}, err
	}

	coerceOpts := a.session.opts
	coerceOpts.StandardConformingStrings = true
	serverInfo := ""
	if a.session.pool != nil {
		coerceOpts.StandardConformingStrings = a.session.pool.Info.StandardConformingStrings
		serverInfo = shortVersion(a.session.pool.Info.Version) + ", " +
			a.session.pool.Info.Database + "/" + a.session.pool.Info.User
	}

	schema := a.session.introspection.Schema
	columns := validator.PlanColumnNames(plan)

	target := sqlgen.Target{
		Schema:  schema.Schema,
		Table:   schema.Table,
		Columns: columns,
	}
	// setval is emitted only when this file supplies explicit key values.
	if a.session.pk.Strategy == domain.PKMapped && a.session.pk.EmitSetval && schema.PKSequence != nil {
		if len(schema.PrimaryKey.Columns) == 1 {
			target.SetvalColumn = schema.PrimaryKey.Columns[0]
		}
	}

	genOpts := sqlgen.Options{
		Mode:                 sqlgen.Mode(opts.Mode),
		BatchSize:            opts.BatchSize,
		WrapInTransaction:    opts.WrapInTransaction,
		IncludeSummaryHeader: opts.IncludeSummaryHeader,
		SkipBlankRows:        opts.SkipBlankRows,
		StatementTimeout:     opts.StatementTimeout,
	}

	rep := a.session.lastReport
	meta := sqlgen.Meta{
		Version:             a.version,
		GeneratedAt:         time.Now(),
		SourceFile:          filepath.Base(a.session.workbook.Path()),
		SheetName:           a.session.sheet.Name,
		HeaderRow:           a.session.sheet.HeaderRow,
		FirstRow:            a.session.sheet.DataStart,
		LastRow:             a.session.sheet.DataStart + a.session.sheet.TotalRows - 1,
		Fingerprint:         a.session.sheet.Fingerprint,
		ConfigName:          a.session.configName,
		TargetSchema:        schema.Schema,
		TargetTable:         schema.Table,
		ServerInfo:          serverInfo,
		ColumnsMapped:       len(columns),
		ColumnsDefault:      defaultedColumns(schema, columns),
		PrimaryKey:          describePK(schema, a.session.pk),
		PrimaryKeyNote:      pkNote(a.session.pk.Strategy),
		Validated:           validator.Describe(a.session.settings, a.session.dryRunOK),
		PersonalDataColumns: sqlgen.FlagPersonalData(columns),
	}
	if rep != nil {
		meta.RowsToInsert = rep.RowsValid
		meta.RowsSkipped = rep.RowsSkipped
		meta.Warnings = rep.WarningCount
	}

	return sqlgen.Input{
		Workbook:  a.session.workbook,
		SheetName: a.session.sheet.Name,
		DataStart: a.session.sheet.DataStart,
		Target:    target,
		Coerce:    validator.RowLiterals(plan, coerceOpts, opts.SkipBlankRows),
		Options:   genOpts,
		Meta:      meta,
		Total:     a.session.sheet.TotalRows,
		Progress:  func(current, total int) { a.emitProgress("generating", current, total, "") },
	}, nil
}

func (a *App) configLocked() (config.Config, error) {
	if a.session.introspection == nil {
		return config.Config{}, errors.New("choose a table first")
	}
	if len(a.session.mappings) == 0 {
		return config.Config{}, errors.New("map at least one column first")
	}

	schema := a.session.introspection.Schema
	tz := ""
	if a.session.opts.SourceTimezone != nil {
		tz = a.session.opts.SourceTimezone.String()
	}

	c := config.Default()
	c.Name = schema.Table
	c.CreatedBy = "pgsheet/" + a.version
	c.Target = config.Target{Schema: schema.Schema, Table: schema.Table}
	c.Source = config.Source{
		SheetName:         a.session.sheet.Name,
		HeaderRow:         a.session.sheet.HeaderRow,
		DataStartRow:      a.session.sheet.DataStart,
		HeaderFingerprint: a.session.sheet.Fingerprint,
		Headers:           a.session.sheet.Headers,
	}
	c.Mappings = a.session.mappings
	c.PrimaryKey = config.PrimaryKey{
		Strategy:   a.session.pk.Strategy,
		Columns:    a.session.pk.Columns,
		EmitSetval: a.session.pk.EmitSetval,
	}
	c.Validation = config.Validation{
		CheckUniqueAgainstDB:    a.session.settings.CheckUniqueAgainstDB,
		CheckForeignKeys:        a.session.settings.CheckForeignKeys,
		ColumnMisalignThreshold: a.session.settings.ColumnMisalignThreshold,
		MaxIssues:               a.session.settings.MaxIssues,
		AllowNumericRounding:    a.session.opts.AllowNumericRounding,
		EnumCaseInsensitive:     a.session.opts.EnumCaseInsensitive,
		SourceTimezone:          tz,
	}
	c.Output.SkipBlankRows = a.session.settings.SkipBlankRows
	return c, nil
}

func (a *App) suggestedSQLName() string {
	a.mu.Lock()
	defer a.mu.Unlock()

	table := "import"
	if a.session.introspection != nil {
		table = a.session.introspection.Schema.Table
	}
	return fmt.Sprintf("%s_%s.sql", table, time.Now().Format("2006_01_02"))
}

// defaultPKChoice picks the safe default: let the database assign the key.
// Fixed values in a file that runs hours later is how an import collides with
// live traffic.
func defaultPKChoice(schema domain.TableSchema) PKChoice {
	if schema.PrimaryKey == nil {
		return PKChoice{Strategy: domain.PKNone}
	}
	if databaseSuppliesKey(schema) {
		return PKChoice{Strategy: domain.PKSequence, Columns: schema.PrimaryKey.Columns}
	}
	return PKChoice{Strategy: domain.PKMapped, Columns: schema.PrimaryKey.Columns}
}

// databaseSuppliesKey reports whether the table can fill its own primary key.
//
// A sequence is the common case, not the only one: a uuid key with DEFAULT
// gen_random_uuid() is database-generated with no sequence anywhere. Testing
// for a sequence would treat it as a natural key and make the operator invent
// values the database was going to produce. Every part of a composite key has
// to be supplied for this to hold.
func databaseSuppliesKey(schema domain.TableSchema) bool {
	if schema.PrimaryKey == nil || len(schema.PrimaryKey.Columns) == 0 {
		return false
	}

	byName := make(map[string]domain.Column, len(schema.Columns))
	for _, c := range schema.Columns {
		byName[c.Name] = c
	}

	for _, name := range schema.PrimaryKey.Columns {
		col, ok := byName[name]
		if !ok {
			return false
		}
		if !col.IsIdentity && !col.HasDefault {
			return false
		}
	}
	return true
}

// keyDefaults describes how the database would fill each key column.
func keyDefaults(schema domain.TableSchema) []string {
	byName := make(map[string]domain.Column, len(schema.Columns))
	for _, c := range schema.Columns {
		byName[c.Name] = c
	}

	var out []string
	for _, name := range schema.PrimaryKey.Columns {
		col := byName[name]
		switch {
		case col.IsIdentity:
			out = append(out, name+": identity")
		case col.DefaultExpr != "":
			out = append(out, name+": "+col.DefaultExpr)
		}
	}
	return out
}

func pkOptions(schema domain.TableSchema) []PKOption {
	if schema.PrimaryKey == nil {
		return []PKOption{{
			Strategy:    domain.PKNone,
			Label:       "This table has no primary key",
			Description: "Every mapped column is inserted as-is. Nothing prevents duplicate rows.",
			Available:   true,
			Recommended: true,
		}}
	}

	generated := databaseSuppliesKey(schema)
	cols := strings.Join(schema.PrimaryKey.Columns, ", ")

	seqDesc := "The database assigns the key when the file runs. The column is left out of the file entirely, so values cannot collide with rows added between now and then."
	switch {
	case schema.PKSequence != nil:
		seqDesc += fmt.Sprintf(" The next value is currently around %d. That is an estimate, not a reservation.",
			schema.PKSequence.NextValue)
	case generated:
		// No sequence to quote a next value from, so say what the database
		// will actually run instead.
		seqDesc += " Assigned by: " + strings.Join(keyDefaults(schema), ", ") + "."
	}

	return []PKOption{
		{
			Strategy:    domain.PKSequence,
			Label:       "Let the database assign " + cols,
			Description: seqDesc,
			Available:   generated,
			Recommended: generated,
			Reason: unavailableReason(generated,
				"this key has no identity and no default, so the database has nothing to assign from. The values have to come from the sheet"),
		},
		{
			Strategy: domain.PKMapped,
			Label:    "Take " + cols + " from the spreadsheet",
			Description: "Values come from the sheet and are checked for uniqueness both within the file and against the table. " +
				"If the key has a sequence, a setval is added at the end of the file so the database's counter moves past the inserted rows.",
			Available: true,
		},
	}
}

func unavailableReason(available bool, reason string) string {
	if available {
		return ""
	}
	return reason
}

func describePK(schema domain.TableSchema, choice PKChoice) string {
	if schema.PrimaryKey == nil {
		return "none"
	}
	cols := strings.Join(schema.PrimaryKey.Columns, ", ")
	switch choice.Strategy {
	case domain.PKSequence:
		if schema.PKSequence != nil {
			return fmt.Sprintf("%s  (sequence %s)", cols, schema.PKSequence.Name)
		}
		if defaults := keyDefaults(schema); len(defaults) > 0 {
			return fmt.Sprintf("%s  (%s)", cols, strings.Join(defaults, ", "))
		}
		return cols
	case domain.PKMapped:
		return fmt.Sprintf("%s  (values from the spreadsheet)", cols)
	default:
		return cols
	}
}

func pkNote(s domain.PKStrategy) string {
	switch s {
	case domain.PKSequence:
		return "Values assigned by the database at run time."
	case domain.PKMapped:
		return "Explicit values from the spreadsheet; the sequence is resynchronised at the end of this file."
	default:
		return ""
	}
}

func defaultedColumns(schema domain.TableSchema, mapped []string) []string {
	inFile := make(map[string]bool, len(mapped))
	for _, m := range mapped {
		inFile[m] = true
	}

	var out []string
	for _, c := range schema.Columns {
		if inFile[c.Name] {
			continue
		}
		if c.HasDefault || c.IsIdentity || c.IsGenerated {
			out = append(out, c.Name)
		}
	}
	return out
}

func previewText(c domain.CellValue) string {
	switch c.Kind {
	case domain.CellEmpty:
		return ""
	case domain.CellDate:
		return c.Time.Format("2006-01-02 15:04:05")
	case domain.CellBool:
		if c.Bool {
			return "TRUE"
		}
		return "FALSE"
	case domain.CellNumber:
		if c.Str != "" {
			return c.Str
		}
		return c.Num.String()
	case domain.CellString:
		return c.Str
	default:
		return c.RawText
	}
}

func kindName(k domain.CellKind) string {
	switch k {
	case domain.CellEmpty:
		return "empty"
	case domain.CellString:
		return "text"
	case domain.CellNumber:
		return "number"
	case domain.CellBool:
		return "boolean"
	case domain.CellDate:
		return "date"
	case domain.CellError:
		return "error"
	default:
		return "unknown"
	}
}

func shortVersion(v string) string {
	if i := strings.Index(v, " on "); i > 0 {
		return v[:i]
	}
	if len(v) > 60 {
		return v[:60]
	}
	return v
}

func headLines(path string, n int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// The preview only ever shows the first lines, so a bounded read is enough
	// however large the generated file is.
	buf := make([]byte, 64*1024)
	read, err := f.Read(buf)
	if err != nil && read == 0 {
		return nil, err
	}

	lines := strings.Split(string(buf[:read]), "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return lines, nil
}
