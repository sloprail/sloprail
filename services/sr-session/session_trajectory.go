package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newSessionTrajectoryCmd is the parent of the commands that read a trajectory —
// the record of one session's turns — rather than acting on the session's own
// engine state.
//
// It is a GROUP, not a command of its own: `trajectory` alone does nothing but
// list what sits under it. Three questions live here, and they are different
// enough to be separate subcommands rather than flags on one:
//
//	sr-session trajectory describe    facts ABOUT a trajectory (is it a
//	                                  sub-agent's, what spawned it, what it spawned)
//	sr-session trajectory cite        turn a remembered quote into <path>:<line>
//	sr-session trajectory envelope    the whole AskUserQuestion answer envelope at
//	                                  a citation's line (question + all answers)
//	sr-session trajectory normalize   the trajectory as normalized entries + events
//
// describe and cite are settled by a trajectory's path and the session's own
// records; envelope reads back the entry a cite citation resolved to; normalize
// walks the entries. Grouping them is what lets a hook or an agent reach all four
// under one noun, and keeps `describe`'s output — other trajectories' paths —
// flowing straight into `normalize --path`, `cite --path` and `envelope --path`.
func newSessionTrajectoryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "trajectory",
		Short: "Read a trajectory — facts about it, a citation into it, its normalized entries",
		Long: `Read a trajectory: the record of one session's turns.

  sr-session trajectory describe    facts about a trajectory — whether it is a
                                    sub-agent's, its parent, the sub-agents it spawned
  sr-session trajectory cite        turn a substring of the user's own words into
                                    a resolvable <path>:<line> citation
  sr-session trajectory envelope    the whole AskUserQuestion answer envelope at a
                                    citation's line — the question and all answers

describe answers "what IS this file"; cite mints a citation an agent can write;
envelope reads back the entry a cite citation resolved to, question included. All
default to the trajectory the hook was invoked for, and all accept --path to read
another — the parent or a sibling that describe named.`,
		Args: cobra.NoArgs,
		// A bare `trajectory` with no subcommand is a usage error, not a no-op:
		// print help and say so, the same as cobra's own default for a group.
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(
		newSessionTrajectoryDescribeCmd(),
		newSessionTrajectoryCiteCmd(),
		newSessionTrajectoryEnvelopeCmd(),
		newSessionTrajectoryNormalizeCmd(),
	)
	return cmd
}

// trajectoryPath resolves which trajectory a `trajectory` subcommand reads: the
// explicit --path when given, otherwise the one the hook was invoked for, taken
// off the payload exactly as `query` and `id` take it.
//
// The flag WINS over the payload when present, which is what lets a rule or an
// agent read a sibling or the parent after `describe` names one — the spec calls
// for exactly this on all three ops. When the flag is absent the payload's record
// is used, and whatever that resolution refuses (a session id that is not a name,
// a guessed file belonging to another conversation) is refused here unchanged:
// the difference between having nothing to read and being pointed somewhere it
// must not read is worth keeping, and folding it into "no trajectory" would lose
// it.
//
// An empty result with a nil error is the honest "there was no trajectory to
// read and none was named" — the caller decides whether it can proceed without
// one. That is distinct from an error, which is a path that was named or guessed
// and found wanting.
//
// The payload is read LAZILY, only when --path is absent. This matters for the
// agent-facing case: `cite --path X` is run by an agent that has not piped a hook
// payload, and reading stdin unconditionally would block on an open terminal
// waiting for an EOF that never comes. With --path given there is nothing on
// stdin to want, so it is not read; the returned payload is the zero value, whose
// Cwd is empty, which searchDirFor handles by deriving the directory from the
// path itself. Without --path the command is the hook-invoked one, a payload is
// on stdin, and it is read as query and id read it.
//
// # Why not resolve the current session from CLAUDE_SESSION_ID when --path is absent
//
// The reviewer asked whether cite with no --path should resolve the current
// session from CLAUDE_SESSION_ID rather than from a piped payload. It cannot, and
// the reason is documented in the Claude Code docs rather than guessed:
//
//   - There is no CLAUDE_SESSION_ID in a tool call's environment. The session id
//     reaches a script only as the `session_id` field of a HOOK's JSON stdin (or via
//     `claude -p --output-format json`); transcripts are stored at
//     ~/.claude/projects/<project>/<session-id>.jsonl with the id in the PATH, not
//     the environment. [code.claude.com/docs/en/sessions.md;
//     code.claude.com/docs/en/hooks.md, "Common input fields"]
//   - Even a session id, however obtained, does not distinguish a sub-agent from the
//     root: it names the root's file either way. The fields that DO mark a sub-agent
//     (agent_id / agent_type) are delivered only on a hook payload, never to a tool
//     call. [code.claude.com/docs/en/hooks.md]
//
// So an env-var branch would key the agent-facing path on a variable that is empty
// in exactly the context cite runs in, and even a non-empty one would resolve the
// root while a sub-agent was asking — the very citation cite must not mint. The two
// resolutions cite already has are the whole of it: an explicit --path (the agent
// at a terminal), and a hook payload record() reads (the hook script). What the
// reviewer's concern demands is not a third resolution but a REFUSAL: cite's own
// RunE fails closed when the only thing that could resolve the trajectory is a
// session id, precisely because that names the root and cannot exclude a sub-agent
// caller — see failClosedNoPath in session_trajectory_cite.go. Were the harness to
// start exporting a session id to tool calls, it still would not belong here as a
// silent resolution, because it could not tell whose session it named.
func resolveTrajectory(cmd *cobra.Command) (string, HookPayload, error) {
	if flag, _ := cmd.Flags().GetString("path"); flag != "" {
		return flag, HookPayload{}, nil
	}
	p := readPayload(cmd)
	path, err := p.record()
	return path, p, err
}

// errNoTrajectory is the message a subcommand prints when no trajectory could be
// resolved — no --path, and no record on the payload. Shared so describe and cite
// refuse the same way.
func errNoTrajectory() error {
	return fmt.Errorf("sloprail: no trajectory to read — pass --path, or invoke this where a transcript is on the hook payload")
}

// searchDirFor is where a trajectory's session keeps its OTHER trajectories — the
// directory `describe` searches to correlate a sub-agent back to its parent, and
// the same directory `session id` derives for the identity walk.
//
// Derived from the trajectory's own location rather than from the reported
// working directory, for the reason projectDirOf gives: a sub-agent's record is
// nested under <project>/<session>/subagents/, and the session's other records
// sit one level above that nesting; a sub-agent dispatched into an isolated
// worktree reports THAT worktree as its cwd while the harness still nests its
// record under the dispatching session's project directory, so the cwd would name
// a directory the sibling trajectories are not in.
func searchDirFor(path string, p HookPayload) string {
	return projectDirOf(path, p.Cwd)
}
