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
		{Kind: "Stop"},
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

// preFileCreate is the kind as the file module declares it, and as the spec
// defines it: path and newContent, both strings, both required.
func preFileCreate() module.KindDecl {
	return module.KindDecl{Name: "PreFileCreate", Fields: []module.FieldDecl{
		{Name: "path", Type: module.TypeString},
		{Name: "newContent", Type: module.TypeString},
	}}
}

// TestMatch_DeclaredFieldOmittedByTheProducerIsItsZeroValue is the invariant
// behind a defect that made a rule fail open on the only file it was about.
//
// The file module omits `newContent` from a PreFileCreate when it is empty, so a
// genuinely empty file produced an event missing a field its kind declares. The
// matcher had been type-checked against that declaration, so `newContent == ""`
// compiled — then evaluated nil against a string, errored, and the engine skips
// a binding whose matcher errors. The rule for empty files let empty files
// through.
//
// A declared field the event omits is now supplied at its type's zero value, so
// the expression sees the shape it was compiled against.
func TestMatch_DeclaredFieldOmittedByTheProducerIsItsZeroValue(t *testing.T) {
	// Exactly what filemod emits for an empty file: newContent omitted.
	empty := event.Event{Kind: "PreFileCreate", Fields: map[string]any{"path": "empty.txt"}}

	m, err := CompileMatcherFor(`newContent == ""`, preFileCreate())
	require.NoError(t, err)

	admitted, err := m.Match(empty)
	require.NoError(t, err,
		"a declared field the producer omitted must not error the matcher")
	assert.True(t, admitted,
		`newContent == "" is the rule for an empty file and must fire on one`)
}

func TestMatch_ZeroValueMatchesTheDeclaredType(t *testing.T) {
	// One declared field per type, none of them carried by the event. Each
	// expression is one only that type supports, so a wrong-shaped fill-in
	// errors rather than quietly comparing false.
	kind := module.KindDecl{Name: "Everything", Fields: []module.FieldDecl{
		{Name: "s", Type: module.TypeString},
		{Name: "b", Type: module.TypeBool},
		{Name: "n", Type: module.TypeInt},
		{Name: "l", Type: module.TypeList},
		{Name: "mp", Type: module.TypeMap},
	}}
	bare := event.Event{Kind: "Everything", Fields: map[string]any{}}

	for _, tc := range []struct {
		src      string
		admitted bool
	}{
		{`s == ""`, true},
		{`s startsWith "x"`, false},
		{`b == false`, true},
		{`b`, false},
		// int. Absent from this table while TypeInt was declared, mapped by
		// fieldType and used by filemod's markers[].line — so the table read as
		// one row per type and was one short. zero() had no int case either, so
		// an omitted `line` came back nil and `.line > 10` errored `<nil> > int`:
		// a CORRECT rule refusing every action and blaming the author's
		// guardrail, which is the identical defect the `newContent` fill-in closed.
		{`n == 0`, true},
		{`n > 0`, false},
		{`len(l) == 0`, true},
		{`len(mp) == 0`, true},
	} {
		t.Run(tc.src, func(t *testing.T) {
			m, err := CompileMatcherFor(tc.src, kind)
			require.NoError(t, err)

			admitted, err := m.Match(bare)
			require.NoError(t, err, "the zero value must have the declared shape")
			assert.Equal(t, tc.admitted, admitted)
		})
	}
}

// TestMatch_ZeroValueOfAListIsEmptyNotNil pins the distinction len() cannot see.
//
// `len(l) == 0` holds for both `[]any{}` and `[]any(nil)`, so the table above
// stays green if the fill-in is changed to hand out nils — which would put a nil
// where the expression was type-checked against a list, exactly the shape this
// whole mechanism exists to prevent. `l == nil` is the only expression that
// tells them apart.
func TestMatch_ZeroValueOfAListIsEmptyNotNil(t *testing.T) {
	kind := module.KindDecl{Name: "K", Fields: []module.FieldDecl{
		{Name: "l", Type: module.TypeList},
		{Name: "mp", Type: module.TypeMap},
	}}
	bare := event.Event{Kind: "K", Fields: map[string]any{}}

	for _, tc := range []struct {
		src      string
		admitted bool
		why      string
	}{
		{`l == nil`, false, "an omitted list reads as empty, not as absent"},
		{`l != nil`, true, "the same claim from the other side"},
		{`mp == nil`, false, "an omitted map reads as empty, not as absent"},
		{`mp != nil`, true, "the same claim from the other side"},
	} {
		t.Run(tc.src, func(t *testing.T) {
			m, err := CompileMatcherFor(tc.src, kind)
			require.NoError(t, err)

			admitted, err := m.Match(bare)
			require.NoError(t, err)
			assert.Equal(t, tc.admitted, admitted, tc.why)
		})
	}
}

// ---------------------------------------------------------------------------
// The fill-in reaches as far as the type check does.
//
// matcherEnv/fieldType recurse: a map's enumerated keys and a list element's
// declared fields are type-checked to the bottom. zeroOf did not, and it only
// acted when a key was ABSENT. Both gaps land in the same place — a nil where
// the expression was checked against a string — and the engine's response to a
// matcher error is to skip the binding and let the action through.
//
// Each case below is a rule that would fail open on exactly the occurrence it
// was written to catch.
// ---------------------------------------------------------------------------

// enumeratedKind declares a map with named keys and a list whose element shape
// is known — the two places fieldType descends into and zeroOf did not.
var enumeratedKind = module.KindDecl{
	Name: "PreThing",
	Fields: []module.FieldDecl{
		{Name: "path", Type: module.TypeString},
		{
			Name: "meta",
			Type: module.TypeMap,
			Fields: []module.FieldDecl{
				{Name: "user", Type: module.TypeString},
				{Name: "admin", Type: module.TypeBool},
			},
		},
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
	},
}

// An explicit JSON null is the likeliest real trigger: it is the ordinary
// unmarshal shape of a producer that sent the key with no value. The key is
// PRESENT, so the absence check skipped it and the expression met a nil.
func TestMatch_ExplicitNullIsTheZeroValueNotAnError(t *testing.T) {
	nulled := event.Event{Kind: "PreFileCreate", Fields: map[string]any{
		"path": "a.txt", "newContent": nil,
	}}

	m, err := CompileMatcherFor(`newContent == ""`, preFileCreate())
	require.NoError(t, err)

	admitted, err := m.Match(nulled)
	require.NoError(t, err,
		"a declared field carried as null must not error the matcher")
	assert.True(t, admitted,
		`newContent == "" is the rule for an empty file and must fire on one sent as null`)
}

// An enumerated map the producer omitted entirely. zeroOf returned a flat
// map[string]any{}, so `meta.user` was a nil where matcherEnv had built a closed
// type over `user` and promised a string.
func TestMatch_OmittedEnumeratedMapFillsItsDeclaredKeys(t *testing.T) {
	bare := event.Event{Kind: "PreThing", Fields: map[string]any{"path": "a"}}

	m, err := CompileMatcherFor(`meta.user == ""`, enumeratedKind)
	require.NoError(t, err)

	admitted, err := m.Match(bare)
	require.NoError(t, err, "an omitted map's declared keys must carry their own zero values")
	assert.True(t, admitted)

	// The bool key too, so this is the type descending rather than one string
	// being special-cased.
	mb, err := CompileMatcherFor(`meta.admin == false`, enumeratedKind)
	require.NoError(t, err)
	admitted, err = mb.Match(bare)
	require.NoError(t, err)
	assert.True(t, admitted)
}

// The map is present and the declared key is not. The fill-in has to reach
// inside a value the producer DID send, which the top-level-only version could
// not do at all.
func TestMatch_PresentMapMissingADeclaredKey(t *testing.T) {
	partial := event.Event{Kind: "PreThing", Fields: map[string]any{
		"path": "a", "meta": map[string]any{},
	}}

	m, err := CompileMatcherFor(`meta.user == ""`, enumeratedKind)
	require.NoError(t, err)

	admitted, err := m.Match(partial)
	require.NoError(t, err, "a declared key missing from a carried map is its zero value")
	assert.True(t, admitted)
}

// A list element missing a field its Elem declares. This is the `commandmod`
// rule shape the spec documents, so it is the one most likely to be written.
func TestMatch_ListElementMissingADeclaredField(t *testing.T) {
	partial := event.Event{Kind: "PreThing", Fields: map[string]any{
		"path":        "a",
		"invocations": []any{map[string]any{"flags": []any{}}},
	}}

	m, err := CompileMatcherFor(`any(invocations, .bin == "npm")`, enumeratedKind)
	require.NoError(t, err)

	admitted, err := m.Match(partial)
	require.NoError(t, err, "an element missing a declared field must not error the matcher")
	assert.False(t, admitted, `.bin is "" there, which is not "npm"`)

	// And the rule that SHOULD fire on it still does, so the fill-in has not
	// flattened everything into a non-match.
	empty, err := CompileMatcherFor(`any(invocations, .bin == "")`, enumeratedKind)
	require.NoError(t, err)
	admitted, err = empty.Match(partial)
	require.NoError(t, err)
	assert.True(t, admitted)
}

// The same claim for an INT inside a list element, which is not a synthetic
// shape: it is exactly what filemod declares for markers[].line, and
// `any(markers, .line > N)` is the rule an author writes about it.
//
// It is separated from the test above rather than folded into enumeratedKind
// because the two halves fail differently and both had to be seen. An omitted
// int came back nil and errored `<nil> > int` — the whole rule refusing, not
// merely declining — while an omitted string came back "" and merely declined.
// A table keyed on "does the matcher still answer" would have shown only the
// second.
//
// filemod populates `line` on every marker it scans, so nothing in the shipped
// build sends this today. That is why the gap survived, and it is not a reason
// to leave it: the fill-in exists precisely so the engine holds the DECLARATION
// rather than depending on each producer to be complete, and markers[].line is
// one module away from arriving over JSON as a float64.
func TestMatch_ListElementMissingADeclaredInt(t *testing.T) {
	kind := module.KindDecl{Name: "K", Fields: []module.FieldDecl{
		{Name: "markers", Type: module.TypeList, Elem: &module.FieldDecl{
			Type: module.TypeMap,
			Fields: []module.FieldDecl{
				{Name: "kind", Type: module.TypeString},
				{Name: "line", Type: module.TypeInt},
			},
		}},
	}}

	m, err := CompileMatcherFor(`any(markers, .line > 10)`, kind)
	require.NoError(t, err)

	admitted, err := m.Match(event.Event{Kind: "K", Fields: map[string]any{
		"markers": []any{map[string]any{"kind": "docs"}},
	}})
	require.NoError(t, err, "an omitted declared int inside an element must fill in, not error the rule")
	assert.False(t, admitted, ".line is 0 there, which is not > 10")

	// The zero is a real 0 rather than a nil that merely compares false, which
	// is the distinction `> 10` alone cannot make.
	zeroRule, err := CompileMatcherFor(`any(markers, .line == 0)`, kind)
	require.NoError(t, err)
	admitted, err = zeroRule.Match(event.Event{Kind: "K", Fields: map[string]any{
		"markers": []any{map[string]any{"kind": "docs"}},
	}})
	require.NoError(t, err)
	assert.True(t, admitted)

	// And the wrong type inside the element errors rather than being compared.
	_, err = m.Match(event.Event{Kind: "K", Fields: map[string]any{
		"markers": []any{map[string]any{"kind": "docs", "line": float64(42)}},
	}})
	require.Error(t, err, "a declared int carried as float64 must not be silently compared")
}

// A carried value inside a nested structure still wins, at every depth. The
// fill-in supplies what is missing and never overwrites what arrived.
func TestMatch_NestedCarriedValuesBeatTheZeroValue(t *testing.T) {
	full := event.Event{Kind: "PreThing", Fields: map[string]any{
		"path": "a",
		"meta": map[string]any{"user": "nikita", "admin": true},
		"invocations": []any{
			map[string]any{"bin": "npm", "flags": []any{"--access"}},
		},
	}}

	for _, src := range []string{
		`meta.user == "nikita"`,
		`meta.admin`,
		`any(invocations, .bin == "npm" && "--access" in .flags)`,
	} {
		m, err := CompileMatcherFor(src, enumeratedKind)
		require.NoError(t, err, "src %q", src)

		admitted, err := m.Match(full)
		require.NoError(t, err, "src %q", src)
		assert.True(t, admitted, "src %q: a carried value is what the expression reads", src)
	}
}

// A matcher CAN still error at run time, and the engine has to have an answer
// for it. The recursion above closes the gaps the declaration knows about; it
// cannot close the ones it does not.
//
// This is the case that remains: a list whose element shape the module did not
// declare leaves its predicate body unchecked, so an expression reaching inside
// an element compiles against nothing and meets whatever actually arrives. The
// command module declares `invocations` exactly this way, and
// `len(.flags.access) > 0` — "was --access given a value" — is an ordinary rule
// to write against it.
//
// Recorded here so the engine's response to it is a decision rather than an
// oversight: see refuseForBroken's sibling in services/sr-session, which refuses
// rather than skipping the binding.
func TestMatch_UndeclaredElementShapeCanStillErrorAtRuntime(t *testing.T) {
	m, err := CompileMatcherFor(`any(items, len(.flags.access) > 0)`, looseKind)
	require.NoError(t, err, "the module did not say what items holds, so nothing checks this")

	admitted, err := m.Match(event.Event{Kind: "PreLoose", Fields: map[string]any{
		"items": []any{map[string]any{"bin": "npm", "flags": map[string]any{}}},
	}})
	require.Error(t, err, "an unchecked predicate body still meets a nil at run time")
	assert.False(t, admitted, "an erroring matcher admits nothing")
}

// TestMatch_UndeclaredElementShapeAlsoFailsSILENTLY is the other half of the
// gap above, and the worse half.
//
// An undeclared element shape does not only permit matcher ERRORS — which are
// caught, because the engine refuses on them. It also permits the silent
// never-fires, and which one an author gets depends on nothing but the operator
// their typo lands under:
//
//	any(items, len(.flags.access) > 0)   // accessor: errors → the action refuses
//	any(items, .bni == "npm")            // bare ==: false, err=nil → nothing
//
// The second is precisely what CompileMatcherFor was written to prevent, present
// one level down. `.bni` is a typo for `.bin`; at the TOP level the identical
// mistake (`paht` for `path`) is refused at load with a diagnostic naming the
// field — see TestMatch_UndeclaredFieldIsNotSuppliedAZeroValue. Inside a
// predicate over an element-less list, nothing checks it, nothing errors, and
// the rule reads as satisfied on every command.
//
// This test asserts the CURRENT behaviour, not the desired one. It is here so
// the gap is visible and measured rather than merely known, and it is expected
// to be inverted — the compile becoming an error — when the module declares what
// its list elements hold. commandmod declares `invocations` with a nil Elem and
// its element's fields are already known (Bin, Argv, Flags), so declaring them
// is a statement of fact; that change belongs to internal/commandmod.
//
// Nothing in the engine can close this from here: with no declared element shape
// there is no vocabulary to check `.bni` against, and refusing every predicate
// over an element-less list would refuse rules that are correct.
func TestMatch_UndeclaredElementShapeAlsoFailsSILENTLY(t *testing.T) {
	m, err := CompileMatcherFor(`any(items, .bni == "npm")`, looseKind)
	require.NoError(t, err,
		"the module did not say what items holds, so a typo inside the predicate is unchecked")

	admitted, err := m.Match(event.Event{Kind: "PreLoose", Fields: map[string]any{
		"items": []any{map[string]any{"bin": "npm"}},
	}})
	require.NoError(t, err, "a bare == against a nil does not error — it compares unequal")
	assert.False(t, admitted,
		"the rule silently does not fire on the very invocation it was written to catch")

	// The same rule spelled correctly DOES fire, so the false above is the typo
	// and not the event failing to match on its own terms.
	right, err := CompileMatcherFor(`any(items, .bin == "npm")`, looseKind)
	require.NoError(t, err)
	admitted, err = right.Match(event.Event{Kind: "PreLoose", Fields: map[string]any{
		"items": []any{map[string]any{"bin": "npm"}},
	}})
	require.NoError(t, err)
	assert.True(t, admitted, "the correctly spelled rule fires on the same event")
}

// A DECLARED element shape catches the same typo at load, which is what makes
// the gap above a missing declaration rather than a limit of the engine.
//
// enumeratedKind declares its list element's fields; looseKind does not. Same
// expression shape, same typo, opposite outcome — so the fix for the silence is
// entirely on the module's side.
func TestMatch_DeclaredElementShapeCatchesTheSameTypoAtLoad(t *testing.T) {
	_, err := CompileMatcherFor(`any(invocations, .bni == "npm")`, enumeratedKind)
	require.Error(t, err, "a declared element shape is what makes the typo checkable")
	assert.Contains(t, err.Error(), "bni", "the diagnostic must name the misspelling")
}

// An unenumerated map keeps its open type, so the fill-in must not invent keys
// for it. matcherEnv leaves such a field as types.Any precisely because the
// module never claimed to know its keys, and manufacturing some here would
// contradict that.
func TestMatch_UnenumeratedMapIsNotGivenInventedKeys(t *testing.T) {
	m, err := CompileMatcherFor(`meta.whatever == nil`, commandKind)
	require.NoError(t, err)

	admitted, err := m.Match(event.Event{Kind: "PreCommand", Fields: map[string]any{}})
	require.NoError(t, err)
	assert.True(t, admitted, "an unenumerated map is empty, and any key of it is nil")
}

// ---------------------------------------------------------------------------
// A value of the WRONG type is not a missing value.
//
// The fill-in that closed the omitted-field fail-open reopened it in a new
// shape. It keyed on the carried VALUE rather than on presence: anything not
// already the declared type was replaced by the zero value. So for the real rule
// `path startsWith "guarded/"`, a producer carrying `path` as a number yielded
// "" and the matcher returned admitted=false with no error — a clean, silent
// non-match, and the write proceeded.
//
// That is strictly worse than the presence-keyed version it replaced. That one
// left a nil, which errored, and the engine's refusal path caught it. This one
// answers a question it cannot answer.
//
// Every case below is a rule that would silently not fire on exactly the
// occurrence it was written to catch.
// ---------------------------------------------------------------------------

func TestMatch_WrongTypedCarriedValueErrors(t *testing.T) {
	// The real rule, verbatim: this is not a synthetic expression.
	m, err := CompileMatcherFor(`path startsWith "guarded/"`, preFileCreate())
	require.NoError(t, err)

	for _, tc := range []struct {
		what    string
		carried any
	}{
		{"a number", 42},
		{"a bool", true},
		{"a list", []any{"guarded/x"}},
		{"a map", map[string]any{"v": "guarded/x"}},
	} {
		t.Run(tc.what, func(t *testing.T) {
			admitted, err := m.Match(event.Event{Kind: "PreFileCreate", Fields: map[string]any{
				"path": tc.carried,
			}})
			require.Error(t, err,
				"a declared string carried as %s has no truth value against startsWith — "+
					"answering false is the fail-open this whole mechanism exists to close", tc.what)
			assert.False(t, admitted, "an erroring matcher admits nothing")
			// The producer at fault has to be findable from the message, and the
			// reader is usually a rule author who did not write the producer.
			assert.Contains(t, err.Error(), "path", "the message must name the field")
			assert.Contains(t, err.Error(), "string", "the message must say what was declared")
		})
	}
}

// The same claim for every declared type, so this is the rule rather than one
// special case for strings.
func TestMatch_WrongTypeErrorsForEveryDeclaredType(t *testing.T) {
	kind := module.KindDecl{Name: "Everything", Fields: []module.FieldDecl{
		{Name: "s", Type: module.TypeString},
		{Name: "b", Type: module.TypeBool},
		{Name: "n", Type: module.TypeInt},
		{Name: "l", Type: module.TypeList},
		{Name: "mp", Type: module.TypeMap},
	}}

	for _, tc := range []struct {
		src     string
		field   string
		carried any
	}{
		{`s == ""`, "s", 42},
		{`b == false`, "b", "yes"},
		// float64 rather than a string, because float64 is the shape this
		// actually arrives in: it is what encoding/json gives every number, so
		// any producer reaching the engine through JSON carries a declared int
		// this way. While fill had no int case it fell to the default branch and
		// was returned UNCHANGED, so `n > 10` compared a float64 the checker had
		// been told was an int and answered cleanly — admitted=false, err=nil,
		// indistinguishable from a rule that legitimately did not match. That is
		// the fail-open half, and it is the one a string in this slot would not
		// have caught.
		{`n > 10`, "n", float64(42)},
		{`len(l) == 0`, "l", "not-a-list"},
		{`len(mp) == 0`, "mp", []any{"not-a-map"}},
	} {
		t.Run(tc.field, func(t *testing.T) {
			m, err := CompileMatcherFor(tc.src, kind)
			require.NoError(t, err)

			admitted, err := m.Match(event.Event{Kind: "Everything", Fields: map[string]any{
				tc.field: tc.carried,
			}})
			require.Error(t, err, "a wrong-typed %s must not be silently zeroed", tc.field)
			assert.False(t, admitted)
		})
	}
}

// The check descends, because so does the fill-in. A wrong-typed value nested
// inside an enumerated map or a typed list element is the same fact one level
// down, and stopping at the top would leave the fail-open exactly where the
// recursion was added to close it.
func TestMatch_WrongTypeErrorsInsideNestedStructures(t *testing.T) {
	t.Run("enumerated map key", func(t *testing.T) {
		m, err := CompileMatcherFor(`meta.user == "nikita"`, enumeratedKind)
		require.NoError(t, err)

		admitted, err := m.Match(event.Event{Kind: "PreThing", Fields: map[string]any{
			"path": "a", "meta": map[string]any{"user": 7},
		}})
		require.Error(t, err, "a declared key carried at the wrong type must not be zeroed")
		assert.False(t, admitted)
		assert.Contains(t, err.Error(), "user")
	})

	t.Run("typed list element field", func(t *testing.T) {
		m, err := CompileMatcherFor(`any(invocations, .bin == "npm")`, enumeratedKind)
		require.NoError(t, err)

		admitted, err := m.Match(event.Event{Kind: "PreThing", Fields: map[string]any{
			"path":        "a",
			"invocations": []any{map[string]any{"bin": 3, "flags": []any{}}},
		}})
		require.Error(t, err, "a declared element field at the wrong type must not be zeroed")
		assert.False(t, admitted)
		assert.Contains(t, err.Error(), "bin")
	})
}

// The other half, and the one that keeps the fix from being a blanket refusal:
// ABSENCE still fills in, in both its spellings, at every depth. Erroring here
// instead would undo the defect this fill-in was originally written to close.
func TestMatch_AbsenceStillFillsInAfterTheWrongTypeCheck(t *testing.T) {
	for _, tc := range []struct {
		what   string
		fields map[string]any
	}{
		{"omitted entirely", map[string]any{"path": "a.txt"}},
		{"carried as an explicit null", map[string]any{"path": "a.txt", "newContent": nil}},
	} {
		t.Run(tc.what, func(t *testing.T) {
			m, err := CompileMatcherFor(`newContent == ""`, preFileCreate())
			require.NoError(t, err)

			admitted, err := m.Match(event.Event{Kind: "PreFileCreate", Fields: tc.fields})
			require.NoError(t, err, "absence is not a type disagreement")
			assert.True(t, admitted, `newContent == "" is the rule for an empty file`)
		})
	}

	// And an omitted ENUMERATED map still gets its declared keys, which the
	// absence short-circuit has to recurse to supply.
	m, err := CompileMatcherFor(`meta.user == "" && meta.admin == false`, enumeratedKind)
	require.NoError(t, err)
	admitted, err := m.Match(event.Event{Kind: "PreThing", Fields: map[string]any{"path": "a"}})
	require.NoError(t, err, "an omitted enumerated map must still fill its declared keys")
	assert.True(t, admitted)
}

func TestMatch_CarriedValueBeatsTheZeroValue(t *testing.T) {
	// The fill-in must never shadow what the producer actually sent — including
	// a field explicitly carried as its zero value.
	m, err := CompileMatcherFor(`newContent == "x"`, preFileCreate())
	require.NoError(t, err)

	admitted, err := m.Match(event.Event{Kind: "PreFileCreate", Fields: map[string]any{
		"path": "a.txt", "newContent": "x",
	}})
	require.NoError(t, err)
	assert.True(t, admitted, "a carried value is what the expression reads")
}

func TestMatch_UndeclaredFieldIsNotSuppliedAZeroValue(t *testing.T) {
	// The fill-in covers DECLARED fields only. A typo is caught at load by
	// CompileMatcherFor, and inventing a value for an unknown name here would
	// undo that check — so the compile must still refuse.
	_, err := CompileMatcherFor(`paht == ""`, preFileCreate())
	require.Error(t, err, "a misspelled field is refused at compile, not filled in")
	assert.Contains(t, err.Error(), "paht")
}

func TestMatch_UnknownFieldIsNilNotAnError(t *testing.T) {
	// This is CompileMatcher — the kindless form, used where no kind is in
	// hand. With no declaration to check against, an absent name resolves to
	// nil and the matcher quietly evaluates false. That is why CompileMatcherFor
	// exists and why every load-time and enforcement-time caller uses it;
	// TestMatch_UndeclaredFieldIsNotSuppliedAZeroValue is the contrast.
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
	// newContent is carried by the file create/update kinds. Read on an event
	// that omits it, it is nil — again false rather than an error.
	m, err := CompileMatcher(`newContent contains "TODO"`)
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

	admitted, err := m.Match(event.Event{Kind: "Stop"})
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
// and the newContent that would be written.
var fileKind = module.KindDecl{
	Name: "PreFileCreate",
	Fields: []module.FieldDecl{
		{Name: "path", Type: module.TypeString},
		{Name: "newContent", Type: module.TypeString},
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
// environment would miss. This synthetic PreFileUpdate carries only `path`, so
// `newContent` (declared on the create fixture) is unknown against it — the real
// PreFileUpdate declares newContent too, but the property under test is that a
// field of one kind is refused on a kind that does not declare it.
func TestCompileMatcherFor_FieldOfAnotherKindIsUnknown(t *testing.T) {
	preUpdate := module.KindDecl{
		Name:   "PreFileUpdate",
		Fields: []module.FieldDecl{{Name: "path", Type: module.TypeString}},
	}

	_, err := CompileMatcherFor(`newContent != ""`, fileKind)
	require.NoError(t, err, "newContent is declared on the create fixture")

	_, err = CompileMatcherFor(`newContent != ""`, preUpdate)
	require.Error(t, err, "newContent is not declared on this path-only kind")
	assert.Contains(t, err.Error(), "newContent")
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
