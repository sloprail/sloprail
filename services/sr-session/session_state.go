package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/sessionstate"
)

// newSessionStateCmd groups the three verbs a guardrail's hook uses to remember
// something past the moment it fires.
//
// A rule that spans more than one cycle needs somewhere to keep what it knows:
// a refactor is declared before it happens and reconciled after, a trigger seen
// in one cycle may be answered in the next, a loop runs until a measure is met.
// None of that fits in an event.
func newSessionStateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "state",
		Short: "What this guardrail remembers within this session",
		Long: `What this guardrail remembers within this session.

A rule spanning more than one cycle needs somewhere to keep what it knows: a
refactor declared before it happens and reconciled after, a trigger seen in one
cycle and answered in the next. None of that fits in an event.

Neither the guardrail nor the session is an argument. Both come from the
environment the engine sets when it runs a hook — ` + GuardrailEnv + `,
` + SessionEnv + `, ` + WorkspaceEnv + ` — because a hook able to name either
could read a rule it was never told about, or reach into another session.

Outside a hook there is no guardrail in scope, and these commands say so rather
than guessing which rule is asking.`,
	}
	cmd.AddCommand(newSessionStateGetCmd(), newSessionStateSetCmd(), newSessionStateListCmd())
	return cmd
}

// openSessionState opens the calling session's store.
//
// Both the guardrail and the session come from the environment the engine set
// when it ran the hook, never from arguments. A hook able to name either could
// read a rule it was never told about, or reach into another session's record.
// sr:invariant cli/state-is-scoped-to-the-calling-rule
// sr:invariant session/state-is-the-guardrails-own
func openSessionState() (sessionstate.Store, string, error) {
	guardrail := os.Getenv(GuardrailEnv)
	if guardrail == "" {
		return nil, "", fmt.Errorf("sloprail: no guardrail in scope — %s is set by the engine when it runs a hook", GuardrailEnv)
	}
	path, err := sessionDBPath(hookStateCwd(), os.Getenv(SessionEnv))
	if err != nil {
		return nil, "", err
	}
	store, err := sessionstate.Open(path)
	if err != nil {
		return nil, "", err
	}
	return store, guardrail, nil
}

// openEngineState opens the store for the session a hook payload belongs to.
//
// Distinct from openSessionState, which serves a guardrail's own hook and reads
// the environment the engine set for it. This one is for the engine's own hook
// points — session start, stop, query — which run before any guardrail is in
// scope and have no such environment. What they have is the payload, and the
// session's identity is derived from it the same way `session id` derives it:
// from where the conversation began, not from the id the harness currently
// reports, which Claude Code re-forks mid-conversation.
func openEngineState(p HookPayload) (sessionstate.Store, error) {
	id, err := stableID(p)
	if err != nil {
		return nil, err
	}
	path, err := sessionDBPath(p.stateCwd(), id)
	if err != nil {
		return nil, err
	}
	return sessionstate.Open(path)
}
