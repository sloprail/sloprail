package dispatch

import (
	"fmt"
	"strings"
)

// This file evaluates the small expression grammar a judge template's `{{ }}` and
// `{% if %}`/`for ... if` use. It is intentionally narrow — see template.go for
// the whole supported set and why it refuses rather than guesses. The grammar,
// lowest precedence first:
//
//	or          a or b            (Jinja2's, returns the first truthy value)
//	and         a and b
//	not         not a
//	==          a == b
//	primary     name | a.b | a["b"] | "lit" | 'lit' | []
//
// Values flow through as `any` decoded from JSON: strings, float64, bool, nil,
// []any, map[string]any — the shapes json.Unmarshal produces, which is what the
// judge input is decoded into before rendering.

// evalTruthy evaluates an expression and reports its Jinja2 truthiness.
func evalTruthy(src string, vars map[string]any) (bool, error) {
	v, err := evalExpr(src, vars)
	if err != nil {
		return false, err
	}
	return truthy(v), nil
}

// evalExpr evaluates one expression to a value.
func evalExpr(src string, vars map[string]any) (any, error) {
	src = strings.TrimSpace(src)
	if src == "" {
		return nil, fmt.Errorf("template: empty expression")
	}
	v, rest, err := parseOr(src, vars)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(rest) != "" {
		return nil, fmt.Errorf("template: unexpected %q after expression in %q — this engine supports only name/member/subscript, not/and/or, == and string literals", strings.TrimSpace(rest), src)
	}
	return v, nil
}

// parseOr handles `a or b`, returning the first truthy operand's value (Jinja2's
// `or`), which is what makes `event.newMarkers or event.oldMarkers or []` pick
// the first non-empty list.
func parseOr(src string, vars map[string]any) (any, string, error) {
	left, rest, err := parseAnd(src, vars)
	if err != nil {
		return nil, "", err
	}
	for {
		rest2, ok := consumeKeyword(rest, "or")
		if !ok {
			return left, rest, nil
		}
		right, rest3, err := parseAnd(rest2, vars)
		if err != nil {
			return nil, "", err
		}
		if truthy(left) {
			// Keep left's VALUE, but continue consuming so `a or b or c` parses.
			rest = rest3
			continue
		}
		left, rest = right, rest3
	}
}

// parseAnd handles `a and b`, returning the last operand when all are truthy and
// the first falsy one otherwise — Jinja2's `and` value semantics, sufficient for
// the boolean uses here.
func parseAnd(src string, vars map[string]any) (any, string, error) {
	left, rest, err := parseNot(src, vars)
	if err != nil {
		return nil, "", err
	}
	for {
		rest2, ok := consumeKeyword(rest, "and")
		if !ok {
			return left, rest, nil
		}
		right, rest3, err := parseNot(rest2, vars)
		if err != nil {
			return nil, "", err
		}
		if !truthy(left) {
			return left, rest3, nil
		}
		left, rest = right, rest3
	}
}

// parseNot handles a leading `not`.
func parseNot(src string, vars map[string]any) (any, string, error) {
	if rest, ok := consumeKeyword(src, "not"); ok {
		v, rest2, err := parseNot(rest, vars)
		if err != nil {
			return nil, "", err
		}
		return !truthy(v), rest2, nil
	}
	return parseEquality(src, vars)
}

// parseEquality handles `a == b`, producing a bool.
func parseEquality(src string, vars map[string]any) (any, string, error) {
	left, rest, err := parsePrimary(src, vars)
	if err != nil {
		return nil, "", err
	}
	trimmed := strings.TrimSpace(rest)
	if strings.HasPrefix(trimmed, "==") {
		right, rest2, err := parsePrimary(trimmed[2:], vars)
		if err != nil {
			return nil, "", err
		}
		return equalValues(left, right), rest2, nil
	}
	return left, rest, nil
}

// parsePrimary parses a literal, an empty-list, or a variable with any trailing
// `.field` / `["key"]` accessors.
func parsePrimary(src string, vars map[string]any) (any, string, error) {
	src = strings.TrimLeft(src, " \t")
	if src == "" {
		return nil, "", fmt.Errorf("template: expected a value")
	}

	switch src[0] {
	case '"', '\'':
		return parseStringLiteral(src)
	case '[':
		// Only the empty-list literal `[]` is supported — the `or []` fallback.
		rest := strings.TrimLeft(src[1:], " \t")
		if len(rest) == 0 || rest[0] != ']' {
			return nil, "", fmt.Errorf("template: only the empty list literal [] is supported, got %q", src)
		}
		return []any{}, rest[1:], nil
	case '(':
		// A parenthesised sub-expression — the `(a or b or [])` grouping the
		// for-loop fallback idiom uses. Evaluate the inside as a full expression up
		// to the matching close paren.
		inner, rest, err := parseOr(src[1:], vars)
		if err != nil {
			return nil, "", err
		}
		rest = strings.TrimLeft(rest, " \t")
		if rest == "" || rest[0] != ')' {
			return nil, "", fmt.Errorf("template: unterminated ( in expression")
		}
		// Accessors may follow a group, e.g. (a or b).field — apply them.
		return applyAccessors(inner, rest[1:])
	}

	// A variable path: an identifier, then a chain of .field / ["key"].
	name, rest := readIdent(src)
	if name == "" {
		return nil, "", fmt.Errorf("template: expected a name or literal, got %q", src)
	}
	cur, ok := vars[name]
	if !ok {
		// An undefined variable is nil, matching Jinja2's undefined — a template
		// that reads a field the payload did not carry gets "nothing", which its
		// `if` and `or []` guards are written for. This is NOT a render error.
		cur = nil
	}
	return applyAccessors(cur, rest)
}

// applyAccessors walks a `.field` / ["key"] chain from a base value.
func applyAccessors(cur any, rest string) (any, string, error) {
	for {
		rest = strings.TrimLeft(rest, " \t")
		if rest == "" {
			return cur, rest, nil
		}
		switch rest[0] {
		case '.':
			name, remaining := readIdent(rest[1:])
			if name == "" {
				return nil, "", fmt.Errorf("template: expected a field name after '.'")
			}
			cur = member(cur, name)
			rest = remaining
		case '[':
			key, remaining, err := parseStringLiteral(strings.TrimLeft(rest[1:], " \t"))
			if err != nil {
				return nil, "", fmt.Errorf("template: subscript must be a string literal: %w", err)
			}
			remaining = strings.TrimLeft(remaining, " \t")
			if remaining == "" || remaining[0] != ']' {
				return nil, "", fmt.Errorf("template: unterminated subscript [")
			}
			s, _ := key.(string)
			cur = member(cur, s)
			rest = remaining[1:]
		default:
			return cur, rest, nil
		}
	}
}

// member reads a key out of a map value, or nil for anything that is not a map or
// a missing key — the undefined-is-nil rule, applied one level down.
func member(v any, key string) any {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	return m[key]
}

// parseStringLiteral reads a single- or double-quoted string. No escapes are
// interpreted beyond a doubled quote character being impossible — the literals in
// these templates are plain marker names and keys, so a full escape grammar would
// be answering a question no template asks.
func parseStringLiteral(src string) (any, string, error) {
	if src == "" || (src[0] != '"' && src[0] != '\'') {
		return nil, "", fmt.Errorf("template: expected a quoted string, got %q", src)
	}
	quote := src[0]
	end := strings.IndexByte(src[1:], quote)
	if end < 0 {
		return nil, "", fmt.Errorf("template: unterminated string literal in %q", src)
	}
	return src[1 : 1+end], src[1+end+1:], nil
}

// readIdent reads a leading identifier (letters, digits, _ and -), returning it
// and the remainder. A leading non-identifier yields "".
func readIdent(src string) (string, string) {
	src = strings.TrimLeft(src, " \t")
	i := 0
	for i < len(src) && isIdentByte(src[i]) {
		i++
	}
	return src[:i], src[i:]
}

// consumeKeyword strips a leading whole-word keyword (with its surrounding space)
// from src, reporting whether it was there. Whole-word so `order` does not consume
// `or`.
func consumeKeyword(src, kw string) (string, bool) {
	trimmed := strings.TrimLeft(src, " \t")
	if !strings.HasPrefix(trimmed, kw) {
		return src, false
	}
	after := trimmed[len(kw):]
	if after != "" && isIdentByte(after[0]) {
		return src, false // part of a longer identifier
	}
	return after, true
}

// truthy is Jinja2's notion of truth over the JSON value shapes.
//
//   - nil / undefined: false
//   - bool: itself
//   - string: non-empty
//   - list / map: non-empty
//   - number: non-zero
func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	case float64:
		return x != 0
	case int:
		return x != 0
	default:
		return v != nil
	}
}

// equalValues compares two decoded values for `==`. String/bool/number compare by
// value; anything else is unequal unless identical nils. Sufficient for the marker
// and flag comparisons the templates use.
func equalValues(a, b any) bool {
	switch av := a.(type) {
	case string:
		bv, ok := b.(string)
		return ok && av == bv
	case bool:
		bv, ok := b.(bool)
		return ok && av == bv
	case float64:
		bv, ok := b.(float64)
		return ok && av == bv
	case nil:
		return b == nil
	default:
		return false
	}
}

// toList coerces a value to a slice for a for-loop, reporting whether it is one.
// A nil (a missing collection) is not a list — the loop renders nothing — which is
// the right reading of `for x in missing`.
func toList(v any) ([]any, bool) {
	if v == nil {
		return nil, false
	}
	l, ok := v.([]any)
	return l, ok
}
