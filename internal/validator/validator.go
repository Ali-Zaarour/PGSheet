package validator

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"pgsheet/internal/domain"
	"pgsheet/internal/excel"
	"pgsheet/internal/introspect"
	"pgsheet/internal/mapper"
)

// Pipeline shape from spec §13. A thousand rows per chunk balances channel
// overhead against progress granularity, and more than eight workers only
// makes them contend on the collector.
const (
	chunkSize  = 1000
	maxWorkers = 8
)

// ProgressFunc receives progress. The app layer throttles it; the validator
// emits one per chunk.
type ProgressFunc func(phase string, current, total int)

// Settings are the run-time choices for one validation.
type Settings struct {
	MaxIssues               int
	ColumnMisalignThreshold float64
	CheckUniqueAgainstDB    bool
	CheckForeignKeys        bool
	SkipBlankRows           bool
}

// Input is everything a run needs. The pool may be nil: everything except the
// in-database checks works offline.
type Input struct {
	Workbook      *excel.Workbook
	Sheet         domain.SheetInfo
	Mappings      []domain.ColumnMapping
	Plan          mapper.Plan
	Introspection introspect.Result
	Pool          *pgxpool.Pool
	Opts          Options
	Settings      Settings
	Progress      ProgressFunc
}

// dbFlushRows bounds how many keys are held before they are checked against
// the database.
//
// Without it, a sheet is only checked once its last row has been read, so a
// million-row file holds a million keys per rule first. Checking every fifty
// thousand keeps memory flat and starts reporting collisions sooner.
const dbFlushRows = 50000

// errCeiling stops the scan once the issue list is full: a control signal,
// not a failure.
var errCeiling = errors.New("issue ceiling reached")

// Run validates the whole file and produces the report.
func Run(ctx context.Context, in Input) (Report, error) {
	started := time.Now()
	b := newBuilder(in.Settings.MaxIssues)

	cols := mappedColumns(in.Plan)
	if len(cols) == 0 {
		b.add(Issue{
			Code:     "E102",
			Severity: SevError,
			Scope:    ScopeFile,
			Message:  "no columns are mapped, so there is nothing to insert",
			Hint:     "map at least one sheet column to a table column",
		})
		return b.finish(0, 0, 0, started), nil
	}

	checks, unverifiable := parseChecks(in.Introspection.Schema.Constraints)
	unverifiable = append(unverifiable, partialIndexes(in.Introspection.UniqueIndexes)...)

	// A table with no primary key cannot stop the same row being inserted
	// twice. That is the operator's decision to make, not an error, but it is
	// said out loud once rather than left to be discovered later (spec §10).
	if in.Introspection.Schema.PrimaryKey == nil {
		b.add(Issue{
			Code:     "W203",
			Severity: SevWarning,
			Scope:    ScopeFile,
			Message: fmt.Sprintf("%s.%s has no primary key, so nothing prevents duplicate rows",
				in.Introspection.Schema.Schema, in.Introspection.Schema.Table),
			Hint: "re-running this import would insert every row a second time",
		})
	}

	// ---- Phase A: is the mapping right? ----
	verdicts, err := runPhaseA(ctx, in, cols, b)
	if err != nil {
		return Report{}, err
	}

	if b.errors > 0 {
		// Phase B on a mapping that is already known to be wrong produces
		// thousands of downstream errors that all have the same cause.
		rep := b.finish(0, 0, 0, started)
		rep.ColumnVerdicts = verdicts
		rep.Unverifiable = unverifiable
		return rep, nil
	}

	// ---- Phase B: is the data right? ----
	checker := newDBChecker(in, b, cols)

	rowsTotal, rowsValid, rowsSkipped, occurrences, err := runPhaseB(ctx, in, cols, checks, b, checker)
	if err != nil && !errors.Is(err, errCeiling) {
		return Report{}, err
	}

	// Whatever the last partial batch left behind.
	if err := checker.flush(ctx, occurrences, true); err != nil {
		return Report{}, err
	}

	rep := b.finish(rowsTotal, rowsValid, rowsSkipped, started)
	rep.ColumnVerdicts = verdicts
	rep.Unverifiable = unverifiable
	return rep, nil
}

// chunk is a batch of rows handed to a worker, carrying its position so the
// collector can put the results back in file order.
type chunk struct {
	index int
	rows  []excel.Row
}

type chunkResult struct {
	index    int
	outcomes []rowOutcome
}

// runPhaseB streams the file through the worker pool. The reader stays on one
// goroutine because the row iterator is sequential; the work that scales
// happens on the workers, and the collector is single-threaded so uniqueness
// tracking needs no lock.
func runPhaseB(
	ctx context.Context,
	in Input,
	cols []mappedColumn,
	checks []CheckExpr,
	b *builder,
	checker *dbChecker,
) (rowsTotal, rowsValid, rowsSkipped int, occurrences map[string][]keyOccurrence, err error) {
	workers := runtime.NumCPU()
	if workers > maxWorkers {
		workers = maxWorkers
	}
	if workers < 1 {
		workers = 1
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan chunk, workers)
	results := make(chan chunkResult, workers*2)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for c := range jobs {
				outcomes := make([]rowOutcome, 0, len(c.rows))
				for _, row := range c.rows {
					outcomes = append(outcomes, evaluateRow(row, cols, checks, in.Opts))
				}
				select {
				case results <- chunkResult{index: c.index, outcomes: outcomes}:
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	// The collector owns every piece of mutable state: the builder, the
	// counters and the uniqueness trackers.
	rules := in.Introspection.UniquenessRules()
	trackers := make([]*uniqueTracker, 0, len(rules))
	for _, r := range rules {
		if !mappedForRule(cols, r.Columns) {
			continue // a rule we cannot evaluate because its columns are not all mapped
		}
		trackers = append(trackers, newUniqueTracker(r, in.Sheet.TotalRows))
	}

	fkRules := foreignKeyRules(in, cols)
	occurrences = map[string][]keyOccurrence{}

	// Keys are only worth keeping if something is going to ask the server
	// about them. In-file duplicate detection needs the tracker, not the list.
	keepKeys := checker.enabled()

	var (
		collectorWG sync.WaitGroup
		pending     = map[int]chunkResult{}
		nextIndex   int
		collectErr  error
	)

	collectorWG.Add(1)
	go func() {
		defer collectorWG.Done()
		for res := range results {
			pending[res.index] = res
			for {
				r, ok := pending[nextIndex]
				if !ok {
					break
				}
				delete(pending, nextIndex)
				nextIndex++

				for _, o := range r.outcomes {
					rowsTotal++
					if o.Blank {
						rowsSkipped++
						continue
					}

					for _, issue := range o.Issues {
						if b.isSuppressed(issue.ExcelColumn) && issue.Scope == ScopeCell {
							continue
						}
						b.add(issue)
					}

					collectKeys(trackers, fkRules, cols, o, b, occurrences, keepKeys)

					if len(o.Issues) == 0 {
						rowsValid++
					}
				}

				if b.full() && collectErr == nil {
					collectErr = errCeiling
					cancel()
				}

				// Ask the database about the keys collected so far, so memory
				// stays flat on a file of any size.
				if collectErr == nil {
					if err := checker.flush(ctx, occurrences, false); err != nil {
						collectErr = err
						cancel()
					}
				}
			}
		}
	}()

	// Reader.
	current := chunk{}
	chunkIndex := 0
	scanErr := in.Workbook.Scan(ctx, in.Sheet.Name, in.Sheet.DataStart, func(row excel.Row) error {
		current.rows = append(current.rows, copyRow(row))
		if len(current.rows) < chunkSize {
			return nil
		}
		current.index = chunkIndex
		chunkIndex++
		select {
		case jobs <- current:
		case <-ctx.Done():
			return ctx.Err()
		}
		current = chunk{}
		report(in.Progress, "validating", chunkIndex*chunkSize, in.Sheet.TotalRows)
		return nil
	})

	if len(current.rows) > 0 && scanErr == nil {
		current.index = chunkIndex
		chunkIndex++
		select {
		case jobs <- current:
		case <-ctx.Done():
		}
	}

	close(jobs)
	wg.Wait()
	close(results)
	collectorWG.Wait()

	report(in.Progress, "validating", in.Sheet.TotalRows, in.Sheet.TotalRows)

	if collectErr != nil {
		return rowsTotal, rowsValid, rowsSkipped, occurrences, collectErr
	}
	if scanErr != nil && !errors.Is(scanErr, context.Canceled) {
		return rowsTotal, rowsValid, rowsSkipped, occurrences, scanErr
	}
	return rowsTotal, rowsValid, rowsSkipped, occurrences, nil
}

// copyRow detaches a row from the reader's reused slice. excelize hands back
// the same backing array every iteration, so a row kept without copying would
// quietly become the next row's contents.
func copyRow(r excel.Row) excel.Row {
	cells := make([]domain.CellValue, len(r.Cells))
	copy(cells, r.Cells)
	return excel.Row{Number: r.Number, Cells: cells}
}

// collectKeys feeds the uniqueness trackers and gathers the key values the
// in-database checks will need later.
// keepKeys says whether the key values are needed after the row is judged.
// Without it a million-row file builds a million key records that nothing ever
// reads.
func collectKeys(
	trackers []*uniqueTracker,
	fkRules []fkRule,
	cols []mappedColumn,
	o rowOutcome,
	b *builder,
	occurrences map[string][]keyOccurrence,
	keepKeys bool,
) {
	for _, t := range trackers {
		parts, ok := keyParts(o, t.rule.Columns)
		if !ok {
			continue // a NULL part: PostgreSQL does not treat those as equal
		}
		key, prev := t.observe(o.Row, parts)
		if prev != 0 {
			m := findColumn(cols, t.rule.Columns[0])
			b.add(t.duplicateIssue(o.Row, prev, parts, m.ExcelColumn, strings.Join(t.rule.Columns, ", "), m.ExcelIndex))
			continue
		}
		if keepKeys {
			occurrences[t.occKey] = append(occurrences[t.occKey],
				keyOccurrence{Key: key, Parts: parts, Row: o.Row})
		}
	}

	// Foreign keys exist only to be checked against the server, so there is
	// nothing to do when nobody will ask.
	if !keepKeys {
		return
	}
	for _, fk := range fkRules {
		parts, ok := keyParts(o, fk.constraint.Columns)
		if !ok {
			continue
		}
		occurrences[fk.occKey] = append(occurrences[fk.occKey],
			keyOccurrence{Key: domain.CompositeKey(parts), Parts: parts, Row: o.Row})
	}
}

// keyParts extracts a key's column values from a row. A NULL in any part means
// the key does not participate: in PostgreSQL two NULLs are not equal, so a
// NULL never collides with anything.
func keyParts(o rowOutcome, columns []string) ([]string, bool) {
	parts := make([]string, 0, len(columns))
	for _, c := range columns {
		v, ok := o.Values[c]
		if !ok || v.IsNull {
			return nil, false
		}
		parts = append(parts, v.Text)
	}
	return parts, true
}

func mappedForRule(cols []mappedColumn, ruleColumns []string) bool {
	for _, rc := range ruleColumns {
		found := false
		for _, c := range cols {
			if c.Column.Name == rc {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

type fkRule struct {
	constraint domain.Constraint
	occKey     string
}

func foreignKeyRules(in Input, cols []mappedColumn) []fkRule {
	if !in.Settings.CheckForeignKeys {
		return nil
	}
	var out []fkRule
	for _, c := range in.Introspection.Schema.Constraints {
		if c.Type != "f" || !mappedForRule(cols, c.Columns) {
			continue
		}
		out = append(out, fkRule{constraint: c, occKey: "f:" + c.Name})
	}
	return out
}

// dbChecker asks the server what already exists, in batches, as the scan goes.
type dbChecker struct {
	in   Input
	b    *builder
	cols []mappedColumn

	columnTypes map[string]string
	mappingOf   map[string]mappedColumn
	rules       map[string]introspect.UniquenessRule
	fks         map[string]domain.Constraint
}

func newDBChecker(in Input, b *builder, cols []mappedColumn) *dbChecker {
	c := &dbChecker{
		in:          in,
		b:           b,
		cols:        cols,
		columnTypes: map[string]string{},
		mappingOf:   map[string]mappedColumn{},
		rules:       map[string]introspect.UniquenessRule{},
		fks:         map[string]domain.Constraint{},
	}

	for _, col := range in.Introspection.Schema.Columns {
		c.columnTypes[col.Name] = col.DataType
	}
	for _, m := range cols {
		c.mappingOf[m.Column.Name] = m
	}
	for _, r := range in.Introspection.UniquenessRules() {
		c.rules[r.Name] = r
	}
	for _, con := range in.Introspection.Schema.Constraints {
		if con.Type == "f" {
			c.fks[con.Name] = con
		}
	}
	return c
}

func (c *dbChecker) enabled() bool {
	return c.in.Pool != nil &&
		(c.in.Settings.CheckUniqueAgainstDB || c.in.Settings.CheckForeignKeys)
}

// flush checks the keys collected so far and drops them. Without force it only
// touches the lists that have grown past dbFlushRows, so the common case costs
// one map walk per chunk.
func (c *dbChecker) flush(ctx context.Context, occurrences map[string][]keyOccurrence, force bool) error {
	if !c.enabled() {
		// collectKeys does not fill these when nothing will read them.
		return nil
	}

	for name, occ := range occurrences {
		if len(occ) == 0 || (!force && len(occ) < dbFlushRows) {
			continue
		}

		var (
			issues []Issue
			err    error
		)
		switch {
		case strings.HasPrefix(name, "u:") && c.in.Settings.CheckUniqueAgainstDB:
			rule, ok := c.rules[strings.TrimPrefix(name, "u:")]
			if !ok || rule.Partial {
				break
			}
			issues, err = checkExistingInDB(ctx, c.in.Pool,
				c.in.Introspection.Schema.Schema, c.in.Introspection.Schema.Table,
				rule, c.columnTypes, occ, c.mappingOf)

		case strings.HasPrefix(name, "f:") && c.in.Settings.CheckForeignKeys:
			fk, ok := c.fks[strings.TrimPrefix(name, "f:")]
			if !ok {
				break
			}
			issues, err = checkForeignKeys(ctx, c.in.Pool, fk, occ, c.mappingOf)
		}

		if err != nil {
			return err
		}
		for _, i := range issues {
			c.b.add(i)
		}

		occurrences[name] = nil
		report(c.in.Progress, "checking against the database", 0, 0)
	}
	return nil
}

// parseChecks separates the CHECK constraints that can be evaluated offline
// from the ones that cannot. An unparseable one is listed with its definition
// rather than dropped, so the operator knows what was not covered.
func parseChecks(constraints []domain.Constraint) ([]CheckExpr, []Unverifiable) {
	var (
		parsed       []CheckExpr
		unverifiable []Unverifiable
	)

	for _, c := range constraints {
		switch c.Type {
		case "c":
			expr, err := ParseCheck(c.Name, c.Definition)
			if err != nil {
				unverifiable = append(unverifiable, Unverifiable{
					Constraint: c.Name,
					Definition: c.Definition,
					Reason:     "the expression is outside the subset that can be checked without the database",
				})
				continue
			}
			parsed = append(parsed, expr)
		case "x":
			unverifiable = append(unverifiable, Unverifiable{
				Constraint: c.Name,
				Definition: c.Definition,
				Reason:     "exclusion constraints are evaluated by the database only",
			})
		}
	}

	sort.SliceStable(parsed, func(i, j int) bool { return parsed[i].Name < parsed[j].Name })
	return parsed, unverifiable
}

// partialIndexes reports unique indexes whose uniqueness covers only the rows
// their WHERE clause selects. That subset cannot be reproduced offline, so
// they are named rather than claimed as checked.
func partialIndexes(indexes []introspect.UniqueIndex) []Unverifiable {
	var out []Unverifiable
	for _, ix := range indexes {
		if !ix.Partial {
			continue
		}
		out = append(out, Unverifiable{
			Constraint: ix.Name,
			Definition: ix.Definition,
			Reason: "this unique index is partial: it applies to a subset of rows, which can only be" +
				" evaluated by the database",
		})
	}
	return out
}

func report(fn ProgressFunc, phase string, current, total int) {
	if fn != nil {
		fn(phase, current, total)
	}
}

// Describe renders a one-line summary of what a run checked, for the SQL
// header block.
func Describe(s Settings, dryRun bool) string {
	parts := []string{"offline (full)"}
	if s.CheckUniqueAgainstDB {
		parts = append(parts, "uniqueness against the table")
	}
	if s.CheckForeignKeys {
		parts = append(parts, "foreign keys")
	}
	if dryRun {
		parts = append(parts, "live dry run (passed)")
	}
	return fmt.Sprint(strings.Join(parts, " + "))
}

// ParseConstraints exposes the CHECK-constraint split to the app layer, so the
// table screen can show what the offline pass will and will not cover before
// the operator spends time on a validation run.
func ParseConstraints(constraints []domain.Constraint) ([]CheckExpr, []Unverifiable) {
	return parseChecks(constraints)
}
