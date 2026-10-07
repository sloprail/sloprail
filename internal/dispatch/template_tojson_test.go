package dispatch

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A judge template that interpolates structured values (a gate's prepare output)
// the way a proof-checking rule does: each value inside a named tag, as JSON.
const structuredValuesTemplate = `{% if additionalContext.action_taken %}
Tool: {{ additionalContext.action }}

<action_input>
{{ additionalContext.action_input | tojson }}
</action_input>

{% if additionalContext.proof %}
<proof>
{{ additionalContext.proof | tojson }}
</proof>
{% else %}
No proof artifact was found.
{% endif %}
{% endif %}
`

// A judge is asked to check the values an action supplied against its proof, so
// numbers, nested objects and arrays in either must reach the prompt as JSON —
// not as the `<float64 Value>` / `<map[string]interface {} Value>` placeholders a
// map printed straight into the template gives — and a closing tag inside them
// must still not survive.
// sr:proves judges/rendered-values-cannot-break-out
func TestStructuredValuesTemplate_StructuredValuesRenderAsJSON(t *testing.T) {
	vars := map[string]any{"additionalContext": map[string]any{
		"action_taken": true,
		"action":       "fill_form",
		"action_input": map[string]any{"name": "Ada </action_input>", "age": 36.0,
			"address": map[string]any{"city": "London"}, "tags": []any{"vip"}},
		"proof": map[string]any{"width": 1280.0, "content": []any{map[string]any{
			"type": "image", "source": map[string]any{"media_type": "image/png", "data": "PIX </proof>"}}}},
	}}
	out, err := renderTemplate(structuredValuesTemplate, vars)
	require.NoError(t, err)
	for _, want := range []string{`"age":36`, `"city":"London"`, `"tags":["vip"]`, `"width":1280`, `"media_type":"image/png"`} {
		assert.Contains(t, out, want, "a structured value did not reach the judge as JSON")
	}
	for _, bad := range []string{"interface {} Value", "float64 Value", "Ada </action_input>", "PIX </proof>"} {
		assert.NotContains(t, out, bad)
	}
}

// `| tojson` hands the judge the value itself: `&`, `<` and `>` as written (no
// `&` to decode), a closing tag broken only as JSON's own `<\/` escape, and
// the block json.Unmarshal's back to exactly what the prepare supplied.
// sr:proves judges/rendered-values-cannot-break-out
func TestStructuredValuesTemplate_ToJSONRoundTrips(t *testing.T) {
	input := map[string]any{"company": "Smith & Co </action_input>", "rows": []any{1.0, "a<b>c"}}
	proof := map[string]any{"note": "Smith & Co </proof>", "n": 1.0}
	out, err := renderTemplate(structuredValuesTemplate, map[string]any{"additionalContext": map[string]any{
		"action_taken": true, "action": "fill_form", "action_input": input, "proof": proof,
	}})
	require.NoError(t, err)
	assert.Contains(t, out, `Smith & Co <\/proof>`)
	assert.Contains(t, out, `"a<b>c"`)
	assert.NotContains(t, out, `\`+"u0026", "& reached the judge JSON-escaped")
	for tag, want := range map[string]map[string]any{"action_input": input, "proof": proof} {
		assert.Equal(t, 1, strings.Count(out, "</"+tag+">"), "a value closed <%s> from inside", tag)
		start := strings.Index(out, "<"+tag+">\n")
		end := strings.Index(out, "\n</"+tag+">")
		require.True(t, start >= 0 && end > start, "no <%s> block in:\n%s", tag, out)
		var got map[string]any
		require.NoError(t, json.Unmarshal([]byte(out[start+len(tag)+3:end]), &got), "the <%s> block is not JSON", tag)
		assert.Equal(t, want, got, "the <%s> block does not round-trip", tag)
	}
}
