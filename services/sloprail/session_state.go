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

NOT YET WIRED. The dispatcher does not set that environment, so these commands
fail with "no guardrail in scope" when called from a hook. A guardrail that
needs to remember across cycles cannot be written on this build.`,
	}
	cmd.AddCommand(newSessionStateGetCmd(), newSessionStateSetCmd(), newSessionStateListCmd())
	return cmd
}

// openSessionState opens the calling session's store.
//
// Both the guardrail and the session come from the environment the engine set
// when it ran the hook, never from arguments. A hook able to name either could
// read a rule it was never told about, or reach into another session's record.
func openSessionState() (sessionstate.Store, string, error) {
	guardrail := os.Getenv(GuardrailEnv)
	if guardrail == "" {
		return nil, "", fmt.Errorf("sloprail: no guardrail in scope — %s is set by the engine when it runs a hook", GuardrailEnv)
	}
	path, err := sessionDBPath(os.Getenv(WorkspaceEnv), os.Getenv(SessionEnv))
	if err != nil {
		return nil, "", err
	}
	store, err := sessionstate.Open(path)
	if err != nil {
		return nil, "", err
	}
	return store, guardrail, nil
}
