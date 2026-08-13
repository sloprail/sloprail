package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
)

// AppName is the directory this tool keeps its own data under.
const AppName = "sloprail"

// GuardrailEnv names the guardrail whose hook is running.
//
// Which guardrail is asking is never a parameter to `session state`. The engine
// ran the hook and knows, and it tells the hook by putting it here rather than
// in the argument vector, where a hook could write a different name and read a
// rule it was never told about — and then depend on when that rule ran.
const GuardrailEnv = "SLOPRAIL_GUARDRAIL"

// SessionEnv names the session a hook belongs to, for the same reason.
const SessionEnv = "SLOPRAIL_SESSION_ID"

// WorkspaceEnv is the tree the session is guarding. A hook runs with its own
// working directory set to the guardrail's folder, so the process's cwd is not
// the workspace and cannot stand in for it.
const WorkspaceEnv = "SLOPRAIL_WORKSPACE"

// dataHome is the platform's directory for data a program keeps between runs.
//
// Deliberately outside the guarded project: state written into the tree would
// show up in the very diffs the engine reads, and in the user's git status.
func dataHome() (string, error) {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("sloprail: locate home directory: %w", err)
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support"), nil
	case "windows":
		if dir := os.Getenv("LocalAppData"); dir != "" {
			return dir, nil
		}
		return filepath.Join(home, "AppData", "Local"), nil
	default:
		// The XDG default, which is also the sensible answer anywhere else.
		return filepath.Join(home, ".local", "share"), nil
	}
}

var nonAlnum = regexp.MustCompile(`[^a-zA-Z0-9]`)

// encodeWorkspace turns a working directory into one path component, the way
// Claude Code encodes one for its own transcripts.
//
// A 1:1 substitution rather than a hash: the same reasoning a10n records, that
// a directory a person can read is worth more when they are looking at their
// own state than the shorter name would be. Symlinks are resolved first because
// macOS reports /var where the filesystem holds /private/var, and the two would
// otherwise be two different sessions of the same tree.
func encodeWorkspace(dir string) string {
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	return nonAlnum.ReplaceAllString(dir, "-")
}

// sessionDBPath resolves where one session's state lives:
//
//	{data home}/sloprail/sessions/{workspace}/{session}/state.db
//
// Per workspace as well as per session because a session may hold its own
// worktree, and one tree's verdicts must not stand in for another's. Resolved
// here rather than in the store: the store is root-agnostic, which is what lets
// a test point it at a temporary directory.
func sessionDBPath(cwd, sessionID string) (string, error) {
	if sessionID == "" {
		return "", fmt.Errorf("sloprail: no session id — set %s", SessionEnv)
	}
	root, err := dataHome()
	if err != nil {
		return "", err
	}
	if cwd == "" {
		if cwd, err = os.Getwd(); err != nil {
			return "", fmt.Errorf("sloprail: locate working directory: %w", err)
		}
	}
	return filepath.Join(root, AppName, "sessions", encodeWorkspace(cwd), sessionID, "state.db"), nil
}
