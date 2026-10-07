package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

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
//	3  cite refused — the user pool was asked of a sub-agent's trajectory whose
//	   session root is not found; its "user" messages are the parent's dispatch
//	   rather than the end user's own words (see runSessionTrajectoryCite)
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
// selected to a question the agent asked — this finds the single line that substring sits
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
user selected to a question the agent asked — find the single line it sits on in the
current trajectory and print it as <path>:<line>. The user's words only, by
default: not the agent's prior output, and not an ordinary tool result that
merely contains the substring.

--source-types selects WHICH pool(s) of a trajectory the quote is resolved
against, comma-separated:

  user          the user's own words (the default): a typed message, or an
                answer to a question the agent asked. Harness-injected user-role messages
                (<system-reminder>, <task-notification>, a slash-command
                envelope) are excluded, and a tool result's body is not
                searched. This is what a task's ASK is cited against.
  tool_result   the body a tool_result produced — a command's output, a test
                that came back green. This is the pool the default REFUSES, and
                what a delivery OBSERVATION is cited against: proof the work
                happened. A quote of the user's words will NOT resolve here.
  user,tool_result   either is acceptable.

The default is "user" alone, so a call with no --source-types behaves exactly as
before. The two pools are mirror images: a quote of a command's result grounds
under tool_result and is refused under user; a quote of the user's ask grounds
under user and is refused under tool_result.

The exit code carries the outcome, so a script branches on it without parsing
stdout:

  exactly one match   the resolvable <path>:<line> on stdout, exit 0
  several matches     every candidate <path>:<line>, one per line, exit 2
  no match            nothing on stdout, exit 1
  user pool, sub-agent
  with no session     refused on stderr, exit 3

The whole session is searched: user in its root record (the end user's own
conversation) alone, tool_result in the root's and every sub-agent's record, so
a sub-agent can cite what its own tools printed. A sub-agent's "user" messages
are the PARENT agent's dispatch prompt, not the end user's own words, and are
never searched: given a sub-agent's trajectory, user is searched in the session
it was dispatched from, and when that session is not found cite refuses with
exit 3 rather than mint a false citation.

The trajectory is auto-detected from the environment — the common case takes no
--path. Pass --path to cite into a sibling or the parent that ` + "`describe`" + ` named.`,
		Args: cobra.ExactArgs(1),
		RunE: runSessionTrajectoryCite,
	}
	cmd.Flags().String("path", "",
		"Which trajectory to search; defaults to the current one from the environment")
	cmd.Flags().String("source-types", string(transcript.SourceUser),
		"Comma-separated pool(s) to resolve the quote against: `user` (the user's own "+
			"words — the default), `tool_result` (a tool's output, proof the work happened), "+
			"or `user,tool_result` for either. Defaults to user alone, today's behaviour.")
	cmd.Flags().Bool("include-envelope", false,
		"On a single match, also print the whole answer envelope at the resolved line "+
			"(the question + its answers), separated from the <path>:<line> "+
			"by a blank line. Lets a caller ground a quote AND read the question it answered in "+
			"ONE call instead of a cite followed by a separate `envelope --line`. Orthogonal to "+
			"--source-types: it reads the answer envelope at whatever line resolved, so it is "+
			"only ever non-empty for an answer-grounded (user-pool) match and prints nothing "+
			"for a tool_result-grounded one.")
	return cmd
}

func runSessionTrajectoryCite(cmd *cobra.Command, args []string) error {
	quote := args[0]

	// --source-types names the pool(s) to resolve against. Parsed BEFORE the
	// trajectory is resolved so an unknown name is refused before any file work —
	// a typo in the flag is the caller's mistake, reported as itself rather than
	// mistaken for a quote that did not match. An empty selection (all names
	// trimmed away) is refused too: a search against no pool would exit 1 (no
	// match) and read as "the user did not say that", which is not what happened.
	sourcesFlag, _ := cmd.Flags().GetString("source-types")
	sources, err := parseSourceTypes(sourcesFlag)
	if err != nil {
		return err
	}

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

	// Search the SESSION the trajectory belongs to (transcript.CiteInSession):
	// the user pool in its ROOT record alone, the tool_result pool in the root's
	// and every sub-agent's record. So a sub-agent — whose tool calls resolve the
	// current session, the root, from CLAUDE_CODE_SESSION_ID — can cite the
	// output its own tools printed, which is recorded only in its own file, and
	// a `cite --source-types tool_result '<q>' && <cmd>` chain works inside it.
	//
	// The user's words are never searched in a sub-agent's record: its "user"
	// messages are the PARENT agent's Task/dispatch prompt, the instructions one
	// agent handed another, and citing those as "the user said this" would ground
	// a claim in words the person never wrote. Handed a sub-agent's trajectory
	// (by --path, or a hook payload's agent_transcript_path / agent_id), the user
	// pool is searched in the root it was dispatched from — and when that root is
	// not where the layout puts it, cite refuses with exit 3 rather than search
	// the sub-agent's dispatch. Whether a trajectory is a sub-agent's is read from
	// the record itself (IsSubagentTranscript: its meta companion, or the
	// isSidechain its origin carries), never from the environment: a tool call's
	// process environment carries no signal that a sub-agent is the caller.
	matches, err := transcript.CiteInSession(path, quote, sources)
	if errors.Is(err, transcript.ErrNoSessionRoot) {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"sloprail: cite is not available in a sub-agent for the user's words — its user messages are the parent agent's dispatch, not the end user's own words, and the session it was dispatched from is not found, so a citation into them would ground a claim in something the user never said (trajectory %s is a sub-agent's). %s\n",
			path, transcript.SubagentUserAdvice)
		os.Exit(citeInSubagent)
	}
	if errors.Is(err, transcript.ErrSubagentUnlinked) {
		// A harness that cannot link a sub-agent to its parent: cite answers nothing from
		// one, in any pool (transcript.CitationsUnavailable).
		fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: cite is not available in a sub-agent (trajectory %s is a sub-agent's): %v\n", path, err)
		os.Exit(citeInSubagent)
	}
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
		// false citation. stderr always says so, and why when it can: a
		// sub-agent quoted its parent's prompt, or the words are in a tool result
		// the pool leaves out (sloprail's own output, a sub-agent's reply). A bare
		// exit 1 sent agents hunting for a typo in a verbatim quote.
		msg := fmt.Sprintf("sloprail: the quote is not in the %s of this session word for word (only whitespace may differ)", poolNames(sources))
		if hint := transcript.UnresolvedHint(path, quote, sources); hint != "" {
			msg = fmt.Sprintf("sloprail: the quote does not resolve in the %s of this session. %s", poolNames(sources), hint)
		}
		fmt.Fprintln(cmd.ErrOrStderr(), msg)
		os.Exit(citeNoMatch)
	case 1:
		fmt.Fprintf(cmd.OutOrStdout(), "%s:%d\n", matches[0].Path, matches[0].Line)
		// --include-envelope folds the follow-up `envelope --line` into this one
		// call: after the citation, print the whole answer envelope sitting at the
		// resolved line (the question the user answered, plus the answers), so a
		// grounding prepare reads a quote AND the question in a single invocation
		// rather than citing and then resolving the same line again. The envelope
		// is the SAME shape `envelope` emits — each on its own, blank-line
		// separated, after a blank line that separates it from the citation. A
		// message-grounded quote has no envelope, which prints nothing (exit still
		// 0): the addition never turns a valid citation into a failure.
		if include, _ := cmd.Flags().GetBool("include-envelope"); include {
			envelopes, err := transcript.EnvelopeAt(matches[0].Path, matches[0].Line)
			if err != nil {
				// The citation stands; only the envelope add-on could not be read.
				// Report it (non-zero via the root) so a caller that asked for the
				// envelope hears it was not delivered, rather than silently getting
				// a citation with no envelope it cannot tell from "no envelope here".
				return err
			}
			for _, envelope := range envelopes {
				fmt.Fprintln(cmd.OutOrStdout())
				fmt.Fprintln(cmd.OutOrStdout(), envelope)
			}
		}
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

// parseSourceTypes turns the --source-types flag into the SourceType slice Cite
// searches. It splits on commas, trims each name, and validates it against the
// pools transcript knows — an unknown name (a typo, an entry type like `assistant`
// that is not a citable pool) is refused with the value the caller wrote, and a
// selection that trims down to nothing is refused too. Both are the caller's
// mistake, and reporting them as errors (which exit non-zero through the root)
// keeps them distinct from exit 1, "the quote was not found in the chosen pool".
func parseSourceTypes(flag string) ([]transcript.SourceType, error) {
	var sources []transcript.SourceType
	for _, name := range strings.Split(flag, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		s, ok := transcript.ParseSourceType(name)
		if !ok {
			return nil, fmt.Errorf(
				"sloprail: unknown --source-types value %q: the pools are %q and %q (or both, comma-separated)",
				name, transcript.SourceUser, transcript.SourceToolResult)
		}
		sources = append(sources, s)
	}
	if len(sources) == 0 {
		return nil, fmt.Errorf(
			"sloprail: --source-types named no pool; give %q, %q, or both comma-separated",
			transcript.SourceUser, transcript.SourceToolResult)
	}
	return sources, nil
}

// poolNames says which pools a cite searched, for its no-match message.
func poolNames(sources []transcript.SourceType) string {
	var names []string
	for _, s := range sources {
		switch s {
		case transcript.SourceUser:
			names = append(names, "user's messages")
		case transcript.SourceToolResult:
			names = append(names, "tool output")
		}
	}
	return strings.Join(names, " or ")
}
