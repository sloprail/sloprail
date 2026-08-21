package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/transcript"
)

// SessionIDEnv is the environment variable Claude Code exports naming the
// CURRENT session's id, to a tool call and to a hook alike.
//
// It is how `cite` (and the other `trajectory` reads) auto-detect "the
// trajectory we are running in right now" when no --path is given and no hook
// payload is on stdin — the plain case of an agent, or a guardrail's own script,
// invoking the CLI directly. See resolveTrajectory's env fallback and
// currentSessionTranscript.
//
// Note the CODE in the name: it is CLAUDE_CODE_SESSION_ID, not CLAUDE_SESSION_ID.
// An earlier draft of this file asserted no session id reached a tool call at all;
// that was wrong — this variable is set, and equals the session's real id.
const SessionIDEnv = "CLAUDE_CODE_SESSION_ID"

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
//	                                  AskUserQuestion answer envelope at that line)
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
                                    AskUserQuestion answer envelope at that line)
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
// HookPayload.record() reconstructs from a payload's session_id. currentSessionTranscript
// builds exactly that path and verifies it is the session's own file before
// returning it.
//
// Resolving the CURRENT session — which is the ROOT when a sub-agent is not the
// caller, and which holds the citations cite grounds (an AskUserQuestion answer is
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
	p := readPayload(cmd)
	path, err := p.record()
	if err != nil {
		return "", p, err
	}
	if path == "" {
		// No --path and nothing on the payload: the direct agent/CLI call. Resolve
		// the current session's own transcript from the environment. A resolution
		// found here is returned with the payload unchanged (its Cwd stays whatever
		// was piped — empty in the direct-call case), so searchDirFor still derives
		// the search directory from the path itself.
		if env := currentSessionTranscript(p.Cwd); env != "" {
			return env, p, nil
		}
	}
	return path, p, err
}

// currentSessionTranscript resolves the CURRENT session's own transcript from the
// environment, or "" when it cannot.
//
// The session id is CLAUDE_CODE_SESSION_ID, which Claude Code exports to a tool
// call and a hook alike (see SessionIDEnv). The file is at the harness's standard
// location, <config>/projects/<encoded-cwd>/<session-id>.jsonl, built with the
// SAME helpers HookPayload.record() uses for a payload's session_id —
// transcript.ProjectDir over transcript.ConfigDir — so the two agree byte for
// byte. The encoding is Claude Code's projects-dir scheme (every non-alphanumeric
// byte of the RESOLVED working directory becomes '-'), NOT the git-root-anchored
// encodeWorkspace this service keys its OWN state under: the projects dir encodes
// the process's working directory verbatim, so an agent that ran cite from a
// subdirectory would resolve to the wrong directory under encodeWorkspace.
//
// cwd is the payload's reported working directory when there is one; when there is
// not — the direct agent/CLI call, whose payload is the zero value — the process's
// own working directory is used, which is the directory the agent is running cite
// from and the one Claude Code filed the transcript under.
//
// The join is guarded for the same reasons record()'s own session-id branch is: a
// session id is a name, and a name that carried a path separator, or a guessed file
// colliding with an unrelated conversation, or one written in a sibling checkout
// that encodes to the same project directory, would otherwise resolve silently to
// the wrong record. BelongsToSession asks the file whether it is this session's;
// BelongsToTree asks whether it was written in this tree.
//
// This resolution differs from record()'s in one deliberate way: it requires the
// derived file to EXIST, and returns "" when it does not. record() is permissive
// about a missing file because a payload's session_id is authoritative — the
// harness handed it over — and the SessionStart case must resolve a path before its
// record is written. Here the session id comes from an environment variable that
// may be stale or a foreign session's (a nested process inherits its launcher's
// CLAUDE_CODE_SESSION_ID), so a derived path with no file behind it is not "the
// trajectory we are running in right now" — it is nothing to read. Returning "" then
// lets the caller report a clean "no trajectory" rather than surfacing a
// file-not-found from transcript.Cite, and makes an inherited-but-irrelevant session
// id harmless: no matching file, no resolution.
func currentSessionTranscript(cwd string) string {
	sessionID := strings.TrimSpace(os.Getenv(SessionIDEnv))
	if sessionID == "" {
		return ""
	}
	// A session id is a name, never a path — the same refusal record() and
	// sessionDBPath make. Both separators are refused because Windows accepts both;
	// "." and ".." are named because they traverse while containing no separator.
	if strings.ContainsAny(sessionID, `/\`) || sessionID == "." || sessionID == ".." {
		return ""
	}
	if cwd == "" {
		wd, err := os.Getwd()
		if err != nil {
			return ""
		}
		cwd = wd
	}
	dir := transcript.ProjectDir(transcript.ConfigDir(), cwd)
	if dir == "" {
		return ""
	}
	path := filepath.Join(dir, sessionID+".jsonl")
	if fi, err := os.Stat(path); err != nil || fi.IsDir() {
		return ""
	}
	if ok, _ := transcript.BelongsToSession(path, sessionID); !ok {
		return ""
	}
	if ok, _ := transcript.BelongsToTree(path, cwd); !ok {
		return ""
	}
	return path
}

// errNoTrajectory is the message a subcommand prints when no trajectory could be
// resolved by any of the three sources — no --path, no record on the payload, and
// no current-session transcript derivable from CLAUDE_CODE_SESSION_ID. Shared so
// describe, cite and normalize refuse the same way.
func errNoTrajectory() error {
	return fmt.Errorf("sloprail: no trajectory to read — pass --path, invoke this where a transcript is on the hook payload, or run it in a session (with %s set) whose transcript exists", SessionIDEnv)
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
