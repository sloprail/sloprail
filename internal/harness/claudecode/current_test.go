package claudecode

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sloprail/sloprail/internal/harness/claudecode/record"
	"github.com/sloprail/sloprail/internal/transcript"
)

// A run in a repository outside the tree the session started in still resolves the
// session's record, by the id in the environment.
func TestCurrentSessionPathFindsTheRecordByIDOutsideItsTree(t *testing.T) {
	config := t.TempDir()
	start := t.TempDir()
	other := t.TempDir()
	const id = "11111111-2222-3333-4444-555555555555"
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	t.Setenv(SessionIDEnv, id)

	dir := record.ProjectDir(config, start)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := filepath.Join(dir, id+".jsonl")
	line := `{"type":"user","sessionId":"` + id + `","cwd":"` + start + `","uuid":"u1","message":{"role":"user","content":"hi"}}` + "\n"
	if err := os.WriteFile(rec, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := transcript.CurrentSessionPath(start); got != rec {
		t.Fatalf("inside the start tree: got %q, want %q", got, rec)
	}
	if got := transcript.CurrentSessionPath(other); got != rec {
		t.Fatalf("outside the start tree: got %q, want %q", got, rec)
	}

	// A file under that name holding another session's records is not this session's.
	other2 := `{"type":"user","sessionId":"someone-else","cwd":"` + start + `","uuid":"u1","message":{"role":"user","content":"hi"}}` + "\n"
	if err := os.WriteFile(rec, []byte(other2), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := transcript.CurrentSessionPath(other); got != "" {
		t.Fatalf("a record of another session resolved: %q", got)
	}

	t.Setenv(SessionIDEnv, "")
	if got := transcript.CurrentSessionPath(other); got != "" {
		t.Fatalf("without an id: got %q", got)
	}
}
