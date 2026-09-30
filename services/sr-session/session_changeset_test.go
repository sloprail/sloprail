package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRepoRelative_InsideTheRepoIsRelativeOutsideIsEmpty(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, ".sloprail", "file-guard", "size")
	require.NoError(t, os.MkdirAll(inside, 0o755))
	outside := t.TempDir() // a plugin cache, elsewhere on disk

	assert.Equal(t, ".sloprail/file-guard/size", repoRelative(root, inside))
	assert.Equal(t, "", repoRelative(root, outside), "a rule outside the repo has no folder for a floor")
	assert.Equal(t, "", repoRelative(root, root), "the root itself is not a rule folder")
}
