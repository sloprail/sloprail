package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// AppName is the directory this tool keeps its own data under.
const AppName = "sloprail"

// GuardrailEnv names the guardrail whose hook is running.
//
// Which guardrail is asking is never a parameter to `session state`. The engine
// ran the hook and knows, and it tells the hook by putting it here rather than
// in the argument vector, where a hook could write a different name and read a
// rule it was never told about — and then depend on when that rule ran.
const GuardrailEnv = "SR_GUARDRAIL"

// SessionEnv names the session a hook belongs to, for the same reason.
const SessionEnv = "SR_SESSION_ID"

// WorkspaceEnv is the tree the session is guarding. A hook runs with its own
// working directory set to the guardrail's folder, so the process's cwd is not
// the workspace and cannot stand in for it.
const WorkspaceEnv = "SR_WORKSPACE"

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
//
// A SUB-AGENT needs nothing added to this, and that is the test the design had
// to pass rather than a convenience. A sub-agent is a session in its own right,
// so it arrives here with its own session id — resolved from its own origin
// record, distinct from its parent's — and with its own cwd, which is its
// isolated worktree when it was dispatched into one and the parent's tree when
// it was not. Both coordinates are already the ones this path is built from, so
// a sub-agent keys somewhere else by the same rule that placed the parent's
// state, not by a special case. Had a sub-agent needed a mechanism the root does
// not, the scheme would have been the wrong one.
//
// What that yields, in the two cases:
//
//   - Own worktree: a different workspace AND a different session, so nothing is
//     shared. Correct, and the stronger of the two requirements — the sub-agent
//     is judging different content at the same repository-relative paths, so a
//     pass it records is not a statement about the parent's file at all. Pooling
//     them would exempt the parent from a check on bytes nobody looked at.
//
//   - Parent's worktree: the same workspace, still a different session. So the
//     two collide in the workspace component and separate in the session one.
//
// That second case is the one worth arguing, because sharing the tree is a real
// argument for sharing the verdicts: the fingerprint exists to avoid re-judging
// content already judged, and here the content genuinely is the same bytes.
// They are still kept apart, for two reasons.
//
// A verdict is not only about content. It is about content AS JUDGED BY A RULE,
// which is why the spec keeps them per guardrail rather than per file: a file
// one rule passed is a file another may never have seen. The same reasoning
// carries across sessions — a judge check is a model call, not a pure function,
// so a pass is one judgement by one agent, and the guardrail state a rule keeps
// alongside it ("I already warned about X") is a fact about a conversation
// rather than about the tree. Letting a sub-agent inherit that would let it
// skip a warning it was never given, and the spec's reasoning that no rule may
// read another rule's keyspace is the same boundary one step out: crossing it
// between agents would let one read work it was never told about.
//
// And the cost of the two choices is not symmetric. Keeping them apart costs a
// re-judgement of unchanged content — a second look, paid once. Pooling them
// risks exempting a file nothing judged, which loses a violation for good. Where
// one error is recoverable and the other is not, the recoverable one is the one
// to take.
func sessionDBPath(cwd, sessionID string) (string, error) {
	if sessionID == "" {
		return "", fmt.Errorf("sloprail: no session id — set %s", SessionEnv)
	}
	// A session id is a name, never a path. It is joined onto the state root
	// below, and filepath.Join CLEANS after concatenating, so "../../elsewhere"
	// resolves out of this workspace's directory and into another project's
	// state — read and written with no error raised anywhere. This is not
	// hypothetical on this codebase: an id reaching another project's state is a
	// bug that has already shipped here once.
	//
	// Refused rather than repaired. filepath.Base would make the path safe and
	// the anomaly invisible, and an id that is not a name means something
	// upstream is wrong in a way worth hearing about. Both separators are
	// refused because Windows accepts both; "." and ".." are named explicitly
	// because they traverse while containing no separator.
	if strings.ContainsAny(sessionID, `/\`) || sessionID == "." || sessionID == ".." {
		return "", fmt.Errorf("sloprail: %q is not a session id: it would resolve outside this session's own state", sessionID)
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
