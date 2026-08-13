package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/sessionstate"
	"github.com/sloprail/sloprail/internal/transcript"
)

// newSessionQueryCmd answers what the agent actually did.
//
// A hook script needs an answer, not a log. Left to read the record itself,
// every rule would reimplement the same traversal — telling a sub-agent's work
// from the main line, finding where this cycle begins, digging a tool name out
// of a message — and each would do it slightly differently, against a format
// that is not ours to keep stable.
//
// What comes back is entries, as JSON. Shaping them into an answer is left to
// whatever the hook already uses for JSON: a tool that selects and reshapes is
// not a thing this needs to reinvent, and one that tried would be a worse
// version of it.
func newSessionQueryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "query",
		Short: "What the agent did — the part of the session not yet judged",
		Long: `What the agent did — the part of the session not yet judged.

Reads from where the last completed cycle stopped. A session's record only
grows, so a turn already judged has not changed, and judging it again both
wastes the reading and invites a judge — a model call, not a function — to
reach a different verdict on a turn the agent can no longer reach to fix.

The position moves only when a cycle finishes. A cycle that was interrupted may
have judged nothing, so the next one reads those turns again: re-reading a turn
costs a second look, skipping one loses a violation for good. The first cycle of
a session, and any session whose position cannot be read, gets the whole record
for the same reason.

--whole-session ignores the position and reads the entire record, for a rule
asking about the session as a whole rather than about this cycle's work.

Entries belonging to sub-agents are left out unless asked for: a rule asking
what the agent did usually means the main line of work.

The answer is entries as JSON, for whatever the hook already uses to read JSON.`,
		Args: cobra.NoArgs,
		RunE: runSessionQuery,
	}
	cmd.Flags().String("where", "",
		"Narrow which entries come back, over the same expression language a guardrail's matcher uses")
	cmd.Flags().Bool("include-sidechains", false,
		"Include entries belonging to sub-agents")
	cmd.Flags().Bool("whole-session", false,
		"Read the entire record, not just the part no cycle has judged yet")
	return cmd
}

func runSessionQuery(cmd *cobra.Command, _ []string) error {
	where, _ := cmd.Flags().GetString("where")
	includeSidechains, _ := cmd.Flags().GetBool("include-sidechains")
	wholeSession, _ := cmd.Flags().GetBool("whole-session")

	p := readPayload(cmd)
	path, err := p.record()
	if err != nil {
		return err
	}
	if path == "" {
		return fmt.Errorf("sloprail: no transcript path on the hook payload — there is no record to read")
	}

	entries, err := transcript.Read(path)
	if err != nil {
		return err
	}

	if !wholeSession {
		entries = transcript.Since(entries, readMark(cmd, p))
	}

	entries, err = transcript.Filter(entries, transcript.Query{
		Where:             where,
		IncludeSidechains: includeSidechains,
	})
	if err != nil {
		return err
	}

	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetEscapeHTML(false)
	return enc.Encode(entries)
}

// readMark is the position the last completed cycle read up to, or "" for a
// session where nothing has been recorded yet.
//
// Every failure yields "", which reads the whole record. That is the safe
// direction to be wrong in and the only one: a position that cannot be read is
// not a position saying nothing needs judging, and defaulting the other way
// would have a rule report no violations because the engine could not open its
// own database. Re-reading a turn costs a second look; skipping one loses a
// violation for good.
//
// Reported on stderr rather than silently, because a session persistently
// unable to read its position is re-judging everything on every cycle, and the
// only symptom otherwise is that things get slower.
func readMark(cmd *cobra.Command, p HookPayload) string {
	store, err := openEngineState(p)
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: reading the whole session:", err)
		return ""
	}
	defer store.Close()

	mark, _, err := store.Meta(sessionstate.MetaTranscriptRead)
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: reading the whole session:", err)
		return ""
	}
	return mark
}
