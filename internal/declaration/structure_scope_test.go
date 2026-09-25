package declaration

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests pin the LOAD-time rules of combined structure gates: a plugin's
// structure.yaml must declare a `scope` (folders it owns), a project's must not,
// a scope is a folder glob that does not cover the whole tree, a plugin's
// allow/deny must lie inside a literal scope, overlapping literal scopes of two
// plugins are reported (both stay loaded), and `disabled: [<plugin>/structure]`
// drops one plugin's structure. The write-time decision is internal/dispatch's.

// loadWithPlugin loads a project (its files) plus ONE plugin "acme" (its files).
func loadWithPlugin(t *testing.T, projectFiles, pluginFiles map[string]string) Loaded {
	t.Helper()
	store := NewWithPlugins(projectDotDir(t, projectFiles), []Origin{{Plugin: "acme", Root: pluginRoot(t, pluginFiles)}})
	loaded, err := store.Load(testRegistry(t))
	require.NoError(t, err)
	return loaded
}

// pluginStructure is a plugin structure.yaml body.
func pluginStructure(body string) map[string]string {
	return map[string]string{"file-guard/structure.yaml": body}
}

// structureInvalid returns the single Invalid for a structure gate, failing if
// there is not exactly one.
func structureInvalid(t *testing.T, l Loaded) Invalid {
	t.Helper()
	require.Len(t, l.Invalid, 1, "expected exactly one invalid declaration; got %v", invalidReasons(l))
	iv := l.Invalid[0]
	require.Equal(t, NatureStructure, iv.Nature)
	return iv
}

// The validation matrix: each row is a plugin (or project) structure.yaml and
// what the loader must say about it.
func TestStructureScope_ValidationMatrix(t *testing.T) {
	cases := []struct {
		name     string
		project  string // project structure.yaml ("" = none)
		plugin   string // plugin structure.yaml ("" = none)
		wantKind error  // nil = must load
		wantText string // substring of the reason, when invalid
	}{
		{
			name:     "plugin without scope",
			plugin:   "allow:\n  - glob: \".mdmap/*.md\"\n",
			wantKind: ErrMissingField, wantText: "must declare a `scope`",
		},
		{
			name:     "plugin with empty scope",
			plugin:   "scope: []\nallow:\n  - glob: \".mdmap/*.md\"\n",
			wantKind: ErrMissingField, wantText: "must declare a `scope`",
		},
		{
			name:     "project with scope",
			project:  "scope:\n  - glob: \"docs/\"\nallow:\n  - glob: \"docs/*.md\"\n",
			wantKind: ErrBadScope, wantText: "must not declare a `scope`",
		},
		{
			name:     "regex scope entry",
			plugin:   "scope:\n  - regex: \"^\\\\.mdmap/\"\nallow:\n  - glob: \".mdmap/*.md\"\n",
			wantKind: ErrBadScope, wantText: "`regex` scopes are not supported",
		},
		{
			name:     "scope not ending in slash",
			plugin:   "scope:\n  - glob: \".mdmap\"\nallow:\n  - glob: \".mdmap/*.md\"\n",
			wantKind: ErrBadScope, wantText: "must name a folder",
		},
		{
			name:     "scope with leading slash",
			plugin:   "scope:\n  - glob: \"/.mdmap/\"\nallow:\n  - glob: \".mdmap/*.md\"\n",
			wantKind: ErrBadScope, wantText: "relative to the project root",
		},
		{
			name:     "whole tree: /",
			plugin:   "scope:\n  - glob: \"/\"\nallow:\n  - glob: \"x/*.md\"\n",
			wantKind: ErrBadScope, wantText: "covers the whole tree",
		},
		{
			name:     "whole tree: **",
			plugin:   "scope:\n  - glob: \"**\"\nallow:\n  - glob: \"x/*.md\"\n",
			wantKind: ErrBadScope, wantText: "covers the whole tree",
		},
		{
			name:     "whole tree: **/",
			plugin:   "scope:\n  - glob: \"**/\"\nallow:\n  - glob: \"x/*.md\"\n",
			wantKind: ErrBadScope, wantText: "covers the whole tree",
		},
		{
			name:     "whole tree: */",
			plugin:   "scope:\n  - glob: \"*/\"\nallow:\n  - glob: \"x/*.md\"\n",
			wantKind: ErrBadScope, wantText: "covers the whole tree",
		},
		{
			name:     "whole tree: **/*/ (equivalent form)",
			plugin:   "scope:\n  - glob: \"**/*/\"\nallow:\n  - glob: \"x/*.md\"\n",
			wantKind: ErrBadScope, wantText: "covers the whole tree",
		},
		{
			name:     "whole tree: empty glob",
			plugin:   "scope:\n  - glob: \"\"\nallow:\n  - glob: \"x/*.md\"\n",
			wantKind: ErrBadScope, wantText: "must set `glob`",
		},
		{
			name:     "literal scope, allow outside it",
			plugin:   "scope:\n  - glob: \".mdmap/\"\nallow:\n  - glob: \"docs/*.md\"\n",
			wantKind: ErrOutsideScope, wantText: "outside this plugin's scope",
		},
		{
			name:     "literal scope, deny outside it",
			plugin:   "scope:\n  - glob: \".mdmap/\"\nallow:\n  - glob: \".mdmap/*.md\"\ndeny:\n  - glob: \"docs/*.tmp\"\n",
			wantKind: ErrOutsideScope, wantText: "deny 0",
		},
		{
			name:     "literal scope, look-alike prefix is outside",
			plugin:   "scope:\n  - glob: \".mdmap/\"\nallow:\n  - glob: \".mdmapx/*.md\"\n",
			wantKind: ErrOutsideScope, wantText: "outside this plugin's scope",
		},
		{
			name:     "literal scope, unanchored regex is outside",
			plugin:   "scope:\n  - glob: \".mdmap/\"\nallow:\n  - regex: \"\\\\.mdmap/.*\\\\.md$\"\n",
			wantKind: ErrOutsideScope, wantText: "not anchored",
		},
		{
			name:   "literal scope, allow and deny inside it",
			plugin: "scope:\n  - glob: \".mdmap/\"\nallow:\n  - glob: \".mdmap/mindmap/*/mindmap.yaml\"\ndeny:\n  - glob: \".mdmap/**/*.tmp\"\n",
		},
		{
			name:   "literal scope, anchored regex inside it",
			plugin: "scope:\n  - glob: \".mdmap/\"\nallow:\n  - regex: \"^\\\\.mdmap/[a-z]+\\\\.md$\"\n",
		},
		{
			name:   "two literal scopes, entries in either",
			plugin: "scope:\n  - glob: \".mdmap/\"\n  - glob: \"docs/maps/\"\nallow:\n  - glob: \".mdmap/*.md\"\n  - glob: \"docs/maps/*.yaml\"\n",
		},
		{
			name:   "wildcard scope, any entries (not checkable at load)",
			plugin: "scope:\n  - glob: \"**/.adr/\"\nallow:\n  - glob: \"**/.adr/*.md\"\n  - glob: \"elsewhere/*.md\"\n",
		},
		{
			name:    "project structure without scope still loads (unchanged)",
			project: "allow:\n  - glob: \"memories/updates/*.md\"\n  - regex: \"^memories/decisions/[0-9]{8}_[a-z0-9-]+/.*\\\\.md$\"\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var projectFiles, pluginFiles map[string]string
			if tc.project != "" {
				projectFiles = map[string]string{"file-guard/structure.yaml": tc.project}
			}
			if tc.plugin != "" {
				pluginFiles = pluginStructure(tc.plugin)
			}
			loaded := loadWithPlugin(t, projectFiles, pluginFiles)

			if tc.wantKind == nil {
				require.Empty(t, loaded.Invalid, "must load: %v", invalidReasons(loaded))
				require.Len(t, loaded.Structures, 1)
				return
			}
			iv := structureInvalid(t, loaded)
			assert.True(t, hasKind(iv, tc.wantKind), "wrong fault kind: %v", iv.Reason)
			assert.Contains(t, iv.Reason, tc.wantText)
			assert.Empty(t, loaded.Structures, "an invalid structure gate must not load")
		})
	}
}

// A broken plugin structure is attributed to the plugin and keyed so the project
// can switch it off: `acme/structure`.
func TestStructureScope_InvalidPluginStructureIsAttributed(t *testing.T) {
	loaded := loadWithPlugin(t, nil, pluginStructure("allow:\n  - glob: \".mdmap/*.md\"\n"))
	iv := structureInvalid(t, loaded)
	assert.Equal(t, "acme/structure", iv.Qualified())
	assert.Contains(t, iv.Attribution(), `from plugin "acme"`)
	assert.Contains(t, iv.Remedy(), "disabled: [acme/structure]")
}

// Two plugins whose LITERAL scopes overlap (equal, or one inside the other) are
// reported, naming both — and both stay loaded.
func TestStructureScope_LiteralOverlapReportedNamingBoth(t *testing.T) {
	cases := []struct {
		name   string
		scopeA string
		scopeB string
		want   bool
	}{
		{"equal", ".mdmap/", ".mdmap/", true},
		{"B inside A", ".mdmap/", ".mdmap/mindmap/", true},
		{"A inside B", "docs/maps/", "docs/", true},
		{"disjoint", ".mdmap/", ".adr/", false},
		{"look-alike prefix is not an overlap", ".md/", ".mdmap/", false},
		{"wildcard is not compared at load", "**/.mdmap/", ".mdmap/", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// An allow entry that lies inside the scope when the scope is literal
			// (the load-time containment rule), anything when it is a wildcard.
			body := func(scope string) string {
				allow := "**/*.md"
				if IsLiteralScope(scope) {
					allow = scope + "*.md"
				}
				return "scope:\n  - glob: \"" + scope + "\"\nallow:\n  - glob: \"" + allow + "\"\n"
			}
			a := pluginRoot(t, pluginStructure(body(tc.scopeA)))
			b := pluginRoot(t, pluginStructure(body(tc.scopeB)))
			store := NewWithPlugins(projectDotDir(t, nil), []Origin{{Plugin: "alpha", Root: a}, {Plugin: "beta", Root: b}})
			loaded, err := store.Load(testRegistry(t))
			require.NoError(t, err)
			require.Empty(t, loaded.Invalid, "invalid: %v", invalidReasons(loaded))
			require.Len(t, loaded.Structures, 2, "both plugin structures stay loaded, overlap or not")

			if !tc.want {
				assert.Empty(t, loaded.ScopeOverlaps)
				return
			}
			require.Len(t, loaded.ScopeOverlaps, 1)
			msg := loaded.ScopeOverlaps[0].Message()
			assert.Contains(t, msg, `plugin "alpha"`)
			assert.Contains(t, msg, `plugin "beta"`)
			assert.Contains(t, msg, tc.scopeA)
			assert.Contains(t, msg, tc.scopeB)
			assert.Contains(t, msg, "alpha/structure")
			assert.Contains(t, msg, "beta/structure")
		})
	}
}

// `disabled: [<plugin>/structure]` drops that plugin's structure entirely — and
// with it any overlap it caused — while the project's own and other plugins'
// stay. The project's own key (`structure`) keeps working as before.
func TestStructureScope_DisabledPluginStructureDropped(t *testing.T) {
	a := pluginRoot(t, pluginStructure("scope:\n  - glob: \".mdmap/\"\nallow:\n  - glob: \".mdmap/*.md\"\n"))
	b := pluginRoot(t, pluginStructure("scope:\n  - glob: \".mdmap/\"\nallow:\n  - glob: \".mdmap/*.yaml\"\n"))
	project := projectDotDir(t, map[string]string{
		"file-guard/structure.yaml": "allow:\n  - glob: \"docs/*.md\"\n",
		"config.yaml":               "disabled:\n  - beta/structure\n",
	})
	loaded, err := NewWithPlugins(project, []Origin{{Plugin: "alpha", Root: a}, {Plugin: "beta", Root: b}}).Load(testRegistry(t))
	require.NoError(t, err)
	require.Empty(t, loaded.Invalid, "invalid: %v", invalidReasons(loaded))

	require.NotNil(t, loaded.ProjectStructure(), "the project's own structure is untouched")
	require.Len(t, loaded.PluginStructures(), 1)
	assert.Equal(t, "alpha", loaded.PluginStructures()[0].Origin.Plugin, "only beta was disabled")
	assert.Empty(t, loaded.ScopeOverlaps, "a disabled plugin's structure cannot conflict")

	// The project's own disable key still drops the project's own structure.
	project = projectDotDir(t, map[string]string{
		"file-guard/structure.yaml": "allow:\n  - glob: \"docs/*.md\"\n",
		"config.yaml":               "disabled:\n  - structure\n",
	})
	loaded, err = NewWithPlugins(project, []Origin{{Plugin: "alpha", Root: a}}).Load(testRegistry(t))
	require.NoError(t, err)
	assert.Nil(t, loaded.ProjectStructure(), "`disabled: [structure]` drops the project's own")
	assert.Len(t, loaded.PluginStructures(), 1, "and leaves the plugin's")
}

// A disabled INVALID plugin structure is silenced too (the invalid set is
// filtered by the same key).
func TestStructureScope_DisableReachesInvalidPluginStructure(t *testing.T) {
	project := projectDotDir(t, map[string]string{"config.yaml": "disabled:\n  - acme/structure\n"})
	plugin := pluginRoot(t, pluginStructure("allow:\n  - glob: \".mdmap/*.md\"\n")) // no scope
	loaded, err := NewWithPlugins(project, []Origin{{Plugin: "acme", Root: plugin}}).Load(testRegistry(t))
	require.NoError(t, err)
	assert.Empty(t, loaded.Invalid)
	assert.Empty(t, loaded.Structures)
}

// Two plugins' structures and the project's all load together, project first
// then plugins in precedence order — no structure is shadowed any more.
func TestStructureScope_AllSoundStructuresLoadTogether(t *testing.T) {
	a := pluginRoot(t, pluginStructure("scope:\n  - glob: \".mdmap/\"\nallow:\n  - glob: \".mdmap/*.md\"\n"))
	b := pluginRoot(t, pluginStructure("scope:\n  - glob: \"**/.adr/\"\nallow:\n  - glob: \"**/.adr/*.md\"\n"))
	project := projectDotDir(t, map[string]string{"file-guard/structure.yaml": "allow:\n  - glob: \"docs/*.md\"\n"})
	loaded, err := NewWithPlugins(project, []Origin{{Plugin: "alpha", Root: a}, {Plugin: "beta", Root: b}}).Load(testRegistry(t))
	require.NoError(t, err)
	require.Empty(t, loaded.Invalid, "invalid: %v", invalidReasons(loaded))
	require.Len(t, loaded.Structures, 3)
	assert.False(t, loaded.Structures[0].Origin.FromPlugin())
	assert.Equal(t, "alpha", loaded.Structures[1].Origin.Plugin)
	assert.Equal(t, "beta", loaded.Structures[2].Origin.Plugin)
	assert.Empty(t, loaded.Shadowed)
}

// NewPlugin validates one plugin's declarations by the plugin rules with no
// project: a scoped structure loads; an unscoped one is refused.
func TestNewPlugin_ValidatesPluginStructureByPluginRules(t *testing.T) {
	ok := pluginRoot(t, pluginStructure("scope:\n  - glob: \".mdmap/\"\nallow:\n  - glob: \".mdmap/*.md\"\n"))
	loaded, err := NewPlugin(Origin{Plugin: "mdmap", Root: ok}).Load(testRegistry(t))
	require.NoError(t, err)
	require.Empty(t, loaded.Invalid, "invalid: %v", invalidReasons(loaded))
	require.Len(t, loaded.PluginStructures(), 1)

	bad := pluginRoot(t, pluginStructure("allow:\n  - glob: \".mdmap/*.md\"\n"))
	loaded, err = NewPlugin(Origin{Plugin: "mdmap", Root: bad}).Load(testRegistry(t))
	require.NoError(t, err)
	iv := structureInvalid(t, loaded)
	assert.True(t, strings.HasPrefix(iv.Qualified(), "mdmap/"), iv.Qualified())
}

// The pure helpers the dispatch shares.
func TestStructureScope_Helpers(t *testing.T) {
	assert.Equal(t, ".mdmap", ScopeFolder(".mdmap/"))
	assert.Equal(t, "**/.adr", ScopeFolder("**/.adr/"))
	assert.True(t, IsLiteralScope(".mdmap/"))
	assert.True(t, IsLiteralScope("docs/maps/"))
	assert.False(t, IsLiteralScope("**/.adr/"))
	assert.False(t, IsLiteralScope("docs/*/maps/"))

	for _, g := range []string{"", "/", "*/", "**/", "**", "**/*/", "?*/"} {
		assert.True(t, coversWholeTree(g), "%q covers the whole tree", g)
	}
	for _, g := range []string{".mdmap/", "**/.adr/", "a*/"} {
		assert.False(t, coversWholeTree(g), "%q names a folder", g)
	}

	p, anchored := regexLiteralPrefix(`^\.mdmap/mindmap/.*`)
	assert.True(t, anchored)
	assert.Equal(t, ".mdmap/mindmap/", p)
	_, anchored = regexLiteralPrefix(`\.mdmap/x`)
	assert.False(t, anchored)
}
