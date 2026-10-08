package harness

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
)

// AppendOrphanToolResult adds to a session's record, in the shape its harness keeps one, a
// tool result whose call is nowhere in the record: output answers call id callID, which no
// call in the session carries. It is how a test on Codex and Cursor — whose mock cannot
// emit a result without its call (no CapOrphanToolResult) — plants the record a citation of
// unknown provenance would have to resolve in. On a harness that has the capability the
// scenario step ToolResult does this, and calling this is a test bug.
//
// Codex keeps a result as a function_call_output item of the rollout; Cursor keeps outputs
// in sloprail's own store, as the `post` line of a call whose `pre` slot never came.
func (e *Env) AppendOrphanToolResult(projDir, sessionID, callID, output string) {
	e.t.Helper()
	switch e.driver.Name() {
	case "codex":
		path := e.TranscriptPath(projDir, sessionID)
		body, err := os.ReadFile(path)
		if err != nil {
			e.t.Fatalf("harness: orphan result: read the rollout: %v", err)
		}
		line, _ := json.Marshal(map[string]any{
			"timestamp": "2026-01-01T00:00:59.000Z", "ordinal": bytes.Count(body, []byte("\n")), "type": "response_item",
			"payload": map[string]any{"type": "function_call_output", "call_id": callID, "output": output},
		})
		e.appendLine(path, body, line)
	case "cursor":
		id := e.harnessID(sessionID)
		path := filepath.Join(dataHome(e.home), "sloprail", "cursor-tool-results", id+".jsonl")
		body, _ := os.ReadFile(path)
		line, _ := json.Marshal(map[string]any{
			"kind": "post", "tool_use_id": callID, "tool": "Bash", "output": output, "at": "2026-01-01T00:00:59Z",
		})
		e.appendLine(path, body, line)
	default:
		e.t.Fatalf("harness: orphan result: %s has the orphan-tool-result capability; use the ToolResult step", e.driver.Name())
	}
}

func (e *Env) appendLine(path string, body, line []byte) {
	e.t.Helper()
	if len(body) > 0 && body[len(body)-1] != '\n' {
		body = append(body, '\n')
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		e.t.Fatalf("harness: orphan result: %v", err)
	}
	if err := os.WriteFile(path, append(append(body, line...), '\n'), 0o600); err != nil {
		e.t.Fatalf("harness: orphan result: %v", err)
	}
}
