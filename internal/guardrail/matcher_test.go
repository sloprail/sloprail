package guardrail

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/module"
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
	// "produced %T, not a boolean" message in Match.
	//
	// DO NOT DELETE THE !ok BRANCH IN Match (matcher.go:50-56) ON THE STRENGTH
	// OF ITS COVERAGE. It is unreachable only because CompileMatcher passes
	// expr.AsBool(), which guarantees the return is either a bool or an error.
	// It is a guard against that changing: drop AsBool(), or compile a matcher
	// by another path, and the branch becomes the only thing standing between
	// a non-boolean result and a matcher that admits by accident. It is
	// deliberately unreachable, not merely untested — which is why no test
	// here exercises it, and why this one asserts that it does NOT fire.
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

// TestMatch_PresentButNilFieldIsNotAnError covers a different path through
// expr than an absent key: here the name resolves, to an explicit nil. A
// module that set a field it had no value for produces this, and it behaves
// like the absent case — false, with no error — even where the same
// comparison against a string operand would fail.
func TestMatch_PresentButNilFieldIsNotAnError(t *testing.T) {
	nilPath := event.Event{Kind: "PreFileCreate", Fields: map[string]any{"path": nil}}

	cases := []struct {
		src      string
		admitted bool
	}{
		{`path == "x"`, false},
		{`path startsWith "memories/"`, false},
		{`path`, false},
		{`path == nil`, true},
		{`path != nil`, false},
	}
	for _, tc := range cases {
		m, err := CompileMatcher(tc.src)
		require.NoError(t, err, "src %q", tc.src)

		admitted, err := m.Match(nilPath)
		require.NoError(t, err, "src %q: a nil-valued field does not error", tc.src)
		assert.Equal(t, tc.admitted, admitted, "src %q", tc.src)
	}

	// Note the contrast with a string-valued field: `path` alone errors when
	// path holds a string (see TestMatch_NonBooleanAtRuntimeIsAnError) but
	// returns false when it holds nil. The bool cast tolerates nil.
	m, err := CompileMatcher(`path`)
	require.NoError(t, err)
	_, err = m.Match(fileEvent("a.md"))
	assert.Error(t, err, "the same expression errors on a string")
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

// ---------------------------------------------------------------------------
// Load-time checking of a matcher against the fields its kind declares.
//
// The tests above are about what a matcher DOES once it runs. These are about
// whether it is allowed to load at all, which is the only place a misspelled
// field can still be distinguished from a rule that legitimately does not match.
// ---------------------------------------------------------------------------

// fileKind mirrors what the file module declares for a pending create: a path
// and the content that would be written.
var fileKind = module.KindDecl{
	Name: "PreFileCreate",
	Fields: []module.FieldDecl{
		{Name: "path", Type: module.TypeString},
		{Name: "content", Type: module.TypeString},
	},
}

// commandKind stands in for the kinds arriving with the command module. Kept
// here so the container field types are exercised before a module ships them —
// a list of maps is the shape the spec's own example matcher reads.
//
// invocations declares its element's fields, which is what makes the predicate
// body of `any(invocations, …)` checkable. meta does not declare keys, standing
// for the case where a module genuinely cannot enumerate them.
var commandKind = module.KindDecl{
	Name: "PreCommand",
	Fields: []module.FieldDecl{
		{Name: "raw", Type: module.TypeString},
		{
			Name: "invocations",
			Type: module.TypeList,
			Elem: &module.FieldDecl{
				Type: module.TypeMap,
				Fields: []module.FieldDecl{
					{Name: "bin", Type: module.TypeString},
					{Name: "flags", Type: module.TypeList, Elem: &module.FieldDecl{Type: module.TypeString}},
				},
			},
		},
		{Name: "meta", Type: module.TypeMap},
	},
}

// looseKind declares a list without an element shape — the honest state before
// a module says what its list holds.
var looseKind = module.KindDecl{
	Name: "PreLoose",
	Fields: []module.FieldDecl{
		{Name: "items", Type: module.TypeList},
	},
}

func TestCompileMatcherFor_AcceptsDeclaredField(t *testing.T) {
	m, err := CompileMatcherFor(`path startsWith "guarded/"`, fileKind)
	require.NoError(t, err)

	admitted, err := m.Match(event.Event{
		Kind:   "PreFileCreate",
		Fields: map[string]any{"path": "guarded/notes.md"},
	})
	require.NoError(t, err)
	assert.True(t, admitted, "a matcher over a declared field should still work")
}

// The point of the whole task: the typo and the rule are both well-formed
// expressions, and only the kind's declared fields tell them apart.
func TestCompileMatcherFor_RefusesMisspelledField(t *testing.T) {
	_, err := CompileMatcherFor(`pth startsWith "guarded/"`, fileKind)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pth")
}

// The acceptance half of the same check. A validator that refused every name
// would pass the test above and be useless.
func TestCompileMatcherFor_AcceptsEveryDeclaredField(t *testing.T) {
	for _, f := range fileKind.Fields {
		_, err := CompileMatcherFor(f.Name+` != ""`, fileKind)
		assert.NoErrorf(t, err, "field %q is declared and should be readable", f.Name)
	}
}

// A field one kind carries and another does not is the case a shared
// environment would miss. content exists on PreFileCreate alone.
func TestCompileMatcherFor_FieldOfAnotherKindIsUnknown(t *testing.T) {
	preUpdate := module.KindDecl{
		Name:   "PreFileUpdate",
		Fields: []module.FieldDecl{{Name: "path", Type: module.TypeString}},
	}

	_, err := CompileMatcherFor(`content != ""`, fileKind)
	require.NoError(t, err, "content is declared on PreFileCreate")

	_, err = CompileMatcherFor(`content != ""`, preUpdate)
	require.Error(t, err, "content is not declared on PreFileUpdate")
	assert.Contains(t, err.Error(), "content")
}

func TestCompileMatcherFor_RefusesNonBoolean(t *testing.T) {
	_, err := CompileMatcherFor(`path`, fileKind)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bool")
}

func TestCompileMatcherFor_RefusesMismatchedComparison(t *testing.T) {
	// path is declared a string, so comparing it to a number is a rule that
	// could never hold — caught by the same type check that catches the typo.
	_, err := CompileMatcherFor(`path == 3`, fileKind)
	require.Error(t, err)
}

func TestCompileMatcherFor_RefusesSyntaxError(t *testing.T) {
	_, err := CompileMatcherFor(`path startsWith`, fileKind)
	require.Error(t, err)
}

// An absent matcher means every occurrence, and must survive the type check
// rather than being refused for reading no fields.
func TestCompileMatcherFor_EmptyAdmitsEverything(t *testing.T) {
	m, err := CompileMatcherFor("", fileKind)
	require.NoError(t, err)

	admitted, err := m.Match(event.Event{Kind: "PreFileCreate"})
	require.NoError(t, err)
	assert.True(t, admitted)
}

// The spec documents this exact expression as how a command line is matched.
// A list field must not be typed so tightly that it stops compiling.
func TestCompileMatcherFor_AcceptsSpecCommandExample(t *testing.T) {
	_, err := CompileMatcherFor(`any(invocations, .bin == "npm" && "--access" in .flags)`, commandKind)
	require.NoError(t, err)
}

// The predicate body is where a command rule actually lives, so a misspelling
// inside it is the same silent never-fires as one at the top level. Catching it
// is what the element shape on invocations buys.
func TestCompileMatcherFor_RefusesMisspelledFieldInsidePredicate(t *testing.T) {
	_, err := CompileMatcherFor(`any(invocations, .nosuchfield == "x")`, commandKind)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nosuchfield")
}

// Every collection operator gets the same treatment, since a rule may be
// written with any of them.
func TestCompileMatcherFor_ChecksPredicateBodyOfEveryOperator(t *testing.T) {
	for _, op := range []string{"any", "all", "one", "none"} {
		_, err := CompileMatcherFor(op+`(invocations, .bin == "npm")`, commandKind)
		assert.NoErrorf(t, err, "%s over a declared element field should compile", op)

		_, err = CompileMatcherFor(op+`(invocations, .bni == "npm")`, commandKind)
		assert.Errorf(t, err, "%s over a misspelled element field should be refused", op)
	}
}

// Indexing reaches the element type too.
func TestCompileMatcherFor_ChecksIndexedElementFields(t *testing.T) {
	_, err := CompileMatcherFor(`invocations[0].bin == "npm"`, commandKind)
	require.NoError(t, err)

	_, err = CompileMatcherFor(`invocations[0].nope == "npm"`, commandKind)
	require.Error(t, err)
}

// A type error inside the predicate is caught for the same reason as at the
// top level: bin is declared a string.
func TestCompileMatcherFor_RefusesMismatchedComparisonInsidePredicate(t *testing.T) {
	_, err := CompileMatcherFor(`any(invocations, .bin == 3)`, commandKind)
	require.Error(t, err)
}

// A list whose element shape the module did not declare leaves its predicate
// body unchecked rather than refusing it. Checking what was never declared
// would refuse a correct matcher for a gap that is ours.
func TestCompileMatcherFor_UndeclaredElementShapeLeavesBodyUnchecked(t *testing.T) {
	_, err := CompileMatcherFor(`any(items, .anything == "x")`, looseKind)
	assert.NoError(t, err, "the module did not say what items holds")
}

// A map field's keys are not something KindDecl can express, so reading one
// must be allowed. The alternative punishes an author for our vocabulary.
func TestCompileMatcherFor_AcceptsAnyKeyOfMapField(t *testing.T) {
	_, err := CompileMatcherFor(`meta.whatever == "x"`, commandKind)
	require.NoError(t, err)
}

// ...while an undeclared name at the top level is still refused. This is what
// makes the concession above safe.
func TestCompileMatcherFor_RefusesUnknownNameAlongsideMapField(t *testing.T) {
	_, err := CompileMatcherFor(`whatever == "x"`, commandKind)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "whatever")
}

func TestCompileMatcherFor_RefusesUnknownNameInCompoundExpression(t *testing.T) {
	// The and-ed clause is the one at fault. A check reading only the first
	// name would pass this.
	_, err := CompileMatcherFor(`path startsWith "a" && pth endsWith "b"`, fileKind)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pth")
}

// CompileMatcher keeps working for callers with no kind in hand, and keeps
// checking the one thing it still can.
func TestCompileMatcher_WithoutKindChecksBooleanOnly(t *testing.T) {
	_, err := CompileMatcher(`pth startsWith "x"`)
	assert.NoError(t, err, "no kind means no field to check against")

	_, err = CompileMatcher(`"not a bool"`)
	assert.Error(t, err, "the boolean requirement holds with or without a kind")
}

// A field the kind declares but this occurrence does not carry reads as its
// zero value, so the matcher declines rather than failing.
//
// Recorded because it is the reason the load check matters rather than a gap in
// it. At runtime a missing field and a non-matching one are indistinguishable —
// both simply do not fire — which is exactly the silence CompileMatcherFor
// exists to rule out beforehand. Once loading guarantees every name is
// declared, the only way to reach here is an occurrence that genuinely omitted
// an optional field, and declining is the right answer to that.
func TestMatch_MissingFieldDeclinesRatherThanFailing(t *testing.T) {
	m, err := CompileMatcherFor(`path startsWith "x"`, fileKind)
	require.NoError(t, err)

	admitted, err := m.Match(event.Event{Kind: "PreFileCreate", Fields: map[string]any{}})
	require.NoError(t, err)
	assert.False(t, admitted)
}

func TestMatch_DeclinesWhenTheFieldDoesNotSatisfy(t *testing.T) {
	m, err := CompileMatcherFor(`path startsWith "guarded/"`, fileKind)
	require.NoError(t, err)

	admitted, err := m.Match(event.Event{
		Kind:   "PreFileCreate",
		Fields: map[string]any{"path": "elsewhere/notes.md"},
	})
	require.NoError(t, err)
	assert.False(t, admitted)
}
