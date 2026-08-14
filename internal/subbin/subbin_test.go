package subbin

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeExecutable puts an executable file at dir/name and returns its path.
func writeExecutable(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), 0o755))
	return p
}

// TestFindPrefersOverrideDir: the override wins over everything, which is what
// lets a test drive the binaries it just built rather than a machine install.
func TestFindPrefersOverrideDir(t *testing.T) {
	dir := t.TempDir()
	want := writeExecutable(t, dir, "sr-session")
	t.Setenv(EnvDir, dir)

	got, err := Find("sr-session")
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

// TestFindFallsBackToPath: with no override and no sibling, $PATH answers.
//
// This is the step a10n's equivalent deliberately omits. sloprail keeps it
// because `go install ./services/...` and a marketplace hook both reach the
// binaries by name, so a resolver that refused to look at PATH would fail the
// ordinary install.
func TestFindFallsBackToPath(t *testing.T) {
	dir := t.TempDir()
	want := writeExecutable(t, dir, "sr-onlyonpath")
	t.Setenv(EnvDir, "")
	t.Setenv("PATH", dir)

	got, err := Find("sr-onlyonpath")
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

// TestFindOverrideBeatsPath pins the ORDER, not merely that both work. A stale
// binary on PATH must never shadow the one a caller pointed at explicitly —
// that is the confusing failure the ordering exists to prevent.
func TestFindOverrideBeatsPath(t *testing.T) {
	override, onPath := t.TempDir(), t.TempDir()
	want := writeExecutable(t, override, "sr-session")
	writeExecutable(t, onPath, "sr-session")

	t.Setenv(EnvDir, override)
	t.Setenv("PATH", onPath)

	got, err := Find("sr-session")
	require.NoError(t, err)
	assert.Equal(t, want, got, "the override directory must win over $PATH")
}

// TestFindIgnoresNonExecutable: a same-named regular file is not a binary, and
// silently "finding" it would produce a permission error far from the cause.
func TestFindIgnoresNonExecutable(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sr-session"), []byte("not a binary"), 0o644))
	t.Setenv(EnvDir, dir)
	t.Setenv("PATH", "")

	_, err := Find("sr-session")
	require.Error(t, err)
}

// TestFindIgnoresDirectory: a directory named like the binary is not it.
func TestFindIgnoresDirectory(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "sr-session"), 0o755))
	t.Setenv(EnvDir, dir)
	t.Setenv("PATH", "")

	_, err := Find("sr-session")
	require.Error(t, err)
}

// TestFindMissingNamesWhereItLooked. The error is the whole diagnostic for an
// install that is half-present, so it has to say what was tried rather than
// just that something was absent.
func TestFindMissingNamesWhereItLooked(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvDir, dir)
	t.Setenv("PATH", "")

	_, err := Find("sr-absent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sr-absent")
	assert.Contains(t, err.Error(), dir, "the error should name the override directory it tried")
	assert.Contains(t, err.Error(), EnvDir, "the error should name the variable that fixes it")
}
