package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/filemod"
)

// sr-file reads a target's current bytes the one safe way the file module
// does: a regular file, capped. A file past the cap is refused as unreadable
// rather than read whole into the resolve record; a link to /dev/zero is not a
// regular file.
func TestCurrentState_ReadsOnlyARegularFileWithinTheCap(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "big.md")
	require.NoError(t, os.WriteFile(big, nil, 0o644))
	require.NoError(t, os.Truncate(big, filemod.MaxContentReadBytes+1))
	_, _, err := currentState(big, "")
	assert.Error(t, err, "a file past the read cap is not read whole")

	zero := filepath.Join(dir, "to-zero.md")
	require.NoError(t, os.Symlink("/dev/zero", zero))
	_, _, err = currentState(zero, "")
	assert.Error(t, err, "a link to a device is not a regular file")

	plain := filepath.Join(dir, "plain.md")
	require.NoError(t, os.WriteFile(plain, []byte("plain\n"), 0o644))
	got, exists, err := currentState(plain, "")
	require.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, "plain\n", got)
}
