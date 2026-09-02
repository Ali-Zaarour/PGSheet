// Command pgsheet-cli is the headless PGSheet binary.
//
// It is deliberately thin: because the engine packages have no Wails
// dependency, the CLI is a wrapper over exactly the same code the desktop app
// runs — the same reader, the same coercion, the same generator. That is what
// makes a recurring monthly import cheap (spec §18).
//
// Exit codes: 0 success, 1 validation errors, 2 connection failure,
// 3 configuration error.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"pgsheet/internal/config"
	"pgsheet/internal/dbconn"
	"pgsheet/internal/domain"
	"pgsheet/internal/excel"
	"pgsheet/internal/introspect"
	"pgsheet/internal/mapper"
	"pgsheet/internal/sqlgen"
	"pgsheet/internal/validator"
	pgversion "pgsheet/internal/version"
)

// version is the single definition, shared with the desktop application.
var version = pgversion.String()

const (
	exitOK            = 0
	exitValidation    = 1
	exitConnection    = 2
	exitConfiguration = 3
)

// exitError carries the exit code a failure should produce, so the codes stay
// meaningful to whatever scheduled the run.
type exitError struct {
	code int
	err  error
}

func (e exitError) Error() string { return e.err.Error() }
func (e exitError) Unwrap() error { return e.err }

func fail(code int, format string, args ...any) error {
	return exitError{code: code, err: fmt.Errorf(format, args...)}
}

func main() {
	root := &cobra.Command{
		Use:           "pgsheet-cli",
		Short:         "Generate and validate PostgreSQL imports from Excel files",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.AddCommand(importCmd(), validateCmd())

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "pgsheet-cli:", err)

		var ee exitError
		if errors.As(err, &ee) {
			os.Exit(ee.code)
		}
		os.Exit(exitConfiguration)
	}
}

// commonFlags are shared by import and validate.
//
// There is no password flag, by design: flags land in shell history and in
// process listings. The password comes from PGPASSWORD or a terminal prompt.
type commonFlags struct {
	config        string
	file          string
	dsn           string
	sheet         string
	jsonOut       bool
	failOnWarning bool
	checkDB       bool
	checkFK       bool
	timezone      string
}

func (f *commonFlags) bind(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.config, "config", "", "path to a .pgsheet.json configuration (required)")
	cmd.Flags().StringVar(&f.file, "file", "", "path to the .xlsx workbook (required)")
	cmd.Flags().StringVar(&f.dsn, "dsn", "", "PostgreSQL connection string, without a password (required)")
	cmd.Flags().StringVar(&f.sheet, "sheet", "", "worksheet to read (defaults to the one named in the configuration)")
	cmd.Flags().BoolVar(&f.jsonOut, "json", false, "emit the report as JSON")
	cmd.Flags().BoolVar(&f.failOnWarning, "fail-on-warning", false, "treat warnings as errors")
	cmd.Flags().BoolVar(&f.checkDB, "check-existing", true, "check unique values against the target table")
	cmd.Flags().BoolVar(&f.checkFK, "check-references", false, "check foreign key values against the referenced tables")
	cmd.Flags().StringVar(&f.timezone, "timezone", "", "source timezone for naive spreadsheet times (overrides the configuration)")

	_ = cmd.MarkFlagRequired("config")
	_ = cmd.MarkFlagRequired("file")
	_ = cmd.MarkFlagRequired("dsn")
}

func importCmd() *cobra.Command {
	var (
		f     commonFlags
		out   string
		force bool
	)

	cmd := &cobra.Command{
		Use:   "import",
		Short: "Validate a workbook and write the .sql import script",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()
			return runImport(ctx, f, out, force)
		},
	}

	f.bind(cmd)
	cmd.Flags().StringVar(&out, "out", "", "path to write the generated .sql file (required)")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite the output file if it exists")
	_ = cmd.MarkFlagRequired("out")
	return cmd
}

func validateCmd() *cobra.Command {
	var f commonFlags

	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate a workbook against the target table without writing anything",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()

			sess, err := prepare(ctx, f)
			if err != nil {
				return err
			}
			defer sess.close()

			rep, err := sess.validate(ctx)
			if err != nil {
				return err
			}
			return sess.finish(rep, f)
		},
	}

	f.bind(cmd)
	return cmd
}

// session holds everything one CLI run resolved, mirroring what the desktop
// app keeps in memory for one operator session.
type session struct {
	cfg      config.Config
	pool     *dbconn.Pool
	res      introspect.Result
	wb       *excel.Workbook
	sheet    domain.SheetInfo
	plan     mapper.Plan
	opts     validator.Options
	settings validator.Settings
	password string
}

func (s *session) close() {
	if s.wb != nil {
		_ = s.wb.Close()
	}
	if s.pool != nil {
		s.pool.Close()
	}
	s.password = ""
}

// prepare does everything the seven screens do, without asking anything.
func prepare(ctx context.Context, f commonFlags) (*session, error) {
	cfg, warnings, err := config.Load(f.config)
	if err != nil {
		return nil, fail(exitConfiguration, "%v", err)
	}
	for _, w := range warnings {
		fmt.Fprintf(os.Stderr, "warning: %s (%s)\n", w.Message, w.Detail)
	}

	password, err := resolvePassword()
	if err != nil {
		return nil, fail(exitConnection, "%v", err)
	}

	pool, err := dbconn.ConnectDSN(ctx, f.dsn, password)
	if err != nil {
		return nil, fail(exitConnection, "%v", err)
	}

	s := &session{cfg: cfg, pool: pool, password: password}

	s.res, err = introspect.Table(ctx, pool.Pool, cfg.Target.Schema, cfg.Target.Table)
	if err != nil {
		s.close()
		return nil, fail(exitConnection, "%v", err)
	}

	for _, w := range config.CheckSchema(cfg, s.res.Schema) {
		fmt.Fprintf(os.Stderr, "warning: %s (%s)\n", w.Message, w.Detail)
	}

	s.wb, err = excel.Open(f.file)
	if err != nil {
		s.close()
		return nil, fail(exitConfiguration, "%v", err)
	}

	sheetName := f.sheet
	if sheetName == "" {
		sheetName = cfg.Source.SheetName
	}
	if sheetName == "" {
		sheets := s.wb.Sheets()
		if len(sheets) == 0 {
			s.close()
			return nil, fail(exitConfiguration, "%s has no worksheets", f.file)
		}
		sheetName = sheets[0]
	}

	s.sheet, err = s.wb.Describe(ctx, sheetName, cfg.Source.HeaderRow)
	if err != nil {
		s.close()
		return nil, fail(exitConfiguration, "%v", err)
	}

	// The fingerprint check is the guard that makes unattended reuse safe: a
	// client who reorders their template must not silently import into the
	// wrong columns (spec §12).
	if w := config.CheckWorkbook(cfg, s.sheet); len(w) > 0 {
		s.close()
		return nil, fail(exitConfiguration, "%s\n  %s\n  Re-map the file in the desktop application and export a new configuration.",
			w[0].Message, w[0].Detail)
	}

	s.opts = validator.Options{
		StandardConformingStrings: pool.Info.StandardConformingStrings,
		AllowNumericRounding:      cfg.Validation.AllowNumericRounding,
		EnumCaseInsensitive:       cfg.Validation.EnumCaseInsensitive,
		SourceTimezone:            time.Local,
	}

	tz := f.timezone
	if tz == "" {
		tz = cfg.Validation.SourceTimezone
	}
	if tz != "" {
		loc, err := time.LoadLocation(tz)
		if err != nil {
			s.close()
			return nil, fail(exitConfiguration, "unknown timezone %q", tz)
		}
		s.opts.SourceTimezone = loc
	}

	s.settings = validator.Settings{
		MaxIssues:               cfg.Validation.MaxIssues,
		ColumnMisalignThreshold: cfg.Validation.ColumnMisalignThreshold,
		CheckUniqueAgainstDB:    f.checkDB && cfg.Validation.CheckUniqueAgainstDB,
		CheckForeignKeys:        f.checkFK || cfg.Validation.CheckForeignKeys,
		SkipBlankRows:           cfg.Output.SkipBlankRows,
	}

	s.plan = mapper.BuildPlan(cfg.Mappings, s.res.Schema, cfg.PrimaryKey.Strategy)
	return s, nil
}

func (s *session) validate(ctx context.Context) (validator.Report, error) {
	rep, err := validator.Run(ctx, validator.Input{
		Workbook:      s.wb,
		Sheet:         s.sheet,
		Mappings:      s.cfg.Mappings,
		Plan:          s.plan,
		Introspection: s.res,
		Pool:          s.pool.Pool,
		Opts:          s.opts,
		Settings:      s.settings,
		Progress:      cliProgress(),
	})
	if err != nil {
		return validator.Report{}, fail(exitValidation, "%v", err)
	}
	return rep, nil
}

// finish prints the report and decides the exit code.
func (s *session) finish(rep validator.Report, f commonFlags) error {
	if f.jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			return err
		}
	} else {
		printReport(rep)
	}

	if rep.ErrorCount > 0 {
		return fail(exitValidation, "%d errors", rep.ErrorCount)
	}
	if f.failOnWarning && rep.WarningCount > 0 {
		return fail(exitValidation, "%d warnings, and --fail-on-warning is set", rep.WarningCount)
	}
	return nil
}

func runImport(ctx context.Context, f commonFlags, out string, force bool) error {
	if !force {
		if _, err := os.Stat(out); err == nil {
			return fail(exitConfiguration, "%s already exists; pass --force to overwrite it", out)
		}
	}

	sess, err := prepare(ctx, f)
	if err != nil {
		return err
	}
	defer sess.close()

	rep, err := sess.validate(ctx)
	if err != nil {
		return err
	}

	if rep.ErrorCount > 0 || (f.failOnWarning && rep.WarningCount > 0) {
		return sess.finish(rep, f)
	}

	columns := validator.PlanColumnNames(sess.plan)
	target := sqlgen.Target{
		Schema:  sess.res.Schema.Schema,
		Table:   sess.res.Schema.Table,
		Columns: columns,
	}
	if sess.cfg.PrimaryKey.Strategy == domain.PKMapped &&
		sess.cfg.PrimaryKey.EmitSetval &&
		sess.res.Schema.PKSequence != nil &&
		len(sess.res.Schema.PrimaryKey.Columns) == 1 {
		target.SetvalColumn = sess.res.Schema.PrimaryKey.Columns[0]
	}

	file, err := os.Create(out)
	if err != nil {
		return fail(exitConfiguration, "%v", err)
	}

	res, genErr := sqlgen.Generate(ctx, file, sqlgen.Input{
		Workbook:  sess.wb,
		SheetName: sess.sheet.Name,
		DataStart: sess.sheet.DataStart,
		Target:    target,
		Coerce:    validator.RowLiterals(sess.plan, sess.opts, sess.cfg.Output.SkipBlankRows),
		Options: sqlgen.Options{
			Mode:                 sqlgen.Mode(sess.cfg.Output.Mode),
			BatchSize:            sess.cfg.Output.BatchSize,
			WrapInTransaction:    sess.cfg.Output.WrapInTransaction,
			IncludeSummaryHeader: sess.cfg.Output.IncludeSummaryHeader,
			SkipBlankRows:        sess.cfg.Output.SkipBlankRows,
		},
		Meta:  sess.meta(rep, f),
		Total: sess.sheet.TotalRows,
	})
	closeErr := file.Close()

	if genErr != nil || closeErr != nil {
		// A half-written script looks runnable and is not.
		os.Remove(out)
		if genErr != nil {
			return fail(exitValidation, "%v", genErr)
		}
		return fail(exitValidation, "%v", closeErr)
	}

	if f.jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{"report": rep, "output": out, "result": res})
	}

	printReport(rep)
	fmt.Printf("\nWrote %s: %d rows, %d statements, %s\n",
		out, res.RowsWritten, res.Statements, byteSize(res.BytesWritten))
	return nil
}

func (s *session) meta(rep validator.Report, f commonFlags) sqlgen.Meta {
	columns := validator.PlanColumnNames(s.plan)
	return sqlgen.Meta{
		Version:             version,
		GeneratedAt:         time.Now(),
		SourceFile:          f.file,
		SheetName:           s.sheet.Name,
		HeaderRow:           s.sheet.HeaderRow,
		FirstRow:            s.sheet.DataStart,
		LastRow:             s.sheet.DataStart + s.sheet.TotalRows - 1,
		Fingerprint:         s.sheet.Fingerprint,
		ConfigName:          f.config,
		TargetSchema:        s.res.Schema.Schema,
		TargetTable:         s.res.Schema.Table,
		ServerInfo:          s.pool.Info.Database + "/" + s.pool.Info.User,
		RowsToInsert:        rep.RowsValid,
		RowsSkipped:         rep.RowsSkipped,
		ColumnsMapped:       len(columns),
		Warnings:            rep.WarningCount,
		Validated:           validator.Describe(s.settings, false),
		PersonalDataColumns: sqlgen.FlagPersonalData(columns),
	}
}

// resolvePassword takes the password from the environment or asks for it.
//
// It is never a flag and never read from the DSN (spec §18).
func resolvePassword() (string, error) {
	if pw, ok := os.LookupEnv("PGPASSWORD"); ok {
		return pw, nil
	}

	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		// Not interactive and nothing in the environment: this is a scheduled
		// run that was set up incompletely, and saying so beats hanging.
		return "", errors.New("no password: set PGPASSWORD, or run interactively to be prompted")
	}

	fmt.Fprint(os.Stderr, "Password: ")
	pw, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	return string(pw), nil
}

func cliProgress() validator.ProgressFunc {
	last := time.Now()
	return func(phase string, current, total int) {
		if time.Since(last) < 500*time.Millisecond && current != total {
			return
		}
		last = time.Now()
		if total > 0 {
			fmt.Fprintf(os.Stderr, "\r%-24s %d/%d", phase, current, total)
			if current >= total {
				fmt.Fprintln(os.Stderr)
			}
		}
	}
}

func printReport(rep validator.Report) {
	fmt.Println(rep.Summary())

	if len(rep.ColumnVerdicts) > 0 {
		blocked := 0
		for _, v := range rep.ColumnVerdicts {
			if v.Blocked {
				blocked++
				fmt.Printf("  column %-24s %s\n", v.ExcelColumn, v.Reason)
			}
		}
	}

	const maxLines = 50
	shown := 0
	for _, i := range rep.Issues {
		if shown >= maxLines {
			fmt.Printf("  ... and %d more\n", len(rep.Issues)-shown)
			break
		}
		where := ""
		if i.ExcelRef != "" {
			where = " " + i.ExcelRef
		} else if i.ExcelColumn != "" {
			where = " " + i.ExcelColumn
		}
		fmt.Printf("  %-7s %s%s  %s\n", i.Severity, i.Code, where, i.Message)
		shown++
	}

	if len(rep.Unverifiable) > 0 {
		fmt.Println("\nNot checked offline:")
		for _, u := range rep.Unverifiable {
			fmt.Printf("  %s: %s\n", u.Constraint, u.Reason)
		}
	}
}

func byteSize(n int64) string {
	switch {
	case n > 1<<20:
		return fmt.Sprintf("%.1fMB", float64(n)/(1<<20))
	case n > 1<<10:
		return fmt.Sprintf("%.1fKB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%dB", n)
	}
}
