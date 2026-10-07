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
	path, err := p.Record()
	if err != nil {
		// A session id that is not a name, a guessed file belonging to another
		// conversation, or one written in another tree. Reported as itself rather
		// than folded into "no record": the difference between having nothing to
		// read and being pointed somewhere it must not read is the whole point of
		// refusing.
		return err
	}
	if path == "" {
		return fmt.Errorf("sloprail: no transcript path on the hook payload — there is no record to read")
	}

	whole, err := transcript.Read(path)
	if err != nil {
		return err
	}
	entries := whole

	// Where this read ends, remembered before anything narrows the answer.
	//
	// Taken from the whole record rather than from what comes back, and taken
	// here rather than at the end of the cycle. Both matter. A --where that
	// matches nothing still means this much of the record was looked at, so the
	// position is about the reading and not about the verdict. And a turn
	// appended after this moment is behind this position, so the cycle that
	// ends later cannot claim to have judged it.
	offered := transcript.Mark(whole)

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

	recordOffered(cmd, p, whole, offered)

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
// sr:invariant session/judged-position-advances-to-what-was-offered
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

// recordOffered remembers how far the record was read out, for the end of the
// cycle to carry forward.
//
// This is the position a cycle is entitled to mark as judged. The alternative —
// reading the record again when the cycle ends and marking whatever is last
// then — marks turns that were appended after the reading, which no rule was
// ever shown. A turn skipped that way is skipped permanently, because a record
// only grows and nothing afterwards goes back.
//
// It only ever moves forward, including when the rules querying within one cycle
// run at once. Each gets the record as it stood when it asked, and the cycle as
// a whole saw the furthest of them. A later query that somehow reads less — a
// record truncated or replaced underneath the session — must not drag the
// position backwards into re-judging settled work.
//
// The concurrent half of that is not free, and was not true when it was first
// claimed here: these are separate hook processes, so comparing against the
// stored position and then writing let two of them lose one another's updates.
// The comparison and the write are one step against the database now — see
// advanceOffered, which is where the claim is actually kept.
//
// Failure is reported and swallowed, the same as everywhere else in this
// bookkeeping. Not recording the position costs the next cycle a re-read;
// refusing here would block the agent's work over the engine's own records.
func recordOffered(cmd *cobra.Command, p HookPayload, entries []transcript.Entry, offered string) {
	if offered == "" {
		return
	}
	store, err := openEngineState(p)
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: read position not recorded:", err)
		return
	}
	defer store.Close()

	if err := advanceOffered(store, entries, offered); err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: read position not recorded:", err)
	}
}
