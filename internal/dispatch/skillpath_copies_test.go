package dispatch

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSkillFilePaths_ADirectoryMarketplacePluginsCachedCopyIsACandidateToo pins the
// gap a Codex onboarding eval exposed, on Claude Code's own layout: a plugin enabled
// from a local directory marketplace resolves to the marketplace SOURCE, while the
// harness may also hold an installed copy in its cache, and the agent reads the
// SKILL.md there. A read of either copy must count.
func TestSkillFilePaths_ADirectoryMarketplacePluginsCachedCopyIsACandidateToo(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CODE_PLUGIN_CACHE_DIR", "")

	market := t.TempDir()
	source := filepath.Join(market, "marketplace", "plugins", "acme")
	require.NoError(t, os.MkdirAll(source, 0o755))
	cached := filepath.Join(home, ".claude", "plugins", "cache", "acme-marketplace", "acme", "1.0.0")
	require.NoError(t, os.MkdirAll(cached, 0o755))

	workspace := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(workspace, ".claude"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(workspace, ".claude", "settings.json"),
		[]byte(`{"enabledPlugins":{"acme@acme-marketplace":true},"extraKnownMarketplaces":{"acme-marketplace":{"source":{"source":"directory","path":"`+market+`"}}}}`), 0o644))

	paths := SkillFilePaths(workspace, "document-decision")
	assert.Contains(t, paths, filepath.Join(source, "skills", "document-decision", "SKILL.md"), "the marketplace source copy")
	assert.Contains(t, paths, filepath.Join(cached, "skills", "document-decision", "SKILL.md"), "the installed cache copy")
}
