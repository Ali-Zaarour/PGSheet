package validator

import (
	"encoding/csv"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Severity separates what blocks generation from what merely deserves a look.
type Severity string

const (
	SevError   Severity = "error"
	SevWarning Severity = "warning"
	SevInfo    Severity = "info"
)

// Scope separates a cell problem from a column or file one, so the report can
// answer "is my mapping wrong?" before "which rows are bad?".
type Scope string

const (
	ScopeCell   Scope = "row"
	ScopeColumn Scope = "column"
	ScopeFile   Scope = "file"
)

// Issue is one finding. Every field that locates it is filled in: an issue
// that cannot say where it happened is not an acceptable message.
type Issue struct {
	Code        string   `json:"code"`
	Severity    Severity `json:"severity"`
	Scope       Scope    `json:"scope"`
	ExcelRow    int      `json:"excelRow"`    // 1-based, as shown in Excel
	ExcelColumn string   `json:"excelColumn"` // header name
	ExcelRef    string   `json:"excelRef"`    // "D47" — paste into Excel's name box
	DBColumn    string   `json:"dbColumn"`
	Value       string   `json:"value"`
	Message     string   `json:"message"`
	Hint        string   `json:"hint"`
}

// Report is the whole verdict on one validation run.
type Report struct {
	Issues       []Issue        `json:"issues"`
	ErrorCount   int            `json:"errorCount"`
	WarningCount int            `json:"warningCount"`
	RowsTotal    int            `json:"rowsTotal"`
	RowsValid    int            `json:"rowsValid"`
	RowsSkipped  int            `json:"rowsSkipped"` // fully blank
	ByColumn     map[string]int `json:"byColumn"`
	ByCode       map[string]int `json:"byCode"`
	Truncated    bool           `json:"truncated"`
	Duration     string         `json:"duration"`

	// ColumnVerdicts is what Phase A decided per column, so the UI can say a
	// column is misaligned instead of leaving it to be inferred.
	ColumnVerdicts []ColumnVerdict `json:"columnVerdicts"`

	// Unverifiable lists constraints that could not be checked offline. Not
	// failures: the reason the live verification exists.
	Unverifiable []Unverifiable `json:"unverifiable"`
}

// ColumnVerdict is Phase A's judgement on one mapped column.
type ColumnVerdict struct {
	ExcelColumn string  `json:"excelColumn"`
	DBColumn    string  `json:"dbColumn"`
	Sampled     int     `json:"sampled"`
	Failures    int     `json:"failures"`
	FailureRate float64 `json:"failureRate"`
	Blocked     bool    `json:"blocked"`
	Reason      string  `json:"reason"`
}

// Unverifiable is a constraint whose definition is beyond the offline parser.
type Unverifiable struct {
	Constraint string `json:"constraint"`
	Definition string `json:"definition"`
	Reason     string `json:"reason"`
}

// OK reports whether generation may proceed. Warnings never block.
func (r *Report) OK() bool { return r.ErrorCount == 0 }

// builder accumulates issues and enforces the ceiling. Past it the run stops:
// a report with fifty thousand rows is not one anybody can act on, and the
// honest message by then is that the mapping is wrong.
type builder struct {
	issues    []Issue
	maxIssues int
	byColumn  map[string]int
	byCode    map[string]int
	errors    int
	warnings  int
	truncated bool

	// suppressed holds columns Phase A already condemned: their row failures
	// are counted, not listed.
	suppressed map[string]bool
}

func newBuilder(maxIssues int) *builder {
	if maxIssues <= 0 {
		maxIssues = 10000
	}
	return &builder{
		maxIssues:  maxIssues,
		byColumn:   map[string]int{},
		byCode:     map[string]int{},
		suppressed: map[string]bool{},
	}
}

func (b *builder) suppress(excelColumn string) { b.suppressed[excelColumn] = true }

func (b *builder) isSuppressed(excelColumn string) bool { return b.suppressed[excelColumn] }

// add records an issue, updating the counters even once the list is full so
// the summary stays truthful.
func (b *builder) add(i Issue) {
	switch i.Severity {
	case SevError:
		b.errors++
	case SevWarning:
		b.warnings++
	}
	if i.ExcelColumn != "" {
		b.byColumn[i.ExcelColumn]++
	}
	if i.Code != "" {
		b.byCode[i.Code]++
	}

	if len(b.issues) >= b.maxIssues {
		b.truncated = true
		return
	}
	b.issues = append(b.issues, i)
}

func (b *builder) full() bool { return b.truncated }

func (b *builder) finish(rowsTotal, rowsValid, rowsSkipped int, started time.Time) Report {
	sort.SliceStable(b.issues, func(i, j int) bool {
		// Column and file scope first: they explain the row-level noise below
		// them, so they must not be buried under it.
		if (b.issues[i].Scope == ScopeCell) != (b.issues[j].Scope == ScopeCell) {
			return b.issues[j].Scope == ScopeCell
		}
		if b.issues[i].ExcelRow != b.issues[j].ExcelRow {
			return b.issues[i].ExcelRow < b.issues[j].ExcelRow
		}
		return b.issues[i].ExcelColumn < b.issues[j].ExcelColumn
	})

	return Report{
		Issues:       b.issues,
		ErrorCount:   b.errors,
		WarningCount: b.warnings,
		RowsTotal:    rowsTotal,
		RowsValid:    rowsValid,
		RowsSkipped:  rowsSkipped,
		ByColumn:     b.byColumn,
		ByCode:       b.byCode,
		Truncated:    b.truncated,
		Duration:     time.Since(started).Round(time.Millisecond).String(),
	}
}

// ExcelRef renders a column index and row as A1 notation, so the operator can
// paste "D47" into Excel's name box and land on the cell.
func ExcelRef(colIndex, row int) string {
	return ColumnLetter(colIndex) + strconv.Itoa(row)
}

// ColumnLetter converts a zero-based column index to spreadsheet letters:
// 0 -> A, 25 -> Z, 26 -> AA.
func ColumnLetter(index int) string {
	if index < 0 {
		return ""
	}
	var out []byte
	for i := index; ; {
		out = append([]byte{byte('A' + i%26)}, out...)
		i = i/26 - 1
		if i < 0 {
			break
		}
	}
	return string(out)
}

// WriteCSV exports the issue list: where, what, why.
func (r *Report) WriteCSV(w io.Writer) error {
	// A BOM so Excel opens the file as UTF-8 rather than the local codepage,
	// which would mangle every non-ASCII value the report is quoting.
	if _, err := io.WriteString(w, "\uFEFF"); err != nil {
		return err
	}

	cw := csv.NewWriter(w)
	defer cw.Flush()

	header := []string{"Severity", "Code", "Excel row", "Excel cell", "Sheet column", "Table column", "Value", "Problem", "Suggested fix"}
	if err := cw.Write(header); err != nil {
		return err
	}

	for _, i := range r.Issues {
		row := []string{
			string(i.Severity),
			i.Code,
			rowText(i.ExcelRow),
			i.ExcelRef,
			i.ExcelColumn,
			i.DBColumn,
			i.Value,
			i.Message,
			i.Hint,
		}
		if err := cw.Write(row); err != nil {
			return err
		}
	}

	cw.Flush()
	return cw.Error()
}

func rowText(row int) string {
	if row <= 0 {
		return ""
	}
	return strconv.Itoa(row)
}

// Summary renders the one-paragraph verdict used in the SQL header and the CLI.
func (r *Report) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d rows, %d valid", r.RowsTotal, r.RowsValid)
	if r.RowsSkipped > 0 {
		fmt.Fprintf(&b, ", %d blank rows skipped", r.RowsSkipped)
	}
	fmt.Fprintf(&b, ", %d errors, %d warnings", r.ErrorCount, r.WarningCount)
	if r.Truncated {
		fmt.Fprintf(&b, " (issue list truncated)")
	}
	return b.String()
}
