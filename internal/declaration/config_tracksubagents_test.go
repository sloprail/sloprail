package declaration

import (
	"os"
	"path/filepath"
	"testing"

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
