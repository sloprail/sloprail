package gitrepo

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Real repositories throughout, the same as the rest of this package: what is
// tested is the reading of git's own `show`, and a stub would only prove this
// file agrees with a second copy of its own assumptions.

func TestContentAt_ReadsAFilesBytesAtACommit(t *testing.T) {
	dir := initRepo(t)
	base := commit(t, dir, "notes.md", "# Notes\nbody\n")

	got, ok := ContentAt(dir, base, "notes.md")
	require.True(t, ok, "a tracked file at the commit must read")
	assert.Equal(t, "# Notes\nbody\n", got)
}

// The whole point of reading from the COMMIT: the file has since changed on
// disk, and oldContent must be what it WAS, not what it is now.
func TestContentAt_ReadsTheBaselineNotTheWorkingTree(t *testing.T) {
	dir := initRepo(t)
	base := commit(t, dir, "notes.md", "original\n")

	// Change it on disk, and even stage and commit the change.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.md"), []byte("rewritten\n"), 0o644))
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "rewrite")

	got, ok := ContentAt(dir, base, "notes.md")
	require.True(t, ok)
	assert.Equal(t, "original\n", got,
		"ContentAt reads the file as of the given commit, not as it now sits")
}

// A file that was genuinely EMPTY at the baseline returns ("", true) — distinct
// from a read that failed, which a delete rule must tell apart.
func TestContentAt_AnEmptyFileReadsAsEmptyAndPresent(t *testing.T) {
	dir := initRepo(t)
	base := commit(t, dir, "empty.md", "")

	got, ok := ContentAt(dir, base, "empty.md")
	assert.True(t, ok, "an empty file is present, just empty")
	assert.Equal(t, "", got)
}

// A path not in the commit returns ("", false): there is no blob to read, which
// is not the same as an empty one.
func TestContentAt_AMissingPathReadsAsAbsent(t *testing.T) {
	dir := initRepo(t)
	base := commit(t, dir, "present.md", "here")

	got, ok := ContentAt(dir, base, "never-existed.md")
	assert.False(t, ok, "a path not in the commit has no blob")
	assert.Equal(t, "", got)
}

// A file that exists NOW but did not at the baseline (a create) has no baseline
// content — ("", false), which is why a create declares no oldContent and is
// never asked.
func TestContentAt_AFileCreatedAfterTheBaselineHasNoBaselineContent(t *testing.T) {
	dir := initRepo(t)
	base := commit(t, dir, "old.md", "old")

	require.NoError(t, os.WriteFile(filepath.Join(dir, "new.md"), []byte("new"), 0o644))

	_, ok := ContentAt(dir, base, "new.md")
	assert.False(t, ok, "a file absent from the baseline commit has no prior bytes")
}

// Exact bytes: git show adds and strips nothing, so a file with no trailing
// newline comes back with none.
func TestContentAt_PreservesExactBytes(t *testing.T) {
	dir := initRepo(t)
	base := commit(t, dir, "raw.txt", "no trailing newline")

	got, ok := ContentAt(dir, base, "raw.txt")
	require.True(t, ok)
	assert.Equal(t, "no trailing newline", got, "no newline is invented or stripped")
}

func TestContentAt_UnicodeAndNul(t *testing.T) {
	dir := initRepo(t)
	base := commit(t, dir, "u.txt", "Правило ✅\nline\x00byte\n")

	got, ok := ContentAt(dir, base, "u.txt")
	require.True(t, ok)
	assert.Equal(t, "Правило ✅\nline\x00byte\n", got)
}

// A path in a subdirectory, in git's forward-slash spelling.
func TestContentAt_SubdirectoryPath(t *testing.T) {
	dir := initRepo(t)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub", "deep"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "deep", "f.md"), []byte("nested"), 0o644))
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "nested")
	base := git(t, dir, "rev-parse", "HEAD")

	got, ok := ContentAt(dir, base, "sub/deep/f.md")
	require.True(t, ok)
	assert.Equal(t, "nested", got)
}

func TestContentAt_EmptyCommitOrPathIsAbsent(t *testing.T) {
	dir := initRepo(t)
	base := commit(t, dir, "a.md", "x")

	_, ok := ContentAt(dir, "", "a.md")
	assert.False(t, ok, "no commit, nothing to read")
	_, ok = ContentAt(dir, base, "")
	assert.False(t, ok, "no path, nothing to read")
}

// Not a repository at all: reported as absent rather than crashing, the same
// honest false a missing blob gets.
func TestContentAt_OutsideARepositoryIsAbsent(t *testing.T) {
	dir := t.TempDir() // no git init
	_, ok := ContentAt(dir, "HEAD", "a.md")
	assert.False(t, ok)
}
