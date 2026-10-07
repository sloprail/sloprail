package harness

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// With no repository root no workspace is named, so a harness that reports absolute paths
// is handed back the cwd-relative spelling a Write tool is given.
func TestFileEffects_OutsideARepositoryAreCwdRelative(t *testing.T) {
	dir := t.TempDir()
	in := HookInput{Cwd: dir, Files: []FileEffect{
		{Kind: FileCreate, Path: filepath.Join(dir, "secret", "keys.md")},
		{Kind: FileCreate, Path: "already/relative.md"},
		{Kind: FileCreate, Path: "/elsewhere/else.md"},
	}}
	got := in.FileEffects()
	want := []string{"secret/keys.md", "already/relative.md", "/elsewhere/else.md"}
	for i, w := range want {
		if filepath.ToSlash(got[i].Path) != w {
			t.Errorf("effect %d: path %q, want %q", i, got[i].Path, w)
		}
	}
}

// Inside a repository the paths stay as the harness reported them: filemod resolves them
// against the root.
func TestFileEffects_InsideARepositoryAreLeftAlone(t *testing.T) {
	dir := t.TempDir()
	if err := exec.Command("git", "-C", dir, "init", "-q").Run(); err != nil {
		t.Skipf("no git: %v", err)
	}
	abs := filepath.Join(dir, "a.md")
	in := HookInput{Cwd: dir, Files: []FileEffect{{Kind: FileCreate, Path: abs}}}
	if got := in.FileEffects(); got[0].Path != abs {
		t.Errorf("path %q, want %q", got[0].Path, abs)
	}
}
