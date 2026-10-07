package e2e

import "testing"

// T003_06: a project that has declared no guardrails sees no difference.
//
// The engine loads whatever is under the dot-directory, and a project with no
// dot-directory at all has no guardrails — which is not an error, it is the
// ordinary state of a project that has installed the plugin and not written a
// rule yet. A session in it has to be indistinguishable from a session in a
// project that never installed this.
//
// Driven through the mock rather than asserted against Load, because "loads
// cleanly with nothing declared" is only worth anything where the harness
// actually calls it: the plugin fires SessionStart and PreToolUse on every run,
// and a project with nothing to enforce is the case where those must stay
// silent rather than fail open or fail loudly.
//
// This carried over from the deleted `init` scenario, which established the
// same property about a freshly scaffolded directory. With no scaffold command
// the case is strictly wider — there is no directory at all — and the store's
// unit tests cover only that Load returns nothing, never that a session is
// quiet.
func TestT003_06_ProjectWithNoGuardrailsPermitsEverything(t *testing.T) {
	e := New(t)
	proj := e.Project()

	got := e.Run(proj, "s-003-06", "write a note", Turns("done",
		Write("w1", "guarded/notes.md", "hello"),
	))

	if got.Saw("denied") || got.Saw("blocked") || got.Saw("sloprail:") {
		t.Fatalf("a project with no guardrails was not silent:\n%s", got.Output)
	}
}
