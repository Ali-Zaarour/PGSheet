package mapper

import (
	"sort"
	"strings"

	"pgsheet/internal/domain"
)

// Score thresholds from spec §8. Above AcceptThreshold the mapping is applied
// automatically; between SuggestThreshold and AcceptThreshold it is offered but
// left unchecked; below it is not shown at all.
const (
	AcceptThreshold  = 0.70
	SuggestThreshold = 0.50
)

// Suggestion is one candidate pairing with the evidence for it, so the UI can
// explain why a match was proposed rather than showing a bare number.
type Suggestion struct {
	ExcelIndex  int     `json:"excelIndex"`
	ExcelColumn string  `json:"excelColumn"`
	DBColumn    string  `json:"dbColumn"`
	Score       float64 `json:"score"`
	Reason      string  `json:"reason"`
	Accepted    bool    `json:"accepted"`
}

// synonyms groups header words that mean the same thing to a database column.
// Deliberately small: a wrong automatic match costs more than a missing one,
// because the operator reviews what is proposed and rarely questions it.
var synonyms = [][]string{
	{"email", "e-mail", "mail", "courriel", "email address"},
	{"phone", "tel", "telephone", "mobile", "cell", "gsm", "phone number"},
	{"name", "full name", "fullname", "nom"},
	{"first name", "firstname", "given name", "prenom"},
	{"last name", "lastname", "surname", "family name"},
	{"address", "addr", "street", "adresse"},
	{"city", "town", "ville"},
	{"country", "pays"},
	{"zip", "zipcode", "postal code", "postcode"},
	{"company", "organisation", "organization", "societe", "business"},
	{"status", "state", "etat", "statut"},
	{"amount", "total", "value", "montant"},
	{"price", "unit price", "prix"},
	{"quantity", "qty", "quantite"},
	{"date", "created", "created at", "creation date"},
	{"note", "notes", "comment", "comments", "remarks"},
	{"reference", "ref", "code", "id number"},
	{"active", "is active", "enabled"},
}

var synonymGroup = buildSynonymIndex()

func buildSynonymIndex() map[string]int {
	idx := make(map[string]int)
	for group, words := range synonyms {
		for _, w := range words {
			idx[w] = group
		}
	}
	return idx
}

// AutoMatch proposes a mapping between the sheet's headers and the table's
// columns.
//
// Assignment is greedy over the sorted score list, which keeps it one-to-one in
// both directions: the strongest pairing wins, and neither of its two columns
// can be used again. A greedy pass is not globally optimal the way a Hungarian
// assignment would be, but it is predictable and explainable, and the operator
// corrects it on the same screen — an optimal match nobody can account for is
// worse here than a good one they can.
func AutoMatch(headers []string, columns []domain.Column) []Suggestion {
	type pair struct {
		h, c   int
		score  float64
		reason string
	}

	var pairs []pair
	for hi, h := range headers {
		for ci, col := range columns {
			if !col.AcceptsValue() {
				continue // GENERATED ALWAYS: never propose it (E103)
			}
			s, reason := scoreNames(h, col.Name)
			if s < SuggestThreshold {
				continue
			}
			pairs = append(pairs, pair{h: hi, c: ci, score: s, reason: reason})
		}
	}

	sort.SliceStable(pairs, func(i, j int) bool {
		if pairs[i].score != pairs[j].score {
			return pairs[i].score > pairs[j].score
		}
		if pairs[i].h != pairs[j].h {
			return pairs[i].h < pairs[j].h
		}
		return pairs[i].c < pairs[j].c
	})

	usedHeader := make(map[int]bool, len(headers))
	usedColumn := make(map[int]bool, len(columns))

	var out []Suggestion
	for _, p := range pairs {
		if usedHeader[p.h] || usedColumn[p.c] {
			continue
		}
		accepted := p.score >= AcceptThreshold
		if accepted {
			usedHeader[p.h] = true
			usedColumn[p.c] = true
		}
		out = append(out, Suggestion{
			ExcelIndex:  p.h,
			ExcelColumn: headers[p.h],
			DBColumn:    columns[p.c].Name,
			Score:       p.score,
			Reason:      p.reason,
			Accepted:    accepted,
		})
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].ExcelIndex < out[j].ExcelIndex })
	return out
}

// ToMappings turns the accepted suggestions into mappings with sensible
// default transforms.
//
// Trim and BlankAsNull are on by default because a leading space and an
// accidentally-spacebarred cell are the two most common things in a client
// sheet, and neither is ever the intended value.
func ToMappings(suggestions []Suggestion, columns []domain.Column) []domain.ColumnMapping {
	byName := make(map[string]domain.Column, len(columns))
	for _, c := range columns {
		byName[c.Name] = c
	}

	var out []domain.ColumnMapping
	for _, s := range suggestions {
		if !s.Accepted {
			continue
		}
		out = append(out, domain.ColumnMapping{
			ExcelColumn: s.ExcelColumn,
			ExcelIndex:  s.ExcelIndex,
			DBColumn:    s.DBColumn,
			Enabled:     true,
			Transform:   DefaultTransform(byName[s.DBColumn]),
		})
	}
	return out
}

// DefaultTransform is the starting transform for a newly mapped column.
func DefaultTransform(col domain.Column) domain.Transform {
	t := domain.Transform{Trim: true}

	// Blank means NULL only where the column accepts NULL. A default does not
	// make it safe: a default applies when the column is left out of the
	// insert, not when it is handed an explicit NULL, so blank-as-NULL on a
	// NOT NULL DEFAULT column produces a file that validates and then fails.
	t.BlankAsNull = col.Nullable

	return t
}

// scoreNames rates one header against one column name, returning the score and
// a short human reason. The ladder is from spec §8.
func scoreNames(header, column string) (float64, string) {
	h := domain.NormalizeHeader(header)
	c := domain.NormalizeHeader(column)

	if h == "" || c == "" {
		return 0, ""
	}
	if h == c {
		return 1.0, "exact match"
	}

	hs := squash(h)
	cs := squash(c)
	if hs == cs {
		return 0.95, "match ignoring separators"
	}

	if gh, ok := synonymGroup[h]; ok {
		if gc, ok2 := synonymGroup[c]; ok2 && gh == gc {
			return 0.70, "known synonym"
		}
	}

	if strings.Contains(hs, cs) || strings.Contains(cs, hs) {
		return 0.75, "one name contains the other"
	}

	if ratio := levenshteinRatio(hs, cs); ratio > 0.8 {
		// Map (0.8, 1.0] onto (0.6, 0.8] so a near-miss never outranks a real
		// containment match.
		return 0.6 + (ratio - 0.8), "similar spelling"
	}

	return 0, ""
}

// squash reduces a name to its alphanumerics: "Client Name" and "client_name"
// both become "clientname".
func squash(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if isAlnum(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isAlnum(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
		r > 127 // keep non-ASCII letters: Arabic and accented headers are real
}

// levenshteinRatio is 1 - distance/maxLen, over runes so a non-ASCII header is
// measured in characters and not bytes.
func levenshteinRatio(a, b string) float64 {
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 && len(rb) == 0 {
		return 1
	}
	maxLen := len(ra)
	if len(rb) > maxLen {
		maxLen = len(rb)
	}
	if maxLen == 0 {
		return 0
	}
	d := levenshtein(ra, rb)
	return 1 - float64(d)/float64(maxLen)
}

func levenshtein(a, b []rune) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}

	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}

	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min3(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}
