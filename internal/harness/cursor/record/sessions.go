package record

import (
	"path/filepath"

	"github.com/sloprail/sloprail/internal/harness"
)

// ProjectSessions implements harness.SessionLister: every conversation under the
// workspace's agent-transcripts/. Only one whose sessionStart was seen (KindRoot) is a
// root; a conversation without that mark is a sub-agent's or unknown, and is listed so a
// caller can name it, but never taken for a root.
func (t Transcripts) ProjectSessions(configDir, dir string) []harness.SessionRecord {
	projDir := ProjectDir(configDir, dir)
	if projDir == "" {
		return nil
	}
	matches, _ := filepath.Glob(filepath.Join(projDir, "agent-transcripts", "*", "*.jsonl"))
	var out []harness.SessionRecord
	for _, m := range matches {
		id := t.ConversationID(m)
		if id == "" {
			continue
		}
		out = append(out, harness.SessionRecord{ID: id, Path: m, Root: IsRoot(id)})
	}
	return out
}

// Companions implements harness.CompanionLocator: the store of the tools' outputs that
// sloprail's hooks recorded for the conversation, which the transcript itself lacks.
func (t Transcripts) Companions(transcriptPath string) []harness.Companion {
	c := harness.Companion{Item: "tool results store", Dir: "tool-results"}
	path, err := ToolResultsPath(t.ConversationID(transcriptPath))
	if err != nil {
		c.Why = err.Error()
	}
	c.Path = path
	return []harness.Companion{c}
}

// IsRoot reports whether the conversation is proven to be the session's own root: a
// sessionStart was seen for it (KindRoot).
func IsRoot(conversationID string) bool { return loadStore(conversationID).root }
