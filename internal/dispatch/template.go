package dispatch

import (
	"fmt"
	"strconv"
	"strings"
)

// This file renders a judge's `.md.j2` prompt template against its JudgeInput.
//
// A judge is "a Jinja2 prompt template (.md.j2)" (dot-dir-file-store/main.tsp),
// rendered against the nature's judge-input model — the payload spread flat plus
// `additionalContext`. The output is the prompt handed to the model.
//
// # Why a focused renderer rather than a Jinja2 library
//
// The templates the spec's examples ship use a small, closed set of Jinja2
// constructs, and the rest of this repo is deliberately dependency-light and
// offline-buildable. A full Jinja2 engine would pull a large transitive tree
// (logging, reflection helpers) for a feature that needs variable interpolation,
// a boolean `if`, and a `for`. So this renders exactly the subset the examples
// use and REFUSES anything outside it rather than guessing — the same
// boolean-or-refuse discipline the match compilers keep, applied to templates: a
// template a judge cannot render is a judge that cannot be asked, which the caller
// turns into a fail-closed refusal, never a silently blank prompt.
//
// The supported grammar, pinned by the tests and matched against every template
// under examples/:
//
//	{{ expr }}                          interpolation
//	{% if expr %} … {% elif expr %} … {% else %} … {% endif %}
//	{% for name in expr %} … {% endfor %}
//	{% for name in expr if expr %} … {% endfor %}   (inline filter)
//
// expressions:
//
//	a.b.c            member access (map key or field)
//	a["b"]           subscript by string
//	a or b           first truthy (Jinja2's `or`, used for `x or []` fallbacks)
//	a and b          both truthy
//	not a            negation
//	a == b           equality
//	"lit" / 'lit'    string literal
//	[]               empty-list literal (the `or []` fallback idiom)
//	name             a variable in scope
//
// Anything else — a filter pipe `|`, arithmetic, a method call — is refused with a
// diagnostic naming the unrecognised construct, so an author who reaches past the
// subset learns at the moment the judge would render rather than getting an empty
// prompt.

// renderTemplate renders src against vars and returns the prompt text.
//
// vars is the judge-input as a decoded JSON object (map[string]any), so a template
// reads `event.newContent`, `context`, `additionalContext.x` as ordinary member
// accesses. A render error is returned rather than a partial string: the caller
// refuses on it (fail-closed), because a half-rendered prompt asks the model a
// different question than the author wrote.
func renderTemplate(src string, vars map[string]any) (string, error) {
	toks, err := tokenizeTemplate(src)
	if err != nil {
		return "", err
	}
	nodes, rest, err := parseNodes(toks, "")
	if err != nil {
		return "", err
	}
	if len(rest) != 0 {
		return "", fmt.Errorf("template: unexpected %s with no opening tag", rest[0].describe())
	}
	var b strings.Builder
	if err := renderNodes(nodes, vars, &b); err != nil {
		return "", err
	}
	return b.String(), nil
}

// -- tokenizing --

// tokKind is what a template token is: literal text, an interpolation, or a
// statement tag.
type tokKind int

const (
	tokText tokKind = iota // literal template text
	tokExpr                // {{ ... }}
	tokStmt                // {% ... %}
)

// token is one lexical piece of a template.
type token struct {
	kind tokKind
	body string // for text: the text; for expr/stmt: the trimmed inside of the braces
}

func (t token) describe() string {
	switch t.kind {
	case tokExpr:
		return "{{ " + t.body + " }}"
	case tokStmt:
		return "{% " + t.body + " %}"
	default:
		return "text"
	}
}

// tokenizeTemplate splits a template into text / {{ }} / {% %} tokens.
//
// A `{{` or `{%` with no matching close is an error rather than being emitted as
// literal text: an unterminated tag is a template mistake, and rendering it as
// prose would put half a tag in the model's prompt.
func tokenizeTemplate(src string) ([]token, error) {
	var toks []token
	i := 0
	for i < len(src) {
		open := strings.IndexByte(src[i:], '{')
		if open < 0 || open+1 >= len(src[i:]) {
			toks = append(toks, token{kind: tokText, body: src[i:]})
			break
		}
		open += i
		next := src[open+1]
		if next != '{' && next != '%' {
			// A lone '{' — literal. Consume up to and including it and continue.
			toks = append(toks, token{kind: tokText, body: src[i : open+1]})
			i = open + 1
			continue
		}
		if open > i {
			toks = append(toks, token{kind: tokText, body: src[i:open]})
		}
		closeSeq := "}}"
		kind := tokExpr
		if next == '%' {
			closeSeq = "%}"
			kind = tokStmt
		}
		end := strings.Index(src[open+2:], closeSeq)
		if end < 0 {
			return nil, fmt.Errorf("template: unterminated %s", map[byte]string{'{': "{{", '%': "{%"}[next])
		}
		body := strings.TrimSpace(src[open+2 : open+2+end])
		toks = append(toks, token{kind: kind, body: body})
		i = open + 2 + end + len(closeSeq)
	}
	return toks, nil
}

// -- parsing into a node tree --

// node is one rendered piece: text, an interpolation, an if, or a for.
type node struct {
	kind tokKind // tokText / tokExpr for leaves
	text string  // for tokText
	expr string  // for tokExpr

	// for control nodes (kind is one of the control markers below)
	control  controlKind
	branches []branch // if/elif/else arms
	loopVar  string   // for-loop variable
	loopExpr string   // for-loop collection expression
	loopIf   string   // optional inline filter expression
	body     []node   // for-loop body
}

// controlKind marks a control node.
type controlKind int

const (
	ctrlNone controlKind = iota
	ctrlIf
	ctrlFor
)

// branch is one arm of an if/elif/else: a condition (empty for else) and its body.
type branch struct {
	cond string // "" means else
	body []node
}

// parseNodes consumes tokens into nodes until it hits a closing tag for `stop`
// (e.g. "endif", "endfor", "elif", "else"), which it returns unconsumed so the
// caller that opened the block can dispatch on it. `stop` is "" at the top level,
// where any closing tag is an error.
func parseNodes(toks []token, stop string) (nodes []node, rest []token, err error) {
	for len(toks) > 0 {
		t := toks[0]
		switch t.kind {
		case tokText:
			nodes = append(nodes, node{kind: tokText, text: t.body})
			toks = toks[1:]
		case tokExpr:
			nodes = append(nodes, node{kind: tokExpr, expr: t.body})
			toks = toks[1:]
		case tokStmt:
			keyword := firstWord(t.body)
			if isCloser(keyword) {
				// A closing/continuation tag: hand it back to whoever opened the
				// block. At the top level (stop == "") there is nothing to close.
				return nodes, toks, nil
			}
			switch keyword {
			case "if":
				n, remaining, perr := parseIf(toks)
				if perr != nil {
					return nil, nil, perr
				}
				nodes = append(nodes, n)
				toks = remaining
			case "for":
				n, remaining, perr := parseFor(toks)
				if perr != nil {
					return nil, nil, perr
				}
				nodes = append(nodes, n)
				toks = remaining
			default:
				return nil, nil, fmt.Errorf("template: unsupported tag {%% %s %%} — this engine renders if/elif/else/for only", t.body)
			}
		}
	}
	if stop != "" {
		return nil, nil, fmt.Errorf("template: missing {%% end%s %%}", strings.TrimPrefix(stop, "end"))
	}
	return nodes, nil, nil
}

// parseIf parses an if/elif/else/endif chain starting at toks[0].
func parseIf(toks []token) (node, []token, error) {
	n := node{control: ctrlIf}
	// The opening `if <cond>`.
	cond := strings.TrimSpace(strings.TrimPrefix(toks[0].body, "if"))
	if cond == "" {
		return node{}, nil, fmt.Errorf("template: {%% if %%} with no condition")
	}
	toks = toks[1:]
	for {
		body, rest, err := parseNodes(toks, "endif")
		if err != nil {
			return node{}, nil, err
		}
		n.branches = append(n.branches, branch{cond: cond, body: body})
		if len(rest) == 0 {
			return node{}, nil, fmt.Errorf("template: {%% if %%} without {%% endif %%}")
		}
		closer := rest[0]
		keyword := firstWord(closer.body)
		switch keyword {
		case "endif":
			return n, rest[1:], nil
		case "elif":
			cond = strings.TrimSpace(strings.TrimPrefix(closer.body, "elif"))
			if cond == "" {
				return node{}, nil, fmt.Errorf("template: {%% elif %%} with no condition")
			}
			toks = rest[1:]
		case "else":
			// The else arm, then a required endif.
			elseBody, elseRest, err := parseNodes(rest[1:], "endif")
			if err != nil {
				return node{}, nil, err
			}
			n.branches = append(n.branches, branch{cond: "", body: elseBody})
			if len(elseRest) == 0 || firstWord(elseRest[0].body) != "endif" {
				return node{}, nil, fmt.Errorf("template: {%% else %%} without {%% endif %%}")
			}
			return n, elseRest[1:], nil
		default:
			return node{}, nil, fmt.Errorf("template: unexpected {%% %s %%} inside an if", closer.body)
		}
	}
}

// parseFor parses a for/endfor block, with an optional inline `if` filter.
//
// The shape is `for <name> in <expr>` or `for <name> in <expr> if <filter>` — the
// two forms the grounding-citations and doc-conformance templates use. The filter
// is applied per element, so `for m in markers if m.kind == "x"` iterates only the
// matching ones.
func parseFor(toks []token) (node, []token, error) {
	header := strings.TrimSpace(strings.TrimPrefix(toks[0].body, "for"))
	name, rest, ok := cutWord(header, "in")
	if !ok {
		return node{}, nil, fmt.Errorf("template: {%% for %%} must read `for <name> in <expr>`, got {%% %s %%}", toks[0].body)
	}
	collExpr := strings.TrimSpace(rest)
	var filter string
	if idx := findKeyword(collExpr, "if"); idx >= 0 {
		filter = strings.TrimSpace(collExpr[idx+len("if"):])
		collExpr = strings.TrimSpace(collExpr[:idx])
	}
	if name == "" || collExpr == "" {
		return node{}, nil, fmt.Errorf("template: {%% for %%} must name a variable and a collection, got {%% %s %%}", toks[0].body)
	}
	body, remaining, err := parseNodes(toks[1:], "endfor")
	if err != nil {
		return node{}, nil, err
	}
	if len(remaining) == 0 || firstWord(remaining[0].body) != "endfor" {
		return node{}, nil, fmt.Errorf("template: {%% for %%} without {%% endfor %%}")
	}
	return node{
		control:  ctrlFor,
		loopVar:  name,
		loopExpr: collExpr,
		loopIf:   filter,
		body:     body,
	}, remaining[1:], nil
}

// -- rendering the node tree --

func renderNodes(nodes []node, vars map[string]any, b *strings.Builder) error {
	for _, n := range nodes {
		if err := renderNode(n, vars, b); err != nil {
			return err
		}
	}
	return nil
}

func renderNode(n node, vars map[string]any, b *strings.Builder) error {
	switch {
	case n.control == ctrlIf:
		for _, br := range n.branches {
			if br.cond == "" {
				return renderNodes(br.body, vars, b) // else
			}
			ok, err := evalTruthy(br.cond, vars)
			if err != nil {
				return err
			}
			if ok {
				return renderNodes(br.body, vars, b)
			}
		}
		return nil
	case n.control == ctrlFor:
		coll, err := evalExpr(n.loopExpr, vars)
		if err != nil {
			return err
		}
		items, ok := toList(coll)
		if !ok {
			// A non-iterable collection renders no iterations rather than erroring:
			// the `or []` fallback idiom means the author already handles "nothing",
			// and a missing field decoded as nil is the common empty case.
			return nil
		}
		for _, item := range items {
			scoped := scopeWith(vars, n.loopVar, item)
			if n.loopIf != "" {
				keep, err := evalTruthy(n.loopIf, scoped)
				if err != nil {
					return err
				}
				if !keep {
					continue
				}
			}
			if err := renderNodes(n.body, scoped, b); err != nil {
				return err
			}
		}
		return nil
	case n.kind == tokExpr:
		v, err := evalExpr(n.expr, vars)
		if err != nil {
			return err
		}
		b.WriteString(stringify(v))
		return nil
	default:
		b.WriteString(n.text)
		return nil
	}
}

// scopeWith returns vars with one name bound to a value, without mutating the
// caller's map — a loop body reads the loop variable alongside the outer scope.
func scopeWith(vars map[string]any, name string, value any) map[string]any {
	out := make(map[string]any, len(vars)+1)
	for k, v := range vars {
		out[k] = v
	}
	out[name] = value
	return out
}

// -- small lexical helpers --

func firstWord(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		return s[:i]
	}
	return s
}

func isCloser(keyword string) bool {
	switch keyword {
	case "endif", "endfor", "elif", "else":
		return true
	}
	return false
}

// cutWord splits `s` at the first occurrence of the whole word `sep`, returning
// the trimmed halves. Used for `for <name> in <expr>`.
func cutWord(s, sep string) (before, after string, ok bool) {
	idx := findKeyword(s, sep)
	if idx < 0 {
		return "", "", false
	}
	return strings.TrimSpace(s[:idx]), strings.TrimSpace(s[idx+len(sep):]), true
}

// findKeyword finds a whole-word keyword in s (bounded by whitespace or ends),
// ignoring occurrences inside quotes, or -1. This keeps `if`/`in`/`or` from
// matching inside a string literal or a longer identifier.
func findKeyword(s, kw string) int {
	inSingle, inDouble := false, false
	for i := 0; i+len(kw) <= len(s); i++ {
		c := s[i]
		switch {
		case c == '\'' && !inDouble:
			inSingle = !inSingle
		case c == '"' && !inSingle:
			inDouble = !inDouble
		}
		if inSingle || inDouble {
			continue
		}
		if s[i:i+len(kw)] != kw {
			continue
		}
		// Whole-word: bounded left and right by non-identifier characters.
		if i > 0 && isIdentByte(s[i-1]) {
			continue
		}
		if i+len(kw) < len(s) && isIdentByte(s[i+len(kw)]) {
			continue
		}
		return i
	}
	return -1
}

func isIdentByte(c byte) bool {
	return c == '_' || c == '-' ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// stringify renders an interpolated value as prompt text.
//
// A string is itself; a nil renders empty (a missing field interpolates to
// nothing, which is what a Jinja2 template expects of an undefined); a number or
// bool renders in its natural form; anything structured (a map or list a template
// dropped in whole, e.g. a JSON blob) renders as its Go form, which is legible
// enough for a prompt. Kept deliberately simple — a judge template that needs
// pretty JSON formats it in a prepare script.
func stringify(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case int:
		return strconv.Itoa(x)
	default:
		return fmt.Sprintf("%v", v)
	}
}
