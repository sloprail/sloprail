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
		Use:   "state --owner <name>",
		Short: "What a plugin remembers within this session",
		Long: `What a plugin remembers within this session.

A rule spanning more than one cycle needs somewhere to keep what it knows: a
file already judged and not worth judging again, a trigger seen in one cycle and
answered in the next. None of that fits in an event.

--owner says whose state this is, and is required. Two plugins judging the same
file keep separate records by naming themselves differently; nothing stops one
from claiming another's name, and that is stated rather than pretended otherwise
— see openSessionState for why the earlier environment-based scoping was a
guarantee that had stopped being one.

The SESSION and WORKSPACE are NOT arguments: they come from ` + SessionEnv + `
and ` + WorkspaceEnv + `. Those are not names a caller picks but facts about
where it is running, and reaching into another session's record stays closed.

EXAMPLE — remembering that a file's current content was already judged:

    hash=$(sr-file changed --turn | jq -r 'select(.path=="a.md") | .fingerprint')
    sr-session state get --owner rubrics "judged:a.md:$hash" || judge_it
    sr-session state set --owner rubrics "judged:a.md:$hash" 1

The fingerprint belongs in the key rather than the value: content edited away and
edited back is the same content, and a key that overwrote by path alone would
throw away a verdict that already covers it.`,
	}
	// Defined on the parent so all three subcommands take it identically — one
	// spelling, and a subcommand cannot come to disagree about the flag's name.
	cmd.PersistentFlags().String("owner", "", "Whose state this is, e.g. the plugin's name (required)")
	cmd.AddCommand(newSessionStateGetCmd(), newSessionStateSetCmd(), newSessionStateListCmd())
	return cmd
}

// openSessionState opens the calling session's store, under the given owner.
//
// # Why the owner is an ARGUMENT and the session is not
//
// The owner used to come from SR_GUARDRAIL, set by the engine when it dispatched
// a hook, and the reasoning was that a hook able to name itself could read a
// rule it was never told about. That held only while the engine was the sole
// thing running hooks.
//
// It no longer is. A plugin registers its own PreToolUse hook and calls this
// directly, with nothing between it and the store — so whoever exports
// SR_GUARDRAIL is the same party the variable was supposed to constrain. Keeping
// it would leave a mechanism that LOOKS like a guarantee and is not, which is
// the precise defect this product exists to catch.
//
// So the owner is named plainly by the caller, and the honesty is the point: two
// plugins judging one file keep separate records because they say different
// names, not because anything stops them lying about it.
//
// The SESSION and WORKSPACE stay in the environment, and that asymmetry is not
// an oversight. They are not names a caller chooses; they are facts about where
// it is running, established by the harness. Reaching into another session's
// record is a real risk and remains closed.
func openSessionState(owner string) (sessionstate.Store, string, error) {
	if owner == "" {
		return nil, "", fmt.Errorf("sloprail: no owner — pass --owner to say which plugin's state this is")
	}
	path, err := sessionDBPath(os.Getenv(WorkspaceEnv), os.Getenv(SessionEnv))
	if err != nil {
		return nil, "", err
	}
	store, err := sessionstate.Open(path)
	if err != nil {
		return nil, "", err
	}
	return store, owner, nil
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
	path, err := sessionDBPath(p.Cwd, id)
	if err != nil {
		return nil, err
	}
	return sessionstate.Open(path)
}
