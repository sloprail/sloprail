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
// configDir empty, a path not under <configDir>/projects/<dir>/, or no match
// returns path unchanged.
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
	projects := filepath.Join(configDir, "projects")
	rel, err := filepath.Rel(projects, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return path
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) < 2 {
		return path
	}
	tail := filepath.Join(parts[1:]...)
	sessionID := strings.TrimSuffix(parts[1], ".jsonl")
	if sessionID == "" {
		return path
	}
	entries, err := os.ReadDir(projects)
	if err != nil {
		return path
	}
	for _, e := range entries {
		if !e.IsDir() || e.Name() == parts[0] {
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
