package record

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScanForTempDir_FindsAPathSplitAcrossChunks(t *testing.T) {
	root := filepath.Join(t.TempDir(), "claude-5", "-p", "sid")
	write(t, filepath.Join(root, "scratchpad", "a"), "a")
	pad := strings.Repeat("x", 4<<20-10)
	path := filepath.Join(t.TempDir(), "t.jsonl")
	write(t, path, pad+" "+root+"/scratchpad\n")
	re := regexp.MustCompile(`(/[^\s"'\\]*/claude-[0-9]+/[A-Za-z0-9-]+/sid)/`)
	if got := scanForTempDir(path, re); got != root {
		t.Errorf("got %q, want %q", got, root)
	}
}

func TestProjectSessions_AreTheTopLevelRecordsOfTheProjectDirectory(t *testing.T) {
	cfg := t.TempDir()
	proj := ProjectDir(cfg, "/work/proj")
	write(t, filepath.Join(proj, "a.jsonl"), "{}\n")
	write(t, filepath.Join(proj, "b.jsonl"), "{}\n")
	write(t, filepath.Join(proj, "a", "subagents", "agent-x.jsonl"), "{}\n")
	got := Transcripts{}.ProjectSessions(cfg, "/work/proj")
	if len(got) != 2 {
		t.Fatalf("want a and b, got %+v", got)
	}
	for _, r := range got {
		if !r.Root || r.Path != filepath.Join(proj, r.ID+".jsonl") {
			t.Errorf("%+v", r)
		}
	}
	if other := (Transcripts{}).ProjectSessions(cfg, "/work/other"); len(other) != 0 {
		t.Errorf("another project's sessions: %+v", other)
	}
}

func TestCompanions_SessionDirLeavesSubagentsToTheirOwnSeam(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CODE_TMPDIR", t.TempDir())
	path := filepath.Join(ProjectDir(cfg, "/work/proj"), "s.jsonl")
	write(t, path, "{}\n")
	got := Transcripts{}.Companions(path)
	if len(got) != 2 {
		t.Fatalf("want the session dir and the temp dir, got %+v", got)
	}
	if got[0].Path != strings.TrimSuffix(path, ".jsonl") || len(got[0].Exclude) != 1 || got[0].Exclude[0] != "subagents" {
		t.Errorf("session dir: %+v", got[0])
	}
	if got[1].Path != "" || got[1].Why == "" {
		t.Errorf("no temp dir exists, so the companion says why: %+v", got[1])
	}
}
