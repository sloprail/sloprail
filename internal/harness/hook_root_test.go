package harness

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A hook's workspace is its repository's root, and the cwd itself where no repository holds it:
// a harness reporting absolute write paths needs a root in a project without one, or the paths
// stay absolute and no relative binding matches.
func TestRootIsTheCwdOutsideARepository(t *testing.T) {
	dir := t.TempDir()
	if got := (HookInput{Cwd: dir}).Root(); got != dir {
		t.Errorf("Root outside a repository = %q, want the cwd %q", got, dir)
	}
	if got := (HookInput{}).Root(); got != "" {
		t.Errorf("Root with no cwd = %q, want none", got)
	}

	repo := t.TempDir()
	if out, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
		t.Skipf("no repository to test with: %v: %s", err, out)
	}
	sub := filepath.Join(repo, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.EvalSymlinks(repo)
	got, _ := filepath.EvalSymlinks((HookInput{Cwd: sub}).Root())
	if got != want {
		t.Errorf("Root below a repository = %q, want its root %q", got, want)
	}
}
