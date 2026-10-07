package cursor

import (
	"os"
	"path/filepath"

	"github.com/sloprail/sloprail/internal/harness/cursor/record"
)

// CurrentTranscript implements harness.CurrentTranscriptLocator: the running Cursor
// session's transcript, for a tool the agent runs (`cite` from a Shell call, the
// cite-before-commit gate).
//
// cursor-agent puts CURSOR_TRANSCRIPT_PATH and CURSOR_CONVERSATION_ID in a shell tool's
// environment (recorded, harness-mocks runs/subprocess-session-env). The path is used as
// it is; failing that the file is derived from the conversation id and the project
// folder, walking up from cwd (a shell keeps its directory between calls) and then
// CURSOR_PROJECT_DIR, taking the first that exists. Neither variable present means this
// is not a Cursor tool call and the harness has no opinion.
func (Harness) CurrentTranscript(getenv func(string) string, cwd string) (string, bool) {
	if p := getenv("CURSOR_TRANSCRIPT_PATH"); p != "" {
		return p, true
	}
	id := getenv("CURSOR_CONVERSATION_ID")
	if id == "" {
		return "", false
	}
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	candidates := []string{}
	for dir := filepath.Clean(cwd); cwd != "" && dir != ""; {
		candidates = append(candidates, dir)
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	if d := getenv("CURSOR_PROJECT_DIR"); d != "" {
		candidates = append([]string{d}, candidates...)
	}
	for _, dir := range candidates {
		path := record.TranscriptPath(record.ConfigDir(), dir, id)
		if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
			return path, true
		}
	}
	return "", true
}
