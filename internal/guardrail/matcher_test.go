package guardrail

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/event"
)

// fileEvent is the shape a file module produces, built inline so a test says
// what it is matching against rather than pointing at a fixture.
func fileEvent(path string) event.Event {
	return event.Event{Kind: "PreFileCreate", Fields: map[string]any{"path": path}}
}

func TestCompileMatcher_EmptyAdmitsEverything(t *testing.T) {
	m, err := CompileMatcher("")
	require.NoError(t, err)
	require.NotNil(t, m)

	// "No matcher" is the absence of a narrowing, not a narrowing to nothing.
	for _, e := range []event.Event{
		fileEvent("memories/a.md"),
		fileEvent(""),
		{Kind: "TurnEnd"},
	} {
		admitted, err := m.Match(e)
		require.NoError(t, err)
		assert.True(t, admitted, "empty matcher must admit %+v", e)
	}
}

func TestMatch_NilMatcherAdmitsEverything(t *testing.T) {
	// A binding that never got a matcher at all reaches Match as a nil
	// pointer; that is the same "no matcher" case and must not panic.
	var m *Matcher
	admitted, err := m.Match(fileEvent("anything"))
	require.NoError(t, err)
	assert.True(t, admitted)
}

func TestMatch_AdmitsAndRejectsOnEventFields(t *testing.T) {
	m, err := CompileMatcher(`path startsWith "memories/" && path endsWith ".md"`)
	require.NoError(t, err)

	cases := []struct {
		path     string
		admitted bool
	}{
		{"memories/decision.md", true},
		{"memories/nested/deep.md", true},
		{"memories/notes.txt", false},
		{"docs/decision.md", false},
		{"", false},
	}
	for _, tc := range cases {
		admitted, err := m.Match(fileEvent(tc.path))
		require.NoError(t, err, "path %q", tc.path)
		assert.Equal(t, tc.admitted, admitted, "path %q", tc.path)
	}
}

func TestMatch_ReadsListFieldsWithAnd0r(t *testing.T) {
	// The spec's own example: a command matched against flattened invocations
	// rather than the raw string, composed with && .
	m, err := CompileMatcher(`any(invocations, .bin == "npm" && "--access" in .flags)`)
	require.NoError(t, err)

	publish := event.Event{Kind: "PreCommand", Fields: map[string]any{
		"invocations": []any{
			map[string]any{"bin": "git", "flags": []any{"--no-pager"}},
			map[string]any{"bin": "npm", "flags": []any{"--access", "public"}},
		},
	}}
	admitted, err := m.Match(publish)
	require.NoError(t, err)
	assert.True(t, admitted)

	plain := event.Event{Kind: "PreCommand", Fields: map[string]any{
		"invocations": []any{
			map[string]any{"bin": "npm", "flags": []any{"install"}},
		},
	}}
	admitted, err = m.Match(plain)
	require.NoError(t, err)
	assert.False(t, admitted)
}

func TestCompileMatcher_SyntaxErrorIsRefused(t *testing.T) {
	// A matcher that will not compile must not degrade into a permissive
	// default: an unparseable rule is louder as a load failure than as a rule
	// that admits everything.
	for _, src := range []string{
		`path startsWith`,
		`path ==`,
		`(path == "a"`,
		`&&`,
	} {
		m, err := CompileMatcher(src)
		require.Error(t, err, "src %q must not compile", src)
		assert.Nil(t, m, "src %q must yield no matcher", src)
		assert.Contains(t, err.Error(), src, "the error names the offending source")
	}
}

func TestCompileMatcher_NonBooleanIsRefusedAtCompile(t *testing.T) {
	// expr.AsBool() makes "must be a boolean" a compile-time contract for any
	// expression whose type is statically known.
	for _, src := range []string{`1 + 1`, `len(path)`, `"a string"`} {
		m, err := CompileMatcher(src)
		require.Error(t, err, "src %q must not compile", src)
		assert.Nil(t, m)
		assert.Contains(t, err.Error(), "expected bool")
	}
}

func TestMatch_NonBooleanAtRuntimeIsAnError(t *testing.T) {
	// Where the type is not statically known, AsBool() compiles in a cast
	// instead. The cast fails at run time, so the expression still never
	// quietly admits — but note it surfaces as a run error, not as the
	// "produced %T, not a boolean" message in Match. That branch is currently
	// unreachable; this test pins the behaviour that actually occurs.
	m, err := CompileMatcher(`path`)
	require.NoError(t, err, "a bare field is statically dynamic, so it compiles")

	admitted, err := m.Match(fileEvent("memories/a.md"))
	require.Error(t, err)
	assert.False(t, admitted, "an erroring matcher admits nothing")
	assert.Contains(t, err.Error(), `matcher "path"`)
	assert.Contains(t, err.Error(), "invalid operation: bool(string)")
	assert.NotContains(t, err.Error(), "not a boolean",
		"documents that the runtime type check in Match is not what fires here")
}

func TestMatch_UnknownFieldIsNilNotAnError(t *testing.T) {
	// CURRENT BEHAVIOUR, deliberately pinned: the environment is exactly the
	// event's fields, and expr resolves an absent name to nil rather than
	// failing. So a matcher naming a field the kind does not carry compiles,
	// runs, and quietly evaluates false — the "silently never fires" outcome
	// the matcher design says it exists to prevent. Nothing validates a
	// matcher against the kind's declared fields yet (Registry.KindDeclFor is
	// the hook for it, and no caller uses it for this).
	m, err := CompileMatcher(`missingfield == "x"`)
	require.NoError(t, err)

	admitted, err := m.Match(fileEvent("memories/a.md"))
	require.NoError(t, err, "no error today — an unknown field is not caught here")
	assert.False(t, admitted)

	// A typo in a field that does exist behaves the same way: false, silently.
	typo, err := CompileMatcher(`paht startsWith "memories/"`)
	require.NoError(t, err)
	admitted, err = typo.Match(fileEvent("memories/a.md"))
	require.NoError(t, err)
	assert.False(t, admitted, "a misspelled field never fires and never complains")
}

func TestMatch_FieldDeclaredOnAnotherKind(t *testing.T) {
	// content is carried by PreFileCreate alone. Read on an event that omits
	// it, it is nil — again false rather than an error.
	m, err := CompileMatcher(`content contains "TODO"`)
	require.NoError(t, err)

	admitted, err := m.Match(event.Event{
		Kind:   "PreFileUpdate",
		Fields: map[string]any{"path": "a.md"},
	})
	require.NoError(t, err, "no error: an absent field is nil, and contains on nil is false")
	assert.False(t, admitted,
		"a rule bound to PreFileUpdate but written against content never fires, silently")
}

func TestMatch_NilFieldsMapIsSafe(t *testing.T) {
	m, err := CompileMatcher(`path == "a"`)
	require.NoError(t, err)

	admitted, err := m.Match(event.Event{Kind: "TurnEnd"})
	require.NoError(t, err)
	assert.False(t, admitted)
}

func TestMatch_UnknownFunctionFailsAtRuntime(t *testing.T) {
	m, err := CompileMatcher(`nosuchfunc(path)`)
	require.NoError(t, err, "an unknown name compiles — it is just another nil")

	admitted, err := m.Match(fileEvent("a.md"))
	require.Error(t, err)
	assert.False(t, admitted)
	assert.Contains(t, err.Error(), "cannot call nil")
}

func TestMatch_ReachesNothingBeyondTheEvent(t *testing.T) {
	// The environment is the event's fields and nothing else, so a matcher
	// cannot read the kind, the process environment, or anything ambient.
	m, err := CompileMatcher(`kind == "PreFileCreate"`)
	require.NoError(t, err)

	admitted, err := m.Match(fileEvent("a.md"))
	require.NoError(t, err)
	assert.False(t, admitted, "Kind is not in scope; only Fields are")
}

func TestMatch_Deterministic(t *testing.T) {
	m, err := CompileMatcher(`path endsWith ".md"`)
	require.NoError(t, err)

	e := fileEvent("memories/a.md")
	first, err := m.Match(e)
	require.NoError(t, err)
	for i := 0; i < 5; i++ {
		again, err := m.Match(e)
		require.NoError(t, err)
		assert.Equal(t, first, again, "the same matcher and event must agree with itself")
	}
}

func TestMatch_DoesNotMutateTheEvent(t *testing.T) {
	// A matcher is a question about an occurrence, not a step that changes it:
	// the hook must be handed the same event the matcher was evaluated against.
	e := fileEvent("memories/a.md")
	m, err := CompileMatcher(`path startsWith "memories/"`)
	require.NoError(t, err)

	_, err = m.Match(e)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"path": "memories/a.md"}, e.Fields)
	assert.Len(t, e.Fields, 1)
}
