package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/transcript"
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
//	                                  (--include-envelope also prints the whole
//	                                  question-answer envelope at that line)
//	sr-session trajectory tool-result is a cited LINE a tool_result, and its content
//	                                  (the line-oriented sibling of cite, for a
//	                                  delivery observation that names a line already)
//	sr-session trajectory normalize   the trajectory as normalized entries + events
//
// describe and cite are settled by a trajectory's path and the session's own
// records; normalize walks the entries. The answer envelope behind a citation is
// read by `cite --include-envelope` in the same call that mints the citation (it
// was once its own `envelope` subcommand — folded into cite so a grounding hook
// makes one call, not two). Grouping them is what lets a hook or an agent reach
// all three under one noun, and keeps `describe`'s output — other trajectories'
// paths — flowing straight into `normalize --path` and `cite --path`.
func newSessionTrajectoryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "trajectory",
		Short: "Read a trajectory — facts about it, a citation into it, its normalized entries",
		Long: `Read a trajectory: the record of one session's turns.

  sr-session trajectory describe    facts about a trajectory — whether it is a
                                    sub-agent's, its parent, the sub-agents it spawned
  sr-session trajectory cite        turn a substring of the user's own words into
                                    a resolvable <path>:<line> citation
                                    (--include-envelope also prints the whole
                                    question-answer envelope at that line)
  sr-session trajectory tool-result whether a cited --line is a tool_result, and
                                    its content — for a delivery observation that
                                    names a transcript line as proof of work

describe answers "what IS this file"; cite mints a citation an agent can write,
and with --include-envelope reads back the question+answers at that citation's
line in the same call; tool-result confirms a line an observation names is a real
tool-call result. All default to the trajectory the hook was invoked for, and all
accept --path to read another — the parent or a sibling that describe named.`,
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
		newSessionTrajectoryToolResultCmd(),
		newSessionTrajectoryNormalizeCmd(),
	)
	return cmd
}

// resolveTrajectory resolves which trajectory a `trajectory` subcommand reads,
// trying three sources in priority order:
//
//  1. an explicit --path, when given;
//  2. otherwise the hook payload's own record (transcript_path, or a sub-agent's
//     agent_transcript_path / agent_id), taken off stdin exactly as `query` and
//     `id` take it;
//  3. otherwise the CURRENT session's own transcript, derived from the
//     CLAUDE_CODE_SESSION_ID environment variable and the working directory.
//
// The flag WINS over the payload, which is what lets a rule or an agent read a
// sibling or the parent after `describe` names one — the spec calls for exactly
// this on all three ops. When the flag is absent the payload's record is used, and
// whatever that resolution refuses (a session id that is not a name, a guessed
// file belonging to another conversation) is refused here unchanged: the
// difference between having nothing to read and being pointed somewhere it must
// not read is worth keeping, and folding it into "no trajectory" would lose it.
//
// # The environment fallback (source 3)
//
// The agent-facing case is `cite "<quote>"` run WITHOUT --path and WITHOUT a piped
// hook payload — the agent, or a guardrail's own script, invoking the CLI directly
// mid-work. There is no payload, so record() returns "" with a nil error, and
// before this fallback the command had nothing to read. It does have something:
// Claude Code exports CLAUDE_CODE_SESSION_ID naming the current session, and the
// transcript lives at a path derived deterministically from it —
// <config>/projects/<encoded-cwd>/<session-id>.jsonl, the same layout
// HookPayload.Record() reconstructs from a payload's session_id. transcript.CurrentSessionPath
// builds exactly that path and verifies it is the session's own file before
// returning it.
//
// Resolving the CURRENT session — which is the ROOT when a sub-agent is not the
// caller, and which holds the citations cite grounds (a question the user answered is
// recorded in the root's transcript) — is sufficient for cite's purpose. It does
// not attempt to detect a sub-agent from the environment, because nothing in a
// tool call's environment distinguishes one; that judgement is made from the
// RESOLVED FILE instead (transcript.IsSubagentTranscript), which reads the record
// itself. See session_trajectory_cite.go.
//
// An empty result with a nil error is the honest "there was no trajectory to read
// and none was named" — no --path, no payload record, and no session id in the
// environment. The caller decides whether it can proceed without one. That is
// distinct from an error, which is a path that was named or guessed and found
// wanting.
//
// The payload is read LAZILY, only when --path is absent. This matters for the
// agent-facing case: `cite --path X` is run by an agent that has not piped a hook
// payload, and reading stdin unconditionally would block on an open terminal
// waiting for an EOF that never comes. With --path given there is nothing on
// stdin to want, so it is not read; the returned payload is the zero value, whose
// Cwd is empty, which searchDirFor handles by deriving the directory from the
// path itself. Without --path the command is the hook-invoked one (or the direct
// agent call whose stdin is empty), a payload is read, and — when it names no
// record — the environment fallback runs.
func resolveTrajectory(cmd *cobra.Command) (string, HookPayload, error) {
	if flag, _ := cmd.Flags().GetString("path"); flag != "" {
		return flag, HookPayload{}, nil
	}
	p := readPayloadIfWaiting(cmd)
	path, err := recordOf(p)
	if err != nil {
		return "", p, err
	}
	if path == "" {
		// No --path and nothing on the payload: the direct agent/CLI call. Resolve
		// the current session's own transcript from the environment. A resolution
		// found here is returned with the payload unchanged (its Cwd stays whatever
		// was piped — empty in the direct-call case), so searchDirFor still derives
		// the search directory from the path itself.
		if env := transcript.CurrentSessionPath(p.Cwd); env != "" {
			return env, p, nil
		}
	}
	return path, p, err
}

// errNoTrajectory is the message a subcommand prints when no trajectory could be
// resolved by any of the three sources — no --path, no record on the payload, and
// no current-session transcript derivable from CLAUDE_CODE_SESSION_ID. Shared so
// describe, cite and normalize refuse the same way.
func errNoTrajectory() error {
	return fmt.Errorf("sloprail: no trajectory to read — pass --path, invoke this where a transcript is on the hook payload, or run it in a session (with %s set) whose transcript exists", transcript.SessionIDEnv)
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
