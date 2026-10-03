package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAdoptSubagentResolvesItsOwnRecordAndAgentID(t *testing.T) {
	dir := t.TempDir()
	parent := filepath.Join(dir, "sess.jsonl")
	own := filepath.Join(dir, "sess", "subagents", "agent-a1.jsonl")
	if err := os.MkdirAll(filepath.Dir(own), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{parent, own} {
		if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := session{record: parent, id: "parent-id"}
	s.adoptSubagent("a1")
	if s.record != own || s.agentID != "a1" || !s.subagent || s.id != "parent-id" {
		t.Fatalf("got record=%q agent=%q subagent=%v id=%q", s.record, s.agentID, s.subagent, s.id)
	}
}
