package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

// T001_20: a rename is selected if `match` holds on its new path OR on the path it
// left. Moving a file out of a guarded path is a change to it: the rule is asked about
// it, with the old path's content as oldContent.
// sr:proves fileguard/rename-selected-by-either-path
func TestT001_20_ARenameOutOfAGuardedPathIsSelected(t *testing.T) {
	e, proj, floor, _ := repoWithRule(t, docsRule(""))
	if err := os.MkdirAll(filepath.Join(proj, "archive"), 0o755); err != nil {
		t.Fatal(err)
	}
	e.Git(proj, "mv", "docs/a.md", "archive/a.md")
	e.Git(proj, "commit", "-m", "archive the doc")

	got, res := show(t, e, proj, e.SessionEnv("s-001-20"), "size", floor)
	if res.Code != 0 {
		t.Fatalf("exit %d:\n%s", res.Code, res.Output)
	}
	files := got.Payload.Changeset.Files
	if len(files) != 1 || files[0].Path != "archive/a.md" || files[0].Status != "R" || files[0].OldPath != "docs/a.md" || files[0].OldContent != "one\n" {
		t.Fatalf("files = %+v, want archive/a.md renamed from the guarded docs/a.md", files)
	}
}
