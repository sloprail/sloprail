package main

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/transcript"
)

// newSessionTrajectoryEnvelopeCmd fetches the WHOLE AskUserQuestion answer
// envelope sitting at a citation's line — the question and ALL its answers, not
// the extracted answer alone.
//
// The fourth question under `trajectory`, and the natural partner of `cite`: cite
// mints a `<path>:<line>` from a remembered quote and stops there, because that
// location is all an agent writing a citation needs. But a JUDGE grounded on an
// answer-quote needs MORE — a change grounded in an AskUserQuestion answer cites
// the line the answer envelope sits on, and the answer alone ("the second option")
// is not something a judge can weigh: it cannot tell what was asked, nor which of
// several sibling answers the user meant, without the question beside it. So a
// guardrail's judge-prepare, having resolved a quote's path:line through cite,
// passes that same path and line HERE to recover the whole envelope and hand it to
// the judge (internal/transcript/envelope.go EnvelopeAt, which this wraps and which
// existed unconsumed until this command).
//
// It is a READ/emit command like `normalize`: it reads the entry at the line and
// prints what sits there, evaluating no matcher and running no guardrail.
//
// The --line is REQUIRED and names a 1-based PHYSICAL line — the line half of a
// `cite` citation, unchanged. Unlike cite, this does NOT refuse in a sub-agent:
// EnvelopeAt reads a specific line a caller already resolved (through cite, which
// DID apply the sub-agent guard), so re-guarding here would refuse a caller that
// already passed the check. What this reads back is whatever tool_result sits on
// that line, and only genuine answer envelopes are emitted — an ordinary tool's
// output on the same turn yields nothing, the same answer/output line cite draws.
func newSessionTrajectoryEnvelopeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "envelope --line <N>",
		Short: "The whole AskUserQuestion answer envelope (question + all answers) at a citation's line",
		Long: `Fetch the whole AskUserQuestion answer envelope sitting at a citation's line.

Given the <path>:<line> a ` + "`cite`" + ` citation resolved to, print the FULL answer
envelope on that entry — the question text, every answer pair, and the trailing
instruction — not the extracted answer alone. A judge grounded on an answer-quote
needs the question (and the sibling answers a user may refer to) to weigh the
answer, which cite's location does not carry.

  --path <PATH>   which trajectory to read; defaults to the hooked-in one
  --line <N>      the 1-based physical line the citation resolved to (required)

The line must name a real entry — the same line cite prints. An entry that carries
no AskUserQuestion answer (a plain message, or a turn with only ordinary tool
results) prints nothing and exits 0: there is simply no envelope to show, which is
the normal case for a message-grounded citation. One AskUserQuestion call can ask
several questions in one envelope, and one turn can carry several such tool_result
blocks, so several envelopes may print, one per block, in the order they sit on the
entry — separated by a blank line.

A line naming no entry is an error (exit non-zero), distinct from the empty-but-
fine result, so a caller can tell "nothing to add for this citation" from "this
line points nowhere".`,
		Args: cobra.NoArgs,
		RunE: runSessionTrajectoryEnvelope,
	}
	cmd.Flags().String("path", "",
		"Which trajectory to read; defaults to the one the hook was invoked for")
	cmd.Flags().String("line", "",
		"The 1-based physical line the citation resolved to (required)")
	return cmd
}

func runSessionTrajectoryEnvelope(cmd *cobra.Command, _ []string) error {
	lineStr, _ := cmd.Flags().GetString("line")
	if lineStr == "" {
		return fmt.Errorf("sloprail: --line is required — pass the line a cite citation resolved to (the <line> in <path>:<line>)")
	}
	line, err := strconv.Atoi(lineStr)
	if err != nil || line < 1 {
		return fmt.Errorf("sloprail: --line must be a positive integer (a 1-based physical line), got %q", lineStr)
	}

	path, _, err := resolveTrajectory(cmd)
	if err != nil {
		// A path named or guessed and found wanting — reported as itself, the same
		// way describe/cite/normalize report it.
		return err
	}
	if path == "" {
		return errNoTrajectory()
	}

	envelopes, err := transcript.EnvelopeAt(path, line)
	if err != nil {
		// A line that names no entry (ErrNoEntryAtLine), or a file that could not be
		// read — both surfaced as themselves, distinct from the empty-but-fine result
		// below, so a caller tells "points nowhere" from "nothing to add here".
		return err
	}

	// Each envelope on its own, blank-line separated. An empty list prints nothing
	// and exits 0 — the message-grounded citation's normal case, not an error.
	for i, envelope := range envelopes {
		if i > 0 {
			fmt.Fprintln(cmd.OutOrStdout())
		}
		fmt.Fprintln(cmd.OutOrStdout(), envelope)
	}
	return nil
}
