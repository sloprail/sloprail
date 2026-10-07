package cursor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/harness"
)

func TestEnvScrubKeepsCredentialsAndDropsTheSession(t *testing.T) {
	env := []string{"CURSOR_AGENT=1", "CURSOR_CONVERSATION_ID=x", "CURSOR_PROJECT_DIR=/p", "CLAUDE_PROJECT_DIR=/p",
		"CURSOR_API_KEY=k", "PATH=/bin", "SR_X=1", "XDG_DATA_HOME=/d"}
	assert.Equal(t, []string{"CURSOR_API_KEY=k", "PATH=/bin", "SR_X=1", "XDG_DATA_HOME=/d"}, Session(env))
	assert.Equal(t, []string{"CURSOR_API_KEY=k", "PATH=/bin"}, Hermetic(env))
}

func TestPluginsResolveFromTheLocalPluginDirectory(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".cursor", "plugins", "local")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "good", ".cursor-plugin"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "good", ".cursor-plugin", "plugin.json"), []byte(`{"name":"good"}`), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "bare"), 0o755))

	res, err := New().ResolvePlugins("/proj", home)
	require.NoError(t, err)
	require.Len(t, res.Roots, 1)
	assert.Equal(t, harness.Plugin{Name: "good", Marketplace: "local"}, res.Roots[0].Plugin)
	assert.Equal(t, filepath.Join(root, "good"), res.Roots[0].Dir)
	require.Len(t, res.Unresolved, 1, "a plugin directory without a manifest is reported, not skipped")
	assert.Equal(t, "bare@local", res.Unresolved[0].Key)

	empty, err := New().ResolvePlugins("/proj", t.TempDir())
	require.NoError(t, err)
	assert.Empty(t, empty.Roots)
}

func TestProcessesAreNeverReadAsGone(t *testing.T) {
	_, found := New().ProcessOfSession("/h", "s")
	assert.False(t, found)
	gone, known := New().ProcessGone("/h", harness.Process{PID: 1})
	assert.False(t, gone)
	assert.False(t, known)
}

func TestTranscriptsLayoutAndRecordedLines(t *testing.T) {
	tr := New().Transcripts()
	assert.Equal(t, "Users-u-my-proj", tr.EncodeProjectDir("/Users/u/my.proj"))
	assert.Equal(t, "/c/projects/Users-u-p", tr.ProjectDir("/c", "/Users/u/p"))
	assert.Equal(t, "", tr.ProjectDir("", "/p"))

	f, err := os.ReadFile("testdata/file-tools.transcript.jsonl")
	require.NoError(t, err)
	var types []string
	var uuids []string
	for _, line := range splitLines(f) {
		r, err := tr.ParseRecord(line)
		require.NoError(t, err)
		types = append(types, r.Type)
		uuids = append(uuids, r.UUID)
		assert.Nil(t, r.ParentUUID)
	}
	assert.Equal(t, []string{"user", "assistant", "assistant", "assistant", "assistant", "system"}, types)
	assert.Len(t, map[string]bool{uuids[0]: true, uuids[1]: true, uuids[2]: true, uuids[3]: true}, 4, "distinct lines get distinct ids")

	first, _ := tr.ParseRecord(splitLines(f)[0])
	again, _ := tr.ParseRecord(splitLines(f)[0])
	assert.Equal(t, first.UUID, again.UUID, "the id is stable, so a conversation's identity is")
	assert.Contains(t, string(first.Message), "user_query")

	_, err = tr.ParseRecord([]byte(`{"role":"tool"}`))
	assert.Error(t, err)
	_, err = tr.ParseRecord([]byte(`nope`))
	assert.Error(t, err)
}

func splitLines(b []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, c := range b {
		if c == '\n' {
			if i > start {
				out = append(out, b[start:i])
			}
			start = i + 1
		}
	}
	if start < len(b) {
		out = append(out, b[start:])
	}
	return out
}
