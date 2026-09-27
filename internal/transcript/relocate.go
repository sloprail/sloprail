package transcript

import (
	"os"
	"path/filepath"
	"strings"
)

// RelocateRecord returns where the session record the harness reported at path
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
// to <projects>/<worktree>/<id>.jsonl. Taking the payload literally, the resumed
// session had no record and so no identity.
//
// So a reported path that does not exist is looked for by its file name in the
// harness's other project directories — the only place it can have gone, since
// the name is the session's id — and accepted only if the file's own records
// say they are that session's (BelongsToSession). That keeps a coincidental
// name from handing back another conversation's identity.
//
// Not a sweep on every hook: the directories are listed only when the reported
// path is missing, which for a live session is the resume from elsewhere, and
// for a fresh one is SessionStart before the harness has written anything —
// where nothing is found and path is returned unchanged, so a caller still
// sees the record as not written yet.
//
// configDir empty, or path already present, returns path as given.
func RelocateRecord(configDir, path string) string {
	if path == "" || configDir == "" {
		return path
	}
	if _, err := os.Stat(path); err == nil {
		return path
	}
	name := filepath.Base(path)
	sessionID := strings.TrimSuffix(name, ".jsonl")
	if sessionID == name || sessionID == "" {
		return path
	}
	projects := filepath.Join(configDir, "projects")
	entries, err := os.ReadDir(projects)
	if err != nil {
		return path
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		cand := filepath.Join(projects, e.Name(), name)
		if sameFile(cand, path) {
			continue
		}
		if _, err := os.Stat(cand); err != nil {
			continue
		}
		if ok, _ := BelongsToSession(cand, sessionID); ok {
			return cand
		}
	}
	return path
}
