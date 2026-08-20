package guardrail

import (
	"testing"

	"github.com/expr-lang/expr/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/module"
)

// ---------------------------------------------------------------------------
// The scopes are the per-nature environments a `match` is checked against.
//
// matcher.go already decides how an expression is evaluated; these tests are
// about WHAT each nature's expression may read. Each scope must compile the
// representative expression the spec's own examples carry, evaluate it against a
// faithful event, and refuse an expression that reads a variable outside the
// scope or does not yield a boolean — the spec's boolean-or-refuse contract, per
// scope rather than shared.
// ---------------------------------------------------------------------------

// fileScopeEvent is the runtime shape a file-guard's `match` reads — the spec's
// FileMatchScope, which the (unwired) file-guard dispatch will assemble by
// scanning the settled file. NOT a file module EVENT: those carry
// oldMarkers/newMarkers to describe a change, whereas a file-guard reasons about
// the file's own state and sees a single `markers` list. Built inline so a test
// says what it matches against: path plus a markers list whose elements are the
// wire form of a scanned sr: marker.
func fileScopeEvent(path string, markers ...map[string]any) event.Event {
	ms := make([]any, 0, len(markers))
	for _, m := range markers {
		ms = append(ms, m)
	}
	return event.Event{Kind: "PreFileCreate", Fields: map[string]any{
		"path":    path,
		"markers": ms,
	}}
}

func marker(kind, fqn string, line int) map[string]any {
	return map[string]any{"kind": kind, "fqn": fqn, "line": line}
}

// ---------------------------------------------------------------------------
// FileMatchScope
// ---------------------------------------------------------------------------

// The spec's own File example, verbatim: a path prefix and a marker predicate
// composed with `and`. It exercises every variable the file scope adds beyond a
// bare event — path AND markers — in one expression.
func TestCompileFileMatch_SpecExampleCompilesAndEvaluates(t *testing.T) {
	m, err := CompileFileMatch(`path startsWith "memories/" and any(markers, .kind == "asked")`)
	require.NoError(t, err)

	admitted, err := m.Match(fileScopeEvent("memories/notes.md", marker("asked", "x", 1)))
	require.NoError(t, err)
	assert.True(t, admitted, "a memories/ path carrying an asked marker matches")

	// The path holds but the marker does not: the `and` must decline.
	admitted, err = m.Match(fileScopeEvent("memories/notes.md", marker("docs", "x", 1)))
	require.NoError(t, err)
	assert.False(t, admitted, "no asked marker, so the conjunction is false")

	// The marker holds but the path does not.
	admitted, err = m.Match(fileScopeEvent("elsewhere/notes.md", marker("asked", "x", 1)))
	require.NoError(t, err)
	assert.False(t, admitted, "not under memories/, so the conjunction is false")
}

// markers is typed into its element's fields, so a predicate reading a marker's
// declared field checks, including the int line — `.line > N` is a real rule an
// author writes about a marker's position.
func TestCompileFileMatch_MarkerPredicateReadsDeclaredFields(t *testing.T) {
	m, err := CompileFileMatch(`any(markers, .kind == "moved-from" and .line > 10)`)
	require.NoError(t, err)

	admitted, err := m.Match(fileScopeEvent("a.go", marker("moved-from", "pkg.Fn", 42)))
	require.NoError(t, err)
	assert.True(t, admitted)

	admitted, err = m.Match(fileScopeEvent("a.go", marker("moved-from", "pkg.Fn", 3)))
	require.NoError(t, err)
	assert.False(t, admitted, ".line is 3 there, which is not > 10")
}

// A typo INSIDE the marker predicate is the silent-never-fires one level down,
// and the element shape on markers is what makes it refusable at load — the same
// property filemod's own `markers` declaration buys, asserted here against the
// scope builder rather than the module.
func TestCompileFileMatch_RefusesMisspelledMarkerField(t *testing.T) {
	_, err := CompileFileMatch(`any(markers, .knid == "asked")`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "knid")
}

// context[<name>].active is the spec's context read, and its negation the ask
// that added `not` to the grammar — matching a context's own absence directly.
// context is open (types.Any) because the keys are project-defined names, so
// these check the run-time evaluation rather than a load-time type.
func TestCompileFileMatch_ContextRead(t *testing.T) {
	active, err := CompileFileMatch(`context["research-run"].active`)
	require.NoError(t, err)

	admitted, err := active.Match(event.Event{Kind: "PreFileCreate", Fields: map[string]any{
		"path":    "a.md",
		"context": map[string]any{"research-run": map[string]any{"active": true}},
	}})
	require.NoError(t, err)
	assert.True(t, admitted, "the named context is active")

	admitted, err = active.Match(event.Event{Kind: "PreFileCreate", Fields: map[string]any{
		"path":    "a.md",
		"context": map[string]any{"research-run": map[string]any{"active": false}},
	}})
	require.NoError(t, err)
	assert.False(t, admitted, "the named context is inactive")
}

func TestCompileFileMatch_NegatedContextRead(t *testing.T) {
	m, err := CompileFileMatch(`not context["x"].active`)
	require.NoError(t, err)

	admitted, err := m.Match(event.Event{Kind: "PreFileCreate", Fields: map[string]any{
		"path":    "a.md",
		"context": map[string]any{"x": map[string]any{"active": false}},
	}})
	require.NoError(t, err)
	assert.True(t, admitted, "the context is not active, so `not active` holds")
}

// The whole reason the scopes are split: a file-guard's `match` reasons about a
// file's own facts, not an event's fields, so `event` is not in its scope and an
// expression reaching for it is refused at load.
func TestCompileFileMatch_RefusesOutOfScopeVariable(t *testing.T) {
	_, err := CompileFileMatch(`event.path == "x"`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "event")
}

// The boolean-or-refuse contract, in the file scope: an expression that yields a
// value expr can see is not a boolean is refused at load, not left to fire
// never.
//
// The inputs carry a quote, so they route to the expression half of the union
// rather than the glob half — a bare `path`, which the old version used here, is
// now a glob (a file literally named `path`), so it would compile as a path
// match rather than be refused. A string-valued expression is the same "not a
// boolean" fact with a shape the discriminator cannot read as a path.
func TestCompileFileMatch_RefusesNonBoolean(t *testing.T) {
	for _, src := range []string{`"a string"`, `"memories/" + path`} {
		_, err := CompileFileMatch(src)
		require.Errorf(t, err, "%q is not a boolean and must be refused", src)
		assert.Contains(t, err.Error(), "bool")
	}
}

// An empty match is "every file", the same no-narrowing an empty matcher is
// everywhere else — neither glob nor expression.
func TestCompileFileMatch_EmptyAdmitsEverything(t *testing.T) {
	m, err := CompileFileMatch("")
	require.NoError(t, err)

	admitted, err := m.Match(fileScopeEvent("anything.md"))
	require.NoError(t, err)
	assert.True(t, admitted)
}

// ---------------------------------------------------------------------------
// Glob shorthand
// ---------------------------------------------------------------------------

// The union's common half: a bare glob is compiled to a path match without the
// author writing `path startsWith`. `**` reaches any depth, which is the case
// the shorthand exists for.
func TestCompileFileMatch_GlobShorthandMatchesPath(t *testing.T) {
	m, err := CompileFileMatch(`memories/**/*.md`)
	require.NoError(t, err)

	for _, tc := range []struct {
		path string
		want bool
	}{
		{"memories/decisions/20260101_x/notes.md", true},
		{"memories/x.md", true}, // ** matches zero segments too
		{"memories/a/b/c/deep.md", true},
		{"memories/notes.txt", false},  // wrong extension
		{"other/memories/x.md", false}, // anchored: not a substring match
		{"memoriesX/x.md", false},      // the literal is `memories/`, boundary matters
	} {
		admitted, err := m.Match(event.Event{Kind: "PreFileCreate", Fields: map[string]any{"path": tc.path}})
		require.NoError(t, err, "path %q", tc.path)
		assert.Equal(t, tc.want, admitted, "glob memories/**/*.md vs %q", tc.path)
	}
}

// A single `*` is one segment: it does NOT cross a separator. This is the
// distinction between `*` and `**`, and the reason a glob needs its own
// translation rather than a substring test.
func TestCompileFileMatch_GlobSingleStarIsOneSegment(t *testing.T) {
	m, err := CompileFileMatch(`memories/*.md`)
	require.NoError(t, err)

	for _, tc := range []struct {
		path string
		want bool
	}{
		{"memories/notes.md", true},
		{"memories/deep/notes.md", false}, // a single * does not span the /
	} {
		admitted, err := m.Match(event.Event{Kind: "PreFileCreate", Fields: map[string]any{"path": tc.path}})
		require.NoError(t, err, "path %q", tc.path)
		assert.Equal(t, tc.want, admitted, "glob memories/*.md vs %q", tc.path)
	}
}

// `?` is exactly one non-separator character.
func TestCompileFileMatch_GlobQuestionMark(t *testing.T) {
	m, err := CompileFileMatch(`file-?.md`)
	require.NoError(t, err)

	for _, tc := range []struct {
		path string
		want bool
	}{
		{"file-1.md", true},
		{"file-a.md", true},
		{"file-.md", false},   // ? requires a character
		{"file-ab.md", false}, // exactly one
	} {
		admitted, err := m.Match(event.Event{Kind: "PreFileCreate", Fields: map[string]any{"path": tc.path}})
		require.NoError(t, err, "path %q", tc.path)
		assert.Equal(t, tc.want, admitted, "glob file-?.md vs %q", tc.path)
	}
}

// A character class passes through, and its glob-style `!` negation is
// translated to a regexp `^`.
func TestCompileFileMatch_GlobCharacterClass(t *testing.T) {
	m, err := CompileFileMatch(`log[0-9].txt`)
	require.NoError(t, err)
	admitted, err := m.Match(event.Event{Kind: "PreFileCreate", Fields: map[string]any{"path": "log7.txt"}})
	require.NoError(t, err)
	assert.True(t, admitted)
	admitted, err = m.Match(event.Event{Kind: "PreFileCreate", Fields: map[string]any{"path": "logX.txt"}})
	require.NoError(t, err)
	assert.False(t, admitted)

	neg, err := CompileFileMatch(`log[!0-9].txt`)
	require.NoError(t, err)
	admitted, err = neg.Match(event.Event{Kind: "PreFileCreate", Fields: map[string]any{"path": "logX.txt"}})
	require.NoError(t, err)
	assert.True(t, admitted, "[!0-9] is any non-digit")
	admitted, err = neg.Match(event.Event{Kind: "PreFileCreate", Fields: map[string]any{"path": "log7.txt"}})
	require.NoError(t, err)
	assert.False(t, admitted)
}

// A dot in a glob is a literal dot, not "any character" — the common case being
// an extension. This is what a naive regexp translation gets wrong.
func TestCompileFileMatch_GlobDotIsLiteral(t *testing.T) {
	m, err := CompileFileMatch(`notes.md`)
	require.NoError(t, err)

	admitted, err := m.Match(event.Event{Kind: "PreFileCreate", Fields: map[string]any{"path": "notes.md"}})
	require.NoError(t, err)
	assert.True(t, admitted)

	admitted, err = m.Match(event.Event{Kind: "PreFileCreate", Fields: map[string]any{"path": "notesXmd"}})
	require.NoError(t, err)
	assert.False(t, admitted, "the . is a literal dot, so it does not match an arbitrary character")
}

// An unterminated character class is a malformed glob, refused at load rather
// than compiled into something that quietly matches wrong.
func TestCompileFileMatch_MalformedGlobIsRefusedAtLoad(t *testing.T) {
	_, err := CompileFileMatch(`log[0-9.txt`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "log[0-9.txt", "the error names the glob the author wrote")
}

// A path carried at the wrong type has no truth value against a glob, the same
// fail-open matcher.go's fill closes for full expressions — answered the same
// way, an error rather than a guessed match.
func TestCompileFileMatch_GlobOnWrongTypedPathErrors(t *testing.T) {
	m, err := CompileFileMatch(`memories/*.md`)
	require.NoError(t, err)

	admitted, err := m.Match(event.Event{Kind: "PreFileCreate", Fields: map[string]any{"path": 42}})
	require.Error(t, err, "a non-string path has no glob truth value")
	assert.False(t, admitted, "an erroring matcher admits nothing")
}

// The Matcher a glob compiles to reports the glob the author wrote in its
// `matcher %q` prefix, so a reader of the error finds the rule they actually
// wrote rather than a synthesized one.
//
// The prefix is what Match controls, and it is set to the authored pattern (see
// compileGlob's override of m.src). expr's own run error still embeds the
// compiled program's source snippet — `| glob(path)` — which is an internal
// representation the caller does not choose; that is a detail of reusing the
// evaluator, not a rule anyone typed, and the prefix naming the real pattern is
// the guarantee that matters.
func TestCompileFileMatch_GlobErrorNamesTheAuthoredPattern(t *testing.T) {
	m, err := CompileFileMatch(`memories/*.md`)
	require.NoError(t, err)

	_, err = m.Match(event.Event{Kind: "PreFileCreate", Fields: map[string]any{"path": 42}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `matcher "memories/*.md"`,
		"the matcher prefix names the glob the author wrote")
}

// ---------------------------------------------------------------------------
// The glob / expression discriminator
// ---------------------------------------------------------------------------

// The union has no wire marker, so the shape decides. looksLikeGlob is the
// positive test — a glob has no whitespace and no quote — and these pin it at
// the boundary cases that would send a string down the wrong path.
//
// The blind spot the earlier negative test had, and the reason it was replaced:
// it read a glob as an expression whenever a path SEGMENT spelled an operator
// keyword or contained `<`/`>`. So the failing cases below — a segment that IS a
// keyword (`commands/one.md`, `any/*.md`, `in/data.md`) and a path with the
// legal filename characters `<`/`>` (`a<b>.md`, `file[<>].md`) — are the ones
// that must now read as globs, and they anchor this test where the old one only
// ever used a keyword as a SUBSTRING (`android/`, `contextual/`).
func TestLooksLikeGlob_Discriminates(t *testing.T) {
	// Every real expression carries a space or a quote, so none is a glob.
	expressions := []string{
		`path startsWith "x"`,
		`path == "x"`,
		`any(markers, .kind == "asked")`,
		`context["x"].active`,
		`not context["x"].active`,
		`path startsWith "a" and path endsWith "b"`,
		`any(markers, .kind == "moved-from" and .line > 10)`,
	}
	for _, src := range expressions {
		assert.Falsef(t, looksLikeGlob(src), "%q is a full expression, not a glob", src)
	}

	globs := []string{
		`memories/**/*.md`,
		`*.go`,
		`memories/*.md`,
		`file-?.md`,
		`log[0-9].txt`,
		`notes.md`,
		// A path whose SEGMENTS merely contain operator or variable letters as a
		// SUBSTRING stays a glob — the case the old negative test already got
		// right.
		`contextual/notes.md`,
		`pathology/*.go`,
		`commands/*.md`,
		`android/build.gradle`,
		// The reviewer's cases: a path SEGMENT that is EXACTLY a keyword. These
		// are what the `\b`-word negative test refused at load.
		`commands/one.md`,
		`one/*.md`,
		`all/*.md`,
		`any/*.md`,
		`none/*.md`,
		`not/*.md`,
		`in/data.md`,
		`or/data.md`,
		// `<` and `>` are legal filename characters, not comparisons.
		`a<b>.md`,
		`file[<>].md`,
	}
	for _, src := range globs {
		assert.Truef(t, looksLikeGlob(src), "%q is a bare glob", src)
	}
}

// The discriminator is not merely a unit on looksLikeGlob: each of the reviewer's
// cases must actually COMPILE as a glob and MATCH as a path, end to end — the
// blind spot was that these were refused at load, so the fix is proven by them
// loading and firing.
func TestCompileFileMatch_KeywordSegmentGlobsCompileAndMatch(t *testing.T) {
	for _, tc := range []struct {
		glob string
		path string
	}{
		{`commands/one.md`, `commands/one.md`},
		{`one/*.md`, `one/notes.md`},
		{`all/*.md`, `all/notes.md`},
		{`any/*.md`, `any/notes.md`},
		{`none/*.md`, `none/notes.md`},
		{`not/*.md`, `not/notes.md`},
		{`in/data.md`, `in/data.md`},
		{`or/data.md`, `or/data.md`},
		{`a<b>.md`, `a<b>.md`},
		{`file[<>].md`, `file<.md`}, // the class [<>] matches a single < or >
	} {
		t.Run(tc.glob, func(t *testing.T) {
			m, err := CompileFileMatch(tc.glob)
			require.NoError(t, err, "a valid glob must not be refused at load")

			admitted, err := m.Match(event.Event{Kind: "PreFileCreate", Fields: map[string]any{"path": tc.path}})
			require.NoError(t, err)
			assert.Truef(t, admitted, "glob %q must match %q", tc.glob, tc.path)
		})
	}
}

// A path segment containing a variable name as a substring must actually COMPILE
// as a glob and match as a path, end to end.
func TestCompileFileMatch_PathLikeAVariableNameStaysAGlob(t *testing.T) {
	m, err := CompileFileMatch(`contextual/*.md`)
	require.NoError(t, err)

	admitted, err := m.Match(event.Event{Kind: "PreFileCreate", Fields: map[string]any{"path": "contextual/notes.md"}})
	require.NoError(t, err)
	assert.True(t, admitted, "a directory named contextual is a glob, not a context read")
}

// ---------------------------------------------------------------------------
// GateMatchScope
// ---------------------------------------------------------------------------

// preCommandKind stands in for a PreCommandInvoke as the command module
// declares it: a list of invocations whose element fields are enumerated, which
// is what makes the spec's command example's predicate body checkable.
var preCommandKind = module.KindDecl{
	Name: "PreCommandInvoke",
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
	},
}

var preFileCreateKind = module.KindDecl{
	Name: "PreFileCreate",
	Fields: []module.FieldDecl{
		{Name: "path", Type: module.TypeString},
		{Name: "newContent", Type: module.TypeString},
	},
}

// eventScopeEvent builds the runtime shape a gate or context matcher reads: the
// kind's own fields nested under `event`, and the context map under `context`.
// See CompileGateMatch's runtime-env note for why this nests where a file scope
// is flat.
func eventScopeEvent(kind string, eventFields map[string]any, contexts map[string]any) event.Event {
	fields := map[string]any{"event": eventFields}
	if contexts != nil {
		fields["context"] = contexts
	}
	return event.Event{Kind: kind, Fields: fields}
}

// A gate reads the fired event's own fields under `event`, typed to the kind the
// trigger named — the spec's command example, reached through `event`.
func TestCompileGateMatch_ReadsEventFields(t *testing.T) {
	m, err := CompileGateMatch(`any(event.invocations, .bin == "npm" and "--access" in .flags)`, preCommandKind)
	require.NoError(t, err)

	publish := eventScopeEvent("PreCommandInvoke", map[string]any{
		"invocations": []any{
			map[string]any{"bin": "npm", "flags": []any{"--access", "public"}},
		},
	}, nil)
	admitted, err := m.Match(publish)
	require.NoError(t, err)
	assert.True(t, admitted)

	plain := eventScopeEvent("PreCommandInvoke", map[string]any{
		"invocations": []any{
			map[string]any{"bin": "npm", "flags": []any{"install"}},
		},
	}, nil)
	admitted, err = m.Match(plain)
	require.NoError(t, err)
	assert.False(t, admitted)
}

// A gate narrowing on a path writes it out under `event` — there is no glob
// half here, so the path is read as an ordinary event field.
func TestCompileGateMatch_ReadsEventPath(t *testing.T) {
	m, err := CompileGateMatch(`event.path startsWith "guarded/"`, preFileCreateKind)
	require.NoError(t, err)

	admitted, err := m.Match(eventScopeEvent("PreFileCreate", map[string]any{"path": "guarded/x.md"}, nil))
	require.NoError(t, err)
	assert.True(t, admitted)

	admitted, err = m.Match(eventScopeEvent("PreFileCreate", map[string]any{"path": "open/x.md"}, nil))
	require.NoError(t, err)
	assert.False(t, admitted)
}

// The event's fields are checked to the same depth a top-level field is: a typo
// on a field the kind does not declare is refused at load.
func TestCompileGateMatch_RefusesMisspelledEventField(t *testing.T) {
	_, err := CompileGateMatch(`event.paht startsWith "x"`, preFileCreateKind)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "paht")
}

// A typo inside the event's list-element predicate is refused too, which the
// element shape on invocations is what buys.
func TestCompileGateMatch_RefusesMisspelledFieldInEventPredicate(t *testing.T) {
	_, err := CompileGateMatch(`any(event.invocations, .bni == "npm")`, preCommandKind)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bni")
}

// A gate reads `context` the same way a file-guard does — the spec keeps the
// map at parity across the scopes.
func TestCompileGateMatch_ReadsContext(t *testing.T) {
	m, err := CompileGateMatch(`context["research-run"].active`, preFileCreateKind)
	require.NoError(t, err)

	admitted, err := m.Match(eventScopeEvent("PreFileCreate",
		map[string]any{"path": "a.md"},
		map[string]any{"research-run": map[string]any{"active": true}},
	))
	require.NoError(t, err)
	assert.True(t, admitted)
}

// The gate scope has no `gates` map (the spec gives it none), and no `markers` —
// those are a file's facts. Reaching for either is refused at load.
func TestCompileGateMatch_RefusesOutOfScopeVariables(t *testing.T) {
	for _, src := range []string{
		`gates["build"].status == "pass"`,
		`any(markers, .kind == "asked")`,
	} {
		_, err := CompileGateMatch(src, preFileCreateKind)
		require.Errorf(t, err, "%q reaches outside the gate scope", src)
	}
}

func TestCompileGateMatch_RefusesNonBoolean(t *testing.T) {
	_, err := CompileGateMatch(`event.path`, preFileCreateKind)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bool")
}

// A gate trigger with no match is every occurrence of its kind — the empty
// no-narrowing, surviving the type check rather than being refused for reading
// nothing.
func TestCompileGateMatch_EmptyAdmitsEverything(t *testing.T) {
	m, err := CompileGateMatch("", preFileCreateKind)
	require.NoError(t, err)

	admitted, err := m.Match(eventScopeEvent("PreFileCreate", map[string]any{"path": "a"}, nil))
	require.NoError(t, err)
	assert.True(t, admitted)
}

// ---------------------------------------------------------------------------
// ContextMatchScope
// ---------------------------------------------------------------------------

// A context trigger's scope is the same shape as a gate's, differing only in
// which event kinds its `event` may be — the caller's KindDecl. A context may
// name a Post event a gate cannot, and its scope reads that event's fields the
// same way.
var postFileCreateKind = module.KindDecl{
	Name:   "PostFileCreate",
	Fields: []module.FieldDecl{{Name: "path", Type: module.TypeString}},
}

func TestCompileContextMatch_ReadsEventAndContext(t *testing.T) {
	m, err := CompileContextMatch(`event.path endsWith "goal.yaml"`, postFileCreateKind)
	require.NoError(t, err)

	admitted, err := m.Match(eventScopeEvent("PostFileCreate", map[string]any{"path": "memories/goal.yaml"}, nil))
	require.NoError(t, err)
	assert.True(t, admitted)

	admitted, err = m.Match(eventScopeEvent("PostFileCreate", map[string]any{"path": "memories/other.yaml"}, nil))
	require.NoError(t, err)
	assert.False(t, admitted)
}

// The context read the spec documents: matching a context's own absence directly
// through `not`, so a script need not re-derive the same fact from the
// trajectory.
func TestCompileContextMatch_NegatedContextRead(t *testing.T) {
	m, err := CompileContextMatch(`not context["research-run"].active`, postFileCreateKind)
	require.NoError(t, err)

	admitted, err := m.Match(eventScopeEvent("PostFileCreate",
		map[string]any{"path": "a.md"},
		map[string]any{"research-run": map[string]any{"active": false}},
	))
	require.NoError(t, err)
	assert.True(t, admitted, "the context is not active")
}

func TestCompileContextMatch_RefusesMisspelledEventField(t *testing.T) {
	_, err := CompileContextMatch(`event.paht == "x"`, postFileCreateKind)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "paht")
}

func TestCompileContextMatch_RefusesNonBoolean(t *testing.T) {
	_, err := CompileContextMatch(`event.path`, postFileCreateKind)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bool")
}

func TestCompileContextMatch_EmptyAdmitsEverything(t *testing.T) {
	m, err := CompileContextMatch("", postFileCreateKind)
	require.NoError(t, err)

	admitted, err := m.Match(eventScopeEvent("PostFileCreate", map[string]any{"path": "a"}, nil))
	require.NoError(t, err)
	assert.True(t, admitted)
}

// ---------------------------------------------------------------------------
// The scope env builders themselves
// ---------------------------------------------------------------------------

func TestFileMatchScope_ExposesExactlyItsVariables(t *testing.T) {
	env := fileMatchScope()

	assert.Len(t, env, 3, "path, markers, context and nothing else")
	assert.Equal(t, types.String, env["path"])
	assert.Equal(t, types.Array(types.Map{"kind": types.String, "fqn": types.String, "line": types.Int}), env["markers"])
	assert.Contains(t, env, "context")

	// No event, no gates: those belong to other scopes.
	assert.NotContains(t, env, "event")
	assert.NotContains(t, env, "gates")
}

func TestEventMatchScope_ExposesEventFieldsAndContext(t *testing.T) {
	env := eventMatchScope(preFileCreateKind)

	assert.Len(t, env, 2, "event and context and nothing else")
	assert.Equal(t, types.Map{"path": types.String, "newContent": types.String}, env["event"])
	assert.Contains(t, env, "context")

	// No markers (a file's fact) and no gates (the spec gives these scopes
	// none).
	assert.NotContains(t, env, "markers")
	assert.NotContains(t, env, "gates")
}

// context is open at the element, the same way an unenumerated map field is,
// because its keys are context names not known when the scope is built. A closed
// type would refuse every real read.
func TestScopes_ContextMapIsOpen(t *testing.T) {
	assert.Equal(t, types.Any, contextMapType())
	assert.Equal(t, types.Any, fileMatchScope()["context"])
	assert.Equal(t, types.Any, eventMatchScope(preFileCreateKind)["context"])
}
