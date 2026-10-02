package e2e

import "testing"

// T001_01: the range a folder answers for starts at the merge base with origin's default
// branch (not at the last commit that touched the rule's folder), and covers every commit up to HEAD.
func TestT001_01_FolderFloorAndSquashedPayload(t *testing.T) {
	e, proj, _, _ := repoWithRule(t, docsRule(""))
	e.Git(proj, "push", "-q", "origin", "main")
	e.Git(proj, "fetch", "-q", "origin")
	floor := e.Git(proj, "rev-parse", "origin/main")

	e.WriteFile(proj, "docs/a.md", "one\ntwo\n")
	e.CommitAll(proj, "first edit")
	e.WriteFile(proj, "docs/a.md", "one\ntwo\nthree\n")
	e.WriteFile(proj, "docs/b.md", "brand new\n")
	e.WriteFile(proj, "README.md", "readme v2\n")
	e.CommitAll(proj, "second edit", "Sloprail-Refactor: move-only")

	e.Run(proj, "s-001-01", "work", Turns("done", Bash("b1", "true")))
	rs := trackedRanges(t, e, proj, "s-001-01")
	if len(rs) != 1 || rs[0].Base != floor || rs[0].Head != "main" || !rs[0].Tracked() {
		t.Fatalf("tracked ranges = %+v, want main from the merge base with origin/main %s", rs, floor)
	}
}
