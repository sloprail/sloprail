package dispatch

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSkillFilePaths_PluginSkillIsACandidateToo pins the second candidate
// location: a plugin the project has ENABLED (through .claude/settings.json,
// read the same way internal/harness reads it for every other purpose)
// contributes its own `skills/<name>/SKILL.md` alongside the project's own.
//
// This reuses harness.Resolve rather than a second discovery mechanism — see
// skillpath.go's own doc comment on why a second answer to "which plugins are
// enabled" would be a place for the two to drift apart.
func TestSkillFilePaths_PluginSkillIsACandidateToo(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	workspace := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(workspace, ".claude"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(workspace, ".claude", "settings.json"),
		[]byte(`{"enabledPlugins":{"acme@acme-marketplace":true}}`),
		0o644,
	))

	pluginDir := filepath.Join(home, ".claude", "plugins", "cache", "acme-marketplace", "acme", "1.0.0")
	require.NoError(t, os.MkdirAll(pluginDir, 0o755))

	paths := SkillFilePaths(workspace, "document-decision")
	require.NotEmpty(t, paths)

	assert.Contains(t, paths, filepath.Join(workspace, ".claude", "skills", "document-decision", "SKILL.md"),
		"the project's own skill path must always be a candidate")
	assert.Contains(t, paths, filepath.Join(pluginDir, "skills", "document-decision", "SKILL.md"),
		"an enabled plugin's skill path must be a candidate too")
}

// TestSkillFilePaths_ADisabledPluginContributesNoCandidate is the control: a
// plugin present in the cache but NOT enabled in the project's settings must
// not contribute a candidate — the same "discovery is a property of what the
// project installed" stance internal/harness itself takes.
func TestSkillFilePaths_ADisabledPluginContributesNoCandidate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	workspace := t.TempDir()
	// No .claude/settings.json at all: nothing enabled.
	pluginDir := filepath.Join(home, ".claude", "plugins", "cache", "acme-marketplace", "acme", "1.0.0")
	require.NoError(t, os.MkdirAll(pluginDir, 0o755))

	paths := SkillFilePaths(workspace, "document-decision")
	assert.NotContains(t, paths, filepath.Join(pluginDir, "skills", "document-decision", "SKILL.md"))
}
