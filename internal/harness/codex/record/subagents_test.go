package record

import (
	"os"
	"path/filepath"
	"testing"
)

// Shapes recorded in harness-mocks codex-mock subagent-transcripts-v2: the sub-agent is
// a rollout of its own whose first line names the parent thread.
func TestSubagentFiles_FindsRolloutsNamingTheThreadAsParent(t *testing.T) {
	const parent = "01a10256-30c2-7943-a8da-522dd95b6a86"
	const child = "01a10256-46d9-7ac1-856a-4d25919d6fb5"
	const grandchild = "01a10256-aaaa-7ac1-856a-4d25919d6fb5"
	const other = "01a10256-bbbb-7ac1-856a-4d25919d6fb5"
	dir := filepath.Join(t.TempDir(), "sessions", "2026", "10", "03")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(id, parentID string) string {
		p := filepath.Join(dir, "rollout-2026-10-03T17-16-06-"+id+".jsonl")
		meta := `{"type":"session_meta","payload":{"id":"` + id + `","parent_thread_id":null}}`
		if parentID != "" {
			meta = `{"type":"session_meta","payload":{"id":"` + id + `","source":{"subagent":{}},"parent_thread_id":"` + parentID + `"}}`
		}
		if err := os.WriteFile(p, []byte(meta+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	root := write(parent, "")
	c := write(child, parent)
	g := write(grandchild, child)
	write(other, "")

	got := Transcripts{}.SubagentFiles(root)
	paths := map[string]bool{}
	for _, f := range got {
		paths[f.Path] = true
	}
	if len(got) != 2 || !paths[c] || !paths[g] {
		t.Fatalf("want the child and its own child, got %v", got)
	}
	if again := (Transcripts{}).SubagentFiles(c); len(again) != 1 || again[0].Path != g {
		t.Fatalf("a sub-agent's own children, got %v", again)
	}
}
