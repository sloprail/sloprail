package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/transcript"
)

// Exit codes for `tool-result`. They ARE the interface — a guardrail's script
// branches on them without parsing stdout — and mirror cite's shape:
//
//	0  the cited line IS a tool_result — its content is on stdout
//	1  the cited line is NOT a tool_result (a user message, an assistant turn,
//	   no entry at that line) — nothing on stdout
//	3  refused — the RESOLVED trajectory is a sub-agent's, whose records are the
//	   parent's dispatch rather than the end user's own session
//
// 1 is the ordinary "not the thing asked for", the same not-found shape cite uses;
// 3 is the same sub-agent refusal cite makes, kept as its own integer so a script
// tells "not a tool_result" from "must not look here". There is no ambiguous (2):
// a physical line names at most one entry, so a line either is a tool_result or is
// not — never several.
const (
	toolResultNotAResult = 1
	toolResultInSubagent = 3
)

// newSessionTrajectoryToolResultCmd resolves whether a cited LINE of a trajectory is
// a tool_result the session produced, and prints that result's content.
//
// This is the LINE-oriented sibling of `cite`. cite turns a remembered QUOTE into a
// location; a delivery OBSERVATION, by contrast, is a `<abs-jsonl>:<ranges>`
// citation the agent already wrote, and what a rule needs to confirm is not "where
// is this quote" but "is the entry at THIS LINE a tool_result, and what did it say".
// A task's observations cite proof the work happened — a test that came back green —
// and this answers, deterministically, whether the cited line carries such a result
// rather than the agent's own prose. It is the check the old guardrail named as the
// missing piece (there was no "this line is a real tool-call result" verb).
//
// It is built on transcript.ToolResultAt, which reads the SAME tool_result pool
// `cite --source-types tool_result` searches — so "is a tool_result" means exactly
// what that source type means, one authority rather than a second entry-type test.
func newSessionTrajectoryToolResultCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tool-result",
		Short: "Report whether a cited line is a tool_result, and print its content",
		Long: `Report whether a cited line of a trajectory is a tool_result.

A delivery OBSERVATION cites a line of the session transcript as proof the work
happened — a test that came back green, a command whose output is the evidence.
Given --line, this confirms the entry at that line IS a tool_result (not the
user's words, not the agent's narration) and prints the result's content:

  the line is a tool_result       its content on stdout, exit 0
  the line is not a tool_result   nothing on stdout, exit 1
  in a sub-agent                  refused on stderr, exit 3

It is the line-oriented counterpart to ` + "`cite --source-types tool_result`" + `,
which takes a quote; this takes the line an observation already names. Built on the
same tool_result classification, so the two agree on what a tool_result is.

The trajectory is auto-detected from the environment — the common case takes no
--path. Pass --path to read a sibling or the parent that ` + "`describe`" + ` named.`,
		Args: cobra.NoArgs,
		RunE: runSessionTrajectoryToolResult,
	}
	cmd.Flags().String("path", "",
		"Which trajectory to read; defaults to the current one from the environment")
	cmd.Flags().Int("line", 0,
		"The 1-based physical line of the transcript entry to classify (required)")
	return cmd
}

func runSessionTrajectoryToolResult(cmd *cobra.Command, _ []string) error {
	line, _ := cmd.Flags().GetInt("line")
	if line <= 0 {
		// A missing or non-positive --line is the caller's mistake, reported as
		// itself (non-zero via the root), not folded into "not a tool_result": there
		// is no line to classify, which is different from a line that is not one.
		return fmt.Errorf("sloprail: --line must be a positive line number, got %d", line)
	}

	path, _, err := resolveTrajectory(cmd)
	if err != nil {
		return err
	}
	if path == "" {
		return errNoTrajectory()
	}

	// Refuse a sub-agent's trajectory, the same judgement and for the same reason as
	// cite: a sub-agent's records are the parent's dispatch, not the end user's
	// session, so an observation cited into them is not proof of the user's work.
	// The fact is read from the RESOLVED FILE, not the environment (a tool call's
	// environment carries no sub-agent signal), exactly as cite does.
	if transcript.IsSubagentTranscript(path) {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"sloprail: tool-result is not available in a sub-agent — its records are the parent agent's dispatch, not the end user's session (trajectory %s is a sub-agent's)\n",
			path)
		os.Exit(toolResultInSubagent)
	}

	text, ok, err := transcript.ToolResultAt(path, line)
	if err != nil {
		// The trajectory could not be read at all — a broken environment, not a line
		// that is merely not a tool_result. Surfaced as an error so a script does not
		// read "the file is gone" as "the line is not a result".
		return err
	}
	if !ok {
		// Not a tool_result: nothing on stdout, exit 1. Silent stdout is the
		// contract — a script tests the exit code, and printing here would let a
		// non-result be read as one.
		os.Exit(toolResultNotAResult)
	}
	fmt.Fprintln(cmd.OutOrStdout(), text)
	return nil
}
