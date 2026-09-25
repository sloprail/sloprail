package declaration

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests exercise the plugin-aware loader: a store built with NewWithPlugins
// reads a project's own `.sloprail` AND each plugin's, tagging every declaration
// with its Origin, resolving precedence (project first, then plugins in order),
// and applying the project's disable list. They mirror the properties
// internal/guardrail's store_test asserts for the old format — a plugin rule in
// force, an origin that names it, precedence, disabling — over the new formats.

// pluginRoot writes a plugin whose files live under a fresh directory, its
// declarations under `<root>/.sloprail/<rel>`, and returns the plugin's INSTALL
// ROOT (the directory holding `.sloprail/`) — which is what an Origin.Root names,
// not the `.sloprail` dir itself. rel is nature-relative, e.g.
// "file-guard/x/file-guard.yaml".
func pluginRoot(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		path := filepath.Join(root, dotDirName, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	}
	return root
}

// projectDotDir writes a project's own declarations under a fresh `.sloprail` and
// returns that `.sloprail` dir (what NewWithPlugins's projectRoot names).
func projectDotDir(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		path := filepath.Join(root, dotDirName, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	}
	return filepath.Join(root, dotDirName)
}

const pluginFileGuardYAML = `
match: "**/*.md"
checks:
  - script: ./check.sh
`

const pluginGateYAML = `
on:
  - event: PreToolUse
checks:
  - script: ./check.sh
`

// A plugin that ships a new-format file-guard is loaded in a project that
// declares nothing of its own — the headline the whole slice exists to enable.
func TestNewWithPlugins_LoadsPluginFileGuard(t *testing.T) {
	project := projectDotDir(t, nil) // the project declares nothing
	plugin := pluginRoot(t, map[string]string{
		"file-guard/authoring-slop/file-guard.yaml": pluginFileGuardYAML,
	})

	store := NewWithPlugins(project, []Origin{{Plugin: "sloprail", Root: plugin}})
	loaded, err := store.Load(testRegistry(t))
	require.NoError(t, err)
	require.Empty(t, loaded.Invalid, "invalid: %v", invalidReasons(loaded))

	require.Len(t, loaded.FileGuards, 1, "the plugin's file-guard was not loaded")
	g := loaded.FileGuards[0]
	assert.Equal(t, "authoring-slop", g.Name)
	assert.True(t, g.Origin.FromPlugin(), "the loaded guard is not tagged as coming from a plugin")
	assert.Equal(t, "sloprail", g.Origin.Plugin)
}

// A plugin declaration carries its origin so a refusal can NAME the plugin, and
// its disable key is namespaced by plugin AND nature.
func TestNewWithPlugins_PluginDeclarationCarriesOrigin(t *testing.T) {
	project := projectDotDir(t, nil)
	plugin := pluginRoot(t, map[string]string{
		"file-guard/slop/file-guard.yaml": pluginFileGuardYAML,
		"gate/checkpoint/gate.yaml":       pluginGateYAML,
	})

	store := NewWithPlugins(project, []Origin{{Plugin: "acme", Root: plugin}})
	loaded, err := store.Load(testRegistry(t))
	require.NoError(t, err)
	require.Empty(t, loaded.Invalid, "invalid: %v", invalidReasons(loaded))

	require.Len(t, loaded.FileGuards, 1)
	require.Len(t, loaded.Gates, 1)

	// Attribution is what a refusal shows: the name PLUS the plugin it came from.
	assert.Equal(t, `"slop" from plugin "acme"`, loaded.FileGuards[0].Attribution())
	assert.Equal(t, `"checkpoint" from plugin "acme"`, loaded.Gates[0].Attribution())

	// Qualified is what a consumer writes in `disabled:` — plugin AND nature.
	assert.Equal(t, "acme/file-guard/slop", loaded.FileGuards[0].Qualified())
	assert.Equal(t, "acme/gate/checkpoint", loaded.Gates[0].Qualified())

	// The guard's Dir points inside the plugin's `.sloprail`, so its scripts
	// resolve where they ship.
	assert.Contains(t, loaded.FileGuards[0].Dir, filepath.Join(dotDirName, "file-guard", "slop"))
}

// A project's own declaration retains an EMPTY origin — no plugin in its
// attribution — so the two are distinguishable.
func TestNewWithPlugins_ProjectDeclarationHasNoPluginOrigin(t *testing.T) {
	project := projectDotDir(t, map[string]string{
		"file-guard/mine/file-guard.yaml": pluginFileGuardYAML,
	})

	store := NewWithPlugins(project, nil)
	loaded, err := store.Load(testRegistry(t))
	require.NoError(t, err)
	require.Len(t, loaded.FileGuards, 1)

	g := loaded.FileGuards[0]
	assert.False(t, g.Origin.FromPlugin())
	assert.Equal(t, `"mine"`, g.Attribution(), "a project's own guard must not append a plugin")
	assert.Equal(t, "file-guard/mine", g.Qualified())
}

// The project's own declaration wins over a plugin's of the same (nature, name),
// and the displaced plugin rule is reported as Shadowed rather than dropped
// silently.
func TestNewWithPlugins_ProjectShadowsPlugin(t *testing.T) {
	project := projectDotDir(t, map[string]string{
		"file-guard/dup/file-guard.yaml": pluginFileGuardYAML,
	})
	plugin := pluginRoot(t, map[string]string{
		"file-guard/dup/file-guard.yaml": pluginFileGuardYAML,
	})

	store := NewWithPlugins(project, []Origin{{Plugin: "sloprail", Root: plugin}})
	loaded, err := store.Load(testRegistry(t))
	require.NoError(t, err)

	// Exactly one loaded, and it is the PROJECT's own (empty origin).
	require.Len(t, loaded.FileGuards, 1)
	assert.False(t, loaded.FileGuards[0].Origin.FromPlugin(), "the plugin's copy won, not the project's")

	// The plugin's copy is reported as shadowed, naming the plugin and the winner.
	require.Len(t, loaded.Shadowed, 1)
	sh := loaded.Shadowed[0]
	assert.Equal(t, NatureFileGuard, sh.Nature)
	assert.Equal(t, "dup", sh.Name)
	assert.Equal(t, "sloprail", sh.Plugin)
	assert.Empty(t, sh.WinnerPlugin, "the project won, so WinnerPlugin must be empty")
	assert.Contains(t, sh.Message(), "takes precedence")
}

// A plugin-shipped STRUCTURE gate is loaded when the project ships none, and a
// plugin's structure gate stays loaded even when the project ALSO ships one —
// the structure gate COMPOSES rather than picking one winner (see
// resolveStructures), so nothing about a plugin's own piece is ever Shadowed.
func TestNewWithPlugins_PluginStructureComposesWithProjects(t *testing.T) {
	// The plugin's own is SCOPED, as ValidateStructureGate now requires of every
	// plugin structure gate.
	const pluginStructureYAML = `
scope:
  - glob: "memories/updates/**"
allow:
  - glob: "memories/updates/*.md"
`
	const projectStructureYAML = `
allow:
  - glob: "src/**"
`
	// First: a project with NO structure gate loads the plugin's.
	projectNone := projectDotDir(t, nil)
	plugin := pluginRoot(t, map[string]string{
		"file-guard/structure.yaml": pluginStructureYAML,
	})
	store := NewWithPlugins(projectNone, []Origin{{Plugin: "sloprail", Root: plugin}})
	loaded, err := store.Load(testRegistry(t))
	require.NoError(t, err)
	require.Empty(t, loaded.Invalid, "invalid: %v", invalidReasons(loaded))
	require.Len(t, loaded.Structures, 1, "the plugin's structure gate was not loaded")
	assert.True(t, loaded.Structures[0].Origin.FromPlugin(), "the loaded structure gate is not tagged as from a plugin")
	assert.Equal(t, "sloprail", loaded.Structures[0].Origin.Plugin)
	assert.Empty(t, loaded.Shadowed, "nothing should be shadowed when the project ships no structure gate")

	// Then: a project that ALSO ships one keeps BOTH in force — composed, not
	// one displacing the other.
	projectOwn := projectDotDir(t, map[string]string{
		"file-guard/structure.yaml": projectStructureYAML,
	})
	store = NewWithPlugins(projectOwn, []Origin{{Plugin: "sloprail", Root: plugin}})
	loaded, err = store.Load(testRegistry(t))
	require.NoError(t, err)
	require.Len(t, loaded.Structures, 2, "both the project's own and the plugin's structure gate must be in force")
	assert.Empty(t, loaded.Shadowed, "a structure gate is never shadowed by another — see resolveStructures")

	var sawProject, sawPlugin bool
	for _, sg := range loaded.Structures {
		if sg.Origin.FromPlugin() {
			sawPlugin = true
			assert.Equal(t, "sloprail", sg.Origin.Plugin)
		} else {
			sawProject = true
		}
	}
	assert.True(t, sawProject, "the project's own structure gate must still be in force")
	assert.True(t, sawPlugin, "the plugin's structure gate must still be in force")
}

// Between two plugins that ship the same (nature, name), the EARLIER-listed one
// wins and the later is shadowed — so the order the resolver is handed is the
// precedence, and it must not be re-sorted.
func TestNewWithPlugins_EarlierPluginWinsOverLater(t *testing.T) {
	project := projectDotDir(t, nil)
	first := pluginRoot(t, map[string]string{
		"gate/shared/gate.yaml": pluginGateYAML,
	})
	second := pluginRoot(t, map[string]string{
		"gate/shared/gate.yaml": pluginGateYAML,
	})

	store := NewWithPlugins(project, []Origin{
		{Plugin: "first", Root: first},
		{Plugin: "second", Root: second},
	})
	loaded, err := store.Load(testRegistry(t))
	require.NoError(t, err)

	require.Len(t, loaded.Gates, 1)
	assert.Equal(t, "first", loaded.Gates[0].Origin.Plugin, "the earlier-listed plugin must win")

	require.Len(t, loaded.Shadowed, 1)
	sh := loaded.Shadowed[0]
	assert.Equal(t, "second", sh.Plugin, "the later plugin is the one shadowed")
	assert.Equal(t, "first", sh.WinnerPlugin, "the earlier plugin is named as the winner")
	assert.Contains(t, sh.Message(), "two installed plugins")
}

// A project can switch off a plugin's declaration from its own config, keyed on
// the qualified name — the only remedy that survives a reinstall, since the
// plugin's file is in an install cache.
func TestNewWithPlugins_DisablePluginDeclaration(t *testing.T) {
	project := projectDotDir(t, map[string]string{
		"config.yaml": "disabled:\n  - acme/file-guard/slop\n",
	})
	plugin := pluginRoot(t, map[string]string{
		"file-guard/slop/file-guard.yaml": pluginFileGuardYAML,
	})

	store := NewWithPlugins(project, []Origin{{Plugin: "acme", Root: plugin}})
	loaded, err := store.Load(testRegistry(t))
	require.NoError(t, err)

	assert.Empty(t, loaded.FileGuards, "the disabled plugin file-guard is still in force")
}

// Disabling is per RULE and keyed on the qualified name: disabling a plugin's
// `acme/gate/x` must NOT disable a project's own `gate/x`. They are different
// rules with different authors.
func TestNewWithPlugins_DisableIsKeyedOnQualifiedName(t *testing.T) {
	// Both the project and the plugin ship a gate `x`. The project's own wins
	// precedence; but to prove the disable key discriminates, give them DIFFERENT
	// names so both load, then disable only the plugin's.
	project := projectDotDir(t, map[string]string{
		"gate/keep/gate.yaml": pluginGateYAML,
		"config.yaml":         "disabled:\n  - acme/gate/drop\n",
	})
	plugin := pluginRoot(t, map[string]string{
		"gate/drop/gate.yaml": pluginGateYAML,
	})

	store := NewWithPlugins(project, []Origin{{Plugin: "acme", Root: plugin}})
	loaded, err := store.Load(testRegistry(t))
	require.NoError(t, err)

	// The project's own `keep` survives; the plugin's `drop` is gone.
	require.Len(t, loaded.Gates, 1)
	assert.Equal(t, "keep", loaded.Gates[0].Name)
	assert.False(t, loaded.Gates[0].Origin.FromPlugin())
}

// A plugin root that does not exist (or ships no `.sloprail`) is handled — no
// error, no declarations — the ordinary state of most plugins, which ship no
// guardrails at all.
func TestNewWithPlugins_AbsentPluginRootIsHandled(t *testing.T) {
	project := projectDotDir(t, map[string]string{
		"file-guard/mine/file-guard.yaml": pluginFileGuardYAML,
	})
	// A plugin root pointing at a directory that does not exist.
	missing := filepath.Join(t.TempDir(), "not-installed")

	store := NewWithPlugins(project, []Origin{{Plugin: "ghost", Root: missing}})
	loaded, err := store.Load(testRegistry(t))
	require.NoError(t, err, "an absent plugin root must not error the load")

	// Only the project's own guard loaded; the missing plugin contributed nothing.
	require.Len(t, loaded.FileGuards, 1)
	assert.False(t, loaded.FileGuards[0].Origin.FromPlugin())
	assert.Empty(t, loaded.Shadowed)
}

// A plugin declaration that could not be PARSED is reported as Invalid, tagged
// with the plugin origin so the report names where the unreadable file lives, and
// the consumer can switch it off from their own config (the file is not theirs to
// fix). This mirrors the old format's handling of a broken plugin guardrail.
func TestNewWithPlugins_BrokenPluginDeclarationIsInvalidAndAttributed(t *testing.T) {
	project := projectDotDir(t, nil)
	plugin := pluginRoot(t, map[string]string{
		// A folder that exists with a gate.yaml that will not parse as a gate.
		"gate/broken/gate.yaml": "on: [this is not valid gate yaml\n",
	})

	store := NewWithPlugins(project, []Origin{{Plugin: "acme", Root: plugin}})
	loaded, err := store.Load(testRegistry(t))
	require.NoError(t, err)

	require.Len(t, loaded.Invalid, 1)
	iv := loaded.Invalid[0]
	assert.True(t, iv.Origin.FromPlugin())
	assert.Equal(t, "acme", iv.Origin.Plugin)
	assert.Contains(t, iv.Attribution(), `from plugin "acme"`)
	assert.Equal(t, "acme/gate/broken", iv.Qualified(), "the disable key must be plugin+nature+name")
}

// The disable list reaches a broken plugin declaration too — the half that
// matters most, since a plugin shipping one unparseable declaration would
// otherwise keep a consuming project's logs noisy with a report it has no way to
// silence (the file is in an install cache it must not edit).
func TestNewWithPlugins_DisableReachesBrokenPluginDeclaration(t *testing.T) {
	project := projectDotDir(t, map[string]string{
		"config.yaml": "disabled:\n  - acme/gate/broken\n",
	})
	plugin := pluginRoot(t, map[string]string{
		"gate/broken/gate.yaml": "on: [this is not valid gate yaml\n",
	})

	store := NewWithPlugins(project, []Origin{{Plugin: "acme", Root: plugin}})
	loaded, err := store.Load(testRegistry(t))
	require.NoError(t, err)

	assert.Empty(t, loaded.Invalid, "a disabled broken plugin declaration must be filtered from the Invalid set too")
}

// A config that exists and cannot be parsed refuses the whole load, rather than
// silently re-enabling every declaration the project switched off — the
// fail-closed the old format's LoadConfig takes.
func TestNewWithPlugins_UnparseableConfigRefusesLoad(t *testing.T) {
	project := projectDotDir(t, map[string]string{
		"config.yaml": "disabled: [unterminated\n",
	})

	store := NewWithPlugins(project, nil)
	_, err := store.Load(testRegistry(t))
	require.Error(t, err, "an unreadable config must refuse the load, not default to no overrides")
}

// A plugin's context is visible to a project declaration's `require: [{context}]`
// — the context-name set is the union across roots, so a prerequisite resolves as
// long as SOME root declares the context, independent of precedence.
func TestNewWithPlugins_ProjectRequireResolvesAgainstPluginContext(t *testing.T) {
	project := projectDotDir(t, map[string]string{
		// A project gate that requires a context the PLUGIN ships.
		"gate/needs-ctx/gate.yaml": `
on:
  - event: PreToolUse
require:
  - context: shipped-ctx
checks:
  - script: ./check.sh
`,
	})
	plugin := pluginRoot(t, map[string]string{
		"context/shipped-ctx/context.yaml": validContextYAML,
	})

	store := NewWithPlugins(project, []Origin{{Plugin: "acme", Root: plugin}})
	loaded, err := store.Load(testRegistry(t))
	require.NoError(t, err, "a project gate requiring a plugin-shipped context should resolve")
	require.Empty(t, loaded.Invalid, "invalid: %v", invalidReasons(loaded))
	require.Len(t, loaded.Gates, 1)
	require.Len(t, loaded.Contexts, 1)
	assert.True(t, loaded.Contexts[0].Origin.FromPlugin())
}
