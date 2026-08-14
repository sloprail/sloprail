package main

import (
	"strings"
	"testing"
)

// TestStateHelpDoesNotClaimUnwired guards the `state` command's help text
// against the claim that the store cannot be used from a hook.
//
// It carried "NOT YET WIRED. The dispatcher does not set that environment, so
// these commands fail with 'no guardrail in scope' when called from a hook"
// long after the dispatcher started setting it. The environment is set on every
// hook the engine runs — hookenv.go, and the tests beside it — and a guardrail
// calling `sr-session state set` from a dispatched hook stores and reads back
// its value.
//
// A stale disclaimer is worse here than no documentation. Someone writing a
// rule that needs to remember across cycles reads this, believes the feature is
// absent, and works around a limitation that does not exist — which is exactly
// what happened while migrating this project's hooks.
//
// The assertion is deliberately about the disclaimer rather than about the
// whole text, so ordinary rewording does not trip it.
func TestStateHelpDoesNotClaimUnwired(t *testing.T) {
	help := newSessionStateCmd().Long

	for _, claim := range []string{
		"NOT YET WIRED",
		"The dispatcher does not set that environment",
		"cannot be written on this build",
	} {
		if strings.Contains(help, claim) {
			t.Errorf("`state` help still claims the store is unavailable from a hook: %q\n"+
				"The dispatcher sets %s, %s and %s on every hook it runs, and the store works. "+
				"Remove the claim rather than the capability.",
				claim, GuardrailEnv, SessionEnv, WorkspaceEnv)
		}
	}

	// The help must still say where the scope comes from: that is the part a
	// hook author needs, and it is what makes the removed paragraph's absence
	// safe.
	if !strings.Contains(help, GuardrailEnv) {
		t.Errorf("`state` help no longer names %s, which is how a hook's scope is resolved", GuardrailEnv)
	}
}
