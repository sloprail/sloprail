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
		Args:  cobra.NoArgs,
		RunE:  runSessionQuery,
	}
	cmd.Flags().String("where", "",
		"Narrow which entries come back, over the same expression language a guardrail's matcher uses")
	cmd.Flags().Bool("whole-session", false,
		"Widen the answer to the whole session rather than the part of it not yet judged")
	cmd.Flags().Bool("include-sidechains", false,
		"Include entries belonging to sub-agents")
	return cmd
}

func runSessionQuery(cmd *cobra.Command, _ []string) error {
	where, _ := cmd.Flags().GetString("where")
	wholeSession, _ := cmd.Flags().GetBool("whole-session")
	includeSidechains, _ := cmd.Flags().GetBool("include-sidechains")

	p := readPayload(cmd)
	if p.TranscriptPath == "" {
		return fmt.Errorf("sloprail: no transcript path on the hook payload — there is no record to read")
	}

	entries, err := transcript.Read(p.TranscriptPath)
	if err != nil {
		return err
	}

	// Off by default, because a rule asking what the agent did means this
	// cycle's work: re-reading what has already been judged both wastes the
	// reading and lets a judge reach a different verdict on work the agent can
	// no longer reach.
	//
	// Where the last cycle stopped is a remembered position rather than
	// anything the record says, and what remembers it is the engine's own
	// per-session state — a separate piece of work. Until that exists there is
	// no mark to resume from, so the answer is the whole record either way.
	// That is the safe direction of the two: re-reading a turn costs a second
	// look, skipping one loses a violation for good.
	if !wholeSession {
		entries = transcript.Since(entries, readMark())
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

// readMark is where the previous cycle stopped reading.
//
// Empty until the engine's per-session state exists to hold it — see
// session-state, which owns the baseline commit and this position together
// because both are facts about what sloprail did rather than about what the
// agent did. Returning empty means the whole record, which is what a first
// cycle gets and is the erring-toward-re-reading side of the choice.
func readMark() string { return "" }
