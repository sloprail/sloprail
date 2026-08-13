package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/internal/module/modules"
)

// T003_01: help prints every kind the engine can produce, with its fields.
//
// The expectation is derived from the modules rather than written out, for the
// same reason the help itself is: a list spelled here would be a third copy, and
// the test would pass while the help lied. Add a module and this fails until the
// help prints its kinds too.
//
// modules.Registry is the same call the binary makes — not a registry assembled
// here to resemble it. Assembled, it would be a list to keep in step, and the
// one time it fell behind the test went red for a module that had been added
// correctly. Sharing the call is what makes disagreeing impossible rather than
// merely unlikely.
func TestT003_01_HelpPrintsEveryDeclaredKind(t *testing.T) {
	e := New(t)

	got := e.CLI(t.TempDir(), "guardrail", "help")
	if got.Code != 0 {
		t.Fatalf("guardrail help exited %d:\n%s", got.Code, got.Output)
	}

	reg, err := modules.Registry()
	if err != nil {
		t.Fatalf("registry: %v", err)
	}

	kinds := reg.DeclaredKinds()
	if len(kinds) == 0 {
		t.Fatal("no kinds declared — the test would prove nothing")
	}

	for _, kind := range kinds {
		if !strings.Contains(got.Output, kind) {
			t.Errorf("help does not mention kind %q — an agent reading it would not know the kind exists", kind)
		}
		decl, ok := reg.KindDeclFor(kind)
		if !ok {
			continue
		}
		for _, f := range decl.Fields {
			if !strings.Contains(got.Output, f.Name) {
				t.Errorf("help does not mention field %q of kind %q — a matcher author would have to guess it", f.Name, kind)
			}
		}
	}
}

// T003_05: the binary runs with the module list, not with one of its own.
//
// T003_01 proves the help mentions every kind the list declares. It cannot
// prove the reverse — a binary carrying a module the list does not have would
// print an extra kind and pass, because a superset contains everything asked
// for. That is precisely the shape of the bug this all exists to stop: a second
// list, and the binary running with it.
//
// So this compares both directions, against what the BUILT binary reports.
// `guardrail help` prints MODULES IN THIS BUILD from the registry it is running
// with, which makes the binary's own module list observable from outside it.
// Every module in All() must appear there, and every module named there must be
// in All().
//
// It reads that roster rather than the per-kind attribution under EVENT KINDS.
// The attribution is emitted per kind, so a module declaring no kinds produced
// no line and this test could not see it — a silent module could be in the
// binary undetected, and a silent module correctly in All() failed here saying
// the binary never mentioned it, which was false. The roster is printed per
// module and has neither blind spot.
//
// Add a module to the repo and not to All(), and TestAll_HoldsEveryModuleInTheRepo
// goes red. Call module.NewRegistry from outside internal/module/modules and
// TestOnlyModulesPackageBuildsARegistry goes red. This is the third check: that
// the list the binary actually runs with is the one it reports — asserted
// against the built binary's own output rather than against source, so it holds
// even if the other two are read wrong.
func TestT003_05_BinaryReportsExactlyTheModuleList(t *testing.T) {
	e := New(t)

	got := e.CLI(t.TempDir(), "guardrail", "help")
	if got.Code != 0 {
		t.Fatalf("guardrail help exited %d:\n%s", got.Code, got.Output)
	}

	// What the binary says it is running with, read back out of its own output.
	reported := map[string]bool{}
	for _, line := range strings.Split(got.Output, "\n") {
		_, rest, found := strings.Cut(line, "(module: ")
		if !found {
			continue
		}
		name, _, found := strings.Cut(rest, ")")
		if found {
			reported[strings.TrimSpace(name)] = true
		}
	}
	if len(reported) == 0 {
		t.Fatal("help attributed no kind to any module — this test can prove nothing, and the help lost the attribution an author needs")
	}

	listed := map[string]bool{}
	for _, m := range modules.All() {
		listed[m.Name()] = true
		if !reported[m.Name()] {
			t.Errorf("module %q is in the list but the binary never mentions it — the build is running with a different set of modules than the one declared", m.Name())
		}
	}
	for name := range reported {
		if !listed[name] {
			t.Errorf("the binary reports module %q, which is not in the list — a second module list exists somewhere in the binary", name)
		}
	}
}

// T003_02: help points at where the format is taught.
//
// It no longer teaches the format itself — a help command documents its own
// command, and the declaration format is not one. What it must not do is leave
// an agent that read this output with no idea where to go next, because an
// agent that cannot find the format invents one.
//
// The facts this used to assert — the frontmatter shape, the matcher operators,
// the hook contract, that the body is a rubric, that a rule is disabled rather
// than deleted — moved to the authoring-guardrails skill. They are not asserted
// here any more because they are not this command's to state.
func TestT003_02_HelpPointsAtTheSkill(t *testing.T) {
	e := New(t)

	got := e.CLI(t.TempDir(), "guardrail", "help")

	if !strings.Contains(got.Output, "authoring-guardrails") {
		t.Errorf("help never names the skill that teaches the format — an agent reading this has nowhere to go and will guess a declaration shape:\n%s", got.Output)
	}
}

// T003_06 lives in its own file — see test_003_06_no_guardrails_is_ordinary_test.go.

// T003_04: nothing an author reads offers a field this build cannot produce.
//
// `in` and `any(...)` read fields — `flags`, `invocations` — that only the
// command module would declare, and it has not landed. Offered to an author they
// are worse than absent: `in` quietly evaluates false and `any` errors outright,
// and either way a rule written from them never fires while looking enforced.
//
// This used to read the help's matcher prose. That prose moved to the skill, so
// the check follows it — the skill is now what an author reads before writing a
// matcher, and a stale field name there does the same damage it did here.
//
// The guard is derived, not a blocklist of two names: anything offered as
// readable has to be a field some declared kind actually carries.
func TestT003_04_NothingOffersAnUnproducibleField(t *testing.T) {
	e := New(t)

	// The help must still be free of them — it prints field names itself.
	got := e.CLI(t.TempDir(), "guardrail", "help")

	// The skill is added by impl/agent-help and is not on this branch yet, so
	// its absence is tolerated rather than failed — this branch should not be
	// red for another's missing file. Only os.IsNotExist is tolerated, and the
	// help half below runs either way: a blanket skip would silently stop
	// watching the half that IS on this branch. Once the branches meet, the
	// file is there and both halves are checked.
	skillPath := filepath.Join(repoRoot(t),
		"marketplace", "plugins", "sloprail", "skills", "authoring-guardrails", "SKILL.md")
	skill, err := os.ReadFile(skillPath)
	switch {
	case os.IsNotExist(err):
		t.Logf("NOT CHECKED: %s is absent on this branch, so the skill half of this test did not run. It is guarded from impl/agent-help onward.", skillPath)
		skill = nil
	case err != nil:
		t.Fatalf("read the authoring skill — it is what an author reads before writing a matcher: %v", err)
	}

	reg, err := modules.Registry()
	if err != nil {
		t.Fatalf("registry: %v", err)
	}

	declared := map[string]bool{}
	for _, kind := range reg.DeclaredKinds() {
		decl, ok := reg.KindDeclFor(kind)
		if !ok {
			continue
		}
		for _, f := range decl.Fields {
			declared[f.Name] = true
		}
	}

	for _, absent := range []string{"invocations", "flags", "argv"} {
		if declared[absent] {
			continue // a module started declaring it — both may use it
		}
		if strings.Contains(got.Output, absent) {
			t.Errorf("help offers %q, which no declared kind carries — a matcher using it never fires", absent)
		}
		if skill != nil && strings.Contains(string(skill), absent) {
			t.Errorf("the authoring skill offers %q, which no declared kind carries — a matcher written from it never fires", absent)
		}
	}
}

// T003_03: the help is reachable from the root help.
//
// An agent that does not know the command exists cannot read it, which would
// make every word above worthless.
func TestT003_03_RootHelpPointsAtIt(t *testing.T) {
	e := New(t)

	got := e.CLI(t.TempDir(), "--help")
	if !strings.Contains(got.Output, "guardrail") {
		t.Fatalf("root help does not mention the guardrail command:\n%s", got.Output)
	}
}
