package harness

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A folder that is no repository still has a workspace, the folder the hook fired in:
// an absolute path a harness reports is then handed back project-relative by filemod.
func TestRoot_OutsideARepositoryIsTheCwd(t *testing.T) {
	dir := t.TempDir()
	if got := (HookInput{Cwd: dir}).Root(); got != filepath.Clean(dir) {
		t.Errorf("root %q, want the cwd %q", got, dir)
	}
	if got := (HookInput{Cwd: "relative/dir"}).Root(); got != "" {
		t.Errorf("a relative cwd names no workspace, got %q", got)
	}
	if got := (HookInput{}).Root(); got != "" {
		t.Errorf("no cwd names no workspace, got %q", got)
	}
}

// Inside a repository the root is the repository's top, however deep the hook fired.
func TestRoot_InsideARepositoryIsItsTop(t *testing.T) {
	dir := t.TempDir()
	if err := exec.Command("git", "-C", dir, "init", "-q").Run(); err != nil {
		t.Skipf("no git: %v", err)
	}
	sub := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.EvalSymlinks(dir)
	got, _ := filepath.EvalSymlinks((HookInput{Cwd: sub}).Root())
	if got != want {
		t.Errorf("root %q, want %q", got, want)
	}
}
