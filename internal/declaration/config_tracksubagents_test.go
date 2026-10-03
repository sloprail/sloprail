package declaration

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnableSubagentStopCheck_OffUnlessTheProjectOptsIn(t *testing.T) {
	dir := t.TempDir()
	assert.False(t, EnableSubagentStopCheck(dir), "no config: not opted in")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("disabled: []\n"), 0o644))
	assert.False(t, EnableSubagentStopCheck(dir))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("enable_subagent_stop_check: true\n"), 0o644))
	assert.True(t, EnableSubagentStopCheck(dir))
}

func TestSubagentThresholds_DefaultsAndOverrides(t *testing.T) {
	dir := t.TempDir()
	silent, stale, err := SubagentThresholds(dir)
	require.NoError(t, err)
	assert.Equal(t, 10*time.Minute, silent)
	assert.Equal(t, 60*time.Minute, stale)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("subagent_silent_after_minutes: 3\nsubagent_stale_after_minutes: 20\n"), 0o644))
	silent, stale, err = SubagentThresholds(dir)
	require.NoError(t, err)
	assert.Equal(t, 3*time.Minute, silent)
	assert.Equal(t, 20*time.Minute, stale)

	// One alone keeps the other's default.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("subagent_stale_after_minutes: 15\n"), 0o644))
	silent, stale, err = SubagentThresholds(dir)
	require.NoError(t, err)
	assert.Equal(t, 10*time.Minute, silent)
	assert.Equal(t, 15*time.Minute, stale)
}

func TestSubagentThresholds_ANonsensicalPairIsAnErrorWithTheDefaults(t *testing.T) {
	for _, body := range []string{
		"subagent_silent_after_minutes: 0\n",
		"subagent_silent_after_minutes: 30\nsubagent_stale_after_minutes: 30\n",
		"subagent_stale_after_minutes: 5\n", // below the default silent threshold
	} {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0o644))
		silent, stale, err := SubagentThresholds(dir)
		require.Error(t, err, body)
		assert.Equal(t, 10*time.Minute, silent)
		assert.Equal(t, 60*time.Minute, stale)
	}
}
