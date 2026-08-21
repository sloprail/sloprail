package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/transcript"
)

// Exit codes for `cite`. They ARE the interface — an agent scripts against them
// and branches without parsing stdout — so they are named here rather than left
// as bare literals, and their meanings are the spec's:
//
//	0  exactly one match — the resolvable <path>:<line> is on stdout
//	1  no match — nothing on stdout
//	2  several matches — every candidate <path>:<line>, one per line, on stdout
//	3  cite refused — the RESOLVED trajectory is a sub-agent's, whose "user"
//	   messages are the parent's dispatch rather than the end user's own words
//	   (see runSessionTrajectoryCite)
//
// citeNoMatch is 1 rather than a distinct high code because "nothing to cite" is
// the ordinary not-found, the same shape a grep or a test uses; citeAmbiguous is
// 2 so that "found, but not uniquely" is distinguishable from both success and
// absence in one integer.
//
// citeInSubagent is a THIRD outcome, above the 0/1/2 citation codes, because it is
// not a fact about whether the quote was found — it is a refusal to look at all.
// Collapsing it into 1 (no match) would tell a script "the user did not say that"
// when the truth is "cite must not answer here", and a script that retries on a
// narrower quote would loop against a command that will refuse every time. A
// distinct code lets the caller tell "not found" from "not available" — the same
// reason ambiguous got its own integer rather than being folded into either
// neighbour. The remedy is the same in every case: point --path at a trajectory
// that is the end user's own.
const (
	citeNoMatch    = 1
	citeAmbiguous  = 2
	citeInSubagent = 3
)

// newSessionTrajectoryCiteCmd turns a remembered quote into a resolvable
// citation — the one command under `trajectory` an AGENT runs, not a hook.
//
// Grounding a written claim in the user's own words means citing the message it
// came from as `<path>:<line>`, but mid-work an agent has the TEXT it remembers,
// not a line. Given a substring of a user message — or of an answer the user
// selected to an AskUserQuestion — this finds the single line that substring sits
// on, so the agent gets a citation without a `normalize | jq` dance it would
// hand-write every time.
//
// The exit code carries the outcome so a script branches on it; see the
// constants above. This is why the command controls its own exit rather than
// returning an error to the root, which would collapse "ambiguous" and "not
// found" into the one code the root gives every error.
func newSessionTrajectoryCiteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cite <quote>",
		Short: "Turn a substring of the user's own words into a <path>:<line> citation",
		Long: `Turn a remembered quote into a resolvable citation.

Given a substring of the user's own words — a plain message, or the answer the
user selected to an AskUserQuestion — find the single line it sits on in the
current trajectory and print it as <path>:<line>. The user's words only: not the
agent's prior output, and not an ordinary tool result that merely contains the
substring.

The exit code carries the outcome, so a script branches on it without parsing
stdout:

  exactly one match   the resolvable <path>:<line> on stdout, exit 0
  several matches     every candidate <path>:<line>, one per line, exit 2
  no match            nothing on stdout, exit 1
  in a sub-agent      refused on stderr, exit 3

cite is not available inside a sub-agent: there the "user" messages are the
PARENT agent's dispatch prompt, not the end user's own words, so a citation into
them would ground a claim in something the user never said. It refuses with exit
3 rather than mint that false citation.

The trajectory is auto-detected from the environment — the common case takes no
--path. Pass --path to cite into a sibling or the parent that ` + "`describe`" + ` named.`,
		Args: cobra.ExactArgs(1),
		RunE: runSessionTrajectoryCite,
	}
	cmd.Flags().String("path", "",
		"Which trajectory to search; defaults to the current one from the environment")
	return cmd
}

func runSessionTrajectoryCite(cmd *cobra.Command, args []string) error {
	quote := args[0]

	path, _, err := resolveTrajectory(cmd)
	if err != nil {
		// A path that was named or guessed and found wanting — a session id that
		// is not a name, a file belonging to another conversation. Reported as
		// itself, and exits non-zero through the root; this is not the "no match"
		// case, which is a resolvable trajectory that simply did not hold the
		// quote.
		return err
	}

	if path == "" {
		return errNoTrajectory()
	}

	// Refuse a sub-agent's TRAJECTORY. cite finds a USER message an agent may cite as
	// the grounding for a written claim — but a sub-agent's "user" messages are not
	// the end user's: they are the PARENT agent's Task/dispatch prompt, the
	// instructions one agent handed another. Citing those as "the user said this"
	// would ground a claim in words the person never wrote, which is the one thing
	// this command exists not to do (it already refuses the agent's own output for
	// the same reason). So a sub-agent's trajectory gets a refusal, not a citation.
	//
	// The judgement is made from the RESOLVED TRAJECTORY, not from the environment —
	// which is the whole reason this guard reads the file rather than sniffing a
	// variable. A tool call's process environment carries no signal that a sub-agent
	// is the caller (the fields that name a sub-agent — agent_id / agent_type —
	// arrive only on a HOOK's JSON stdin, never to a tool call), so whether a
	// trajectory is a sub-agent's has to be read from the record itself:
	// IsSubagentTranscript inspects its meta companion, or the isSidechain its own
	// origin carries (see that function). This is precisely the fact `describe`
	// reports as `isSubagent`, read here to refuse rather than to report.
	//
	// This is why the environment fallback in resolveTrajectory is safe for cite: it
	// resolves the CURRENT session — the root when a sub-agent is not the caller,
	// which holds the citations cite grounds — and if a sub-agent's OWN transcript is
	// ever what resolves (by --path, or by a hook payload's agent_transcript_path /
	// agent_id), this guard catches it here. A ROOT citing a sub-agent's trajectory
	// with an explicit --path is refused too, and correctly: whoever runs cite, a
	// sub-agent's user-words are the parent's dispatch, so they are never a citable
	// grounding for the end user's intent no matter who reads them. Citing INTO a
	// sub-agent is not what cite is for; reading a sub-agent's trajectory for other
	// purposes is `normalize --path`, which does not refuse.
	if transcript.IsSubagentTranscript(path) {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"sloprail: cite is not available in a sub-agent — its user messages are the parent agent's dispatch, not the end user's own words, so a citation into them would ground a claim in something the user never said (trajectory %s is a sub-agent's)\n",
			path)
		os.Exit(citeInSubagent)
	}

	matches, err := transcript.Cite(path, quote)
	if err != nil {
		// The trajectory could not be read at all — a broken environment, not an
		// absence of the quote. Surfaced as an error rather than as exit 1, so a
		// script does not read "the file is gone" as "the quote is not there".
		return err
	}

	switch len(matches) {
	case 0:
		// No match: nothing on stdout, exit 1. Silent on stdout is the contract —
		// a script tests the exit code, and printing a candidate here would be a
		// false citation.
		os.Exit(citeNoMatch)
	case 1:
		fmt.Fprintf(cmd.OutOrStdout(), "%s:%d\n", matches[0].Path, matches[0].Line)
		// Exit 0 is the default return; nil takes the ordinary path.
		return nil
	default:
		// Several matches: every candidate, one per line, exit 2. The agent
		// narrows the quote or shows the candidates.
		for _, m := range matches {
			fmt.Fprintf(cmd.OutOrStdout(), "%s:%d\n", m.Path, m.Line)
		}
		os.Exit(citeAmbiguous)
	}
	return nil
}
