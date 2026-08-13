package transcript

import (
	"path/filepath"
	"testing"
)

// TestEncodeProjectDir pins the rule exactly — every non-alphanumeric becomes a
// dash, with no collapsing of the runs that produces. Pinned against a real
// directory name observed on disk, for a path holding both slashes and a dot,
// which land as adjacent dashes.
func TestEncodeProjectDir(t *testing.T) {
	got := EncodeProjectDir("/Users/nsviridenko/ws/horizon-37/a10n/.claude/worktrees/ecstatic-hermann-959022")
	want := "-Users-nsviridenko-ws-horizon-37-a10n--claude-worktrees-ecstatic-hermann-959022"
	if got != want {
		t.Fatalf("EncodeProjectDir = %q, want %q", got, want)
	}
}

// TestResolveWorkDirResolvesSymlinks is why the encoding is taken from a
// resolved path: on macOS the temp roots are symlinked, so an encoding of the
// unresolved path names a directory nothing is in.
func TestResolveWorkDirResolvesSymlinks(t *testing.T) {
	real := t.TempDir()
	resolved := ResolveWorkDir(real)
	if resolved == "" {
		t.Fatal("ResolveWorkDir returned nothing for a directory that exists")
	}
	// Resolving twice must not move it again — the resolved form is a fixed
	// point, which is what makes the encoding agree with the harness's.
	if again := ResolveWorkDir(resolved); again != resolved {
		t.Fatalf("ResolveWorkDir is not idempotent: %q then %q", resolved, again)
	}
}

// TestResolveWorkDirHandlesAPathNotYetOnDisk: the longest existing ancestor is
// resolved and the missing tail rejoined, so a directory about to be created
// still encodes to where the harness will put it.
func TestResolveWorkDirHandlesAPathNotYetOnDisk(t *testing.T) {
	base := t.TempDir()
	missing := filepath.Join(base, "not", "there", "yet")

	got := ResolveWorkDir(missing)
	if got == "" {
		t.Fatal("ResolveWorkDir returned nothing")
	}
	if filepath.Base(got) != "yet" {
		t.Fatalf("ResolveWorkDir = %q, want the missing tail rejoined", got)
	}
	if prefix := ResolveWorkDir(base); !filepath.IsAbs(got) || got[:len(prefix)] != prefix {
		t.Fatalf("ResolveWorkDir = %q, want it under the resolved ancestor %q", got, prefix)
	}
}

func TestResolveWorkDirEmpty(t *testing.T) {
	if got := ResolveWorkDir(""); got != "" {
		t.Fatalf("ResolveWorkDir(\"\") = %q, want empty", got)
	}
}

// TestProjectDirIsDerivedNotSearched: the directory is computed from the
// working directory, which is the whole point — one real harness config held
// 1067 project directories, and globbing across them runs on every resolution.
func TestProjectDirIsDerivedNotSearched(t *testing.T) {
	got := ProjectDir("/cfg", "/w/p")
	want := filepath.Join("/cfg", "projects", EncodeProjectDir(ResolveWorkDir("/w/p")))
	if got != want {
		t.Fatalf("ProjectDir = %q, want %q", got, want)
	}
}

// TestProjectDirWithoutAConfigDir returns nothing rather than a path rooted at
// nowhere, so the caller reports it instead of quietly searching the wrong
// place and calling the conversation new.
func TestProjectDirWithoutAConfigDir(t *testing.T) {
	if got := ProjectDir("", "/w/p"); got != "" {
		t.Fatalf("ProjectDir with no config dir = %q, want empty", got)
	}
}

// TestConfigDirPrefersTheEnvironment: the harness's own variable wins, which is
// what keeps a sandboxed run off the host's real data.
func TestConfigDirPrefersTheEnvironment(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/somewhere/isolated")
	if got := ConfigDir(); got != "/somewhere/isolated" {
		t.Fatalf("ConfigDir = %q, want the environment's", got)
	}
}
