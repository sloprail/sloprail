package checkrun

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "github.com/sloprail/sloprail/internal/harness/cursor"
)

func cursorRepo(t *testing.T) (root, sub string) {
	t.Helper()
	t.Setenv("SLOPRAIL_HARNESS", "cursor")
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, exec.Command("git", "-C", root, "init", "-q").Run())
	sub = filepath.Join(root, "a", "b")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	return root, sub
}

func TestProjectDir_CursorFindsNearestCursorDir(t *testing.T) {
	root, sub := cursorRepo(t)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "a", ".cursor"), 0o755))
	assert.Equal(t, filepath.Join(root, "a"), ProjectDir(sub))
}

func TestProjectDir_CursorIgnoresClaudeDir(t *testing.T) {
	root, sub := cursorRepo(t)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "a", ".claude"), 0o755))
	assert.Equal(t, root, ProjectDir(sub))
}

func TestProjectDir_CursorFallsBackToAnchor(t *testing.T) {
	root, sub := cursorRepo(t)
	assert.Equal(t, root, ProjectDir(sub))
}
