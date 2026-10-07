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

// A sub-agent's rollout says so in its session_meta (thread_source), and names the rollout
// of the thread that dispatched it (parent_thread_id): recorded in harness-mocks codex-mock
// nested-subagents.
func TestParentRecordAndSidechain(t *testing.T) {
	const parent = "01a10278-caa7-76c1-ad6e-2ee53c4a55eb"
	const child = "01a10279-067c-7cf3-bbdb-327ccb5126e2"
	cfg := t.TempDir()
	dir := filepath.Join(cfg, "sessions", "2026", "10", "03")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(id, meta string) string {
		p := filepath.Join(dir, "rollout-2026-10-03T17-53-53-"+id+".jsonl")
		if err := os.WriteFile(p, []byte(meta+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	root := write(parent, `{"type":"session_meta","payload":{"id":"`+parent+`","session_id":"`+parent+`","parent_thread_id":null,"thread_source":"user"}}`)
	sub := write(child, `{"type":"session_meta","payload":{"id":"`+child+`","session_id":"`+parent+`","parent_thread_id":"`+parent+`","thread_source":"subagent"}}`)

	if got, isSub := (Transcripts{}).ParentRecord(sub); !isSub || got != root {
		t.Fatalf("the sub-agent's parent = %q, want the root rollout %q", got, root)
	}
	if got, isSub := (Transcripts{}).ParentRecord(root); isSub || got != "" {
		t.Fatalf("a root has no parent, got %q", got)
	}

	for path, want := range map[string]bool{root: false, sub: true} {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		rec, err := Transcripts{}.ParseRecord(b)
		if err != nil {
			t.Fatal(err)
		}
		if rec.IsSidechain != want {
			t.Fatalf("%s: IsSidechain = %v, want %v", filepath.Base(path), rec.IsSidechain, want)
		}
	}
}
