package record

import (
	"os"
	"path/filepath"
	"testing"
)

// A Cursor conversation is a root only when its sessionStart was seen; every conversation of the
// workspace is listed, and its tool results store is its companion.
func TestProjectSessions_OnlyAMarkedConversationIsARoot(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cfg := t.TempDir()
	const rootID = "0dc1b062-efed-4730-83e7-af3df435363b"
	const otherID = "1ec2c173-f0fe-4841-94f8-b04ef46454cc"
	for _, id := range []string{rootID, otherID} {
		p := TranscriptPath(cfg, "/work/proj", id)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := AppendLine(rootID, StoredLine{Kind: KindRoot}); err != nil {
		t.Fatal(err)
	}

	got := map[string]bool{}
	for _, r := range (Transcripts{}).ProjectSessions(cfg, "/work/proj") {
		got[r.ID] = r.Root
	}
	if len(got) != 2 || !got[rootID] || got[otherID] {
		t.Fatalf("want both listed and only the marked one a root, got %v", got)
	}

	c := (Transcripts{}).Companions(TranscriptPath(cfg, "/work/proj", rootID))
	want, _ := ToolResultsPath(rootID)
	if len(c) != 1 || c[0].Path != want || c[0].Dir != "tool-results" {
		t.Fatalf("companions: %+v, want the store %s", c, want)
	}
}
