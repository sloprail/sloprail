package dispatch

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The judge-template renderer is exercised here against both the constructs the
// grammar promises and the ACTUAL templates the spec's examples ship — the gate
// one this slice owns and the file-guard ones the next slice will, since the next
// slice renders them through this same code. A template beyond the supported
// subset must return an error (which the caller turns into a fail-closed refusal),
// not a silent blank.

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

// An undefined variable interpolates to nothing and is falsy — Jinja2's undefined,
// which the templates' `if` guards rely on.
func TestTemplate_UndefinedIsEmptyAndFalsy(t *testing.T) {
	assert.Equal(t, "[]", render(t, "[{{ missing.field }}]", `{}`))
	assert.Equal(t, "no", render(t, "{% if missing %}yes{% else %}no{% endif %}", `{}`))
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

// A for-loop iterates a list, binding the loop variable; the inline `if` filter
// keeps only matching elements.
func TestTemplate_ForLoop(t *testing.T) {
	out := render(t, `{% for c in cites %}[{{ c.quote }}]{% endfor %}`,
		`{"cites":[{"quote":"one"},{"quote":"two"}]}`)
	assert.Equal(t, "[one][two]", out)

	// Inline filter.
	out = render(t, `{% for m in markers if m.kind == "x" %}({{ m.fqn }}){% endfor %}`,
		`{"markers":[{"kind":"x","fqn":"a"},{"kind":"y","fqn":"b"},{"kind":"x","fqn":"c"}]}`)
	assert.Equal(t, "(a)(c)", out)
}

// The `or []` fallback: a missing or empty collection yields no iterations without
// erroring, which the doc-conformance template relies on.
func TestTemplate_ForLoopOrFallback(t *testing.T) {
	out := render(t, `{% for m in (a or b or []) %}x{% endfor %}`, `{}`)
	assert.Equal(t, "", out)

	out = render(t, `{% for m in (a or b or []) %}[{{ m }}]{% endfor %}`,
		`{"a":null,"b":["p","q"]}`)
	assert.Equal(t, "[p][q]", out)
}

// A construct outside the supported subset is an error, not a silent blank — the
// fail-closed contract the caller depends on.
func TestTemplate_UnsupportedConstructErrors(t *testing.T) {
	for _, src := range []string{
		`{{ event.path | upper }}`,    // filter pipe
		`{% set x = 1 %}`,             // set
		`{% if event.path %}unclosed`, // missing endif
		`{{ event.path`,               // unterminated interpolation
		`{% for x in y %}no end`,      // missing endfor
	} {
		_, err := renderTemplate(src, map[string]any{})
		assert.Errorf(t, err, "%q must be refused, not rendered blank", src)
	}
}

// The ACTUAL gate judge template this slice owns renders both branches.
func TestTemplate_ActionProofTemplate(t *testing.T) {
	// The proof-present branch: an action with a screenshot.
	present := render(t, actionProofTemplate,
		`{"additionalContext":{"action_taken":true,"action":"fill_form","action_input":"{}","proof":"a screenshot"}}`)
	assert.Contains(t, present, "Does the proof actually show")
	assert.Contains(t, present, "fill_form")
	assert.Contains(t, present, "a screenshot")

	// The no-action branch: nothing to prove, passes.
	none := render(t, actionProofTemplate, `{"additionalContext":{"action_taken":false}}`)
	assert.Contains(t, none, "No auditable action this turn")
	assert.NotContains(t, none, "Does the proof actually show")

	// The action-without-proof branch names the missing proof.
	noProof := render(t, actionProofTemplate,
		`{"additionalContext":{"action_taken":true,"action":"download","action_input":"{}"}}`)
	assert.Contains(t, noProof, "No proof artifact was found")
}

// A representative file-guard template (the next slice's) with a real for-loop
// renders — proving the shared renderer serves that slice too.
func TestTemplate_CitationsForLoopTemplate(t *testing.T) {
	out := render(t, citationsTemplate,
		`{"additionalContext":{"citations":[{"quote":"q1","source_excerpt":"s1"},{"quote":"q2","source_excerpt":"s2"}]}}`)
	assert.Contains(t, out, "q1")
	assert.Contains(t, out, "s1")
	assert.Contains(t, out, "q2")
	// Whole-word `in` inside quoted content must not be treated as a keyword.
	assert.NotContains(t, strings.ToLower(out), "template:")
}

// actionProofTemplate is the gate judge template from
// examples/action-proof, trimmed to the branch structure under test.
const actionProofTemplate = `{% if not additionalContext.action_taken %}
# No auditable action this turn

No form fill or download happened, so there is nothing to prove. Pass.
{% else %}
# Does the proof actually show the action was done correctly?

## The action

- Tool: {{ additionalContext.action }}
- Inputs: {{ additionalContext.action_input }}

## The proof supplied

{% if additionalContext.proof %}
{{ additionalContext.proof }}
{% else %}
**No proof artifact was found.**
{% endif %}
{% endif %}`

// citationsTemplate is a for-loop template of the shape a file-guard uses.
const citationsTemplate = `Check each citation resolves:
{% for c in additionalContext.citations %}
- Quote: {{ c.quote }}
  Source: {{ c.source_excerpt }}
{% endfor %}`
