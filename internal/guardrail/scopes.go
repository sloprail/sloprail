package guardrail

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/types"

	"github.com/sloprail/sloprail/internal/module"
)

// This file adds the per-nature MATCH SCOPES on top of matcher.go's evaluator.
//
// matcher.go compiles and runs an expression against a type environment and is
// nature-agnostic: hand it an env and it checks names against it, hand it an
// event and it evaluates. What differs between a file-guard, a gate and a
// context is not how an expression is evaluated but WHAT is in scope while it
// is — the set of variables the expression may read. The spec models that as
// three distinct scope shapes (FileMatchScope, GateMatchScope,
// ContextMatchScope), and this file is those three shapes translated into the
// type environment matcher.go already knows how to check against, plus the one
// piece of grammar the natures added that the shared evaluator did not have: a
// file-guard's bare-glob shorthand.
//
// Nothing here re-implements matching. Each Compile* below ends in the same
// compile() matcher.go exposes, and every Matcher it returns runs through the
// same Match. The additions are the ENV builders and the glob translation.

// scopeContextKey is the name of the map every scope exposes context under.
//
// Named once so the scope builders and the tests that read them cannot drift on
// the spelling. It is `context` in the spec's every *MatchScope.
const scopeContextKey = "context"

// scopeEventKey is the name the gate and context scopes expose the fired event
// under. A file scope has no such variable — it reads a file's own facts, not
// an event's — which is the whole reason the scopes are split.
const scopeEventKey = "event"

// CompileFileMatch compiles a file-guard's `match`, which the spec models as a
// FileMatchExpression: EITHER a bare GlobPattern (the common "this path" case)
// OR a full MatchExprString over FileMatchScope.
//
// The two halves are told apart by looksLikeGlob: a glob is a single path
// pattern with no whitespace and no quote, whereas every expression in the
// grammar carries at least one (a space around each operator and after a
// quantifier comma, a quote around each compared literal). So a whitespace-and-
// quote-free string compiles as a glob (path patterns like `commands/one.md` or
// `any/*.md` route here even though a segment spells an operator word), and
// everything else routes to the expression parser. This is the union's
// discriminator made concrete — a reader of the rule sees `"memories/**/*.md"`
// and a reader of this code sees the same two named things, even though both
// erase to a string on the wire.
//
// An empty match means every file, the same "no narrowing" an empty matcher
// means everywhere else — it is neither a glob nor an expression, and compile
// handles it before either path is taken.
func CompileFileMatch(src string) (*Matcher, error) {
	if src == "" {
		return compile(src)
	}
	if looksLikeGlob(src) {
		return compileGlob(src)
	}
	return compile(src, expr.Env(fileMatchScope()))
}

// CompileGateMatch compiles a gate trigger's `match` against GateMatchScope: the
// fired event under `event`, typed to the kind the trigger named, plus the
// `context` map. No glob half — a gate is not "a file at a path" by default, so
// the shorthand does not apply and a gate narrowing on a path writes it out.
//
// `kind` is the event kind that trigger fires on. Its declared fields are what
// `event` exposes, so `event.path` on a PreFileCreate trigger checks and
// `event.invocations` on a PreCommandInvoke trigger checks, each against the
// shape that kind actually carries — the faithful reading of "typed to
// GateEventKind with whichever shape that event kind carries".
//
// # Runtime env shape
//
// The scope's variables are `event` and `context`, so those are the top-level
// keys the returned Matcher reads at run time. This differs from a file-guard,
// whose variables are a file's own flat facts (path, markers, oldMarkers, context) and
// which therefore reads an event's Fields directly. A gate NESTS: the event
// handed to Match carries the kind's own fields under an `event` key and the
// context map under `context` —
//
//	event.Event{Fields: {"event": {<kind fields>}, "context": {<name>: {...}}}}
//
// This asymmetry is the spec's, not an accident of wiring: FileMatchScope
// exposes file facts, GateMatchScope exposes an event under `event`. The engine
// slice that dispatches gate triggers is what assembles this shape; the scope
// here defines it, and the tests build it the same way that slice will. See
// eventMatchScope for the compile-time counterpart.
func CompileGateMatch(src string, kind module.KindDecl) (*Matcher, error) {
	return compile(src, expr.Env(eventMatchScope(kind)))
}

// CompileContextMatch compiles a context trigger's `match` against
// ContextMatchScope. Structurally identical to a gate's scope — `event` and
// `context` — and split from it only because a context trigger's `event` is
// typed to the wider ContextEventKind (Post variants included) while a gate's
// is not. That difference lives in WHICH kind the caller passes, not in the
// scope's shape, so the two share eventMatchScope and differ only in the
// KindDecl handed to it.
//
// The runtime env shape is the gate's — `event` nested, `context` at top level;
// see CompileGateMatch.
func CompileContextMatch(src string, kind module.KindDecl) (*Matcher, error) {
	return compile(src, expr.Env(eventMatchScope(kind)))
}

// fileMatchScope is the type environment for a file-guard's `match` —
// FileMatchScope in the spec.
//
//	path       string                  the file's path
//	markers    []Marker                the sr: markers the file carries
//	oldMarkers []Marker                the sr: markers it carried before this change
//	context    map[string]ContextState every declared context, by name
//
// `oldMarkers` is the one piece of the change a file scope exposes, and for one
// reason: a rule about a marker must be able to see the marker LEAVE. With
// `markers` alone, an update that strips a file's last marker reads as a file
// with none, so a marker-scoped guard (`any(markers, .kind == "invariant")`)
// never selects the very write that removes what it guards — the silent escape.
// `any(oldMarkers, …)` selects it. On a create it is empty (nothing preceded
// it); on a Post kind it is what the file held at the session's baseline.
//
// `markers` is the spec's own FileMatchScope field (singular), NOT an event
// field. This is the seam a file-guard's scope is split for: a file-guard
// reasons about a FILE'S SETTLED STATE, so it sees the one set of markers the
// file carries — whereas the EVENTS describe a CHANGE and therefore carry a
// before/after pair (`oldMarkers`/`newMarkers` on filemod's file kinds). The two
// are deliberately different shapes, which is why this scope is not built from
// filemod.Kinds and must not be: the file-guard dispatch (a later slice) will
// populate this `markers` by scanning the settled file, the same scan filemod
// runs, exposed under the single name the spec's FileMatchScope declares.
//
// It is typed all the way into its element's fields, so `any(markers, .kind ==
// "asked")` has its predicate body checked and a typo inside it — `.knid` — is
// refused at load rather than silently never firing. The element shape is the
// Marker model's three keys (kind/fqn/line), and markerElem is the single place
// this side states them — kept faithful to the spec's Marker by the test that
// pins the shape, not by a shared symbol with filemod.
//
// context is left open (types.Any), NOT a closed map, and that is deliberate.
// The keys are project-defined context NAMES not known when the scope is built
// — `context["research-run"]` names a context this engine has never heard of —
// so a closed type over enumerated keys would refuse every real read. This is
// exactly how fieldType treats a map whose keys a module did not enumerate:
// open at types.Any, because punishing an author for a vocabulary we do not
// have is the checker being wrong, not the rule. The cost is that
// `context[<name>].active` is checked at run time rather than at load, the same
// bare-Any run-time cast a bare `path` gets — see matcher.go's env notes.
//
// No `gates` here: the spec gives a file-guard's scope no gates map (no unit so
// far has a file-guard reading a gate's verdict), and adding one would be
// claiming a variable the spec does not.
func fileMatchScope() types.Map {
	return types.Map{
		"path":          types.String,
		"markers":       types.Array(markerElem()),
		"oldMarkers":    types.Array(markerElem()),
		scopeContextKey: contextMapType(),
	}
}

// eventMatchScope is the type environment shared by GateMatchScope and
// ContextMatchScope: the fired event's own fields under `event`, and the
// `context` map.
//
// The two scopes are the same shape and differ only in which event kinds the
// caller's KindDecl may be — a gate's never a Post variant, a context's
// possibly one. That difference is the caller's to enforce by which kind it
// passes; the scope built from a kind is the same regardless.
//
// `event` is a closed structure over the kind's declared fields, reusing the
// same structure/fieldType path a nested map gets in matcherEnv. So the event's
// fields are checked to the same depth a top-level field would be — a
// PreCommandInvoke's `event.invocations[].bin` is as checkable here as
// `invocations[].bin` is in the retired EventBinding scope — and a name the
// kind does not declare, `event.paht`, is refused at load.
func eventMatchScope(kind module.KindDecl) types.Map {
	return types.Map{
		scopeEventKey:   structure(kind.Fields),
		scopeContextKey: contextMapType(),
	}
}

// contextMapType is what every scope exposes `context` as.
//
// types.Any rather than a closed structure, for the reason fileMatchScope
// spells out: the keys are context names the engine does not know at build
// time. Factored out so all three scopes agree on the one answer and a later
// decision to type it more tightly — once contexts are enumerable at load, say
// — changes one place.
func contextMapType() types.Type {
	return types.Any
}

// markerElem is the element shape of the `markers` array a file scope exposes —
// the three keys a scanned sr: marker carries.
//
// Stated here rather than imported from filemod because internal/guardrail sits
// BELOW the modules (filemod's own tests import guardrail), so importing filemod
// here would invert that and risk a cycle. The keys are the marker's wire form,
// and filemod.Marker.fields writes exactly these three — kept faithful by the
// test that pins the shape against a representative expression, not by a shared
// symbol. A marker's line is an int, matching module.TypeInt → types.Int, so
// `.line > 10` checks.
func markerElem() types.Type {
	return types.Map{
		"kind": types.String,
		"fqn":  types.String,
		"line": types.Int,
	}
}

// compileGlob turns a bare GlobPattern into a path match, reusing the evaluator
// rather than matching outside it.
//
// The glob is translated to an anchored regexp ONCE, here at load, so a
// malformed pattern is refused when the rule loads rather than at the moment it
// should have fired — the same boolean-or-refuse contract the grammar states.
// The compiled regexp is closed over by a `glob(path) bool` function registered
// in the expression environment, and the trivial expression `glob(path)` is
// what actually compiles and runs. This keeps three properties at once:
//
//   - the compile/evaluate core is matcher.go's, unchanged: `glob(path)` is an
//     ordinary expression that compile() checks for a boolean and Match() runs;
//   - the pattern is never string-interpolated into an expression, so a glob
//     containing quotes or expression syntax cannot become executable grammar —
//     it rides in a closure, as data;
//   - `path` is read through the same env the full expression uses, so a glob
//     and a full expression see the identical `path` variable.
func compileGlob(pattern string) (*Matcher, error) {
	re, err := globRegexp(pattern)
	if err != nil {
		return nil, fmt.Errorf("matcher %q: %w", pattern, err)
	}
	globFn := expr.Function(
		"glob",
		func(params ...any) (any, error) {
			path, ok := params[0].(string)
			if !ok {
				// path is declared a string in the scope, so reaching here means
				// a producer carried it as something else — the same wrong-type
				// fail-open matcher.go's fill closes for the full expressions,
				// answered the same way: error, do not guess a match.
				return nil, fmt.Errorf("glob: path is %T, not a string", params[0])
			}
			return re.MatchString(path), nil
		},
		new(func(string) bool),
	)
	// The env still carries the full file scope, so `path` type-checks as the
	// string the function is handed. compile() adds AsBool() itself.
	m, err := compile("glob(path)", expr.Env(fileMatchScope()), globFn)
	if err != nil {
		return nil, err
	}
	// The src the Matcher reports is the glob the author wrote, not the
	// synthesized `glob(path)` — an error or a log naming `glob(path)` would
	// send a reader looking for a rule they never wrote.
	m.src = pattern
	return m, nil
}

// looksLikeGlob decides whether a FileMatchExpression is the bare-glob half of
// the union rather than a full expression.
//
// The union has no marker on the wire — both sides are strings — so the shape
// itself must discriminate. The earlier attempt did this the wrong way round: a
// NEGATIVE test that read a string as an expression when it contained an
// operator token or a scope-variable word, and as a glob otherwise. That is
// inherently leaky, because a glob's own path can spell those tokens — a segment
// that IS a keyword (`commands/one.md`, `any/*.md`, `in/data.md`) is a whole
// word to a `\b` boundary, and `<`/`>` are legal filename characters an
// operator scan reads as comparisons. Every such glob was refused at load with a
// cryptic expr error the author never wrote — the exact failure the union exists
// to avoid.
//
// So this is a POSITIVE test for "is this a well-formed glob", and
// CompileFileMatch treats everything else as an expression. The discriminator is
// what a glob CANNOT contain: the two characters no path holds and every real
// expression does.
//
//   - WHITESPACE. A glob is a single path pattern with no spaces; the grammar
//     puts a space around every operator (`path startsWith "x"`, `a and b`) and
//     after the comma in a quantifier (`any(markers, …)`). One space is enough
//     to be an expression.
//   - QUOTES, single or double. Every literal an expression compares against is
//     quoted (`startsWith "memories/"`, `context["x"]`); a glob names paths
//     directly and quotes nothing.
//
// Either present ⇒ not a glob ⇒ compiled as an expression, where a genuine
// mistake is refused at load with a diagnostic naming the field. A glob has
// neither, so `commands/one.md`, `any/*.md`, `in/data.md`, `a<b>.md`,
// `file[<>].md` and `memories/**/*.md` all read as globs and are compiled to a
// path match — while `path startsWith "x"`, `any(markers, .kind == "asked")`,
// `context["x"].active` and `not context["x"].active` all carry a space or a
// quote and read as expressions.
//
// An empty string is neither, and never reaches here — CompileFileMatch handles
// it as "every file" before the split.
//
// The final authority on whether a glob is WELL-FORMED is globRegexp, which
// compileGlob runs at load: a string that passes this surface test but is a
// malformed glob (an unterminated `[`) is still refused there, by its own error
// rather than an expression parser's.
func looksLikeGlob(src string) bool {
	return !strings.ContainsAny(src, " \t\n\"'")
}

// globRegexp translates a GlobPattern into an anchored regexp over a path.
//
// The grammar is the one the spec's examples use, and no more — a glob is
// deliberately not general-purpose:
//
//	**   any run of characters INCLUDING the separator — matches across
//	     directories, so `memories/**/*.md` reaches any depth
//	*    any run of characters EXCEPT the separator — one path segment
//	?    any single character except the separator
//	[..] a character class, passed through to the regexp as one
//	{..} intentionally NOT expanded here — see below
//	.    a literal dot, the common case in an extension, so it is escaped
//
// A separator is `/`, the path form filemod emits (repository-relative, forward
// slashes). The result is anchored at both ends: a glob describes the WHOLE
// path, not a substring of it, so `memories/*.md` does not match
// `old/memories/x.md`.
//
// Brace alternation `{a,b}` is not expanded. It is absent from every example
// the spec carries, expanding it correctly means handling nesting and commas
// inside classes, and a half-expansion that silently mismatched would be the
// quiet failure this engine exists to prevent. A brace is therefore treated as
// a literal for now; a rule that needs alternation writes the full expression
// with `or`, which the union already offers. Recorded so this is a bounded
// choice rather than a missing case.
func globRegexp(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString(`\A`)
	runes := []rune(pattern)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		switch c {
		case '*':
			if i+1 < len(runes) && runes[i+1] == '*' {
				// `**` — cross-separator. Consume the second star, and a `/`
				// immediately after it too, so `**/` matches zero or more whole
				// segments rather than leaving a `/` that forces at least one.
				i++
				if i+1 < len(runes) && runes[i+1] == '/' {
					i++
					b.WriteString(`(?:.*/)?`)
				} else {
					b.WriteString(`.*`)
				}
			} else {
				b.WriteString(`[^/]*`)
			}
		case '?':
			b.WriteString(`[^/]`)
		case '[':
			// A character class, passed through. Find its end so its contents —
			// which may include regexp metacharacters that mean the same thing
			// in a class — are not escaped out of meaning. An unterminated class
			// is an error rather than a guess.
			j := i + 1
			if j < len(runes) && (runes[j] == '!' || runes[j] == '^') {
				j++
			}
			if j < len(runes) && runes[j] == ']' {
				j++
			}
			for j < len(runes) && runes[j] != ']' {
				j++
			}
			if j >= len(runes) {
				return nil, fmt.Errorf("unterminated [ in glob %q", pattern)
			}
			class := string(runes[i : j+1])
			// A glob negates a class with `!`; a regexp uses `^`. Translate only
			// a leading one.
			if strings.HasPrefix(class, "[!") {
				class = "[^" + class[2:]
			}
			b.WriteString(class)
			i = j
		default:
			// Everything else is a literal, escaped so a `.` in an extension is a
			// dot and not "any character", and so a `{`, `+`, `(` etc. cannot
			// leak regexp meaning.
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString(`\z`)
	return regexp.Compile(b.String())
}
