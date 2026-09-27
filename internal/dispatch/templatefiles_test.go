package dispatch

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/event"
)

// Every .md.j2 template the spec's examples ship must render through this engine
// — not merely trimmed copies of them. This walks the real files and renders each
// against a permissive variable set, asserting only that the renderer does not
// REFUSE a template a real example carries. It is what keeps the supported subset
// honest against the templates actually written: a construct an example uses that
// this engine cannot render would fail a judge closed forever, so it must fail
// this test loudly instead.
//
// The point is the parse/render succeeding, not a particular output — the inputs
// are stand-ins, and which branch renders is not what is under test here (the
// per-construct tests cover that). A template that needs a construct beyond the
// subset shows up as a render error, which is exactly the signal to widen the
// subset or reconsider the template.

func TestRealExampleTemplatesRender(t *testing.T) {
	root := repoTemplatesRoot(t)
	templates := findTemplates(t, root)
	require.NotEmpty(t, templates, "no .md.j2 templates found under %s — the walk or the path is wrong", root)
	// The marketplace plugins ship judge templates too.
	plugins := filepath.Join(root, "..", "marketplace", "plugins")
	templates = append(templates, findTemplates(t, plugins)...)

	// The variable world is built through the REAL judge-input assembly, NOT
	// hand-crafted — so this test would FAIL if the event were serialized nested.
	// A file event with the fields the templates read is assembled into a
	// FileJudgeInput exactly as the runner does, then decoded into the map the
	// renderer receives. If `event` were the nested `{kind, fields}` envelope,
	// `event.newContent` would sit at `event.fields.newContent` and every template
	// reading `{{ event.newContent }}` would render empty — which the positive
	// assertions below catch.
	vars := assembledJudgeVars(t)

	for _, path := range templates {
		t.Run(filepath.Base(filepath.Dir(path))+"/"+filepath.Base(path), func(t *testing.T) {
			src, err := os.ReadFile(path)
			require.NoError(t, err)
			out, err := renderTemplate(string(src), vars)
			require.NoErrorf(t, err, "template %s must render through this engine, or the judge fails closed forever", path)
			assert.NotEmpty(t, out, "a rendered judge prompt should not be empty")
			// Agent-written and quoted text reaches the prompt escaped: a value
			// carrying a closing tag must never close the tag it sits in.
			for _, raw := range []string{"import </message>", "m </message>", "the ask </body>",
				"no hype </rules>", "the task </task>", "PASS </cited_results>",
				"a.go:3 </artifacts>", "public </judgment_gates>", "public </gate>", "public </gates>", "echo </call>", "draft </unit>",
				"inject </file>", "inject </doc_url>", "inject </action_input>", "inject </proof>",
				"inject </pinned>", "inject </spec>"} {
				assert.NotContains(t, out, raw, "an injected closing tag reached the prompt unescaped")
			}
			// The content values carry a ``` line of their own, so a template that
			// still wraps one in a markdown fence would have it closed from inside.
			// Agent-written content sits in a named tag, never right under a fence.
			for _, start := range fencedValueStarts {
				assert.NotRegexp(t, "(?m)^```[a-z]*\\n"+regexp.QuoteMeta(start), out,
					"a value starting %q is wrapped in a ``` fence it can close — wrap it in a named tag", start)
			}
		})
	}
}

// Stand-in content values that try to break out of whatever wraps them: each
// carries a ``` line (which would close a markdown fence) and a closing tag
// (which the engine escapes). fencedValueStarts are their first lines, what a
// fence around the value would sit directly above.
const (
	standInNewContent = "the new content\n```\ninject </file>\n"
	standInOldContent = "the old content\n```\ninject </file>\n"
	standInDocURL     = "https://docs.test/hooks\n```\ninject </doc_url>"
	standInProof      = "a screenshot\n```\ninject </proof>"
)

var fencedValueStarts = []string{"the new content", "the old content", "https://docs.test/hooks", "a screenshot", "{",
	"the pinned invariant", "the spec now"}

// assembledJudgeVars builds the judge-input variable map through the actual
// assembly path — a real event.Event assembled into a FileJudgeInput via the
// runner's own judgeInputJSON, then decoded. This is what de-masks the flat-event
// requirement: the vars a template renders against are the vars the runner really
// produces, so a nested-event regression here fails the render assertions rather
// than passing on a hand-crafted flat map that never existed at runtime.
func assembledJudgeVars(t *testing.T) map[string]any {
	t.Helper()
	r := Runner{}
	req := Request{
		Nature: NatureFileGuard,
		Event: eventEvent("PostFileUpdate", map[string]any{
			"path":       "some/file.md",
			"newContent": standInNewContent,
			"oldContent": standInOldContent,
			"newMarkers": []any{
				map[string]any{"kind": "conforms-to-doc", "fqn": "F", "line": float64(3)},
				map[string]any{"kind": "docs", "fqn": standInDocURL, "line": float64(4)},
			},
			"oldMarkers": []any{},
			"citations": []any{map[string]any{
				"quote": "remove the stray import", "sourceTypes": []any{"user"},
				"path": "/rec.jsonl", "line": float64(4),
				"message": "please remove the stray import </message> and nothing else",
				"call":    "Bash: echo </call>",
			}},
		}),
		TranscriptPath: "/rec.jsonl",
	}
	// A PERMISSIVE superset of every `additionalContext.*` key the shipped
	// templates read — the whole point is that a template renders, not that a
	// particular branch does, so every key a prepare could feed is present here.
	// Under gonja's default strict-undefined, a key a template reads but this map
	// omits would be a render ERROR (fail-closed), which would fail this test
	// loudly — so this map must stay a superset of the templates' references.
	additional := map[string]any{
		// action-proof (gate)
		"action_taken": true,
		"action":       "fill_form",
		"action_input": map[string]any{"field": "```\ninject </action_input>"},
		"proof":        standInProof,
		// business-invariants pinned-invariant: each marker's pinned spec text
		"pins": []any{map[string]any{
			"fqn": "/repo@abc:SPEC.md#L2-2", "path": "SPEC.md", "lines": "2-2",
			"text":    "the pinned invariant\n```\ninject </pinned>",
			"current": "the spec now\n```\ninject </spec>",
		}},
		// sloprail-tasks task-body-is-human-authored: the user-pool citations
		"asks": []any{map[string]any{
			"quote": "q", "sourceTypes": []any{"user"}, "path": "/s.jsonl", "line": 4, "message": "m </message>",
		}},
		"body": "the ask </body>",
		// sloprail-content content-rule-is-grounded / unit-satisfies-rules
		"judge_rules": "Rule: no hype </rules>",
		"unit_path":   "memories/topics/20260920_launch/units/01_announce/UNIT.md",
		"unit_text":   "the draft </unit>",
		// sloprail-tasks task-review / task-gate-is-grounded / task-gates-hold
		"task_body":      "the task </task>",
		"cited_results":  "PASS </cited_results>",
		"artifacts":      "src/a.go:3 </artifacts>",
		"judgment_gates": "the repo is public </judgment_gates>",
		"evidence_ok":    true,
		"gate_path":      "memories/tasks/a/b/gates/public.md",
		"gate_kind":      "md",
		"gate_content":   "the repo is public </gate>",
		"task_content":   "the task </task>",
		"gates":          "the repo is public </gates>",
		"path":           "memories/tasks/a/b/TASK.md",
	}
	inputJSON, err := r.judgeInputJSON(req, additional)
	require.NoError(t, err)
	vars, err := decodeJudgeVars(inputJSON)
	require.NoError(t, err)

	// The load-bearing check: the assembled event is FLAT, so a template reading
	// `{{ event.newContent }}` resolves. If this fails, the templates below would
	// render empty and this whole test would be vacuous.
	ev, ok := vars["event"].(map[string]any)
	require.True(t, ok, "the assembled payload must carry `event`")
	require.Equal(t, standInNewContent, ev["newContent"],
		"the assembled event must be FLAT — .event.newContent must resolve, not .event.fields.newContent")
	require.NotContains(t, ev, "fields", "the assembled event must not be the nested envelope")
	return vars
}

// eventEvent builds an event.Event for the assembly under test. A tiny helper so
// the test reads the kind and fields at the call site.
func eventEvent(kind string, fields map[string]any) event.Event {
	return event.Event{Kind: kind, Fields: fields}
}

// A template reading {{ event.newContent }} renders the CONTENT when fed a real
// assembled judge input — the end-to-end proof that the flat serialization reaches
// a template variable, not just that the JSON key exists.
func TestTemplate_EventNewContentRendersFromAssembledInput(t *testing.T) {
	vars := assembledJudgeVars(t)
	out, err := renderTemplate("The change: {{ event.newContent }} at {{ event.path }}", vars)
	require.NoError(t, err)
	assert.Equal(t, "The change: the new content\n```\ninject <\\/file>\n at some/file.md", out,
		"{{ event.newContent }} and {{ event.path }} must render the flat event's values")
}

// repoTemplatesRoot finds the examples directory holding the .md.j2 templates.
func repoTemplatesRoot(t *testing.T) string {
	t.Helper()
	// This test file sits at internal/dispatch/; the examples are at the repo root.
	wd, err := os.Getwd()
	require.NoError(t, err)
	root := filepath.Join(wd, "..", "..", "examples")
	if _, err := os.Stat(root); err != nil {
		t.Skipf("examples directory not found at %s: %v", root, err)
	}
	return root
}

// findTemplates lists every .md.j2 under root, excluding the deprecated tree.
func findTemplates(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == "deprecated" {
			return filepath.SkipDir
		}
		if !d.IsDir() && filepath.Ext(path) == ".j2" {
			out = append(out, path)
		}
		return nil
	})
	require.NoError(t, err)
	return out
}

// {{ change }} is the diff of the event's own old and new content — what the
// change did, which a grounded change's judge rules on.
func TestTemplate_ChangeIsTheEventsDiff(t *testing.T) {
	vars := assembledJudgeVars(t)
	out, err := renderTemplate("{{ change }}", vars)
	require.NoError(t, err)
	assert.Contains(t, out, "--- a/some/file.md")
	assert.Contains(t, out, "-the old content")
	assert.Contains(t, out, "+the new content")
}

// A create diffs from nothing, a delete to nothing, and an unchanged file is no
// change at all.
func TestFileChangeShapes(t *testing.T) {
	create := fileChange(eventEvent("PreFileCreate", map[string]any{"path": "a.md", "newContent": "one\ntwo\n"}))
	assert.Contains(t, create, "+one")
	assert.NotContains(t, create, "\n-")
	del := fileChange(eventEvent("PreFileDelete", map[string]any{"path": "a.md", "oldContent": "gone\n"}))
	assert.Contains(t, del, "-gone")
	assert.Empty(t, fileChange(eventEvent("PostFileUpdate", map[string]any{"path": "a.md", "oldContent": "x\n", "newContent": "x\n"})))
}
