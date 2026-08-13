package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/internal/guardrail"
	"github.com/sloprail/sloprail/internal/module/modules"
)

// The skill is the authoring interface now that `guardrail help` prints only
// the registry-derived vocabulary. That move is right — a help command should
// document its command — but it costs the format the one property that kept the
// old help honest: being generated. A skill is a hand-written file, and this
// repo's recurring failure is a confident sentence about behaviour the code does
// not have. Six of those shipped in the help text these tests replaced, two of
// them found only by running the thing.
//
// So the claims that can be pinned are pinned here. Not the prose — the facts
// an author would act on and be wrong about.

// skillPath locates the shipped skill from the repo root.
//
// The plugin copy is the source; .claude/skills/authoring-guardrails is a
// symlink to it, so there is one file rather than two that drift.
func skillPath(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("locate repo root: %v", err)
	}
	return filepath.Join(strings.TrimSpace(string(out)),
		"marketplace", "plugins", "sloprail", "skills", "authoring-guardrails", "SKILL.md")
}

func skillText(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(skillPath(t))
	if err != nil {
		t.Fatalf("read skill: %v", err)
	}
	return string(body)
}

// T003_14: the skill does not restate the event vocabulary the binary derives.
//
// The division of labour is the whole design: `guardrail help` prints the kinds
// and fields from the modules, so it cannot go stale; the skill carries the
// format, which the binary no longer says. A kind name written into the skill
// would be a second copy of the one thing that is generated — and being the copy
// an author reads, it is the one trusted when a module is added and the skill is
// not updated.
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

	// One kind is allowed to appear: the format section has to show `hooks`
	// keyed by SOMETHING, and an example with a placeholder teaches the shape
	// less well than one with a real key. Two or more is a vocabulary list
	// forming.
	var named []string
	for _, kind := range kinds {
		if strings.Contains(skill, kind) {
			named = append(named, kind)
		}
	}
	if len(named) > 1 {
		t.Errorf("the skill names %d event kinds (%s) — that is a copy of the list `guardrail help` derives, and it is the copy an author trusts when a module is added",
			len(named), strings.Join(named, ", "))
	}

	// It must point at the command that does carry them.
	if !strings.Contains(skill, "sloprail guardrail help") {
		t.Error("the skill never tells the author to run `sloprail guardrail help` — the kinds are per-build and cannot be guessed")
	}
}

// T003_15: every matcher expression the skill shows compiles against a kind
// this build declares.
//
// The skill now owns the operator tables, which the binary used to print and
// which T003_07 used to check. An operator that does not exist produces a rule
// that will not load; one documented for the wrong field type produces a rule
// that loads and never matches. Neither is visible by reading.
//
// The expressions are scraped from the skill's own fenced blocks and inline
// code, so adding an example puts it under this check without anyone
// remembering to.
func TestT003_15_EverySkillMatcherExampleCompiles(t *testing.T) {
	skill := skillText(t)

	reg, err := modules.Registry()
	if err != nil {
		t.Fatalf("registry: %v", err)
	}

	examples := skillMatcherExamples(skill, declaredFields(t))
	if len(examples) == 0 {
		t.Fatal("found no matcher examples in the skill — this test would prove nothing")
	}

	for _, src := range examples {
		var compiled bool
		var lastErr error
		for _, kind := range reg.DeclaredKinds() {
			decl, ok := reg.KindDeclFor(kind)
			if !ok {
				continue
			}
			if _, err := guardrail.CompileMatcherFor(src, decl); err == nil {
				compiled = true
				break
			} else {
				lastErr = err
			}
		}
		if !compiled {
			t.Errorf("the skill shows matcher %q, which compiles against no declared kind: %v", src, lastErr)
		}
	}
}

// skillMatcherExamples pulls the expressions out of the skill's operator tables
// and fenced blocks.
//
// It looks for the shapes a matcher has — a declared field name followed by an
// operator — rather than for every code span, since the skill also shows shell,
// YAML and JSON in the same notation.
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

	// Fenced blocks: take their lines.
	parts := strings.Split(s, "```")
	for i, part := range parts {
		if i%2 == 0 {
			continue // outside a fence
		}
		for _, line := range strings.Split(part, "\n") {
			out = append(out, strings.TrimSpace(line))
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
// a real one is added.
func looksLikeMatcher(s string, fields map[string]bool) bool {
	if s == "" || strings.HasPrefix(s, "#") || strings.HasPrefix(s, "$") {
		return false
	}
	// Shell, YAML and JSON tells. A matcher has none of these.
	if strings.ContainsAny(s, "|{}") || strings.Contains(s, ": ") || strings.Contains(s, "--") {
		return false
	}
	// It has to read a declared field, as a whole word, and do something with
	// it. `path` alone is prose; `path startsWith "x"` is an expression.
	var readsField bool
	for _, word := range strings.FieldsFunc(s, func(r rune) bool {
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
	if len(fields) == 0 {
		t.Fatal("no fields declared anywhere — the scraper would recognise nothing")
	}
	return fields
}

// T003_16: the skill's claims about what does and does not load are true.
//
// Each of these was WRONG in the help text this replaced, and each was found
// only by running the engine rather than by reading it:
//
//   - the old help said a name "must be kebab-case". Nothing validates a name;
//     any directory loads. An author could have been told to rename a working
//     rule.
//   - it said repeating a key is "not an error YAML reports — the second
//     silently replaces the first". The loader reports duplicates by path and
//     line and refuses the declaration.
//   - it said a matcher naming an absent field is "NOT currently caught". It is
//     caught at compile, against the kind's declared fields.
//
// So the skill's replacements for those three are asserted against the engine
// itself, driving real declarations through the load check.
func TestT003_16_SkillLoadClaimsMatchTheLoader(t *testing.T) {
	e := New(t)
	skill := skillText(t)

	const script = "#!/bin/sh\ncat >/dev/null\nexit 0\n"

	t.Run("an unusual folder name loads", func(t *testing.T) {
		proj := e.Project()
		e.Guardrail(proj, "Weird_NAME.v2", validDeclaration, map[string]string{"h.sh": script})

		got := e.CLI(proj, "session", "start")
		if strings.Contains(got.Output, "not loaded") {
			t.Fatalf("a non-kebab-case name was refused, so the skill must state a naming RULE rather than a convention:\n%s", got.Output)
		}
		if strings.Contains(skill, "must be kebab-case") {
			t.Error("the skill states kebab-case as a requirement, and nothing enforces one")
		}
	})

	t.Run("a duplicate key is refused, not silently overwritten", func(t *testing.T) {
		proj := e.Project()
		e.Guardrail(proj, "dupes", duplicateKeyDeclaration, map[string]string{"h.sh": script})

		got := e.CLI(proj, "session", "start")
		if !strings.Contains(got.Output, "not loaded") {
			t.Fatalf("a duplicated event key loaded — the skill says it is refused:\n%s", got.Output)
		}
		if !strings.Contains(got.Output, "defined twice") {
			t.Errorf("the duplicate was refused without naming it as a duplicate:\n%s", got.Output)
		}
		// The engine half above is only half the claim. The old help said the
		// second key "silently replaces the first" and told authors to police it
		// themselves; that was false, and checking only the loader would let the
		// same false sentence back into the skill unnoticed.
		if strings.Contains(skill, "silently replaces") {
			t.Error("the skill says a duplicate key silently replaces the first — the loader refuses the declaration and names both lines")
		}
	})

	t.Run("a binding written as a map is refused", func(t *testing.T) {
		proj := e.Project()
		e.Guardrail(proj, "mapshape", mapBindingDeclaration, map[string]string{"h.sh": script})

		got := e.CLI(proj, "session", "start")
		if !strings.Contains(got.Output, "not loaded") {
			t.Fatalf("`hooks` written as a bare mapping loaded — the skill says this is refused at load, and an author trusting that would ship a half-rule:\n%s", got.Output)
		}
	})

	t.Run("a misspelled top-level field is refused", func(t *testing.T) {
		proj := e.Project()
		e.Guardrail(proj, "typo", misspelledFieldDeclaration, map[string]string{"h.sh": script})

		got := e.CLI(proj, "session", "start")
		if !strings.Contains(got.Output, "not loaded") {
			t.Fatalf("a matcher naming a field the kind does not carry loaded — the skill tells authors they need not defend against that, which would then be false:\n%s", got.Output)
		}
	})
}

const validDeclaration = `---
hooks:
  PreFileCreate:
    - matcher: path startsWith "x/"
      hooks:
        - type: command
          command: ./h.sh
---

# Body
`

// Two entries for one kind written as two KEYS, which is the mistake the skill
// says is caught.
const duplicateKeyDeclaration = `---
hooks:
  PreFileCreate:
    - matcher: path startsWith "a/"
      hooks:
        - type: command
          command: ./h.sh
  PreFileCreate:
    - matcher: path startsWith "b/"
      hooks:
        - type: command
          command: ./h.sh
---

# Body
`

// A binding written as a bare mapping rather than as a list item — the shape an
// author guesses when they have not been told `hooks` maps to a LIST.
const mapBindingDeclaration = `---
hooks:
  PreFileCreate:
    matcher: path startsWith "x/"
    hooks:
      - type: command
        command: ./h.sh
---

# Body
`

// `pth` for `path`.
const misspelledFieldDeclaration = `---
hooks:
  PreFileCreate:
    - matcher: pth startsWith "x/"
      hooks:
        - type: command
          command: ./h.sh
---

# Body
`

// T003_17: the skill's hook contract matches what the engine sends and accepts.
//
// The stdin shape is what a hook script is written against — a wrong key name
// there produces a hook whose extraction returns empty and which then permits
// everything, which is the silent no-op wearing the shape of a working rule.
//
// Asserted against the constant the engine builds from where one exists, so a
// rename breaks this rather than leaving the skill behind.
func TestT003_17_SkillHookContractMatchesTheEngine(t *testing.T) {
	skill := skillText(t)

	for _, key := range []string{"guardrailDir", "event", "kind", "fields"} {
		if !strings.Contains(skill, key) {
			t.Errorf("the skill never names %q, which a hook must read off its stdin", key)
		}
	}

	if !strings.Contains(skill, "type: "+guardrail.HookCommand) {
		t.Errorf("the skill does not show `type: %s`, the only hook type this build accepts", guardrail.HookCommand)
	}
}

// T003_18: the skill warns off exactly the things that do not work.
//
// Both warnings are claims about the engine, and both must disappear the day
// the gap closes — a warning that outlives its gap sends an author away from a
// rule that would now work, which is the same failure wearing the opposite sign.
//
// So each is pinned to the fact rather than to itself. `session state` is run
// the way a hook runs it, with no scope in the environment; the Post claim is
// checked against the phases something actually dispatches with.
func TestT003_18_SkillWarnsOffOnlyWhatIsActuallyBroken(t *testing.T) {
	e := New(t)
	skill := skillText(t)

	// CLI runs the built binary with only HOME set, which is the environment a
	// hook process has today.
	got := e.CLI(t.TempDir(), "session", "state", "get", "anything")
	if got.Code == 0 {
		t.Fatalf("`session state get` succeeded with no scope in the environment — the gap has closed and the skill's warning is now false:\n%s", got.Output)
	}
	if !strings.Contains(skill, "session state") {
		t.Error("the skill never mentions `session state`, which is listed under `sloprail session --help` and looks usable")
	}

	// Post kinds: declared, bindable, never dispatched. Only session pre-tool
	// dispatches and it passes the pre phase, so a Post kind cannot arrive.
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
	if hasPost && !strings.Contains(skill, "Post") {
		t.Error("this build declares Post kinds and the skill does not warn that they are never dispatched — a rule bound to one loads, validates and never fires")
	}
	if !hasPost && strings.Contains(skill, "Post") {
		t.Error("the skill warns about Post kinds and none are declared")
	}
}
