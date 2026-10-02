package e2e

import (
	"github.com/sloprail/sloprail/tests/e2e/harness"
	"testing"
)

// T001_08: a rule's match sees `status` and `trailers` beside `path` and
// `markers`. Selected only when the commits carry the trailer AND the file was
// added; without the trailer nothing is selected — an empty selection is an
// answer, not an error.
func TestT001_08_MatchSeesStatusAndTrailers(t *testing.T) {
	rule := "match: 'path startsWith \"docs/\" and status == \"A\" and \"move-only\" in (trailers[\"Sloprail-Refactor\"] ?? [])'\n" +
		"checks:\n  - script: ./check.sh\n"
	e, proj, floor, _ := repoWithRule(t, rule)

	e.WriteFile(proj, "docs/a.md", "edited but not added\n")
	e.WriteFile(proj, "docs/new.md", "added\n")
	e.CommitAll(proj, "no trailer yet")

	got, res := show(t, e, proj, harness.NoSessionEnv, "size", floor)
	if res.Code != 0 {
		t.Fatalf("exit %d:\n%s", res.Code, res.Output)
	}
	if len(got.Payload.Changeset.Files) != 0 {
		t.Fatalf("without the trailer the rule selects nothing, got %v", filesOf(got))
	}

	e.WriteFile(proj, "docs/more.md", "more\n")
	e.CommitAll(proj, "now it says so", "Sloprail-Refactor: move-only")
	got, res = show(t, e, proj, harness.NoSessionEnv, "size", floor)
	if res.Code != 0 {
		t.Fatalf("exit %d:\n%s", res.Code, res.Output)
	}
	// Trailers are across the range, so both added files are selected; the
	// edited one is not an addition.
	if want := map[string]string{"docs/new.md": "A", "docs/more.md": "A"}; !equal(filesOf(got), want) {
		t.Fatalf("files = %v, want %v", filesOf(got), want)
	}
	if _, ok := othersOf(got)["docs/a.md"]; !ok {
		t.Fatalf("the edited file should be listed in others: %v", othersOf(got))
	}
}

// T001_09: a rule reads commits, never the working tree: an uncommitted edit and
// an untracked file are invisible to it.
func TestT001_09_TheDirtyTreeIsInvisible(t *testing.T) {
	e, proj, floor, _ := repoWithRule(t, docsRule(""))
	e.WriteFile(proj, "docs/a.md", "one\ncommitted\n")
	e.CommitAll(proj, "the committed edit")

	e.WriteFile(proj, "docs/a.md", "one\ncommitted\nHALF-FINISHED\n")
	e.WriteFile(proj, "docs/scratch.md", "untracked\n")

	got, res := show(t, e, proj, harness.NoSessionEnv, "size", floor)
	if res.Code != 0 {
		t.Fatalf("exit %d:\n%s", res.Code, res.Output)
	}
	if len(got.Payload.Changeset.Files) != 1 || got.Payload.Changeset.Files[0].Path != "docs/a.md" {
		t.Fatalf("files = %v, want only the committed docs/a.md", filesOf(got))
	}
	if f := got.Payload.Changeset.Files[0]; f.NewContent != "one\ncommitted\n" {
		t.Fatalf("newContent = %q, want the committed content", f.NewContent)
	}
}
