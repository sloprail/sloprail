package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/grounding"
)

// A rule judges the path an sr-file call spells. Through a symbolic link the
// bytes would land somewhere else, so every verb refuses one — in the target or
// in a directory on the way to it — and touches nothing.
// sr:proves citations/file-command-writes-nothing-unless-exact
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

// In resolve mode an invocation that cannot compute its change records the
// failure against its OWN target, so the hook quotes it beside that file and
// never beside another call's in the same line.
func TestResolveModeRecordsAFailureAgainstItsTarget(t *testing.T) {
	proj := t.TempDir()
	t.Chdir(proj)
	resolve := t.TempDir()
	t.Setenv(grounding.EnvResolveDir, resolve)
	require.NoError(t, os.WriteFile(filepath.Join(proj, "b.md"), []byte("b\n"), 0o644))

	for _, args := range [][]string{
		{"write", "a.md", "--content", "a\n"},
		{"edit", "b.md", "--old-string", "missing", "--new-string", "x"},
	} {
		cmd := newRoot()
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs(args)
		_ = cmd.Execute()
	}

	raw, err := os.ReadFile(grounding.FailedFile(resolve))
	require.NoError(t, err)
	lines := bytes.Split(bytes.TrimSpace(raw), []byte("\n"))
	require.Len(t, lines, 1, "only the failing call is recorded as failed: %s", raw)
	var f grounding.Failed
	require.NoError(t, json.Unmarshal(lines[0], &f))
	assert.Equal(t, grounding.VerbEdit, f.Verb)
	assert.Equal(t, resolvedPath(t, filepath.Join(proj, "b.md")), resolvedPath(t, f.Path))
	assert.Contains(t, f.Error, "--old-string not found")
	assert.FileExists(t, grounding.ResolvedFile(resolve), "the call that computed its change is recorded as resolved")
}

func resolvedPath(t *testing.T, p string) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(filepath.Dir(p))
	require.NoError(t, err)
	return filepath.Join(dir, filepath.Base(p))
}
