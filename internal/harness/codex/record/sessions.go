package record

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/sloprail/sloprail/internal/harness"
)

// ProjectSessions implements harness.SessionLister. Codex files every rollout in one
// date-sharded tree whatever the project, so the project's are the rollouts whose
// session_meta records dir as their working directory; a sub-agent's rollout (it names a
// parent thread) is listed but is not a root.
func (t Transcripts) ProjectSessions(configDir, dir string) []harness.SessionRecord {
	projectDir := t.ProjectDir(configDir, dir)
	if projectDir == "" {
		return nil
	}
	want := resolved(dir)
	var out []harness.SessionRecord
	for _, p := range t.ListRecords(projectDir) {
		id := rolloutThreadID(p)
		if id == "" {
			continue
		}
		cwd, parent := rolloutCwdAndParent(p)
		if cwd == "" || resolved(cwd) != want {
			continue
		}
		out = append(out, harness.SessionRecord{ID: id, Path: p, Root: parent == ""})
	}
	return out
}

func resolved(dir string) string {
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		return r
	}
	return filepath.Clean(dir)
}

// rolloutCwdAndParent is the working directory and the parent thread a rollout's
// session_meta names ("" for a root).
func rolloutCwdAndParent(path string) (cwd, parent string) {
	f, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer f.Close()
	var l struct {
		Payload struct {
			Cwd            string `json:"cwd"`
			ParentThreadID string `json:"parent_thread_id"`
		} `json:"payload"`
	}
	if json.NewDecoder(f).Decode(&l) != nil {
		return "", ""
	}
	return l.Payload.Cwd, l.Payload.ParentThreadID
}
