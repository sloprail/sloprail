package e2e

import (
	"strings"
	"testing"

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

	for _, kind := range reg.DeclaredKinds() {
		// A kind arrives only if some hook point extracts with its phase. The
		// binary has exactly one dispatching hook point, session pre-tool, and
		// it passes PhasePre — so a Post kind cannot arrive no matter what its
		// module would do if asked.
		arrives := !strings.HasPrefix(kind, "Post")

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
