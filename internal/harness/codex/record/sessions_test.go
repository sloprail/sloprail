package record

import (
	"os"
	"path/filepath"
	"testing"
)

// Codex files every rollout in one date-sharded tree; a project's are those whose session_meta
// records its working directory, and a sub-agent's rollout (it names a parent thread) is not a root.
func TestProjectSessions_AreTheProjectsRolloutsAndOnlyRootsAreRoots(t *testing.T) {
	const root = "01a10256-30c2-7943-a8da-522dd95b6a86"
	const sub = "01a10256-46d9-7ac1-856a-4d25919d6fb5"
	const elsewhere = "01a10256-bbbb-7ac1-856a-4d25919d6fb5"
	cfg := t.TempDir()
	proj := t.TempDir()
	write := func(day, id, meta string) string {
		dir := filepath.Join(cfg, "sessions", "2026", "10", day)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, "rollout-2026-10-"+day+"T17-16-06-"+id+".jsonl")
		if err := os.WriteFile(p, []byte(meta+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	rootPath := write("03", root, `{"type":"session_meta","payload":{"id":"`+root+`","cwd":"`+proj+`","parent_thread_id":null}}`)
	subPath := write("04", sub, `{"type":"session_meta","payload":{"id":"`+sub+`","cwd":"`+proj+`","parent_thread_id":"`+root+`"}}`)
	write("03", elsewhere, `{"type":"session_meta","payload":{"id":"`+elsewhere+`","cwd":"/some/other/project","parent_thread_id":null}}`)

	got := map[string]bool{}
	paths := map[string]string{}
	for _, r := range (Transcripts{}).ProjectSessions(cfg, proj) {
		got[r.ID] = r.Root
		paths[r.ID] = r.Path
	}
	if len(got) != 2 || !got[root] || got[sub] {
		t.Fatalf("want the root (a root) and the sub-agent (not), got %v", got)
	}
	if paths[root] != rootPath || paths[sub] != subPath {
		t.Fatalf("paths %v", paths)
	}
}
