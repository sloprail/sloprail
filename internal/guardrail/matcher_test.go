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

// preFileCreate is the kind as the file module declares it, and as the spec
// defines it: path and content, both strings, both required.
func preFileCreate() module.KindDecl {
	return module.KindDecl{Name: "PreFileCreate", Fields: []module.FieldDecl{
		{Name: "path", Type: module.TypeString},
		{Name: "content", Type: module.TypeString},
	}}
}

// TestMatch_DeclaredFieldOmittedByTheProducerIsItsZeroValue is the invariant
// behind a defect that made a rule fail open on the only file it was about.
//
// The file module omits `content` from a PreFileCreate when it is empty, so a
// genuinely empty file produced an event missing a field its kind declares. The
// matcher had been type-checked against that declaration, so `content == ""`
// compiled — then evaluated nil against a string, errored, and the engine skips
// a binding whose matcher errors. The rule for empty files let empty files
// through.
//
// A declared field the event omits is now supplied at its type's zero value, so
// the expression sees the shape it was compiled against.
func TestMatch_DeclaredFieldOmittedByTheProducerIsItsZeroValue(t *testing.T) {
	// Exactly what filemod emits for an empty file: content omitted.
	empty := event.Event{Kind: "PreFileCreate", Fields: map[string]any{"path": "empty.txt"}}

	m, err := CompileMatcherFor(`content == ""`, preFileCreate())
	require.NoError(t, err)

	admitted, err := m.Match(empty)
	require.NoError(t, err,
		"a declared field the producer omitted must not error the matcher")
	assert.True(t, admitted,
		`content == "" is the rule for an empty file and must fire on one`)
}

func TestMatch_ZeroValueMatchesTheDeclaredType(t *testing.T) {
	// One declared field per type, none of them carried by the event. Each
	// expression is one only that type supports, so a wrong-shaped fill-in
	// errors rather than quietly comparing false.
	kind := module.KindDecl{Name: "Everything", Fields: []module.FieldDecl{
		{Name: "s", Type: module.TypeString},
		{Name: "b", Type: module.TypeBool},
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
		"path": "a.txt", "content": nil,
	}}

	m, err := CompileMatcherFor(`content == ""`, preFileCreate())
	require.NoError(t, err)

	admitted, err := m.Match(nulled)
	require.NoError(t, err,
		"a declared field carried as null must not error the matcher")
	assert.True(t, admitted,
		`content == "" is the rule for an empty file and must fire on one sent as null`)
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
// oversight: see refuseForBroken's sibling in services/sloprail, which refuses
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

func TestMatch_CarriedValueBeatsTheZeroValue(t *testing.T) {
	// The fill-in must never shadow what the producer actually sent — including
	// a field explicitly carried as its zero value.
	m, err := CompileMatcherFor(`content == "x"`, preFileCreate())
	require.NoError(t, err)

	admitted, err := m.Match(event.Event{Kind: "PreFileCreate", Fields: map[string]any{
		"path": "a.txt", "content": "x",
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
