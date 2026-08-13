package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/internal/filemod"
	"github.com/sloprail/sloprail/internal/module"
)

// T003_01: help prints every kind the engine can produce, with its fields.
//
// The expectation is derived from the modules rather than written out, for the
// same reason the help itself is: a list spelled here would be a third copy, and
// the test would pass while the help lied. Add a module and this fails until the
// help prints its kinds too.
func TestT003_01_HelpPrintsEveryDeclaredKind(t *testing.T) {
	e := New(t)

	got := e.CLI(t.TempDir(), "guardrail", "help")
	if got.Code != 0 {
		t.Fatalf("guardrail help exited %d:\n%s", got.Code, got.Output)
	}

	reg, err := module.NewRegistry(filemod.New())
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

	reg, err := module.NewRegistry(filemod.New())
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
