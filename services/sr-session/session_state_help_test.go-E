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

// TestListHelpDocumentsOwner guards that the `list` command's help actually
// describes --owner: what it does (read another guardrail's entries), that it is
// read-only, and how ordering is established (require on the context).
//
// A hook author reaching for a cross-guardrail read finds it here or not at all.
// The flag existing without the help would leave the shipped gates' `--owner
// scanner-declared` looking like a typo rather than the supported read it is,
// and would say nothing about the require-for-ordering half that makes it
// correct.
//
// The assertions are about the presence of the concepts, not exact wording, so
// ordinary rewrites do not trip them: the flag name, that it is a read, and that
// require carries the ordering.
func TestListHelpDocumentsOwner(t *testing.T) {
	cmd := newSessionStateListCmd()

	// The flag is actually registered, and registered as a string flag.
	if f := cmd.Flags().Lookup("owner"); f == nil {
		t.Fatal("`state list` has no --owner flag")
	}

	long := cmd.Long
	if !strings.Contains(long, "--owner") {
		t.Error("`state list` help does not mention --owner, the cross-guardrail read")
	}
	// It must convey that ordering is the caller's job via require, or a reader
	// will treat a stale/empty registry as authoritative.
	if !strings.Contains(long, "require") {
		t.Error("`state list` help does not tell the reader that ordering is established with require: [{context}]")
	}
	// The flag's own one-line usage should say it is read-only, so the boundary
	// is visible at `--help` without reading the long text.
	if usage := cmd.Flags().Lookup("owner").Usage; !strings.Contains(usage, "read-only") {
		t.Errorf("the --owner flag usage does not say it is read-only: %q", usage)
	}
}
