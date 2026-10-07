package harness

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// What each driver forges for a test that needs a record the mock does not write (a
// sub-agent's, tied to its parent; a session no hook ever ran for), in the shape its own
// harness keeps it, so the test is written once. See Driver.SubagentRecordPath and the
// methods beside it.

func writeForged(e *Env, path string, lines ...any) {
	e.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		e.t.Fatalf("harness: forge %s: %v", path, err)
	}
	var b strings.Builder
	for _, l := range lines {
		j, err := json.Marshal(l)
		if err != nil {
			e.t.Fatalf("harness: forge %s: %v", path, err)
		}
		b.Write(j)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		e.t.Fatalf("harness: forge %s: %v", path, err)
	}
}

// --- Claude Code: <session>/subagents/agent-<id>.jsonl, a sidechain record.

func (c claudeDriver) SubagentRecordPath(e *Env, projDir, sessionID, agentID string) string {
	return filepath.Join(strings.TrimSuffix(c.TranscriptPath(e, projDir, sessionID), ".jsonl"), "subagents", "agent-"+agentID+".jsonl")
}

func (c claudeDriver) ForgeSubagentRecord(e *Env, projDir, sessionID, agentID, cwd, prompt string) string {
	path := c.SubagentRecordPath(e, projDir, sessionID, agentID)
	writeForged(e, path, map[string]any{
		"type": "user", "uuid": "sub-origin-" + agentID, "cwd": cwd, "sessionId": sessionID, "isSidechain": true,
		"agentId": agentID, "message": map[string]any{"role": "user", "content": prompt},
	})
	return path
}

func (c claudeDriver) SubagentHookPayload(e *Env, projDir, sessionID, agentID, event, cwd string, extra map[string]any) string {
	m := map[string]any{
		"session_id": sessionID, "transcript_path": c.TranscriptPath(e, projDir, sessionID), "cwd": cwd,
		"agent_id": agentID, "agent_type": "general-purpose", "hook_event_name": event,
	}
	for k, v := range extra {
		m[k] = v
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func (c claudeDriver) ForgeBareTranscript(e *Env, projDir, sessionID string) string {
	path := c.TranscriptPath(e, projDir, sessionID)
	writeForged(e, path, map[string]any{
		"type": "user", "uuid": "u-" + sessionID, "parentUuid": nil, "cwd": projDir,
		"message": map[string]any{"role": "user", "content": "hi"},
	})
	return path
}

// Companions: Claude Code keeps a session's tool results in its session directory.
func (c claudeDriver) Companions(e *Env, projDir, sessionID string) map[string]string {
	sessDir := strings.TrimSuffix(c.TranscriptPath(e, projDir, sessionID), ".jsonl")
	const content = "a big tool result\n"
	if err := os.MkdirAll(filepath.Join(sessDir, "tool-results"), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessDir, "tool-results", "r.txt"), []byte(content), 0o644); err != nil {
		e.t.Fatal(err)
	}
	return map[string]string{"session-dir/tool-results/r.txt": content}
}

// --- Codex: a rollout of its own beside the parent's, whose session_meta names the parent thread.

// codexThreadOf is a stable thread id (a UUID's shape) for a sub-agent a test names.
func codexThreadOf(agentID string) string {
	sum := sha1.Sum([]byte(agentID))
	h := hex.EncodeToString(sum[:])
	return fmt.Sprintf("%s-%s-7%s-8%s-%s", h[0:8], h[8:12], h[13:16], h[17:20], h[20:32])
}

func (c codexDriver) SubagentRecordPath(e *Env, projDir, sessionID, agentID string) string {
	root := c.TranscriptPath(e, projDir, sessionID)
	return filepath.Join(filepath.Dir(root), "rollout-2026-10-03T00-00-00-"+codexThreadOf(agentID)+".jsonl")
}

func (c codexDriver) ForgeSubagentRecord(e *Env, projDir, sessionID, agentID, cwd, prompt string) string {
	path := c.SubagentRecordPath(e, projDir, sessionID, agentID)
	parent := e.harnessID(sessionID)
	writeForged(e, path,
		map[string]any{"timestamp": "2026-10-03T00:00:00.000Z", "ordinal": 0, "type": "session_meta", "payload": map[string]any{
			"session_id": parent, "id": codexThreadOf(agentID), "cwd": cwd, "thread_source": "subagent", "parent_thread_id": parent,
		}},
		map[string]any{"timestamp": "2026-10-03T00:00:01.000Z", "ordinal": 1, "type": "response_item", "payload": map[string]any{
			"type": "message", "role": "user", "content": []map[string]any{{"type": "input_text", "text": prompt}},
		}},
	)
	return path
}

// SubagentHookPayload: a sub-agent's hooks name its own rollout as transcript_path and the
// parent as session_id; SubagentStop alone reports the parent's beside agent_transcript_path.
func (c codexDriver) SubagentHookPayload(e *Env, projDir, sessionID, agentID, event, cwd string, extra map[string]any) string {
	own := c.SubagentRecordPath(e, projDir, sessionID, agentID)
	m := map[string]any{
		"session_id": e.harnessID(sessionID), "transcript_path": own, "cwd": cwd,
		"agent_id": codexThreadOf(agentID), "agent_type": "default", "hook_event_name": event,
	}
	if event == "SubagentStop" {
		m["transcript_path"] = c.TranscriptPath(e, projDir, sessionID)
		m["agent_transcript_path"] = own
	}
	for k, v := range extra {
		m[k] = v
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func (c codexDriver) ForgeBareTranscript(e *Env, projDir, sessionID string) string {
	const thread = "01a10255-45a3-7780-b91f-9c1e276138d7"
	e.setHarnessID(sessionID, thread)
	path := filepath.Join(e.configDir, "sessions", "2026", "10", "03", "rollout-2026-10-03T00-00-00-"+thread+".jsonl")
	writeForged(e, path,
		map[string]any{"timestamp": "2026-10-03T00:00:00.000Z", "ordinal": 0, "type": "session_meta", "payload": map[string]any{
			"session_id": thread, "id": thread, "cwd": resolveWorkDir(projDir), "thread_source": "user", "parent_thread_id": nil,
		}},
		map[string]any{"timestamp": "2026-10-03T00:00:01.000Z", "ordinal": 1, "type": "response_item", "payload": map[string]any{
			"type": "message", "role": "user", "content": []map[string]any{{"type": "input_text", "text": "hi"}},
		}},
	)
	return path
}

// Companions: Codex keeps nothing for a session beside its rollout.
func (codexDriver) Companions(*Env, string, string) map[string]string { return nil }

// --- Cursor: a sub-agent is a conversation of its own that names no parent.

func (cursorDriver) SubagentRecordPath(*Env, string, string, string) string { return "" }

func (cursorDriver) ForgeSubagentRecord(*Env, string, string, string, string, string) string {
	return ""
}

func (cursorDriver) SubagentHookPayload(e *Env, projDir, sessionID, agentID, event, cwd string, extra map[string]any) string {
	m := map[string]any{
		"conversation_id": e.harnessID(sessionID), "session_id": e.harnessID(sessionID), "cwd": cwd,
		"hook_event_name": event, "agent_id": agentID,
	}
	for k, v := range extra {
		m[k] = v
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func (c cursorDriver) ForgeBareTranscript(e *Env, projDir, sessionID string) string {
	const conv = "0dc1b062-efed-4730-83e7-af3df435363b"
	e.setHarnessID(sessionID, conv)
	path := c.TranscriptPath(e, projDir, sessionID)
	writeForged(e, path, map[string]any{
		"role": "user", "message": map[string]any{"content": []map[string]any{{"type": "text", "text": "<user_query>\nhi\n</user_query>"}}},
	})
	return path
}

// Companions: the tool outputs Cursor's hooks recorded, in sloprail's store.
func (cursorDriver) Companions(e *Env, projDir, sessionID string) map[string]string {
	id := e.harnessID(sessionID)
	path := filepath.Join(dataHome(e.home), "sloprail", "cursor-tool-results", id+".jsonl")
	b, err := os.ReadFile(path)
	if err != nil || len(b) == 0 {
		e.t.Fatalf("harness: premise: the hooks recorded no tool outputs for the conversation %s at %s: %v", id, path, err)
	}
	return map[string]string{"tool-results/" + id + ".jsonl": string(b)}
}
