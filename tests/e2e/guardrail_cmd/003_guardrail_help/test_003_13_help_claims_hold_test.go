package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/internal/module"
	"github.com/sloprail/sloprail/internal/module/modules"
)

// T003_13: every kind the help lists as undispatched really is undispatched,
// and every kind it does NOT list really does arrive.
//
// The help now prints a NOT DISPATCHED YET list, which is a claim about the
// engine rather than about the registry — the one kind of claim this output has
// no structural defence against. EVENT KINDS cannot be wrong because it is
// printed from the declarations; this list is printed from a map of phases
// maintained by hand, and a hand-maintained map is exactly what goes stale.
//
// Both directions, because each failure is bad in its own way. A kind listed
// here that actually fires scares an author off a rule that would have worked.
// A kind NOT listed that never fires is the silent no-op the whole document
// exists to prevent, delivered by the document itself.
//
// Ground truth is what the modules do with each phase: a kind is dispatchable
// when its owning module, asked with the phase the kind's name implies, is
// asked at all by some hook point. Only pre-tool dispatches today and only with
// PhasePre, so Post kinds are stranded — and this recomputes that rather than
// restating it, so the day a Post dispatch lands the test says so.
func TestT003_13_UndispatchedListMatchesWhatTheEngineDispatches(t *testing.T) {
	e := New(t)

	got := e.CLI(t.TempDir(), "guardrail", "help")
	if got.Code != 0 {
		t.Fatalf("guardrail help exited %d:\n%s", got.Code, got.Output)
	}

	reg, err := modules.Registry()
	if err != nil {
		t.Fatalf("registry: %v", err)
	}

	listed := undispatchedFromHelp(got.Output)

	dispatched := phasesTheEngineDispatches(t)

	for _, kind := range reg.DeclaredKinds() {
		// A kind arrives only if some hook point extracts with its phase.
		//
		// Ground truth is READ FROM THE DISPATCH SITES, not restated here. This
		// line used to be `!strings.HasPrefix(kind, "Post")` — a hand-written
		// copy of the same assumption `dispatchedPhases` in help.go encodes, so
		// the two went stale together and the test that existed to catch the
		// staleness asserted it instead. When the Post dispatch landed in
		// dispatch_post.go, help kept warning authors off Post kinds and this
		// test kept passing. A check whose expectation is a duplicate of the
		// thing it checks is not a check.
		arrives := dispatched[phaseOfKind(kind)]

		switch {
		case arrives && listed[kind]:
			t.Errorf("help lists %q as not dispatched, but it is — an author is being warned off a rule that would work", kind)
		case !arrives && !listed[kind]:
			t.Errorf("help does not warn that %q is never dispatched — a rule bound to it loads, validates and never fires", kind)
		}
	}

	if len(listed) == 0 {
		return // nothing stranded is a legitimate future state
	}
	for kind := range listed {
		if _, declared := reg.KindDeclFor(kind); !declared {
			t.Errorf("help warns about %q, which no module declares at all", kind)
		}
	}
}

// phasesTheEngineDispatches reports which module phases some hook point really
// extracts with, read off the dispatch sites in services/sr-session.
//
// Deriving it is the whole point. help.go's `dispatchedPhases` is a map
// maintained by hand, and the only honest way to check a hand-maintained map is
// against something that moves when the engine moves. Both of this test's
// earlier expectations — the map itself and the `HasPrefix(kind, "Post")` line
// above — were the same assumption written twice, so neither could catch the
// other being wrong.
//
// A source scan rather than a call: the phase is chosen at the dispatch site,
// inside a command that wants a whole session to run, so there is nothing to
// invoke that answers "which phases does this binary pass" without also doing
// the dispatching. Grepping the constant is coarse, and it is coarse in the
// direction that fails loudly — a site that stops dispatching disappears from
// the scan and the test says a kind is stranded that help does not warn about.
func phasesTheEngineDispatches(t *testing.T) map[string]bool {
	t.Helper()

	root := repoRootFromTest(t)
	dir := filepath.Join(root, "services", "sr-session")

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}

	// The phase constants as they appear at a dispatch site.
	want := map[string]string{
		"module.PhasePre":  module.PhasePre,
		"module.PhasePost": module.PhasePost,
	}

	dispatched := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		text := string(src)
		// A dispatch site is one that builds a module.Input naming a phase.
		if !strings.Contains(text, "module.InputPhase") {
			continue
		}
		for literal, phase := range want {
			if strings.Contains(text, literal) {
				dispatched[phase] = true
			}
		}
	}

	if len(dispatched) == 0 {
		t.Fatalf("found no dispatch site in %s — the scan this test's ground truth depends on has stopped matching the engine", dir)
	}
	return dispatched
}

// phaseOfKind reads a kind's timing off its name, the same way help.go does.
func phaseOfKind(kind string) string {
	if strings.HasPrefix(kind, "Post") {
		return module.PhasePost
	}
	return module.PhasePre
}

// repoRootFromTest walks up to the module root, so the scan does not depend on
// where `go test` was invoked from.
func repoRootFromTest(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above %s", dir)
		}
		dir = parent
	}
}

// undispatchedFromHelp reads back the kinds the help says never arrive.
func undispatchedFromHelp(out string) map[string]bool {
	listed := map[string]bool{}
	_, rest, found := strings.Cut(out, "NOT DISPATCHED YET")
	if !found {
		return listed
	}
	// The list runs to the blank line that ends it — the indented kind names.
	for _, line := range strings.Split(rest, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || !strings.HasPrefix(line, "  ") {
			continue
		}
		if strings.Contains(trimmed, " ") {
			break // reached the prose under the list
		}
		listed[trimmed] = true
	}
	return listed
}

// T003_07: the matcher operators the help offers all compile against a kind
// this build declares.
//
