package record

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestEncodeProjectDir pins the rule exactly — every non-alphanumeric becomes a
// dash, with no collapsing of the runs that produces. Pinned against a real
// directory name observed on disk, for a path holding both slashes and a dot,
// which land as adjacent dashes.
func TestEncodeProjectDir(t *testing.T) {
	got := EncodeProjectDir("/Users/nsviridenko/ws/horizon-37/a10n/.claude/worktrees/ecstatic-hermann-959022")
	want := "-Users-nsviridenko-ws-horizon-37-a10n--claude-worktrees-ecstatic-hermann-959022"
	assert.Equal(t, want, got)
}

// TestConfigDirPrefersTheEnvironment: the harness's own variable wins, which is
// what keeps a sandboxed run off the host's real data.
func TestConfigDirPrefersTheEnvironment(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/somewhere/isolated")
	assert.Equal(t, "/somewhere/isolated", ConfigDir(), "the harness's own variable wins")
}
