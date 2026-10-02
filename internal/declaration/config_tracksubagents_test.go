package declaration

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTrackSubagents_OffUnlessTheProjectOptsIn(t *testing.T) {
	dir := t.TempDir()
	assert.False(t, TrackSubagents(dir), "no config: not opted in")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("disabled: []\n"), 0o644))
	assert.False(t, TrackSubagents(dir))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("track_subagents: true\n"), 0o644))
	assert.True(t, TrackSubagents(dir))
}
