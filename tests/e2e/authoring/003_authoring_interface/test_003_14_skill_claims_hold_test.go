package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/internal/guardrail"
	"github.com/sloprail/sloprail/internal/module"
	"github.com/sloprail/sloprail/internal/module/modules"
)

// RE-VEHICLED onto the new nature format (was old e.Guardrail / GUARDRAIL.md).
//
// These tests pin the authoring-guardrails SKILL's own claims by installing a
// declaration in the format the skill teaches and checking the engine behaves as
// the skill says (an unusual folder name loads, a duplicate key is refused, a
// misspelled field is caught, an unknown kind is reported, the vocabulary is not
// restated, the flat check payload is the one the skill documents). The skill
// (marketplace/plugins/sloprail/skills/authoring-guardrails/, read here via
// skillDir) now teaches the new nature format — file-guard/gate/context YAML, the
// flat `CheckPayload` (`.event.path`, `.transcriptPath`, `.context`), and
// `script:`/`judge:` checks. So the declarations these tests install ARE new-format
// on purpose: they must match what the skill teaches, or they stop being tests OF
// the skill.
//
// The skill is the ENTIRE authoring interface now. `sr-guardrail help` is gone —
// it was the only subcommand of its binary, and a help command that describes a
// declaration format rather than its own command's behaviour was the wrong home for
// that content in the first place.
//
// That deletion moves the prose here but NOT the vocabulary, which is the point
// these tests defend. A skill is a hand-written file, and this repo's recurring
// failure is a confident sentence about behaviour the code does not have. The kinds
// must therefore still come from the engine, and they do: the load check reports
// them from the same registry the enforcement runs on — see the load-check
// assertions in T003_14 and T003_19.
//
// So the claims that can be pinned are pinned here. Not the prose — the facts an
// author would act on and be wrong about.

// skillDir locates the shipped skill's folder from the repo root.
//
// The plugin copy is the source; .claude/skills/authoring-guardrails is a
// symlink to it, so there is one file rather than two that drift.
func skillDir(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("locate repo root: %v", err)
	}
	return filepath.Join(strings.TrimSpace(string(out)),
		"marketplace", "plugins", "sloprail", "skills", "authoring-guardrails")
}

// skillPath is the skill's entry point — the file loaded whole, every time.
func skillPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(skillDir(t), "SKILL.md")
}

func skillText(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(skillPath(t))
	if err != nil {
		t.Fatalf("read skill: %v", err)
	}
	return string(body)
}

// skillCorpusText is SKILL.md plus every reference file beside it.
//
// The skill is a folder now: the entry point carries the decision path and the
// depth lives in linked files. Which half a given claim sits in is an editorial
// choice that will keep moving, so any check about what the skill SHOWS — an
// example that has to compile, an invocation that has to be real — reads the
// whole folder. Otherwise moving a section behind a link silently moves it out
// of the tests, and the reader still follows it.
//
// The checks about what the skill must NOT restate stay on SKILL.md alone. See
// T003_14, which is about the cost of the file loaded on every task.
func skillCorpusText(t *testing.T) string {
	t.Helper()
	entries, err := os.ReadDir(skillDir(t))
	if err != nil {
		t.Fatalf("read skill dir: %v", err)
	}
	var b strings.Builder
	var files int
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(skillDir(t), e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		b.Write(body)
		b.WriteString("\n")
		files++
	}
	if files == 0 {
		t.Fatal("the skill folder holds no markdown — this test would prove nothing")
	}
	return b.String()
}

// T003_14: the skill does not restate the event vocabulary the engine derives.
//
// The division of labour is the whole design, and deleting `sr-guardrail help`
// did not change it — only which derived surface the skill points at. The
// LOAD CHECK now reports the kinds and fields, from the same registry the
// enforcement runs on, so it still cannot go stale; the skill carries the
// format, which no binary states. A kind name written into the skill would be a
// second copy of the one thing that is generated — and being the copy an author
// reads, it is the one trusted when a module is added and the skill is not
// updated.
//
// Checked against the registry rather than a list of names, so a new module puts
// its kinds under this guard automatically.
func TestT003_14_SkillDoesNotRestateTheDerivedVocabulary(t *testing.T) {
	skill := skillText(t)

	reg, err := modules.Registry()
	if err != nil {
		t.Fatalf("registry: %v", err)
	}

	kinds := reg.DeclaredKinds()
	if len(kinds) == 0 {
		t.Fatal("no kinds declared — this test would prove nothing")
	}

	// The skill DOES have to name some kinds — the format sections show a gate on
	// `PreCommandInvoke`, a context on `PostTagWrite`, the `PreFileWrite`/
	// `PostFileWrite` aliases — because a nature's YAML is shown with a real `event`
	// key, and a placeholder teaches the shape less well than a real one. The
	// property is not "names zero kinds"; it is "does not RESTATE the whole list a
	// module would extend". So this counts how many of the build's kinds appear and
	// fails only if ~all of them do — a copy of the derived list forming — while
	// tolerating the handful the worked examples legitimately name.
	//
	// Some event-kind names coincide with a harness HOOK-POINT name, and the skill
	// must be free to reference the hook point without that reading as a vocabulary
	// restatement. `Stop` and `PreToolUse` are the clearest cases — the harness's
	// own hook points, a fixed set the skill documents as plumbing. So they are
	// excluded from the count.
	harnessHookPoints := map[string]bool{
		"SessionStart": true,
		"PreToolUse":   true,
		"Stop":         true,
		"SubagentStop": true,
	}
	var named, eligible []string
	for _, kind := range kinds {
		if harnessHookPoints[kind] {
			continue
		}
		eligible = append(eligible, kind)
		if strings.Contains(skill, kind) {
			named = append(named, kind)
		}
	}
	// A restatement is the WHOLE list appearing. The worked examples name a few
	// kinds by necessity; the failure is the skill having become the second copy of
	// everything the registry produces. Allow the examples' handful, catch the list.
	if len(eligible) > 0 && len(named) == len(eligible) {
		t.Errorf("the skill names every one of the %d bindable event kinds (%s) — that is a copy of the list the loader derives, and it is the copy an author trusts when a module is added",
			len(eligible), strings.Join(named, ", "))
	}

	// It must point at something that DOES carry them. Not naming the vocabulary
	// is only half the property: a skill that withholds the list and also never
	// says where to get it leaves an author guessing kind names, which is the
	// silent no-op this whole document exists to prevent.
	//
	// This half reads the WHOLE folder (skillCorpusText, not skillText): SKILL.md
	// itself only has to point at events.md, the single reference for the
	// vocabulary — the pointer does not have to duplicate the mechanism it points
	// at. `sr-session start` is that mechanism. It is not a help screen — it is
	// the loader reporting, from the registry the enforcement itself runs on,
	// every kind this build produces and every field a kind carries. T003_19
	// below proves it actually answers that way rather than merely being cited.
	if !strings.Contains(skillCorpusText(t), "sr-session start") {
		t.Error("nothing in the skill folder tells the author how to get the kinds from the engine — they are per-build and cannot be guessed, so an author who is not sent to the load check will invent a kind name")
	}
}

// T003_15: every matcher expression the skill shows compiles against a scope this
// build declares.
//
// The skill now owns the operator tables, which the binary used to print. An
// operator that does not exist produces a rule that will not load; one documented
// for the wrong field type produces a rule that loads and never matches. Neither
// is visible by reading.
//
// The expressions are scraped from the skill's own fenced blocks and inline code,
// so adding an example puts it under this check without anyone remembering to.
//
// A match compiles against one of the three NEW scopes: a file-guard's bare scope
// (`path`, `markers`, `context`) via CompileFileMatch, or a gate's/context's
// event-nested scope (`event.*`, `context`) via CompileGateMatch/CompileContextMatch
// against some kind. An example is accepted if ANY scope compiles it — the skill
// shows expressions for all three natures, and a file-guard glob is not expected to
// parse as a gate expression or vice versa.
func TestT003_15_EverySkillMatcherExampleCompiles(t *testing.T) {
	skill := skillCorpusText(t)

	reg, err := modules.Registry()
	if err != nil {
		t.Fatalf("registry: %v", err)
	}

	examples := skillMatcherExamples(skill, declaredFields(t))
	if len(examples) == 0 {
		t.Fatal("found no matcher examples in the skill — this test would prove nothing")
	}

	for _, src := range examples {
		if compilesInSomeScope(reg, src) {
			continue
		}
		t.Errorf("the skill shows matcher %q, which compiles against no declared scope", src)
	}
}

// compilesInSomeScope reports whether an expression compiles against ANY scope
// this engine has: the file-guard's bare scope (CompileFileMatch), the flat
// per-kind scope a file-guard's content match reads (CompileMatcherFor — where a
// file-write kind carries newContent/newMarkers/resultKnown bare), or the
// gate's/context's event-nested scope (CompileGateMatch/CompileContextMatch)
// against some kind.
//
// An example is accepted if any of these compiles it — the skill shows expressions
// for all three natures and for both the glob scope and the content-field scope, so
// a file-guard glob is not expected to parse as a gate expression and a content
// match is not expected to parse against the bare file scope.
func compilesInSomeScope(reg interface {
	DeclaredKinds() []string
	KindDeclFor(string) (module.KindDecl, bool)
}, src string) bool {
	if _, err := guardrail.CompileFileMatch(src); err == nil {
		return true
	}
	for _, kind := range reg.DeclaredKinds() {
		decl, ok := reg.KindDeclFor(kind)
		if !ok {
			continue
		}
		if _, err := guardrail.CompileMatcherFor(src, decl); err == nil {
			return true
		}
		if _, err := guardrail.CompileGateMatch(src, decl); err == nil {
			return true
		}
		if _, err := guardrail.CompileContextMatch(src, decl); err == nil {
			return true
		}
	}
	return false
}

// skillMatcherExamples pulls the expressions out of the skill's operator tables
// and fenced blocks.
//
// It looks for the shapes a matcher has — a declared field name (bare or under
// `event.`) followed by an operator — rather than for every code span, since the
// skill also shows shell, YAML and JSON in the same notation.
func skillMatcherExamples(skill string, fields map[string]bool) []string {
	var out []string
	for _, span := range codeSpans(skill) {
		if looksLikeMatcher(span, fields) {
			out = append(out, span)
		}
	}
	return out
}

// codeSpans returns the contents of every inline `code span` and every line
// inside a fenced block.
func codeSpans(s string) []string {
	var out []string

	// Fenced blocks: take their lines. The skill aligns some example blocks in two
	// columns — `<expression>␣␣␣␣<prose annotation>` — so a run of two or more
	// spaces separates code from its inline note. Take only the first column, or an
	// annotated expression would be scraped WITH its prose and never compile.
	parts := strings.Split(s, "```")
	for i, part := range parts {
		if i%2 == 0 {
			continue // outside a fence
		}
		for _, line := range strings.Split(part, "\n") {
			line = strings.TrimSpace(line)
			if idx := strings.Index(line, "  "); idx >= 0 {
				line = strings.TrimSpace(line[:idx])
			}
			out = append(out, line)
		}
	}

	// Inline spans, from the prose outside fences.
	for i, part := range parts {
		if i%2 == 1 {
			continue
		}
		chunks := strings.Split(part, "`")
		for j := 1; j < len(chunks); j += 2 {
			out = append(out, strings.TrimSpace(chunks[j]))
		}
	}
	return out
}

// looksLikeMatcher keeps the spans that are matcher expressions.
//
// Anchored on the FIELD NAMES the modules declare, never on the operators. An
// earlier version required a known operator, which inverted the test: an
// expression using an operator that does not exist — the exact thing this is
// here to catch — failed the filter and was discarded as "not a matcher", so
// inserting `path glob "**/*.md"` into the skill passed. A test blind to its
// own subject is worse than no test, and this repo has already had to delete
// two of them.
//
// Field names are the right anchor because they come from the registry, so the
// filter cannot be fooled by a made-up operator and does not need updating when
// a real one is added. A field may appear bare (file-guard scope: `path`) or
// under the gate/context `event.` prefix (`event.path`); a leading `.event.`
// stdin-jq accessor is NOT a match expression and is excluded.
func looksLikeMatcher(s string, fields map[string]bool) bool {
	if s == "" || strings.HasPrefix(s, "#") || strings.HasPrefix(s, "$") {
		return false
	}
	// A jq accessor into the check payload (`.event.path`, `.transcriptPath`) is a
	// stdin read shown in a shell example, not a match expression. It starts with a
	// dot; a real match never does.
	if strings.HasPrefix(s, ".") {
		return false
	}
	// A deliberate COUNTEREXAMPLE. The skill shows mistyped predicates
	// (`any(markers, .knid == …)`) to teach what fails, with an ellipsis where the
	// value would go. An ellipsis is never in a real expression, and a counterexample
	// is not meant to compile — scraping it would assert the skill's bad examples are
	// good, inverting the test.
	if strings.Contains(s, "…") {
		return false
	}
	// Shell, YAML and JSON tells. A matcher has none of these. A shell assignment
	// (`input="$(cat)"`), a command list (`&&`, `;;`), a redirect (`>&2`) or a `$`
	// expansion is shell shown in the same fenced block as expressions.
	if strings.ContainsAny(s, "|{}$") || strings.Contains(s, ": ") || strings.Contains(s, "--") ||
		strings.Contains(s, `="`) || strings.Contains(s, "&&") || strings.Contains(s, ";;") ||
		strings.Contains(s, ">&") {
		return false
	}
	// It has to read a declared field, as a whole BARE word, and do something with
	// it. `path` alone is prose; `path startsWith "x"` is an expression. The word
	// scan splits on non-identifier characters, so `event.path` yields both `event`
	// and `path` — the declared `path` is what matches.
	//
	// Quoted string contents are stripped first, so a field name that appears only
	// inside a literal is not counted: `has("newContent")` is a jq builtin shown in
	// prose, not a match reading `newContent`, and its only `newContent` is inside
	// the quotes.
	bare := stripQuoted(s)
	var readsField bool
	for _, word := range strings.FieldsFunc(bare, func(r rune) bool {
		return !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9')
	}) {
		if fields[word] {
			readsField = true
			break
		}
	}
	if !readsField {
		return false
	}
	return strings.Contains(s, `"`) || strings.Contains(s, "==") || strings.Contains(s, ">")
}

// stripQuoted removes the contents of double-quoted string literals, leaving the
// surrounding expression, so a field name that appears only inside a string is not
// mistaken for a bare field read.
func stripQuoted(s string) string {
	var b strings.Builder
	inQuote := false
	for _, r := range s {
		if r == '"' {
			inQuote = !inQuote
			continue
		}
		if !inQuote {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// declaredFields is every field name any kind carries, which is what a matcher
// reads and therefore how one is recognised.
func declaredFields(t *testing.T) map[string]bool {
	t.Helper()
	reg, err := modules.Registry()
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	fields := map[string]bool{}
	for _, kind := range reg.DeclaredKinds() {
		decl, ok := reg.KindDeclFor(kind)
		if !ok {
			continue
		}
		for _, f := range decl.Fields {
			fields[f.Name] = true
		}
	}
	// The file-guard scope exposes `markers` (a list) which is not a module field
	// name — it is the scanned-marker view of new/oldMarkers. Recognise it too, so
	// a file-guard example like `any(markers, .kind == "docs")` is scraped.
	fields["markers"] = true
	if len(fields) == 0 {
		t.Fatal("no fields declared anywhere — the scraper would recognise nothing")
	}
	return fields
}

// T003_16: the skill's claims about what does and does not load are true.
//
// Each of these was WRONG in the help text this replaced, and each was found
// only by running the engine rather than by reading it. So the skill's claims are
// asserted against the engine itself, driving real NEW-FORMAT declarations through
// the load check (`sr-session start`).
func TestT003_16_SkillLoadClaimsMatchTheLoader(t *testing.T) {
	e := New(t)
	skill := skillText(t)

	const script = "#!/bin/sh\ncat >/dev/null\nexit 0\n"

	t.Run("an unusual folder name loads", func(t *testing.T) {
		proj := e.Project()
		e.Gate(proj, "Weird_NAME.v2", validGate, map[string]string{"h.sh": script})

		got := e.CLI(proj, "session", "start")
		if strings.Contains(got.Output, "not loaded") {
			t.Fatalf("a non-kebab-case name was refused, so the skill must state a naming CONVENTION rather than a rule:\n%s", got.Output)
		}
		if strings.Contains(skill, "must be kebab-case") {
			t.Error("the skill states kebab-case as a requirement, and nothing enforces one")
		}
	})

	t.Run("a duplicate key is refused, not silently overwritten", func(t *testing.T) {
		proj := e.Project()
		e.Gate(proj, "dupes", duplicateKeyGate, map[string]string{"h.sh": script})

		got := e.CLI(proj, "session", "start")
		if !strings.Contains(got.Output, "not loaded") {
			t.Fatalf("a duplicated key loaded — the skill says a duplicate is refused:\n%s", got.Output)
		}
		if !strings.Contains(got.Output, "already defined") {
			t.Errorf("the duplicate was refused without naming it as a duplicate:\n%s", got.Output)
		}
		// The engine half above is only half the claim. If the skill told authors a
		// duplicate key "silently replaces" the first, checking only the loader would
		// let that false sentence back in unnoticed.
		if strings.Contains(skill, "silently replaces") {
			t.Error("the skill says a duplicate key silently replaces the first — the loader refuses the declaration and names the line")
		}
	})

	t.Run("a misspelled event field is refused", func(t *testing.T) {
		proj := e.Project()
		e.Gate(proj, "typo", misspelledFieldGate, map[string]string{"h.sh": script})

		got := e.CLI(proj, "session", "start")
		if !strings.Contains(got.Output, "not loaded") {
			t.Fatalf("a match naming a field the kind does not carry loaded — the skill tells authors they need not defend against that, which would then be false:\n%s", got.Output)
		}
	})

	t.Run("a file-guard with no match is refused", func(t *testing.T) {
		proj := e.Project()
		e.FileGuard(proj, "nomatch", noMatchFileGuard, map[string]string{"h.sh": script})

		got := e.CLI(proj, "session", "start")
		if !strings.Contains(got.Output, "not loaded") {
			t.Fatalf("a file-guard with no match loaded — the skill says a file-guard must say which files it covers:\n%s", got.Output)
		}
	})
}

// validGate is a well-formed gate on a pre-file write.
const validGate = `on:
  - event: PreFileCreate
    match: event.path startsWith "x/"
checks:
  - script: ./h.sh
`

// duplicateKeyGate repeats a top-level key, which the loader refuses.
const duplicateKeyGate = `on:
  - event: PreFileCreate
    match: event.path startsWith "x/"
checks:
  - script: ./h.sh
checks:
  - script: ./h.sh
`

// misspelledFieldGate names a field PreFileCreate does not carry (`paht`).
const misspelledFieldGate = `on:
  - event: PreFileCreate
    match: event.paht startsWith "x/"
checks:
  - script: ./h.sh
`

// noMatchFileGuard omits the required `match`.
const noMatchFileGuard = `checks:
  - script: ./h.sh
`

// T003_16b: the skill no longer teaches a `preventive:` key.
//
// A file-guard acts only at Stop, and the load REFUSES `preventive:` (any value),
// naming the split into a PreFileWrite gate plus a plain file-guard. A YAML example
// carrying the key would teach an author a declaration this build refuses to load.
// The word may still appear in prose that explains the removal; only a `preventive:`
// KEY at the start of a line (as in a yaml example) is what this pins.
func TestT003_16b_SkillDoesNotTeachPreventive(t *testing.T) {
	for _, line := range strings.Split(skillCorpusText(t), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "preventive:") {
			t.Errorf("the skill shows a `preventive:` key, which the loader refuses: %q", line)
		}
	}
}

// T003_17: the skill's check contract matches what the engine sends and accepts.
//
// The stdin shape is what a check script is written against — a wrong key name
// there produces a check whose read returns empty and which then permits
// everything, which is the silent no-op wearing the shape of a working rule.
//
// The new format hands a check a FLAT CheckPayload: the event's fields directly
// under `.event`, `.transcriptPath` for the record, `.context` for the declared
// contexts. There is no `.event.fields.*` nesting and no `guardrailDir` field. A
// check is a `script:` or a `judge:`, never `type: command`. So the skill must
// name those, and must not have carried the old wire form back in.
func TestT003_17_SkillCheckContractMatchesTheEngine(t *testing.T) {
	skill := skillText(t)

	// The flat payload keys a check reads off its stdin.
	for _, key := range []string{"CheckPayload", ".event", "transcriptPath", "context"} {
		if !strings.Contains(skill, key) {
			t.Errorf("the skill never names %q, which a check reads off its flat stdin payload", key)
		}
	}

	// The two check forms the engine accepts.
	if !strings.Contains(skill, "script:") {
		t.Error("the skill does not show `script:`, one of the two check forms this build accepts")
	}
	if !strings.Contains(skill, "judge:") {
		t.Error("the skill does not show `judge:`, the model check form this build accepts")
	}

	// The new payload is FLAT: `.event.path`, not the old `.event.fields.path`. The
	// skill is expected to STATE this — it says outright "there is no `.event.fields.*`
	// nesting" to steer an author off the old shape — so the assertion is that the
	// skill says the fields are flat under `.event`, not that the string
	// ".event.fields" never appears (it appears precisely in the warning).
	if !strings.Contains(skill, "no") || !strings.Contains(skill, ".event.fields") {
		t.Error("the skill does not warn that there is no `.event.fields.*` nesting — an author carrying the old nested shape over writes a check that reads nothing")
	}
}

// T003_18: the skill warns off exactly the things that do not work.
//
// A warning that outlives its gap sends an author away from a rule that would now
// work, which is the same failure wearing the opposite sign. So the one live
// warning is pinned to the fact rather than to itself.
//
// The Post-kind warning the OLD version pinned is GONE deliberately: in the new
// format the Post file events and PostTagWrite ARE dispatched (a file-guard's
// after-check and a context's enter fire on them at Stop), so "Post kinds never
// fire" is no longer true and the skill no longer says it. Asserting the skill
// still carries that warning would pin a claim the migration made false.
func TestT003_18_SkillWarnsOffOnlyWhatIsActuallyBroken(t *testing.T) {
	e := New(t)
	skill := skillText(t)

	// `session state` used with no scope in the environment fails, and the skill
	// points authors at it for cross-cycle reads (state-management.md). CLI runs the
	// built binary with only HOME set, which is the environment a hook process has
	// when it is not scoped — so this must still fail, or the skill's turn-scoping
	// caveat has gone stale.
	got := e.CLI(t.TempDir(), "session", "state", "get", "anything")
	if got.Code == 0 {
		t.Fatalf("`session state get` succeeded with no scope in the environment — the gap has closed and the skill's caveat is now false:\n%s", got.Output)
	}
	if !strings.Contains(skill, "session state") {
		t.Error("the skill never mentions `session state`, which cross-cycle rules depend on and which behaves scope-sensitively")
	}

	// The Post kinds this build declares ARE dispatched now, so the skill is right
	// to document them as usable rather than warn them off. Guard against the old
	// false warning regressing back in.
	reg, err := modules.Registry()
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	var hasPost bool
	for _, kind := range reg.DeclaredKinds() {
		if strings.HasPrefix(kind, "Post") {
			hasPost = true
			break
		}
	}
	if hasPost && strings.Contains(skill, "never dispatched") {
		t.Error("the skill warns that Post kinds are never dispatched, but the new Stop dispatch fires them for file-guards and contexts — the warning is now false")
	}
}

// T003_19: the load check really is a vocabulary oracle.
//
// This is the test that keeps the deletion of `sr-guardrail help` honest. That
// command was the one place an author could read the whole event vocabulary.
// Removing it without replacing that surface would leave the skill telling authors
// to run something that might answer with nothing — a claim about the engine with
// no test behind it, which is exactly the failure the rest of this file exists to
// prevent.
//
// So the property moves rather than disappears: "the loader NAMES the kinds a gate
// admits when you name one it does not, and names a kind's fields when you misspell
// one". Both halves are what an author actually needs, and both are derived from the
// registry rather than written down.
//
// Driven through the real binary with real declarations, because the claim is about
// what an author SEES.
func TestT003_19_LoadCheckReportsTheVocabulary(t *testing.T) {
	e := New(t)

	reg, err := modules.Registry()
	if err != nil {
		t.Fatalf("registry: %v", err)
	}

	const script = "#!/bin/sh\ncat >/dev/null\nexit 0\n"

	// Half one: a gate naming an unknown kind is answered with the kinds a gate
	// DOES admit. This is what replaces `guardrail help`'s EVENT KINDS list as the
	// way to find out what may be bound to.
	t.Run("an unknown kind is answered with the kinds a gate admits", func(t *testing.T) {
		proj := e.Project()
		e.Gate(proj, "probe-kind", unknownKindGate, map[string]string{"h.sh": script})

		got := e.CLI(proj, "session", "start")
		if !strings.Contains(got.Output, "not loaded") {
			t.Fatalf("a gate on an unknown kind was not reported as not loaded:\n%s", got.Output)
		}
		// A gate admits the pre-action kinds plus Stop. The report names them, so an
		// author sent here by the skill can discover what to bind to.
		for _, kind := range []string{"PreFileCreate", "PreCommandInvoke", "Stop"} {
			if !strings.Contains(got.Output, kind) {
				t.Errorf("the load check does not name %q when refusing an unknown gate kind — an author sent here by the skill cannot discover it:\n%s", kind, got.Output)
			}
		}
	})

	// Half two: a gate whose match misspells a field is answered with that kind's
	// real fields AND their types. The types matter as much as the names — the
	// skill's operator tables are split by type, and a `list` matched with a string
	// operator is a rule that does not compile.
	t.Run("a misspelled field is answered with the kind's real fields and types", func(t *testing.T) {
		decl, ok := reg.KindDeclFor("PreFileCreate")
		if !ok || len(decl.Fields) == 0 {
			t.Skip("PreFileCreate carries no fields to report")
		}

		proj := e.Project()
		e.Gate(proj, "probe-field", misspelledFieldGate, map[string]string{"h.sh": script})

		got := e.CLI(proj, "session", "start")
		if !strings.Contains(got.Output, "not loaded") {
			t.Fatalf("a gate misspelling a field was not reported as not loaded:\n%s", got.Output)
		}

		for _, f := range decl.Fields {
			if !strings.Contains(got.Output, f.Name) {
				t.Errorf("PreFileCreate carries field %q, and the load check does not name it when refusing a misspelling — a matcher author would have to guess it:\n%s", f.Name, got.Output)
			}
			if !strings.Contains(got.Output, string(f.Type)) {
				t.Errorf("the load check names field %q without its type %q — the operator groups are not interchangeable, so a type-less field name is not enough to write a matcher from:\n%s", f.Name, f.Type, got.Output)
			}
		}
	})
}

// unknownKindGate binds a gate to a kind no module produces, so the loader answers
// with the kinds a gate does admit. Deliberately not a near-miss of a real name.
const unknownKindGate = `on:
  - event: NoSuchKindExistsHere
checks:
  - script: ./h.sh
`
