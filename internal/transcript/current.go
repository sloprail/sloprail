package transcript

import (
	"os"
	"path/filepath"
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
// location, <config>/projects/<encoded-cwd>/<session-id>.jsonl, built with
// ProjectDir over ConfigDir. The encoding is Claude Code's projects-dir scheme
// over the RESOLVED working directory, not the git-root-anchored one sloprail
// keys its own state under: an agent running from a subdirectory would resolve
// to the wrong directory otherwise.
//
// cwd is the caller's reported working directory when there is one; "" uses the
// process's own, which is the directory the agent ran the command from and the
// one Claude Code filed the transcript under.
//
// The derived file must EXIST, belong to this session, and have been written in
// this tree — a session id inherited from a launcher, or a guessed file
// colliding with an unrelated conversation, resolves to nothing rather than to
// the wrong record.
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
	dir := ProjectDir(ConfigDir(), cwd)
	if dir == "" {
		return ""
	}
	path := filepath.Join(dir, sessionID+".jsonl")
	if fi, err := os.Stat(path); err != nil || fi.IsDir() {
		return ""
	}
	if ok, _ := BelongsToSession(path, sessionID); !ok {
		return ""
	}
	if ok, _ := BelongsToTree(path, cwd); !ok {
		return ""
	}
	return path
}
