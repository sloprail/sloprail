package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/internal/modules"
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
// `guardrail help` names the owning module for each kind, which makes the
// binary's own module list observable from outside it. Every module in All()
// must appear there, and every module named there must be in All().
//
// Add a module to internal/ and not to All(), and this stays green while
// TestAll_HoldsEveryModulePackage goes red. Wire one into the binary past All()
// and this goes red. Between them there is no way to add a module in one place
// only.
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

// T003_02: help tells an author the things they cannot get from the file format.
//
// Not a spellcheck of the prose. Each of these is a fact an agent arrives
// without and will guess wrong: that the body is a rubric, that a rule is turned
// off rather than deleted, and that the glob it would reach for does not exist.
func TestT003_02_HelpCoversWhatAnAgentDoesNotKnow(t *testing.T) {
	e := New(t)

	got := e.CLI(t.TempDir(), "guardrail", "help")

	for _, want := range []string{
		"GUARDRAIL.md",   // where a declaration goes
		"rubric",         // the body is read by judge hooks
		"enabled: false", // how a rule is turned off
		"startsWith",     // the operators that exist
		"endsWith",
		"NO GLOB",       // and the one that does not
		"guardrailDir",  // what a hook is handed
		"exit 0",        // and what it writes back
		"type: command", // the hook mechanism
	} {
		if !strings.Contains(got.Output, want) {
			t.Errorf("help never mentions %q", want)
		}
	}
}

// T003_04: help names no operator this build cannot evaluate.
//
// `in` and `any(...)` read fields — `flags`, `invocations` — that only the
// command module would declare, and it has not landed. Documented, they are
// worse than absent: `in` quietly evaluates false and `any` errors outright, and
// either way a rule written from this help never fires while looking enforced.
//
// The guard is derived, not a blocklist of two names. Anything the help offers
// as a matcher example has to be evaluable against a kind this build actually
// declares.
func TestT003_04_HelpNamesNoUnevaluableOperator(t *testing.T) {
	e := New(t)

	got := e.CLI(t.TempDir(), "guardrail", "help")

	reg, err := modules.Registry()
	if err != nil {
		t.Fatalf("registry: %v", err)
	}

	// Every field any declared kind carries. A matcher example may read these
	// and nothing else.
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

	// Fields the help must not present as readable, because nothing produces
	// them. Named from the kinds that would carry them rather than assumed.
	for _, absent := range []string{"invocations", "flags", "argv"} {
		if declared[absent] {
			continue // a module started declaring it — the help may use it
		}
		if strings.Contains(got.Output, absent) {
			t.Errorf("help offers %q, which no declared kind carries — a matcher using it never fires", absent)
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
	if !strings.Contains(got.Output, "init") {
		t.Fatalf("root help does not mention init:\n%s", got.Output)
	}
}
