package transcript

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SessionIDEnv is the environment variable Claude Code exports naming the
// CURRENT session's id, to a tool call and to a hook alike.
//
// Note the CODE in the name: it is CLAUDE_CODE_SESSION_ID, not CLAUDE_SESSION_ID.
const SessionIDEnv = "CLAUDE_CODE_SESSION_ID"

// CurrentSessionPath resolves the CURRENT session's own transcript from the
// environment, or "" when it cannot.
//
// The session id is SessionIDEnv. The file is at the harness's standard
// location, <config>/projects/<encoded-dir>/<session-id>.jsonl, built with
// ProjectDir over ConfigDir. The encoding is Claude Code's projects-dir scheme
// over the RESOLVED directory the session was started in, not the
// git-root-anchored one sloprail keys its own state under.
//
// cwd is the caller's reported working directory when there is one; "" uses the
// process's own, which is the directory the agent ran the command from. That is
// not always the directory Claude Code filed the record under: its Bash tool
// keeps the working directory between calls, so after a `cd internal` a command
// runs below it. So the lookup walks UP from cwd, and the first directory whose
// projects entry holds this session's record — one that exists, belongs to this
// session, and was written in that directory's tree — is the answer.
//
// A session id inherited from a launcher, or a guessed file colliding with an
// unrelated conversation, resolves to nothing rather than to the wrong record.
func CurrentSessionPath(cwd string) string {
	sessionID := strings.TrimSpace(os.Getenv(SessionIDEnv))
	if sessionID == "" {
		return ""
	}
	// A session id is a name, never a path. Both separators are refused because
	// Windows accepts both; "." and ".." traverse while containing no separator.
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
	config := ConfigDir()
	for dir := filepath.Clean(cwd); ; {
		if path := sessionPathIn(config, dir, sessionID); path != "" {
			return path
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return sessionPathByID(config, sessionID)
		}
		dir = parent
	}
}

// sessionPathByID is the session's record found by its id alone, under any
// projects entry, or "" when no file there belongs to the session.
//
// The record is filed under the directory the session STARTED in, so a command
// the agent runs in another repository or folder — outside that tree — finds
// nothing walking up from its own directory, although the id in its environment
// names this very session. Without this such a run would be session-less. The
// file must still hold records of this session (BelongsToSession); the tree
// check is dropped, since the directory is by definition not the session's.
func sessionPathByID(config, sessionID string) string {
	if config == "" {
		return ""
	}
	matches, _ := filepath.Glob(filepath.Join(config, "projects", "*", sessionID+".jsonl"))
	sort.Strings(matches)
	for _, path := range matches {
		if fi, err := os.Stat(path); err != nil || fi.IsDir() {
			continue
		}
		if ok, _ := BelongsToSession(path, sessionID); ok {
			return path
		}
	}
	return ""
}

// sessionPathIn is the session's record filed under the projects entry for dir,
// or "" when there is none that belongs to this session and this tree.
func sessionPathIn(config, dir, sessionID string) string {
	projects := ProjectDir(config, dir)
	if projects == "" {
		return ""
	}
	path := filepath.Join(projects, sessionID+".jsonl")
	if fi, err := os.Stat(path); err != nil || fi.IsDir() {
		return ""
	}
	if ok, _ := BelongsToSession(path, sessionID); !ok {
		return ""
	}
	if ok, _ := BelongsToTree(path, dir); !ok {
		return ""
	}
	return path
}
