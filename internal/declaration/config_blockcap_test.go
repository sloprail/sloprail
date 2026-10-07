package declaration

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/harness"
	_ "github.com/sloprail/sloprail/internal/harness/claudecode"
	_ "github.com/sloprail/sloprail/internal/harness/cursor"
)

func TestStopHookBlockCap(t *testing.T) {
	cases := []struct {
		name, yaml string
		want       int
		wantErr    bool
	}{
		{"absent file", "", DefaultStopHookBlockCap(), false},
		{"absent key", "disabled: [gate/x]\n", DefaultStopHookBlockCap(), false},
		{"zero is no cap", "stop_hook_block_cap: 0\n", 0, false},
		{"explicit", "stop_hook_block_cap: 3\n", 3, false},
		{"negative", "stop_hook_block_cap: -2\n", DefaultStopHookBlockCap(), true},
		{"not a number", "stop_hook_block_cap: lots\n", DefaultStopHookBlockCap(), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			if c.yaml != "" {
				require.NoError(t, os.WriteFile(filepath.Join(root, configFile), []byte(c.yaml), 0o644))
			}
			got, err := StopHookBlockCap(root)
			assert.Equal(t, c.want, got)
			if c.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
	assert.Equal(t, 8, DefaultStopHookBlockCap(), "mirrors Claude Code's CLAUDE_CODE_STOP_HOOK_BLOCK_CAP default")
}

// A harness with no cap of its own (Codex, Cursor) must not make the default 0:
// 0 is "never stop blocking", so an unsatisfiable Stop gate would loop forever.
func TestDefaultStopHookBlockCap_FiniteWithoutHarnessCap(t *testing.T) {
	t.Setenv(harness.SelectEnv, "cursor")
	assert.Equal(t, 0, harness.StopBlockCap(harness.Current()))
	assert.Equal(t, FallbackStopHookBlockCap, DefaultStopHookBlockCap())
}
