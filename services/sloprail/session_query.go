package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

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
		Short: "What the agent did — the session's own record, filtered",
		Long: `What the agent did — the session's own record, filtered.

Reads the whole session. Narrowing to just the part not yet judged needs the
position the last cycle stopped at, which is not yet recorded, so nothing here
takes it as given.

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
	return cmd
}

func runSessionQuery(cmd *cobra.Command, _ []string) error {
	where, _ := cmd.Flags().GetString("where")
	includeSidechains, _ := cmd.Flags().GetBool("include-sidechains")

	p := readPayload(cmd)
	if p.TranscriptPath == "" {
		return fmt.Errorf("sloprail: no transcript path on the hook payload — there is no record to read")
	}

	entries, err := transcript.Read(p.TranscriptPath)
	if err != nil {
		return err
	}

	// There is deliberately no --whole-session flag yet.
	//
	// A rule asking what the agent did means this cycle's work, and the answer
	// should be the part of the session not already judged: re-reading settled
	// work wastes the reading and lets a judge reach a different verdict on a
	// turn the agent can no longer reach to fix. So narrowing is right, and a
	// flag to widen back out is right beside it.
	//
	// But where the last cycle stopped is a REMEMBERED position, not anything
	// the record says — the store now has sessionstate.MetaTranscriptRead
	// waiting to hold it, and reading and advancing it is the read-mark task,
	// not this one. Until that is wired, both settings of such a flag would
	// return the identical whole record, and a flag whose two positions do the
	// same thing is a promise in --help that the code does not keep. One honest
	// behaviour beats a flag that appears to do something.
	//
	// The whole record is also the safe direction to be wrong in while waiting:
	// re-reading a turn costs a second look, skipping one loses a violation for
	// good.

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
