package cursor

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

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
// CURSOR_PROJECT_DIR, taking the first that exists, and failing those the conversation's
// file under any project folder (a command run outside the workspace's own tree). Neither variable present means this
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
	return conversationPath(id), true
}

// conversationPath is the conversation's file found by its id alone, under any project
// folder, or "" when none holds it.
//
// The file is filed under the workspace the session was opened on, so a command the agent
// runs below it, or in another folder, finds nothing walking up from its own directory
// although the id in its environment names this very conversation. Without this such a run
// would be session-less (the same fallback Claude Code's locator has). The id names the
// directory and the file, so what the pattern matches is the conversation's own.
func conversationPath(id string) string {
	if strings.ContainsAny(id, `/\*?[.`) {
		return ""
	}
	matches, _ := filepath.Glob(filepath.Join(record.ConfigDir(), "projects", "*", "agent-transcripts", id, id+".jsonl"))
	sort.Strings(matches)
	for _, m := range matches {
		if fi, err := os.Stat(m); err == nil && !fi.IsDir() {
			return m
		}
	}
	return ""
}
