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
//	3  cite refused — it cannot safely resolve the END USER's own words here,
//	   either because the resolved trajectory is a sub-agent's (its "user" messages
//	   are the parent's dispatch), or because no --path was given and a sub-agent
//	   context cannot be RULED OUT (see runSessionTrajectoryCite)
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
// neighbour. It covers BOTH sub-agent refusals — a resolved sub-agent trajectory
// and an unruleable no-path context — because to a scripting caller they are the
// one outcome "cite will not answer here", distinct from absence, and the remedy
// for both is the same: pass an explicit --path to a trajectory that is the end
// user's.
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

	explicitPath, _ := cmd.Flags().GetString("path")

	path, payload, err := resolveTrajectory(cmd)
	if err != nil {
		// A path that was named or guessed and found wanting — a session id that
		// is not a name, a file belonging to another conversation. Reported as
		// itself, and exits non-zero through the root; this is not the "no match"
		// case, which is a resolvable trajectory that simply did not hold the
		// quote.
		return err
	}

	// Fail closed when a sub-agent context cannot be RULED OUT. This is the no-path
	// half of the guard, and the reason it exists is a fact about Claude Code that
	// is documented rather than guessed:
	//
	// cite is agent-facing — an ordinary TOOL CALL the agent makes mid-work, not a
	// hook — and a tool call's process environment carries NO signal that it is
	// running inside a sub-agent rather than the root. Confirmed against the Claude
	// Code docs, primary sources:
	//
	//   - The fields that DO name a sub-agent — agent_id ("Present only when the
	//     hook fires inside a subagent call. Use this to distinguish subagent hook
	//     calls from main-thread calls") and agent_type — are delivered ONLY in a
	//     HOOK's JSON stdin payload, never as environment variables to a tool call.
	//     [code.claude.com/docs/en/hooks.md, "Common input fields"]
	//   - The env vars Claude Code sets for a tool call / Bash are CLAUDE_PROJECT_DIR,
	//     CLAUDE_PLUGIN_ROOT, CLAUDE_PLUGIN_DATA, CLAUDE_PLUGIN_OPTION_<KEY>,
	//     CLAUDE_CODE_REMOTE, CLAUDE_CODE_BRIDGE_SESSION_ID and CLAUDE_EFFORT — none
	//     of which names the agent or distinguishes a sub-agent from a root.
	//     [code.claude.com/docs/en/hooks.md, environment-variable table]
	//   - There is no CLAUDE_SESSION_ID exported to a tool call: the session id
	//     reaches a script only as the `session_id` hook-stdin field or via
	//     `claude -p --output-format json`, and transcripts live at
	//     ~/.claude/projects/<project>/<session-id>.jsonl with the id in the PATH,
	//     not the environment. [code.claude.com/docs/en/sessions.md]
	//
	// This is the engine's own settled principle too, pinned by
	// TestInvariant_identity_comes_from_the_hook: a sub-agent's identity is sourced
	// from what the harness REPORTS on the payload, never sniffed from the agent's
	// own process. So cite has exactly two safe ways to know whose words a
	// trajectory holds, and a third that is a trap:
	//
	//   1. An explicit --path names a CONCRETE FILE, and IsSubagentTranscript reads
	//      the file itself (its meta companion, or the isSidechain its origin
	//      carries) to settle whether it is a sub-agent's. Safe, whoever is asking —
	//      handled below.
	//   2. A HOOK payload is self-disambiguating: a sub-agent's hook ALWAYS carries
	//      agent_transcript_path or agent_id (see hookio's record()), so record()
	//      resolves the sub-agent's OWN path, which IsSubagentTranscript then catches.
	//      A payload with only transcript_path is therefore genuinely the root's, and
	//      citing it is correct.
	//   3. A path resolved from a SESSION ID ALONE names the root and CANNOT exclude a
	//      sub-agent caller — it is exactly the reviewer's case, "if it's resolved via
	//      CLAUDE_SESSION_ID it'll always resolve to the root." Nothing in the
	//      environment can tell whether a sub-agent is the one asking, so a citation
	//      here risks grounding a claim in the parent's dispatch that a sub-agent was
	//      handed. cite REFUSES rather than mint it.
	//
	// failClosedNoPath draws the line at (3): when no --path was given and the only
	// thing that resolved the trajectory was a session id, cite cannot rule out a
	// sub-agent and refuses. It is deliberately narrow — a hook payload naming a
	// transcript_path (case 2) still resolves and cites, which is what keeps the
	// hook-invoked default working — but it closes the one door through which cite
	// could silently cite a root while running as a sub-agent's tool call.
	if explicitPath == "" && failClosedNoPath(payload) {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"sloprail: cite cannot safely resolve the user's own words with no --path here — the current session was resolvable only from a session id, which names the root even when a sub-agent is asking, and nothing in a tool call's environment can rule out a sub-agent context (Claude Code exposes agent identity only on a hook payload, not to a tool call). Pass --path to the trajectory whose user messages are the end user's, or this cannot be safely resolved in a possible sub-agent context\n")
		os.Exit(citeInSubagent)
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
	// This is the PATH-based half of the guard, and it settles the question from the
	// RESOLVED TRAJECTORY rather than the environment — because, per the CC-doc
	// finding above, the environment cannot settle it. A sub-agent asking cite about
	// its own trajectory resolves (by --path, or by a hook payload's
	// agent_transcript_path / agent_id) to a sub-agent transcript, and
	// IsSubagentTranscript reads that from the record itself: its meta companion, or
	// the isSidechain its own origin carries (see that function). This is precisely
	// the fact `describe` reports as `isSubagent`, read here to refuse rather than to
	// report.
	//
	// A ROOT citing a sub-agent's trajectory with an explicit --path is refused too,
	// and correctly: whoever runs cite, a sub-agent's user-words are the parent's
	// dispatch, so they are never a citable grounding for the end user's intent no
	// matter who reads them. Citing INTO a sub-agent is not what cite is for; reading
	// a sub-agent's trajectory for other purposes is `normalize --path`, which does
	// not refuse.
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

// failClosedNoPath reports whether a no-path cite must refuse because it cannot
// rule out a sub-agent context — the case where the trajectory would be resolved
// from a SESSION ID ALONE.
//
// It is the predicate behind guard case (3) in runSessionTrajectoryCite. record()
// resolves a path from p.SessionID only when the payload names no path and no agent
// at all (no transcript_path, no agent_transcript_path, no agent_id); that path is
// built as <project>/<session-id>.jsonl and so always names the ROOT session's
// file. Inside a sub-agent that resolution still lands on the root — the reviewer's
// "it'll always resolve to the root" — and, per the CC-doc finding above, nothing
// in a tool call's environment can tell whether a sub-agent is the caller. So a
// path reached this way cannot be proven to be the end user's, and cite fails
// closed rather than cite the parent's dispatch as the user's words.
//
// The other resolutions are NOT fail-closed, each for a reason that holds:
//
//   - transcript_path present (with or without agent fields): a hook payload, and a
//     sub-agent's hook always carries agent_transcript_path or agent_id, so record()
//     resolves the sub-agent's OWN path and the path-based IsSubagentTranscript guard
//     catches it. A transcript_path with no agent fields is genuinely the root's.
//   - nothing resolvable (empty payload — the plain agent tool call with no --path):
//     record() returns "", and cite refuses with errNoTrajectory below. That is
//     already fail-closed; it just refuses for "nothing to read" rather than for the
//     sub-agent hazard, which is the honest description of an empty payload.
//
// So this fires on exactly the one resolution that yields a usable path yet cannot
// exclude a sub-agent: session id alone. An explicit --path never reaches here —
// the caller checks the flag first — so a --path into the root is unaffected.
func failClosedNoPath(p HookPayload) bool {
	return p.TranscriptPath == "" &&
		p.AgentTranscriptPath == "" &&
		p.AgentID == "" &&
		p.SessionID != ""
}
