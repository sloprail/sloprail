package transcript

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResolveWorkDirResolvesSymlinks is why the encoding is taken from a
// resolved path: on macOS the temp roots are symlinked, so an encoding of the
// unresolved path names a directory nothing is in.
func TestResolveWorkDirResolvesSymlinks(t *testing.T) {
	real := t.TempDir()
	resolved := ResolveWorkDir(real)
	require.NotEmpty(t, resolved, "ResolveWorkDir returned nothing for a directory that exists")
	// Resolving twice must not move it again — the resolved form is a fixed
	// point, which is what makes the encoding agree with the harness's.
	assert.Equal(t, resolved, ResolveWorkDir(resolved), "the resolved form must be a fixed point")
}

// TestResolveWorkDirHandlesAPathNotYetOnDisk: the longest existing ancestor is
// resolved and the missing tail rejoined, so a directory about to be created
// still encodes to where the harness will put it.
func TestResolveWorkDirHandlesAPathNotYetOnDisk(t *testing.T) {
	base := t.TempDir()
	missing := filepath.Join(base, "not", "there", "yet")

	got := ResolveWorkDir(missing)
	require.NotEmpty(t, got)
	assert.Equal(t, "yet", filepath.Base(got), "the missing tail must be rejoined")
	assert.True(t, filepath.IsAbs(got))
	assert.True(t, strings.HasPrefix(got, ResolveWorkDir(base)), "it must sit under the resolved ancestor")
}

func TestResolveWorkDirEmpty(t *testing.T) {
	assert.Empty(t, ResolveWorkDir(""))
}

// TestProjectDirIsDerivedNotSearched: the directory is computed from the
// working directory, which is the whole point — one real harness config held
// 1067 project directories, and globbing across them runs on every resolution.
func TestProjectDirIsDerivedNotSearched(t *testing.T) {
	got := ProjectDir("/cfg", "/w/p")
	want := filepath.Join("/cfg", "projects", EncodeProjectDir(ResolveWorkDir("/w/p")))
	assert.Equal(t, want, got)
}

// TestProjectDirWithoutAConfigDir returns nothing rather than a path rooted at
// nowhere, so the caller reports it instead of quietly searching the wrong
// place and calling the conversation new.
func TestProjectDirWithoutAConfigDir(t *testing.T) {
	assert.Empty(t, ProjectDir("", "/w/p"), "no config dir must yield nothing, not a path rooted at nowhere")
}
