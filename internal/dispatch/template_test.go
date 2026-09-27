package dispatch

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The judge-template renderer is gonja (a real Jinja2), so these tests pin the
// constructs the shipped templates actually use — interpolation, `if`/`elif`/
// `else`, a `for` with an inline filter, the value-fallback ternary, the two
// registered filters — and, crucially, the FAIL-CLOSED contract: a template gonja
// cannot parse or cannot resolve returns an ERROR from renderTemplate, which the
// caller (judge.go) turns into a refusal, never a silent blank prompt.
//
// The per-construct cases here are the unit half; templatefiles_test.go renders
// the REAL example templates against the REAL assembled JudgeInput, which is what
// proves the flat-event contract end to end.

func render(t *testing.T, src string, varsJSON string) string {
	t.Helper()
	var vars map[string]any
	require.NoError(t, json.Unmarshal([]byte(varsJSON), &vars))
	out, err := renderTemplate(src, vars)
	require.NoError(t, err, "template: %q", src)
	return out
}

func TestTemplate_Interpolation(t *testing.T) {
	assert.Equal(t, "path is a.md", render(t, "path is {{ event.path }}", `{"event":{"path":"a.md"}}`))
	// A subscript by string reads the same key.
	assert.Equal(t, "active", render(t, `{% if context["r"].active %}active{% endif %}`,
		`{"context":{"r":{"active":true}}}`))
}

func TestTemplate_IfElifElse(t *testing.T) {
	src := `{% if a %}A{% elif b %}B{% else %}C{% endif %}`
	assert.Equal(t, "A", render(t, src, `{"a":true,"b":true}`))
	assert.Equal(t, "B", render(t, src, `{"a":false,"b":true}`))
	assert.Equal(t, "C", render(t, src, `{"a":false,"b":false}`))
}

func TestTemplate_NotAndOr(t *testing.T) {
	assert.Equal(t, "yes", render(t, `{% if not x.active %}yes{% endif %}`, `{"x":{"active":false}}`))
	assert.Equal(t, "yes", render(t, `{% if a or b %}yes{% endif %}`, `{"a":false,"b":true}`))
	assert.Equal(t, "", render(t, `{% if a and b %}yes{% endif %}`, `{"a":true,"b":false}`))
}

func TestTemplate_Equality(t *testing.T) {
	assert.Equal(t, "match", render(t, `{% if m.kind == "conforms-to-doc" %}match{% endif %}`,
		`{"m":{"kind":"conforms-to-doc"}}`))
	assert.Equal(t, "", render(t, `{% if m.kind == "other" %}match{% endif %}`,
		`{"m":{"kind":"conforms-to-doc"}}`))
}

// The value-fallback ternary — `X if X else Y` — is the gonja-correct way the
// templates render "newContent, or oldContent when it is empty". (gonja's `or` is
// a boolean operator, not Jinja2's value-returning `or`, so the templates use the
// ternary; this pins that the ternary returns the VALUE, not a bool.)
func TestTemplate_ValueFallbackTernary(t *testing.T) {
	src := `{{ event.newContent if event.newContent else event.oldContent }}`
	assert.Equal(t, "NEW", render(t, src, `{"event":{"newContent":"NEW","oldContent":"OLD"}}`))
	assert.Equal(t, "OLD", render(t, src, `{"event":{"newContent":"","oldContent":"OLD"}}`))
}

// A for-loop iterates a list, binding the loop variable; the inline `if` filter
// keeps only matching elements; loop.index is 1-based.
func TestTemplate_ForLoop(t *testing.T) {
	out := render(t, `{% for c in cites %}#{{ loop.index }}:{{ c.quote }} {% endfor %}`,
		`{"cites":[{"quote":"one"},{"quote":"two"}]}`)
	assert.Equal(t, "#1:one #2:two ", out)

	// Inline filter over a real list.
	out = render(t, `{% for m in markers if m.kind == "x" %}({{ m.fqn }}){% endfor %}`,
		`{"markers":[{"kind":"x","fqn":"a"},{"kind":"y","fqn":"b"},{"kind":"x","fqn":"c"}]}`)
	assert.Equal(t, "(a)(c)", out)

	// An empty list yields no iterations without erroring.
	assert.Equal(t, "DONE", render(t, `{% for m in markers if m.kind == "x" %}({{ m.fqn }}){% endfor %}DONE`,
		`{"markers":[]}`))
}

// The two filters this renderer registers (matching a10n's set) work.
func TestTemplate_RegisteredFilters(t *testing.T) {
	assert.Equal(t, "a/b", render(t, `{{ p | dirname }}`, `{"p":"a/b/c.md"}`))
	assert.Equal(t, "TestFoo", render(t, `{{ s | funcname }}`, `{"s":"e2e.TestFoo"}`))
}

// A value rendered inside a quoted tag attribute — `<file path="{{ event.path }}">`
// — cannot end the attribute and add one of its own: there its quotes and `&`
// are escaped too, while the same value in the tag's body keeps them.
func TestTemplate_AttributeValuesEscapeQuotes(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{`<file path="{{ p }}">{{ p }}</file>`, `<file path="x&#34; evil=&#34;1">x" evil="1</file>`},
		{`<f a='{{ q }}'>`, `<f a='it&#39;s &amp; more'>`},
		// Two interpolations in one value, a filter with a quoted argument, a
		// filter chain, whitespace control and a ternary all stay one expression.
		{`<c source="{{ p }}:{{ n | int }}" pools="{{ list | join(",") }}">`, `<c source="x&#34; evil=&#34;1:4" pools="a,b">`},
		{`<c k="{{- p -}}">`, `<c k="x&#34; evil=&#34;1">`},
		{`<c k="{{ p if b else q }}">`, `<c k="it&#39;s &amp; more">`},
		// Outside a tag — prose, a comparison, a tag's body — nothing changes.
		{`a < b and x="{{ p }}"`, `a < b and x="x" evil="1"`},
		{`<t>{{ q }}</t> {% if p == "<x a=\"" %}y{% endif %}`, `<t>it's & more</t> `},
		{`if a<b then c = '{{ q }}'`, `if a<b then c = 'it's & more'`},
		// A raw block is literal: nothing inside it is rewritten, and a quote in it
		// does not throw the scan of what follows.
		{`{% raw %}<a b="{{ p }}">{% endraw %}`, `<a b="{{ p }}">`},
		{`x {%- raw %}<a b="{{ p }}">{% endraw -%} y`, `x<a b="{{ p }}"> y`}, // as gonja renders it unrewritten
		{`{% raw %}{{ it's{% endraw %}<a b="{{ p }}">`, `{{ it's<a b="x&#34; evil=&#34;1">`},
		// An entity already in the value cannot pass for a quote.
		{`<c k="{{ ent }}">`, `<c k="x&amp;#34; evil=&amp;#34;1">`},
		// `| raw` undoes the `</` break, not the attribute escape.
		{`<c k="{{ p | raw }}">`, `<c k="x&#34; evil=&#34;1">`},
		// A tag whose name is itself interpolated is still a tag.
		{`<{{ tag }} path="{{ p }}">`, `<file path="x&#34; evil=&#34;1">`},
		{`<{% if b %}x{% else %}y{% endif %} k="{{ p }}">`, `<y k="x&#34; evil=&#34;1">`},
		// CRLF (and a form feed) between `=` and the quote.
		{"<c a=\r\n\"{{ p }}\">", "<c a=\r\n\"x&#34; evil=&#34;1\">"},
		{"<c a=\f'{{ q }}'>", "<c a=\f'it&#39;s &amp; more'>"},
		// A dict literal's closing braces inside the expression are not its end.
		{`<c k="{{ p if {"a": {"b": 1}} else q }}">`, `<c k="x&#34; evil=&#34;1">`},
	} {
		out, err := renderTemplate(tc.src, map[string]any{"p": `x" evil="1`, "q": "it's & more", "n": 4,
			"list": []any{"a", "b"}, "b": false, "tag": "file", "ent": "x&#34; evil=&#34;1"})
		if assert.NoError(t, err, "template: %q", tc.src) {
			assert.Equal(t, tc.want, out, "template: %q", tc.src)
		}
	}
}

// The attribute escape is the engine's, applied by context: a template that
// names it is refused as naming an unknown filter, so no value is escaped twice.
func TestTemplate_AttributeEscapeIsReservedToTheEngine(t *testing.T) {
	_, err := renderTemplate(`<c k="{{ p | attrescape }}">`, map[string]any{"p": `a"b`})
	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), `"attrescape"`)
	}
}

// A template that does not parse is reported in the author's own terms: the
// same error the original template gives, positions included — nothing the
// engine injected to escape attribute values.
func TestTemplate_ParseErrorNamesOnlyTheAuthorsTemplate(t *testing.T) {
	for _, src := range []string{
		`<c k="{{ p }}"> {{ p | }}`, // a filter with no name, after an attribute value
		`<c k="{{ p. }}">`,          // a malformed expression inside one
		`<c k="{{ p }}">{% for %}`,  // a malformed tag after one
	} {
		_, err := renderTemplate(src, map[string]any{"p": "a"})
		require.Error(t, err, src)
		assert.NotContains(t, err.Error(), "attrescape", "the parse error leaks the injected filter: %v", err)
		_, direct := renderGonja(src, map[string]any{"p": "a"})
		require.Error(t, direct, src)
		assert.Equal(t, direct.Error(), err.Error(), "the error (and its position) must be the original template's: %s", src)
	}
}

// A RUNTIME error inside an attribute value is also reported at the author's
// own position, not the rewritten template's.
func TestTemplate_RuntimeErrorNamesTheAuthorsPosition(t *testing.T) {
	for _, src := range []string{
		`<c k="{{ missing.deep }}">`,
		`ab <c k="{{ p }}" j="{{ missing.deep }}">`,
	} {
		_, err := renderTemplate(src, map[string]any{"p": "v"})
		require.Error(t, err, src)
		_, direct := renderGonja(src, map[string]any{"p": "v"})
		require.Error(t, direct, src)
		assert.Equal(t, direct.Error(), err.Error(), "the error must carry the author's positions: %s", src)
	}
}

// FAIL-CLOSED on a filter this engine does not have: gonja itself renders an
// unknown filter as its error object's name (`<*errors.errorString>`) and
// reports no error, so a typo would hand the judge garbage. Every filter name —
// in an expression, in a `{% filter %}` block, in a branch that does not run, in
// an attribute value — is checked before rendering, and the error names it.
func TestTemplate_UnknownFilterFailsClosed(t *testing.T) {
	for _, src := range []string{
		`{{ p | nosuch }}`,
		`{{ p | upper | nosuch(1) }}`,
		`{% filter nosuch %}x{% endfilter %}`,
		`{% if b %}{{ p | nosuch }}{% endif %}`,
		`{% for x in list %}{{ x | nosuch }}{% endfor %}`,
		`<c k="{{ p | nosuch }}">`,
		`{% set y = p | nosuch %}{{ y }}`,
	} {
		out, err := renderTemplate(src, map[string]any{"p": "v", "b": false, "list": []any{"a"}})
		if assert.Error(t, err, "an unknown filter rendered %q from %s", out, src) {
			assert.Contains(t, err.Error(), `"nosuch"`, "the error must name the filter: %s", src)
		}
		assert.Error(t, CheckTemplate(src), "CheckTemplate must report the unknown filter: %s", src)
	}
	assert.NoError(t, CheckTemplate(`{{ p | upper | tojson }} {% filter lower %}X{% endfilter %}`))
}

// FAIL-CLOSED on a filter or test named as an ARGUMENT — map's filter, the test
// select/reject/selectattr/rejectattr apply — and on an unknown `is` test. A
// literal name is checked before rendering; a name that only exists at render
// time (a variable) is caught when it runs. Either way the render is an error
// naming it, and the process survives (an unknown test is a panic gonja cannot
// print, which used to take the whole hook down).
func TestTemplate_UnknownFilterOrTestInAnArgumentFailsClosed(t *testing.T) {
	vars := map[string]any{"p": "v", "list": []any{map[string]any{"x": "a"}}, "f": "nosuch", "tst": "nosuchtest"}
	for src, name := range map[string]string{
		`{{ list | map("nosuch") | join(",") }}`:            "nosuch",
		`{{ list | map(filter="nosuch") | join(",") }}`:     "nosuch",
		`{% if p is nosuchtest %}x{% endif %}`:              "nosuchtest",
		`{{ list | select("nosuchtest") | join(",") }}`:     "nosuchtest",
		`{{ list | reject("nosuch") | join(",") }}`:         "nosuch",
		`{{ list | selectattr("x", "nosuchtest") | list }}`: "nosuchtest",
		`{{ list | rejectattr("x", "nosuchtest") | list }}`: "nosuchtest",
		`{{ list | map(f) | join(",") }}`:                   "nosuch",
		`{{ list | map(f) | list }}`:                        "nosuch",
		`{{ list | select(tst) | list }}`:                   "nosuchtest",
		`<c k="{{ list | selectattr("x", tst) | list }}">`:  "nosuchtest",
	} {
		out, err := renderTemplate(src, vars)
		if assert.Error(t, err, "%s rendered %q", src, out) {
			assert.Contains(t, err.Error(), name, "the error must name %q: %s", name, src)
		}
	}
	for _, src := range []string{
		`{{ list | map("nosuch") | join(",") }}`, `{% if p is nosuchtest %}x{% endif %}`,
		`{{ list | select("nosuchtest") | list }}`, `{{ list | selectattr("x", "nosuchtest") | list }}`,
	} {
		assert.Error(t, CheckTemplate(src), "CheckTemplate must report it: %s", src)
	}
	assert.NoError(t, CheckTemplate(`{{ list | map("upper") | select("defined") | selectattr("x", "defined") | list }} {% if p is string %}{% endif %}`))
}

// FAIL-CLOSED on a filter that returns an error instead of raising one — gonja's
// own slice, sum, unique and urlize do — which would otherwise print the error
// object's name into the prompt.
func TestTemplate_FilterErrorValueFailsClosed(t *testing.T) {
	out, err := renderTemplate(`{{ p | slice("3") }}`, map[string]any{"p": []any{"a", "b"}})
	if assert.Error(t, err, "a filter's error value rendered as %q", out) {
		assert.Contains(t, err.Error(), "slice")
	}
}

// FAIL-CLOSED: a template gonja cannot PARSE (a malformed or unclosed tag) is an
// error, not a silent blank — the caller refuses on it. These are the malformed
// shapes gonja rejects promptly with a parse error; the one it instead HANGS on
// (an unterminated `{{`) is covered by the watchdog test below.
func TestTemplate_ParseErrorFailsClosed(t *testing.T) {
	for _, src := range []string{
		`{% if event.path %}unclosed`, // missing endif
		`{% for x in y %}no end`,      // missing endfor
		`{% if %}empty{% endif %}`,    // no condition
	} {
		_, err := renderTemplate(src, map[string]any{"event": map[string]any{"path": "a"}, "y": []any{}})
		assert.Errorf(t, err, "%q must be refused (parse error), not rendered blank", src)
	}
}

// FAIL-CLOSED against a gonja HANG: this fork's lexer loops forever on an
// unterminated `{{` rather than returning a parse error, and that render runs
// before the sr-agent shell timeout — so without the watchdog it would wedge the
// hook. The watchdog turns "did not finish in time" into an error (→ refusal).
// renderTimeout is lowered here so the proof is fast; production keeps the
// generous bound.
func TestTemplate_HangingTemplateFailsClosedViaWatchdog(t *testing.T) {
	orig := renderTimeout
	renderTimeout = 150 * time.Millisecond
	t.Cleanup(func() { renderTimeout = orig })

	start := time.Now()
	_, err := renderTemplate(`{{ event.path`, map[string]any{"event": map[string]any{"path": "a"}})
	require.Error(t, err, "an unterminated {{ hangs gonja's lexer; the watchdog must refuse rather than wedge")
	assert.Contains(t, err.Error(), "did not finish")
	assert.Less(t, time.Since(start), 2*time.Second, "the watchdog must fire near its bound, not run unbounded")
}

// FAIL-CLOSED: reading a key off an ABSENT parent — and using it in a condition —
// is a gonja RENDER error (its undefined resolution is not uniformly strict, but a
// missing-parent access and an `{% if %}` on an undefined both throw). So a template
// written for a prepare-backed judge that references `additionalContext.<x>` when no
// prepare ran refuses, rather than asking the model a prompt with a hole in it.
func TestTemplate_MissingReferenceFailsClosed(t *testing.T) {
	// `additionalContext` absent (no prepare ran) but the template reads a key off
	// it: an error, so a template written for a prepare-backed judge cannot be
	// rendered blank when no prepare fed it.
	_, err := renderTemplate(`{% if additionalContext.proof %}x{% endif %}`, map[string]any{
		"event": map[string]any{"path": "a"},
	})
	assert.Error(t, err, "reading additionalContext.* when additionalContext is absent must fail closed")

	// Reading a nested attribute off a missing top-level name likewise errors.
	_, err = renderTemplate(`{{ missing.deep }}`, map[string]any{})
	assert.Error(t, err, "a nested access off a missing variable must fail closed")
}
