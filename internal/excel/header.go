package excel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"pgsheet/internal/domain"
)

// Fingerprint hashes a normalized header row. A saved configuration stores it,
// so a client who quietly adds, removes or reorders a column is caught rather
// than imported into the wrong columns.
func Fingerprint(headers []string) string {
	norm := make([]string, len(headers))
	for i, h := range headers {
		norm[i] = domain.NormalizeHeader(h)
	}
	sum := sha256.Sum256([]byte(strings.Join(norm, "\x1f")))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// headerCandidate is a row considered as the header, with the one below it.
// Client files routinely open with a title or a blank row, so row 1 is a
// guess.
type headerCandidate struct {
	RowIndex int // 1-based, as shown in Excel
	Cells    []domain.CellValue
	Next     []domain.CellValue // empty when the candidate is the last row
}

// Weights for the four signals, summing to 1. Type contrast carries the most
// because it is what actually separates a header from a data row: by every
// other measure, data rows in an all-text sheet look like headers.
const (
	weightTypeContrast = 0.35
	weightNonEmpty     = 0.30
	weightAllStrings   = 0.20
	weightUnique       = 0.15
)

// scoreHeaderCandidate rates how likely c is to be the header row, 0 to 1.
// The operator can always override, so this has to be right often, not always.
// What it must not do is look confident about a near-tie; DetectHeaderRow
// reports the margin for that.
func scoreHeaderCandidate(c headerCandidate) float64 {
	width := len(c.Cells)
	if len(c.Next) > width {
		width = len(c.Next)
	}
	if width == 0 {
		return 0
	}

	nonEmpty, strings_, contrast := 0, 0, 0
	seen := make(map[string]bool, width)
	duplicates := 0

	for i := 0; i < width; i++ {
		cell := cellAt(c.Cells, i)
		next := cellAt(c.Next, i)

		if cell.Kind != domain.CellEmpty {
			nonEmpty++
		}
		if cell.Kind == domain.CellString {
			strings_++

			key := domain.NormalizeHeader(cell.Str)
			if key != "" {
				if seen[key] {
					duplicates++
				}
				seen[key] = true
			}
		}

		// A header is text above something that is not text. When the row
		// below is also text, that is no evidence either way rather than
		// evidence against — half credit keeps an all-text sheet scoreable.
		switch {
		case len(c.Next) == 0:
			contrast++ // nothing below: cannot argue against it
		case cell.Kind == domain.CellString && next.Kind != domain.CellString && next.Kind != domain.CellEmpty:
			contrast += 2
		case cell.Kind == domain.CellString && next.Kind == domain.CellString:
			contrast++
		}
	}

	nonEmptyRatio := float64(nonEmpty) / float64(width)
	stringRatio := float64(strings_) / float64(width)
	contrastRatio := float64(contrast) / float64(2*width)
	uniqueRatio := 1.0
	if strings_ > 0 {
		uniqueRatio = 1 - float64(duplicates)/float64(strings_)
	}

	score := weightNonEmpty*nonEmptyRatio +
		weightAllStrings*stringRatio +
		weightUnique*uniqueRatio +
		weightTypeContrast*contrastRatio

	// A row with one or two filled cells across a wide sheet is a title, not a
	// header, however text-like it looks. The ratio alone is too forgiving on
	// narrow sheets, so this is explicit.
	if width >= 3 && nonEmpty <= width/3 {
		score *= 0.4
	}

	return score
}

func cellAt(cells []domain.CellValue, i int) domain.CellValue {
	if i < 0 || i >= len(cells) {
		return domain.CellValue{Kind: domain.CellEmpty}
	}
	return cells[i]
}

// HeaderGuess is the proposal shown on the file screen. Confident is false
// when the winner barely beat the runner-up, which is the UI's cue to ask.
type HeaderGuess struct {
	Row       int     `json:"row"`
	Score     float64 `json:"score"`
	Margin    float64 `json:"margin"`
	Confident bool    `json:"confident"`
}

// DetectHeaderRow scans the first rows of a sheet and proposes the header row.
func (w *Workbook) DetectHeaderRow(ctx context.Context, sheet string) (HeaderGuess, error) {
	rows, err := w.Preview(ctx, sheet, 1, headerScanRows+1)
	if err != nil {
		return HeaderGuess{}, err
	}
	if len(rows) == 0 {
		return HeaderGuess{}, fmt.Errorf("sheet %q is empty", sheet)
	}

	best, second := HeaderGuess{Row: 1}, HeaderGuess{}
	for i, r := range rows {
		if i >= headerScanRows {
			break
		}
		var next []domain.CellValue
		if i+1 < len(rows) {
			next = rows[i+1].Cells
		}

		s := scoreHeaderCandidate(headerCandidate{
			RowIndex: r.Number,
			Cells:    r.Cells,
			Next:     next,
		})

		switch {
		case s > best.Score:
			second = best
			best = HeaderGuess{Row: r.Number, Score: s}
		case s > second.Score:
			second = HeaderGuess{Row: r.Number, Score: s}
		}
	}

	best.Margin = best.Score - second.Score
	// A clear winner is one that is both good in absolute terms and clearly
	// ahead. Either alone produces confident-looking wrong answers.
	best.Confident = best.Score >= 0.6 && best.Margin >= 0.15
	return best, nil
}

// Describe reads the headers at headerRow and assembles the sheet description
// the rest of the pipeline works from.
func (w *Workbook) Describe(ctx context.Context, sheet string, headerRow int) (domain.SheetInfo, error) {
	if headerRow < 1 {
		return domain.SheetInfo{}, fmt.Errorf("header row must be 1 or greater, got %d", headerRow)
	}

	rows, err := w.Preview(ctx, sheet, headerRow, 1)
	if err != nil {
		return domain.SheetInfo{}, err
	}
	if len(rows) == 0 {
		return domain.SheetInfo{}, fmt.Errorf("sheet %q has no row %d", sheet, headerRow)
	}

	headers := HeaderNames(rows[0].Cells)

	total, lastNonBlank, err := w.CountRows(ctx, sheet)
	if err != nil {
		return domain.SheetInfo{}, err
	}
	_ = total

	dataStart := headerRow + 1
	if lastNonBlank < dataStart {
		lastNonBlank = headerRow
	}

	return domain.SheetInfo{
		Name:        sheet,
		Headers:     headers,
		HeaderRow:   headerRow,
		DataStart:   dataStart,
		TotalRows:   lastNonBlank - headerRow,
		Fingerprint: Fingerprint(headers),
	}, nil
}

// HeaderNames renders a header row as column names. A blank header gets its
// spreadsheet letter, so it can still be mapped and reported on; duplicates
// are suffixed, or two columns called "Amount" collapse into one.
func HeaderNames(cells []domain.CellValue) []string {
	out := make([]string, 0, len(cells))
	seen := map[string]int{}

	for i, c := range cells {
		name := strings.TrimSpace(domain.NormalizeInvisible(cellText(c)))
		if name == "" {
			name = "(column " + columnLetter(i) + ")"
		}

		key := strings.ToLower(name)
		seen[key]++
		if n := seen[key]; n > 1 {
			name = fmt.Sprintf("%s (%d)", name, n)
		}
		out = append(out, name)
	}

	// Trailing placeholder columns are Excel's phantom columns, not data.
	for len(out) > 0 && strings.HasPrefix(out[len(out)-1], "(column ") {
		out = out[:len(out)-1]
	}
	return out
}

func cellText(c domain.CellValue) string {
	switch c.Kind {
	case domain.CellString:
		return c.Str
	case domain.CellNumber:
		if c.Str != "" {
			return c.Str
		}
		return c.Num.String()
	case domain.CellBool:
		if c.Bool {
			return "TRUE"
		}
		return "FALSE"
	case domain.CellEmpty:
		return ""
	default:
		return c.RawText
	}
}

func columnLetter(index int) string {
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

// FingerprintDiff is what changed between a saved layout and this workbook.
// The warning shows this, not just "the file changed".
type FingerprintDiff struct {
	Match     bool     `json:"match"`
	Added     []string `json:"added"`
	Removed   []string `json:"removed"`
	Reordered []string `json:"reordered"`
}

// CompareHeaders diffs saved headers against the ones just read.
func CompareHeaders(saved, current []string) FingerprintDiff {
	d := FingerprintDiff{Match: Fingerprint(saved) == Fingerprint(current)}
	if d.Match {
		return d
	}

	savedIdx := map[string]int{}
	for i, h := range saved {
		savedIdx[domain.NormalizeHeader(h)] = i
	}
	currentIdx := map[string]int{}
	for i, h := range current {
		currentIdx[domain.NormalizeHeader(h)] = i
	}

	for _, h := range current {
		if _, ok := savedIdx[domain.NormalizeHeader(h)]; !ok {
			d.Added = append(d.Added, h)
		}
	}
	for _, h := range saved {
		if _, ok := currentIdx[domain.NormalizeHeader(h)]; !ok {
			d.Removed = append(d.Removed, h)
		}
	}
	for _, h := range saved {
		k := domain.NormalizeHeader(h)
		if ci, ok := currentIdx[k]; ok && ci != savedIdx[k] {
			d.Reordered = append(d.Reordered, h)
		}
	}
	return d
}
