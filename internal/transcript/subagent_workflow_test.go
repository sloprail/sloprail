package transcript

import (
	"os"
	"path/filepath"
	"testing"
)

// A sub-agent a workflow started is recorded under subagents/workflows/<run>/:
// its record must be found there, or every requirement read from it refuses.
func TestSubagentTranscriptPath_FindsAWorkflowSubagent(t *testing.T) {
	dir := t.TempDir()
	parent := filepath.Join(dir, "session.jsonl")
	want := filepath.Join(dir, "session", SubagentDir, "workflows", "wf_123", subagentFilePrefix+"abc.jsonl")
	if err := os.MkdirAll(filepath.Dir(want), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(want, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := SubagentTranscriptPath(parent, "abc")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

// A direct sub-agent's path is unchanged, whether or not its record exists yet.
func TestSubagentTranscriptPath_DirectPathWhenNoWorkflowRecord(t *testing.T) {
	dir := t.TempDir()
	parent := filepath.Join(dir, "session.jsonl")
	got, err := SubagentTranscriptPath(parent, "abc")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "session", SubagentDir, subagentFilePrefix+"abc.jsonl"); got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}
