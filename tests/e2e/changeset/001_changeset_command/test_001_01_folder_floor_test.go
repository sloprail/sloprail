package e2e

import (
	"github.com/sloprail/sloprail/tests/e2e/harness"
	"strings"
	"testing"
)

// T001_01: with no watermark, the range starts at the last commit that touched
// the rule's folder, and the payload is the squashed net change to HEAD.
func TestT001_01_FolderFloorAndSquashedPayload(t *testing.T) {
	e, proj, floor := repoWithRule(t, docsRule(""))

	e.WriteFile(proj, "docs/a.md", "one\ntwo\n")
	e.CommitAll(proj, "first edit")
	e.WriteFile(proj, "docs/a.md", "one\ntwo\nthree\n")
	e.WriteFile(proj, "docs/b.md", "brand new\n")
	e.WriteFile(proj, "README.md", "readme v2\n")
	head := e.CommitAll(proj, "second edit", "Sloprail-Refactor: move-only")

	got, res := show(t, e, proj, harness.NoSessionEnv, "size")
	if res.Code != 0 {
		t.Fatalf("changeset exited %d:\n%s", res.Code, res.Output)
	}
	if got.Origin != "floor" || got.Base != floor || got.Head != head {
		t.Fatalf("range = %s %s..%s, want floor %s..%s", got.Origin, got.Base, got.Head, floor, head)
	}
	if got.Payload.Changeset.Base != floor || got.Payload.Changeset.Head != head {
		t.Fatalf("payload range does not match: %+v", got.Payload.Changeset)
	}
	if got.Payload.Event.Kind != "Changeset" {
		t.Fatalf("event kind = %q, want Changeset", got.Payload.Event.Kind)
	}

	// The rule's own commit and both edits, oldest first: the commit that adds a rule
	// is judged by it. The trailer is on the one that carried it.
	commits := got.Payload.Changeset.Commits
	if len(commits) != 3 || commits[0].Subject != "add the size rule" || commits[1].Subject != "first edit" || commits[2].Subject != "second edit" {
		t.Fatalf("commits = %+v", commits)
	}
	if v := commits[2].Trailers["Sloprail-Refactor"]; len(v) != 1 || v[0] != "move-only" {
		t.Fatalf("trailers = %+v", commits[1].Trailers)
	}

	// One squashed diff for a.md: both edits in it, old content from the floor.
	files := map[string]int{}
	for i, f := range got.Payload.Changeset.Files {
		files[f.Path] = i
	}
	if len(files) != 2 {
		t.Fatalf("files = %+v, want docs/a.md and docs/b.md", got.Payload.Changeset.Files)
	}
	a := got.Payload.Changeset.Files[files["docs/a.md"]]
	if a.Status != "M" || a.OldContent != "one\n" || a.NewContent != "one\ntwo\nthree\n" ||
		!strings.Contains(a.Diff, "+two") || !strings.Contains(a.Diff, "+three") {
		t.Fatalf("docs/a.md = %+v", a)
	}
	if b := got.Payload.Changeset.Files[files["docs/b.md"]]; b.Status != "A" || b.OldContent != "" {
		t.Fatalf("docs/b.md = %+v", b)
	}

	// What the rule did not select is named, not carried.
	others := map[string]string{}
	for _, o := range got.Payload.Changeset.Others {
		others[o.Path] = o.Status
	}
	want := map[string]string{"README.md": "M", ".sloprail/file-guard/size/file-guard.yaml": "A", ".sloprail/file-guard/size/check.sh": "A"}
	if !equal(others, want) {
		t.Fatalf("others = %v, want %v (the rule's own files are in the range, and not selected)", others, want)
	}
	if s := got.Payload.Subject; s.ID != "changeset" || len(s.Files) != 2 {
		t.Fatalf("subject = %+v", s)
	}

	// Showing a changeset runs nothing.
	if n := e.FileGuardLedger(proj, "size", "ledger"); n != 0 {
		t.Fatalf("the check ran %d times; changeset must not run checks", n)
	}
}
