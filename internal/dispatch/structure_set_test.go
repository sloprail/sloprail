package dispatch

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/declaration"
)

// These pin the WRITE-time decision of combined structure gates
// (StructureSet.Decide): ownership by scope, the ownership conflict, the
// project's veto, no widening by the project's allow inside an owned scope, and
// the unowned remainder following the project (or permitted with none).

// projectGate is a project structure gate (no origin).
func projectGate(allow, deny []declaration.StructureEntry) declaration.StructureGate {
	return declaration.StructureGate{Allow: allow, Deny: deny, Dir: ".sloprail/file-guard"}
}

// pluginGate is a plugin structure gate owning the given scopes.
func pluginGate(plugin string, scope []string, allow, deny []declaration.StructureEntry) declaration.StructureGate {
	sg := declaration.StructureGate{
		Allow:  allow,
		Deny:   deny,
		Dir:    "/plugins/" + plugin + "/.sloprail/file-guard",
		Origin: declaration.Origin{Plugin: plugin, Root: "/plugins/" + plugin},
	}
	for _, s := range scope {
		sg.Scope = append(sg.Scope, glob(s))
	}
	return sg
}

func globs(ps ...string) []declaration.StructureEntry {
	out := make([]declaration.StructureEntry, 0, len(ps))
	for _, p := range ps {
		out = append(out, glob(p))
	}
	return out
}

func compileSet(t *testing.T, gates ...declaration.StructureGate) *StructureSet {
	t.Helper()
	set, err := CompileStructureSet(gates)
	require.NoError(t, err)
	return set
}

// The fixtures the decision matrix combines.
var (
	// The project allows docs/ and .mdmap/notes/*.md, and vetoes *.secret anywhere.
	fxProject = projectGate(
		globs("docs/**", ".mdmap/notes/*.md"),
		globs("**/*.secret"),
	)
	// mdmap owns .mdmap/: only mindmap.yaml files, never a .tmp.
	fxMdmap = pluginGate("mdmap", []string{".mdmap/"},
		globs(".mdmap/mindmap/*/mindmap.yaml", ".mdmap/**/*.tmp", ".mdmap/**/*.secret"),
		globs(".mdmap/**/*.tmp"),
	)
	// adr owns every .adr folder at any depth.
	fxADR = pluginGate("adr", []string{"**/.adr/"}, globs("**/.adr/*.md"), nil)
	// shadow-adr ALSO owns .adr folders under docs/ — overlapping adr's wildcard scope.
	fxShadowADR = pluginGate("shadow-adr", []string{"docs/**/.adr/"}, globs("docs/**/.adr/*.md"), nil)
)

func TestStructureSet_DecisionMatrix(t *testing.T) {
	cases := []struct {
		name    string
		gates   []declaration.StructureGate
		path    string
		allowed bool
		// wantIn are substrings the refusal must carry (the deciding source).
		wantIn []string
	}{
		// -- no structures anywhere --
		{name: "nothing declared: anything permitted", gates: nil, path: "src/main.go", allowed: true},

		// -- plugin only --
		{name: "plugin only: inside scope and allowed", gates: []declaration.StructureGate{fxMdmap},
			path: ".mdmap/mindmap/a/mindmap.yaml", allowed: true},
		{name: "plugin only: inside scope, not allowed", gates: []declaration.StructureGate{fxMdmap},
			path: ".mdmap/stray.md", allowed: false,
			wantIn: []string{`plugin "mdmap"'s structure gate`, `scope ".mdmap/"`, "matches no `allow` entry",
				// The plugin's structure.yaml lives in its install, so the refusal
				// says what a path in its scope must look like.
				"a path must match one of: glob .mdmap/mindmap/*/mindmap.yaml"}},
		{name: "plugin only: inside scope, allowed but plugin deny", gates: []declaration.StructureGate{fxMdmap},
			path: ".mdmap/x/cache.tmp", allowed: false,
			wantIn: []string{"`deny` exception", `plugin "mdmap"'s structure gate`}},
		{name: "plugin only: outside scope permitted", gates: []declaration.StructureGate{fxMdmap},
			path: "src/main.go", allowed: true},

		// -- project + plugin --
		{name: "combined: unowned and project-allowed", gates: []declaration.StructureGate{fxProject, fxMdmap},
			path: "docs/guide.md", allowed: true},
		{name: "combined: unowned and not project-allowed", gates: []declaration.StructureGate{fxProject, fxMdmap},
			path: "src/main.go", allowed: false,
			wantIn: []string{"this project's structure gate", "deny-by-default"}},
		{name: "combined: owned, plugin-allowed, not project-allowed", gates: []declaration.StructureGate{fxProject, fxMdmap},
			path: ".mdmap/mindmap/a/mindmap.yaml", allowed: true},
		{name: "combined: owned, project-allowed, not plugin-allowed (no widening)", gates: []declaration.StructureGate{fxProject, fxMdmap},
			path: ".mdmap/notes/n.md", allowed: false,
			wantIn: []string{`plugin "mdmap"'s structure gate`, "does not widen"}},
		{name: "combined: owned, plugin-allowed, project deny vetoes", gates: []declaration.StructureGate{fxProject, fxMdmap},
			path: ".mdmap/a/k.secret", allowed: false,
			wantIn: []string{"this project's structure gate", "last word", `plugin "mdmap"`}},

		// -- two plugins, disjoint scopes --
		{name: "disjoint: mdmap governs its own", gates: []declaration.StructureGate{fxProject, fxMdmap, fxADR},
			path: ".mdmap/stray.md", allowed: false, wantIn: []string{`plugin "mdmap"`}},
		{name: "disjoint: adr governs its own (allowed)", gates: []declaration.StructureGate{fxProject, fxMdmap, fxADR},
			path: "src/.adr/0001.md", allowed: true},
		{name: "disjoint: adr governs its own (refused)", gates: []declaration.StructureGate{fxProject, fxMdmap, fxADR},
			path: "src/.adr/notes.txt", allowed: false, wantIn: []string{`plugin "adr"`, `scope "**/.adr/"`, "glob **/.adr/*.md"}},
		{name: "disjoint: unowned remainder follows the project (allowed)", gates: []declaration.StructureGate{fxProject, fxMdmap, fxADR},
			path: "docs/x.md", allowed: true},
		{name: "disjoint: unowned remainder follows the project (refused)", gates: []declaration.StructureGate{fxProject, fxMdmap, fxADR},
			path: "src/x.go", allowed: false, wantIn: []string{"this project's structure gate"}},

		// -- two plugins, overlapping wildcard scopes --
		{name: "overlap: write in the overlap refused naming both", gates: []declaration.StructureGate{fxADR, fxShadowADR},
			path: "docs/a/.adr/0001.md", allowed: false,
			wantIn: []string{"ownership conflict", `plugin "adr"`, `plugin "shadow-adr"`, "adr/structure", "shadow-adr/structure"}},
		{name: "overlap: write only in one scope decided by that one", gates: []declaration.StructureGate{fxADR, fxShadowADR},
			path: "src/.adr/0001.md", allowed: true},
		{name: "overlap: write only in one scope refused by that one", gates: []declaration.StructureGate{fxADR, fxShadowADR},
			path: "src/.adr/x.txt", allowed: false, wantIn: []string{`plugin "adr"`}},

		// -- wildcard scope matching --
		{name: "wildcard scope matches a nested folder", gates: []declaration.StructureGate{fxADR},
			path: "a/b/.adr/x.txt", allowed: false, wantIn: []string{`plugin "adr"`}},
		{name: "wildcard scope matches a root-level folder", gates: []declaration.StructureGate{fxADR},
			path: ".adr/x.txt", allowed: false, wantIn: []string{`plugin "adr"`}},
		{name: "wildcard scope does not match a look-alike", gates: []declaration.StructureGate{fxADR},
			path: "a/.adrx/y.md", allowed: true},
		{name: "a FILE named like the scope folder is not inside it", gates: []declaration.StructureGate{fxMdmap},
			path: ".mdmap", allowed: true},
		{name: "literal scope does not match a look-alike", gates: []declaration.StructureGate{fxMdmap},
			path: ".mdmapx/y.md", allowed: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			set := compileSet(t, tc.gates...)
			allowed, reason := set.Decide(tc.path)
			assert.Equal(t, tc.allowed, allowed, "decision for %q (reason: %s)", tc.path, reason)
			if tc.allowed {
				assert.Empty(t, reason)
			}
			for _, want := range tc.wantIn {
				assert.Contains(t, reason, want)
			}
		})
	}
}

// A project-only set decides EXACTLY as the single structure gate always has —
// same verdict AND same refusal wording — over every shape the existing structure
// tests exercise (glob, regex, deny exception, one-segment *).
func TestStructureSet_ProjectOnlyIsUnchanged(t *testing.T) {
	gate := projectGate(
		[]declaration.StructureEntry{
			glob("memories/updates/*.md"),
			regex(`^memories/decisions/[0-9]{8}_[a-z0-9-]+/.*\.md$`),
			glob("memories/**/*.md"),
		},
		[]declaration.StructureEntry{glob("memories/secret/*.md"), regex(`(^|/)DRAFT_.*\.md$`)},
	)
	single := compile(t, gate.Allow, gate.Deny)
	set := compileSet(t, gate)

	for _, p := range []string{
		"memories/updates/note.md",
		"memories/updates/deep/note.md",
		"memories/decisions/20260101_the-call/notes.md",
		"old/memories/decisions/20260101_x/notes.md",
		"memories/secret/keys.md",
		"memories/topics/DRAFT_x.md",
		"src/main.go",
		".mdmap/x.md",
	} {
		wantOK, wantReason := single.Allows(p)
		gotOK, gotReason := set.Decide(p)
		assert.Equal(t, wantOK, gotOK, "verdict for %q", p)
		assert.Equal(t, wantReason, gotReason, "refusal wording for %q", p)
	}
}

// A disabled plugin's structure never reaches the set (the loader drops it), so
// its former scope is unowned: the project decides there, or nothing does.
func TestStructureSet_DisabledPluginScopeBecomesUnowned(t *testing.T) {
	// With mdmap loaded, the project's allow does not widen .mdmap/.
	withPlugin := compileSet(t, fxProject, fxMdmap)
	ok, _ := withPlugin.Decide(".mdmap/notes/n.md")
	assert.False(t, ok)

	// With mdmap disabled (absent from the set), the project's allow decides.
	disabled := compileSet(t, fxProject)
	ok, _ = disabled.Decide(".mdmap/notes/n.md")
	assert.True(t, ok, "the former scope is unowned, so the project's allow applies")
	ok, _ = disabled.Decide(".mdmap/stray.md")
	assert.False(t, ok, "and the project's deny-by-default applies too")

	// With neither, the former scope is simply permitted.
	ok, _ = compileSet(t).Decide(".mdmap/stray.md")
	assert.True(t, ok)
}

func TestStructureSet_Empty(t *testing.T) {
	assert.True(t, compileSet(t).Empty())
	assert.False(t, compileSet(t, fxMdmap).Empty())
	assert.False(t, compileSet(t, fxProject).Empty())
}
