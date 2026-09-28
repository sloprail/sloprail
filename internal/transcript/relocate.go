package transcript

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// RelocateRecord returns where the record the harness reported at path
// actually is.
//
// Usually that is path itself, and then this costs one stat. It is somewhere
// else when a session is resumed from a different working directory: Claude
// Code keeps appending to the transcript where the session began, but reports
// transcript_path under the project directory of the directory it was resumed
// in, where no such file exists. Measured on one machine: a session begun in a
// worktree and resumed from the main checkout got a SessionStart:resume payload
// naming <projects>/<main checkout>/<id>.jsonl, a file that was never written,
// while every record of that very hook — and of the rest of the session — went
// to <projects>/<worktree>/<id>.jsonl. Taken literally, the resumed session had
// no record and so no identity.
//
// So a reported path that does not exist is looked for at the same place
// relative to the harness's OTHER project directories — <id>.jsonl for a
// session's record, <id>/subagents/…/agent-<a>.jsonl for a sub-agent's, which
// nests under the session's record and so moves with it. A file found there is
// accepted only if at least one of its records carries sessionId <id>, read
// positively: a file with no sessionId anywhere, or one that cannot be read, is
// not taken, because a coincidental name would otherwise hand back another
// conversation's identity.
//
// A caller must not ask this for a FRESH session's SessionStart (source
// "startup"): that record does not exist yet by design, and a fixed
// --session-id reused across directories would find another project's
// transcript under the same name. See HookPayload.sessionRecord.
//
// Not a sweep on every hook: the directories are listed only when the reported
// path is missing, and a relocation found is remembered for the life of the
// process (a hook asks for it several times).
//
// configDir empty, a path not under <configDir's own "projects" folder>, or no
// match returns path unchanged.
//
// The "projects" root this searches is derived from PATH ITSELF — climbed to
// from path, not built by joining configDir — and configDir is used only to
// confirm the two name the same directory, resolved-symlink to resolved-symlink
// so a spelling difference between them can never defeat that confirmation
// (see sameTree). Building it as filepath.Join(configDir, "projects") and then
// filepath.Rel-ing path against that join breaks the moment the two spell one
// directory differently: a config folder itself reached through a symlink (a
// symlinked CLAUDE_CONFIG_DIR, or the macOS /var → /private/var most callers
// resolve for exactly this reason but a bare join does not), or a reported path
// under a different-looking but identical directory. filepath.Rel does not
// resolve symlinks, so it either errors or returns a path escaping the tree
// with "../…", and relocation silently turns itself off — the caller falls
// straight back to the original, unwritten path with no diagnostic at all.
// Climbing from path's own directory names sidesteps this: whatever path a
// caller was handed, its own ancestry is asked directly, never compared to a
// second, independently-built path.
func RelocateRecord(configDir, path string) string {
	if path == "" || configDir == "" {
		return path
	}
	if _, err := os.Stat(path); err == nil {
		return path
	}
	if cached, ok := relocated.Load(path); ok {
		return cached.(string)
	}
	projects, own, tail, sessionID := projectsRootOf(path)
	if projects == "" || sessionID == "" {
		return path
	}
	if !sameTree(ResolveWorkDir(projects), ResolveWorkDir(filepath.Join(configDir, "projects"))) {
		return path
	}
	entries, err := os.ReadDir(projects)
	if err != nil {
		return path
	}
	for _, e := range entries {
		if !e.IsDir() || e.Name() == own {
			continue
		}
		cand := filepath.Join(projects, e.Name(), tail)
		if _, err := os.Stat(cand); err != nil {
			continue
		}
		if namesSession(cand, sessionID) {
			relocated.Store(path, cand)
			return cand
		}
	}
	return path
}

// projectsRootOf climbs from path to find the "projects" directory it sits
// under, and returns: that root; own, the name of path's own encoded-project
// subdirectory directly below "projects" (what must be EXCLUDED from the
// search, since it is where path itself, missing, was reported); tail, the
// path relative to that subdirectory (<sessionID>.jsonl, or
// <sessionID>/subagents/…/agent-<a>.jsonl); and the session id named by tail's
// first component. All "" when path carries no "projects" ancestor at all — a
// shape this was never asked to relocate.
func projectsRootOf(path string) (projects, own, tail, sessionID string) {
	dir := filepath.Dir(path)
	rest := []string{filepath.Base(path)}
	for {
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", "", "", ""
		}
		if filepath.Base(dir) == "projects" {
			if len(rest) < 2 {
				return "", "", "", ""
			}
			return dir, rest[0], filepath.Join(rest[1:]...), strings.TrimSuffix(rest[1], ".jsonl")
		}
		rest = append([]string{filepath.Base(dir)}, rest...)
		dir = parent
	}
}

// relocated remembers, per process, a reported path already relocated.
var relocated sync.Map

// namesSession reports whether at least one record in path carries sessionID
// — positively; an unreadable file or one with no sessionId at all is a no.
func namesSession(path, sessionID string) bool {
	found := false
	_ = scanFile(path, func(rec claudeRecord) bool {
		if rec.SessionID == sessionID {
			found = true
			return false
		}
		return true
	})
	return found
}
