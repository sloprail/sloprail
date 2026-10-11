package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
)

// A run records the commit of the source it used, and says when the tree held
// changes that commit does not: a dirty tree is not reproducible from the sha.
func TestRefOf_CommitRemoteAndDirty(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return string(out)
	}
	if refOf(context.Background(), dir) != nil {
		t.Fatal("a directory in no repository has no ref")
	}
	git("init", "--quiet")
	mustWriteFile(t, filepath.Join(dir, "a.txt"), "a\n")
	git("add", "-A")
	git("commit", "--quiet", "-m", "a")
	git("remote", "add", "origin", "https://example.com/x/y.git")

	clean := refOf(context.Background(), dir)
	if clean == nil || len(clean.Commit) != 40 || clean.Dirty || clean.Remote != "https://example.com/x/y.git" {
		t.Fatalf("clean tree: got %+v, want a 40-char commit, the origin URL and dirty false", clean)
	}
	mustWriteFile(t, filepath.Join(dir, "a.txt"), "changed\n")
	if dirty := refOf(context.Background(), dir); dirty == nil || !dirty.Dirty || dirty.Commit != clean.Commit {
		t.Fatalf("changed tree: got %+v, want the same commit and dirty true", dirty)
	}
}
