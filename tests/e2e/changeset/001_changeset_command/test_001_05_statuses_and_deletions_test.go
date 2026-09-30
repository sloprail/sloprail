package e2e

import (
	"testing"
)

const movable = "a long enough body that similarity detection keeps it as a rename\nline two\nline three\n"

// statusesRepo commits, after the rule, one of each kind of change.
func statusesRepo(t *testing.T, ruleExtra string) Shown {
	t.Helper()
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "docs/gone.md", "goodbye\n")
	e.WriteFile(proj, "docs/old.md", movable)
	e.WriteFile(proj, "docs/kept.md", "kept\n")
	e.CommitAll(proj, "before")
	e.FileGuard(proj, "size", docsRule(ruleExtra), map[string]string{"check.sh": passingCheck})
	e.CommitAll(proj, "add the rule")

	e.Git(proj, "rm", "-q", "docs/gone.md")
	e.Git(proj, "mv", "docs/old.md", "docs/new.md")
	e.WriteFile(proj, "docs/kept.md", "kept and edited\n")
	e.WriteFile(proj, "docs/added.md", "added\n")
	e.CommitAll(proj, "delete, rename, edit, add")

	got, res := show(t, e, proj, noSession, "size")
	if res.Code != 0 {
		t.Fatalf("changeset exited %d:\n%s", res.Code, res.Output)
	}
	return got
}

func filesOf(s Shown) map[string]string {
	m := map[string]string{}
	for _, f := range s.Payload.Changeset.Files {
		m[f.Path] = f.Status
	}
	return m
}

func othersOf(s Shown) map[string]string {
	m := map[string]string{}
	for _, o := range s.Payload.Changeset.Others {
		m[o.Path] = o.Status
	}
	return m
}

func equal(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// T001_05: `deletions:` is a status filter. The default (skip) leaves deleted
// files out of `files` and names them in `others`; a rename is not a deletion.
func TestT001_05_DeletionsSkipIsTheDefault(t *testing.T) {
	for _, extra := range []string{"", "deletions: skip\n"} {
		got := statusesRepo(t, extra)
		if want := map[string]string{"docs/new.md": "R", "docs/kept.md": "M", "docs/added.md": "A"}; !equal(filesOf(got), want) {
			t.Fatalf("[%q] files = %v, want %v", extra, filesOf(got), want)
		}
		if want := map[string]string{"docs/gone.md": "D"}; !equal(othersOf(got), want) {
			t.Fatalf("[%q] others = %v, want %v", extra, othersOf(got), want)
		}
		for _, f := range got.Payload.Changeset.Files {
			if f.Path == "docs/new.md" && f.OldPath != "docs/old.md" {
				t.Fatalf("the rename lost its old path: %+v", f)
			}
		}
	}
}

// T001_06: `deletions: include` lets deleted files in, with their old content
// and no new content.
func TestT001_06_DeletionsInclude(t *testing.T) {
	got := statusesRepo(t, "deletions: include\n")
	want := map[string]string{"docs/new.md": "R", "docs/kept.md": "M", "docs/added.md": "A", "docs/gone.md": "D"}
	if !equal(filesOf(got), want) {
		t.Fatalf("files = %v, want %v", filesOf(got), want)
	}
	for _, f := range got.Payload.Changeset.Files {
		if f.Path == "docs/gone.md" && (f.OldContent != "goodbye\n" || f.NewContent != "") {
			t.Fatalf("deleted file = %+v", f)
		}
	}
}

// T001_07: `deletions: only` keeps just the deleted files.
func TestT001_07_DeletionsOnly(t *testing.T) {
	got := statusesRepo(t, "deletions: only\n")
	if want := map[string]string{"docs/gone.md": "D"}; !equal(filesOf(got), want) {
		t.Fatalf("files = %v, want %v", filesOf(got), want)
	}
	if want := map[string]string{"docs/new.md": "R", "docs/kept.md": "M", "docs/added.md": "A"}; !equal(othersOf(got), want) {
		t.Fatalf("others = %v, want %v", othersOf(got), want)
	}
}
