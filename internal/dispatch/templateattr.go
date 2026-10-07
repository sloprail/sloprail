package dispatch

import (
	"regexp"
	"strings"

	"github.com/aisbergg/gonja/pkg/gonja/exec"
)

// Attribute-context escaping for judge templates: every `{{ … }}` inside a
// quoted attribute value of a tag renders through attrescape, so a value with a
// quote in it cannot end the attribute and add one of its own (template.go, "A
// value inside a tag's quoted attribute").

// attrReplacer escapes what could end a quoted attribute value, and `&` — so a
// value that already holds `&#34;` cannot pass, to a judge reading entities, for
// a quote that ends the attribute: it reaches the prompt as `&amp;#34;`. Not
// idempotent, which is why a template cannot name the filter (reservedFilters):
// the engine applies it, once, by context.
var attrReplacer = strings.NewReplacer("&", "&amp;", `"`, "&#34;", "'", "&#39;")

// attrEscape is `| attrescape`, applied by escapeAttributeValues to every
// interpolation inside a quoted attribute value.
func attrEscape(e *exec.Evaluator, in exec.Value, params *exec.VarArgs) exec.Value {
	return e.ValueFactory.Value(attrReplacer.Replace(in.String()))
}

// endRaw finds the tag that ends a `{% raw %}` block, in any whitespace-control
// spelling.
var endRaw = regexp.MustCompile(`\{%[-+]?\s*endraw\s*[-+]?%\}`)

// escapeAttributeValues rewrites src so every `{{ expr }}` inside a quoted
// attribute value of a tag (`<name attr="…{{ expr }}…">`) renders through the
// attrescape filter (wrapAttrEscape).
//
// The scan reads the template's markup, not its Jinja: a Jinja tag, comment or
// expression is one opaque unit (string literals and nested braces inside it
// included, jinjaEnd), so a quote in `join(",")` or `{% if x == "<a b=\"" %}`
// never moves it, and a `{% raw %}` block is copied through untouched — it is
// literal output. A tag opens at a `<` that does not follow a word character
// (`a<b` is a comparison) and is followed by a letter or by a Jinja unit (a tag
// whose name is interpolated), and closes at `>`. An attribute value opens at a
// quote whose previous non-space byte is `=`. An unterminated Jinja delimiter is
// left as written, for gonja to refuse (or the watchdog, where gonja loops).
// sr:invariant judges/rendered-values-cannot-break-out
func escapeAttributeValues(src string) string {
	var b strings.Builder
	inTag := false
	var quote byte // the quote of the attribute value being scanned, or 0
	var prev byte  // the last non-space byte seen
	for i := 0; i < len(src); {
		if open, closer, ok := jinjaDelims(src[i:]); ok {
			end := jinjaEnd(src, i+len(open), closer)
			if end < 0 {
				b.WriteString(src[i:])
				break
			}
			unit := src[i : end+len(closer)]
			i = end + len(closer)
			switch {
			case open == "{%" && isRawTag(unit):
				stop := len(src)
				if loc := endRaw.FindStringIndex(src[i:]); loc != nil {
					stop = i + loc[1]
				}
				unit += src[i:stop]
				i = stop
			case open == "{{" && quote != 0:
				unit = wrapAttrEscape(unit)
			}
			b.WriteString(unit)
			continue
		}
		c := src[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case inTag:
			switch {
			case (c == '"' || c == '\'') && prev == '=':
				quote = c
			case c == '>':
				inTag = false
			}
		case c == '<' && !isWordByte(prev) && opensTagName(src[i+1:]):
			inTag = true
		}
		if !isSpaceByte(c) {
			prev = c
		}
		b.WriteByte(c)
		i++
	}
	return b.String()
}

// opensTagName reports whether s — what follows a `<` — begins a tag's name: a
// letter, or a Jinja expression or tag that renders one.
func opensTagName(s string) bool {
	return s != "" && (isASCIILetter(s[0]) || strings.HasPrefix(s, "{{") || strings.HasPrefix(s, "{%"))
}

// isRawTag reports whether a `{% … %}` unit is `{% raw %}` (any whitespace
// control).
func isRawTag(unit string) bool {
	return strings.Trim(unit[2:len(unit)-2], "-+ \t\r\n\f\v") == "raw"
}

// jinjaDelims reports the Jinja delimiter pair s starts with, if any.
func jinjaDelims(s string) (open, closer string, ok bool) {
	switch {
	case strings.HasPrefix(s, "{{"):
		return "{{", "}}", true
	case strings.HasPrefix(s, "{%"):
		return "{%", "%}", true
	case strings.HasPrefix(s, "{#"):
		return "{#", "#}", true
	}
	return "", "", false
}

// jinjaEnd returns the index in src of closer at or after from; -1 when there is
// none. Inside an expression or tag, a closer within a string literal or within
// a `{…}` dict literal is not the end. A comment holds neither, so it ends at the
// first closer.
func jinjaEnd(src string, from int, closer string) int {
	var quote byte
	depth := 0
	for i := from; i < len(src); i++ {
		c := src[i]
		switch {
		case closer == "#}":
			if strings.HasPrefix(src[i:], closer) {
				return i
			}
		case quote != 0 && c == '\\':
			i++
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case depth == 0 && strings.HasPrefix(src[i:], closer):
			return i
		case c == '{':
			depth++
		case c == '}' && depth > 0:
			depth--
		}
	}
	return -1
}

// wrapAttrEscape turns `{{ expr }}` into
// `{% filter attrescape %}{{ expr }}{% endfilter %}`: a filter block escapes the
// expression's whole rendered value whatever it is — a filter chain, a ternary —
// with no parsing of it. Whitespace control moves onto the block's own tags, so
// `{{- expr -}}` still trims what surrounds it.
func wrapAttrEscape(unit string) string {
	inner := unit[2 : len(unit)-2]
	lead, trail := "", ""
	if strings.HasPrefix(inner, "-") {
		lead, inner = "-", inner[1:]
	}
	if strings.HasSuffix(inner, "-") {
		trail, inner = "-", inner[:len(inner)-1]
	}
	return "{%" + lead + " filter attrescape %}{{" + inner + "}}{% endfilter " + trail + "%}"
}

func isASCIILetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

func isWordByte(c byte) bool { return isASCIILetter(c) || c >= '0' && c <= '9' || c == '_' }

func isSpaceByte(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'
}
