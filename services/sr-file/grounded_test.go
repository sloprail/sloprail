package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A rule judges the path an sr-file call spells. Through a symbolic link the
// bytes would land somewhere else, so every verb refuses one — in the target or
// in a directory on the way to it — and touches nothing.
func TestGroundedRefusesSymbolicLinks(t *testing.T) {
	proj := t.TempDir()
	outside := t.TempDir()
	t.Chdir(proj)
	require.NoError(t, os.WriteFile(filepath.Join(outside, "ci.yml"), []byte("safe\n"), 0o644))
	require.NoError(t, os.Symlink(filepath.Join(outside, "ci.yml"), filepath.Join(proj, "notes.md")))
	require.NoError(t, os.Symlink(outside, filepath.Join(proj, "linkdir")))
	require.NoError(t, os.Mkdir(filepath.Join(proj, "real"), 0o755))

	for _, args := range [][]string{
		{"write", "notes.md", "--content", "evil\n"},
		{"edit", "notes.md", "--old-string", "safe", "--new-string", "evil"},
		{"write", "linkdir/ci.yml", "--content", "evil\n"},
		{"write", "linkdir/new.yml", "--content", "evil\n"},
		{"delete", "linkdir/ci.yml"},
		{"write", filepath.Join(proj, "linkdir", "ci.yml"), "--content", "evil\n"},
	} {
		cmd := newRoot()
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetArgs(args)
		err := cmd.Execute()
		require.Error(t, err, "%v", args)
		assert.Contains(t, err.Error(), "symbolic link", "%v", args)
	}
	b, err := os.ReadFile(filepath.Join(outside, "ci.yml"))
	require.NoError(t, err)
	assert.Equal(t, "safe\n", string(b), "nothing reached the link's target")
	assert.NoFileExists(t, filepath.Join(outside, "new.yml"))

	// A plain path, including one that leaves and re-enters through `..`, is
	// not a link and is written.
	cmd := newRoot()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{"write", "real/../real/a.md", "--content", "ok\n"})
	require.NoError(t, cmd.Execute())
	assert.FileExists(t, filepath.Join(proj, "real", "a.md"))
}
