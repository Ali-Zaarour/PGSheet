package validator

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/shopspring/decimal"
)

// CHECK constraints are arbitrary SQL. This file parses a deliberate subset
// and refuses the rest, because a parser that guesses is worse than one that
// says "I cannot verify this offline — run the live verification" (spec §9).
//
// Parsed:  IN lists, = ANY (ARRAY[...]), numeric comparisons, BETWEEN,
//          length()/char_length() comparisons, regex match, IS NOT NULL,
//          and conjunctions of those.
// Refused: function calls, subqueries, OR across different columns,
//          cross-column expressions, everything else.

// ErrUnparseable means the definition is outside the supported subset. It is
// an expected outcome, not a failure of the tool.
var ErrUnparseable = errors.New("check constraint is outside the offline-verifiable subset")

// Predicate is one conjunct of a parsed CHECK.
type Predicate interface {
	// Eval reports whether the predicate holds. applicable is false when the
	// row does not supply the column the predicate is about, in which case the
	// database will apply the column default and the check cannot be judged
	// here.
	Eval(values map[string]Coerced) (ok bool, applicable bool)
	Column() string
	Describe() string
}

// CheckExpr is a parsed CHECK constraint: a conjunction of predicates.
type CheckExpr struct {
	Name       string
	Definition string
	Predicates []Predicate
}

// Eval returns the first predicate that fails, or nil when the row satisfies
// all of them.
func (c CheckExpr) Eval(values map[string]Coerced) Predicate {
	for _, p := range c.Predicates {
		ok, applicable := p.Eval(values)
		if applicable && !ok {
			return p
		}
	}
	return nil
}

var (
	reCheckWrapper = regexp.MustCompile(`(?is)^\s*CHECK\s*\((.*)\)\s*(?:NOT\s+VALID\s*)?$`)
	reCast         = regexp.MustCompile(`::\s*[a-zA-Z_][a-zA-Z0-9_ ]*(\s*\[\s*\])?`)
	reIsNotNull    = regexp.MustCompile(`(?is)^\(?\s*([a-zA-Z_][\w]*)\s*\)?\s+IS\s+NOT\s+NULL\s*$`)
	reIsNull       = regexp.MustCompile(`(?is)^\(?\s*([a-zA-Z_][\w]*)\s*\)?\s+IS\s+NULL\s*$`)
	reIn           = regexp.MustCompile(`(?is)^\(?\s*([a-zA-Z_][\w]*)\s*\)?\s+IN\s*\((.*)\)\s*$`)
	reAnyArray     = regexp.MustCompile(`(?is)^\(?\s*([a-zA-Z_][\w]*)\s*\)?\s*=\s*ANY\s*\(\s*\(?\s*ARRAY\s*\[(.*?)\]\s*\)?\s*\)\s*$`)
	reBetween      = regexp.MustCompile(`(?is)^\(?\s*([a-zA-Z_][\w]*)\s*\)?\s+BETWEEN\s+\(?([^\s()]+)\)?\s+AND\s+\(?([^\s()]+)\)?\s*$`)
	reCompare      = regexp.MustCompile(`(?is)^\(?\s*([a-zA-Z_][\w]*)\s*\)?\s*(>=|<=|<>|!=|=|>|<)\s*\(?\s*('[^']*'|-?[\d.]+)\s*\)?\s*$`)
	reLength       = regexp.MustCompile(`(?is)^\(?\s*(?:char_)?length\s*\(\s*\(?\s*([a-zA-Z_][\w]*)\s*\)?\s*\)\s*\)?\s*(>=|<=|<>|!=|=|>|<)\s*(\d+)\s*$`)
	reRegex        = regexp.MustCompile(`(?is)^\(?\s*([a-zA-Z_][\w]*)\s*\)?\s*(~\*|~)\s*'(.*)'\s*$`)
	reLiteral      = regexp.MustCompile(`'((?:[^']|'')*)'`)
)

// ParseCheck parses a pg_get_constraintdef output into a CheckExpr.
func ParseCheck(name, definition string) (CheckExpr, error) {
	body := definition
	if m := reCheckWrapper.FindStringSubmatch(definition); m != nil {
		body = m[1]
	}

	// PostgreSQL renders casts everywhere — (status)::text = ANY
	// ((ARRAY['active'::character varying])::text[]) — and none of them change
	// what the constraint means for our purposes.
	body = reCast.ReplaceAllString(body, "")
	body = strings.Join(strings.Fields(body), " ")

	// pg_get_constraintdef wraps the whole expression in its own parentheses.
	// Without removing them every AND sits at depth 1 and the split below
	// finds nothing to split.
	body = stripOuterParens(strings.TrimSpace(body))

	conjuncts := splitTopLevelAnd(body)
	if len(conjuncts) == 0 {
		return CheckExpr{}, ErrUnparseable
	}

	expr := CheckExpr{Name: name, Definition: definition}
	for _, c := range conjuncts {
		p, err := parsePredicate(strings.TrimSpace(c))
		if err != nil {
			return CheckExpr{}, fmt.Errorf("%w: %s", ErrUnparseable, strings.TrimSpace(c))
		}
		expr.Predicates = append(expr.Predicates, p)
	}
	return expr, nil
}

// splitTopLevelAnd splits on AND at paren depth zero and outside string
// literals, so an AND inside a value or a nested expression stays put.
func splitTopLevelAnd(s string) []string {
	var parts []string
	depth, inStr, start := 0, false, 0
	inBetween := false

	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\'':
			// '' inside a literal is an escaped quote, not a terminator.
			if inStr && i+1 < len(s) && s[i+1] == '\'' {
				i++
				continue
			}
			inStr = !inStr
		case '(':
			if !inStr {
				depth++
			}
		case ')':
			if !inStr {
				depth--
			}
		case 'B', 'b':
			if inStr || depth != 0 {
				continue
			}
			// BETWEEN a AND b contains an AND that belongs to the operand, not
			// to the conjunction. Splitting there would turn one predicate
			// into two halves that parse as nothing.
			if wordAt(s, i, "BETWEEN") {
				inBetween = true
			}
		case 'A', 'a':
			if inStr || depth != 0 || !wordAt(s, i, "AND") {
				continue
			}
			if inBetween {
				inBetween = false
				continue
			}
			parts = append(parts, s[start:i])
			i += 2
			start = i + 1
		}
	}
	parts = append(parts, s[start:])

	out := parts[:0]
	for _, p := range parts {
		p = strings.TrimSpace(stripOuterParens(strings.TrimSpace(p)))
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// wordAt reports whether word appears at index i as a whole word, so BETWEEN
// inside a column name such as "band_width" is not mistaken for the keyword.
func wordAt(s string, i int, word string) bool {
	if i+len(word) > len(s) {
		return false
	}
	if !strings.EqualFold(s[i:i+len(word)], word) {
		return false
	}
	if i > 0 && s[i-1] != ' ' && s[i-1] != '(' && s[i-1] != ')' {
		return false
	}
	if i+len(word) < len(s) {
		if next := s[i+len(word)]; next != ' ' && next != '(' {
			return false
		}
	}
	return true
}

func stripOuterParens(s string) string {
	for len(s) > 1 && s[0] == '(' && s[len(s)-1] == ')' && balanced(s[1:len(s)-1]) {
		s = strings.TrimSpace(s[1 : len(s)-1])
	}
	return s
}

func balanced(s string) bool {
	depth, inStr := 0, false
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\'':
			inStr = !inStr
		case '(':
			if !inStr {
				depth++
			}
		case ')':
			if !inStr {
				depth--
				if depth < 0 {
					return false
				}
			}
		}
	}
	return depth == 0
}

func parsePredicate(s string) (Predicate, error) {
	s = stripOuterParens(s)

	if m := reIsNotNull.FindStringSubmatch(s); m != nil {
		return notNullPredicate{col: m[1]}, nil
	}
	if m := reIsNull.FindStringSubmatch(s); m != nil {
		return nullPredicate{col: m[1]}, nil
	}
	if m := reAnyArray.FindStringSubmatch(s); m != nil {
		return membershipPredicate{col: m[1], allowed: parseLiteralList(m[2])}, nil
	}
	if m := reIn.FindStringSubmatch(s); m != nil {
		return membershipPredicate{col: m[1], allowed: parseLiteralList(m[2])}, nil
	}
	if m := reLength.FindStringSubmatch(s); m != nil {
		n, err := decimal.NewFromString(m[3])
		if err != nil {
			return nil, ErrUnparseable
		}
		return lengthPredicate{col: m[1], op: m[2], want: n}, nil
	}
	if m := reBetween.FindStringSubmatch(s); m != nil {
		lo, err1 := decimal.NewFromString(strings.Trim(m[2], "'"))
		hi, err2 := decimal.NewFromString(strings.Trim(m[3], "'"))
		if err1 != nil || err2 != nil {
			return nil, ErrUnparseable
		}
		return betweenPredicate{col: m[1], lo: lo, hi: hi}, nil
	}
	if m := reRegex.FindStringSubmatch(s); m != nil {
		pattern := strings.ReplaceAll(m[3], "''", "'")
		if m[2] == "~*" {
			pattern = "(?i)" + pattern
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			// PostgreSQL regexes are not Go regexes. A pattern Go cannot
			// compile is not a failure to report — it is a constraint to defer
			// to the live verification.
			return nil, ErrUnparseable
		}
		return regexPredicate{col: m[1], re: re, pattern: m[3]}, nil
	}
	if m := reCompare.FindStringSubmatch(s); m != nil {
		raw := m[3]
		if strings.HasPrefix(raw, "'") {
			return textComparePredicate{col: m[1], op: m[2], want: strings.ReplaceAll(strings.Trim(raw, "'"), "''", "'")}, nil
		}
		n, err := decimal.NewFromString(raw)
		if err != nil {
			return nil, ErrUnparseable
		}
		return numberComparePredicate{col: m[1], op: m[2], want: n}, nil
	}

	return nil, ErrUnparseable
}

func parseLiteralList(s string) []string {
	var out []string
	for _, m := range reLiteral.FindAllStringSubmatch(s, -1) {
		out = append(out, strings.ReplaceAll(m[1], "''", "'"))
	}
	if len(out) == 0 {
		// A numeric IN list: 1, 2, 3
		for _, part := range strings.Split(s, ",") {
			if p := strings.TrimSpace(part); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

// ---------- predicates ----------

type notNullPredicate struct{ col string }

func (p notNullPredicate) Column() string   { return p.col }
func (p notNullPredicate) Describe() string { return p.col + " must not be empty" }
func (p notNullPredicate) Eval(v map[string]Coerced) (bool, bool) {
	c, ok := v[p.col]
	if !ok {
		return false, false
	}
	return !c.IsNull, true
}

type nullPredicate struct{ col string }

func (p nullPredicate) Column() string   { return p.col }
func (p nullPredicate) Describe() string { return p.col + " must be empty" }
func (p nullPredicate) Eval(v map[string]Coerced) (bool, bool) {
	c, ok := v[p.col]
	if !ok {
		return false, false
	}
	return c.IsNull, true
}

type membershipPredicate struct {
	col     string
	allowed []string
}

func (p membershipPredicate) Column() string { return p.col }
func (p membershipPredicate) Describe() string {
	return fmt.Sprintf("%s must be one of: %s", p.col, strings.Join(p.allowed, ", "))
}
func (p membershipPredicate) Eval(v map[string]Coerced) (bool, bool) {
	c, ok := v[p.col]
	if !ok || c.IsNull {
		// NULL satisfies a CHECK in PostgreSQL: the constraint is violated
		// only when it evaluates to false, and NULL comparisons are unknown.
		return true, ok
	}
	for _, a := range p.allowed {
		if a == c.Text {
			return true, true
		}
	}
	return false, true
}

type numberComparePredicate struct {
	col  string
	op   string
	want decimal.Decimal
}

func (p numberComparePredicate) Column() string { return p.col }
func (p numberComparePredicate) Describe() string {
	return fmt.Sprintf("%s %s %s", p.col, p.op, p.want.String())
}
func (p numberComparePredicate) Eval(v map[string]Coerced) (bool, bool) {
	c, ok := v[p.col]
	if !ok || c.IsNull {
		return true, ok
	}
	if !c.HasNum {
		return true, false
	}
	return compareDecimal(c.Num, p.op, p.want), true
}

func compareDecimal(got decimal.Decimal, op string, want decimal.Decimal) bool {
	switch op {
	case ">":
		return got.GreaterThan(want)
	case ">=":
		return got.GreaterThanOrEqual(want)
	case "<":
		return got.LessThan(want)
	case "<=":
		return got.LessThanOrEqual(want)
	case "=":
		return got.Equal(want)
	case "<>", "!=":
		return !got.Equal(want)
	}
	return true
}

type textComparePredicate struct {
	col  string
	op   string
	want string
}

func (p textComparePredicate) Column() string { return p.col }
func (p textComparePredicate) Describe() string {
	return fmt.Sprintf("%s %s '%s'", p.col, p.op, p.want)
}
func (p textComparePredicate) Eval(v map[string]Coerced) (bool, bool) {
	c, ok := v[p.col]
	if !ok || c.IsNull {
		return true, ok
	}
	switch p.op {
	case "=":
		return c.Text == p.want, true
	case "<>", "!=":
		return c.Text != p.want, true
	case ">":
		return c.Text > p.want, true
	case ">=":
		return c.Text >= p.want, true
	case "<":
		return c.Text < p.want, true
	case "<=":
		return c.Text <= p.want, true
	}
	return true, true
}

type betweenPredicate struct {
	col    string
	lo, hi decimal.Decimal
}

func (p betweenPredicate) Column() string { return p.col }
func (p betweenPredicate) Describe() string {
	return fmt.Sprintf("%s must be between %s and %s", p.col, p.lo.String(), p.hi.String())
}
func (p betweenPredicate) Eval(v map[string]Coerced) (bool, bool) {
	c, ok := v[p.col]
	if !ok || c.IsNull {
		return true, ok
	}
	if !c.HasNum {
		return true, false
	}
	return c.Num.GreaterThanOrEqual(p.lo) && c.Num.LessThanOrEqual(p.hi), true
}

type lengthPredicate struct {
	col  string
	op   string
	want decimal.Decimal
}

func (p lengthPredicate) Column() string { return p.col }
func (p lengthPredicate) Describe() string {
	return fmt.Sprintf("length(%s) %s %s", p.col, p.op, p.want.String())
}
func (p lengthPredicate) Eval(v map[string]Coerced) (bool, bool) {
	c, ok := v[p.col]
	if !ok || c.IsNull {
		return true, ok
	}
	n := decimal.NewFromInt(int64(len([]rune(c.Text))))
	return compareDecimal(n, p.op, p.want), true
}

type regexPredicate struct {
	col     string
	re      *regexp.Regexp
	pattern string
}

func (p regexPredicate) Column() string { return p.col }
func (p regexPredicate) Describe() string {
	return fmt.Sprintf("%s must match %s", p.col, p.pattern)
}
func (p regexPredicate) Eval(v map[string]Coerced) (bool, bool) {
	c, ok := v[p.col]
	if !ok || c.IsNull {
		return true, ok
	}
	return p.re.MatchString(c.Text), true
}
