package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/checkstore"
)

// A sibling session's check store that cannot be read fails the range closed: what an
// earlier session was refused for is unknown, and unknown must not read as "nothing".
func TestOutstandingRefusalBases_AnUnreadableSiblingStoreFailsClosed(t *testing.T) {
	sessions := t.TempDir()
	own, err := checkstore.Open(filepath.Join(sessions, "own", "checks.db"))
	require.NoError(t, err)
	defer own.Close()

	bases, err := outstandingRefusalBases(t.TempDir(), "file-guard/x", own)
	require.NoError(t, err)
	assert.Empty(t, bases, "no sibling: nothing to extend over")

	require.NoError(t, os.MkdirAll(filepath.Join(sessions, "other"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sessions, "other", "checks.db"), []byte("this is not a database"), 0o644))
	_, err = outstandingRefusalBases(t.TempDir(), "file-guard/x", own)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not be read")
}
