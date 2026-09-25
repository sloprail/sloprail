package dispatch

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/declaration"
)

// The structure gate is deny-by-default over the paths it COVERS, with `deny`
// carving exceptions out of `allow`. These pin each half: an allowed path
// passes, an unlisted (but covered) path is refused, a deny exception subtracts
// from allow, and both entry kinds (glob and regex) work — first over a single
// UNSCOPED gate (the project's own, or the historical single-file shape), then
// over several gates COMPOSED together (a project's plus a plugin's).

// compile builds a composed gate from one UNSCOPED declaration — the shape a
// project's own structure.yaml takes, and the shape every test in this file
// used before composition existed. Kept so the single-gate tests below read
// exactly as they did; compileMany below is the multi-gate counterpart.
func compile(t *testing.T, allow, deny []declaration.StructureEntry) *ComposedStructureGate {
	t.Helper()
	return compileMany(t, declaration.StructureGate{Allow: allow, Deny: deny})
}

// compileMany composes several structure declarations at once — a project's
// alongside one or more plugins'.
func compileMany(t *testing.T, sgs ...declaration.StructureGate) *ComposedStructureGate {
	t.Helper()
	c, err := CompileStructureGates(sgs)
	require.NoError(t, err)
	return c
}

func glob(p string) declaration.StructureEntry  { return declaration.StructureEntry{Glob: p} }
func regex(p string) declaration.StructureEntry { return declaration.StructureEntry{Regex: p} }

// A path matching an allow glob is permitted; one matching nothing is denied.
func TestStructure_DenyByDefault(t *testing.T) {
	sg := compile(t, []declaration.StructureEntry{glob("memories/updates/*.md")}, nil)

	allowed, _ := sg.Allows("memories/updates/note.md")
	assert.True(t, allowed, "a path under an allow glob is permitted")

	allowed, reason := sg.Allows("src/main.go")
	assert.False(t, allowed, "a path matching no allow entry is denied by default")
	assert.Contains(t, reason, "deny-by-default")
}

// A single `*` does not cross a separator — the allow glob's anchoring is the
// file-guard glob's, so a nested path is not admitted by a one-segment glob.
func TestStructure_GlobIsOneSegment(t *testing.T) {
	sg := compile(t, []declaration.StructureEntry{glob("memories/updates/*.md")}, nil)

	allowed, _ := sg.Allows("memories/updates/note.md")
	assert.True(t, allowed)

	allowed, _ = sg.Allows("memories/updates/deep/note.md")
	assert.False(t, allowed, "* is one segment, so a deeper path is not allowed by it")
}

// A regex allow entry works — the case a glob cannot express (a dated folder).
func TestStructure_RegexAllow(t *testing.T) {
	sg := compile(t, []declaration.StructureEntry{
		regex(`^memories/decisions/[0-9]{8}_[a-z0-9-]+/.*\.md$`),
	}, nil)

	allowed, _ := sg.Allows("memories/decisions/20260101_the-call/notes.md")
	assert.True(t, allowed, "a path matching the regex is permitted")

	allowed, _ = sg.Allows("memories/decisions/nope/notes.md")
	assert.False(t, allowed, "a path not matching the dated-folder shape is denied")

	// Anchored: not a substring match.
	allowed, _ = sg.Allows("old/memories/decisions/20260101_x/notes.md")
	assert.False(t, allowed, "the regex is anchored by the author's own ^, so it is not a substring match")
}

// Both a glob AND a regex allow entry in one gate each permit their own shape —
// the example's exact structure.yaml.
func TestStructure_GlobAndRegexTogether(t *testing.T) {
	sg := compile(t, []declaration.StructureEntry{
		glob("memories/updates/*.md"),
		regex(`^memories/decisions/[0-9]{8}_[a-z0-9-]+/.*\.md$`),
	}, nil)

	allowed, _ := sg.Allows("memories/updates/note.md")
	assert.True(t, allowed, "the glob entry permits its shape")

	allowed, _ = sg.Allows("memories/decisions/20260101_x/notes.md")
	assert.True(t, allowed, "the regex entry permits its shape")

	allowed, _ = sg.Allows("elsewhere/x.md")
	assert.False(t, allowed, "a path matching neither is denied")
}

// A deny exception carves a path back OUT of what allow permitted.
func TestStructure_DenyCarvesException(t *testing.T) {
	sg := compile(t,
		[]declaration.StructureEntry{glob("memories/**/*.md")},
		[]declaration.StructureEntry{glob("memories/secret/*.md")},
	)

	// Allowed by the broad allow.
	allowed, _ := sg.Allows("memories/topics/a.md")
	assert.True(t, allowed)

	// Under allow, but the deny carves it out.
	allowed, reason := sg.Allows("memories/secret/keys.md")
	assert.False(t, allowed, "a deny exception subtracts from allow")
	assert.Contains(t, reason, "deny")
}

// A deny that matches nothing already allowed is a no-op — it cannot ADD a
// permission, only subtract.
func TestStructure_DenyMatchingNothingAllowed_IsNoOp(t *testing.T) {
	sg := compile(t,
		[]declaration.StructureEntry{glob("memories/updates/*.md")},
		[]declaration.StructureEntry{glob("src/*.go")}, // matches nothing in allow
	)

	// The allowed path is unaffected by a deny that does not cover it.
	allowed, _ := sg.Allows("memories/updates/note.md")
	assert.True(t, allowed)

	// The deny does not grant src/ a permission it never had.
	allowed, _ = sg.Allows("src/main.go")
	assert.False(t, allowed, "a deny is deny-only; it never adds an allowance")
}

// A regex deny exception carves out too.
func TestStructure_RegexDeny(t *testing.T) {
	sg := compile(t,
		[]declaration.StructureEntry{glob("**/*.md")},
		[]declaration.StructureEntry{regex(`(^|/)DRAFT_.*\.md$`)},
	)

	allowed, _ := sg.Allows("docs/final.md")
	assert.True(t, allowed)

	allowed, _ = sg.Allows("docs/DRAFT_wip.md")
	assert.False(t, allowed, "the regex deny carves out draft files")
}

// ---------------------------------------------------------------------------
// Composition across several covering files
// ---------------------------------------------------------------------------

// scoped builds a StructureGate with a scope, allow and origin — the shape a
// plugin's structure gate takes (scope required, per ValidateStructureGate).
func scoped(scope []declaration.StructureEntry, allow []declaration.StructureEntry, deny []declaration.StructureEntry, origin declaration.Origin, dir string) declaration.StructureGate {
	return declaration.StructureGate{Scope: scope, Allow: allow, Deny: deny, Origin: origin, Dir: dir}
}

// A plugin's scoped allow permits a path inside its scope that the PROJECT's
// own allowlist does not list — the headline composition property: the
// project's global allowlist does not need to name a plugin-owned shape.
func TestComposed_PluginScopeAllowsWhatProjectDoesNot(t *testing.T) {
	project := declaration.StructureGate{
		Allow: []declaration.StructureEntry{glob("src/**")},
	}
	plugin := scoped(
		[]declaration.StructureEntry{glob("memories/tasks/**")},
		[]declaration.StructureEntry{regex(`^memories/tasks/[a-z0-9-]+/[a-z0-9-]+/.*$`)},
		nil,
		declaration.Origin{Plugin: "sloprail-tasks"},
		"plugin/.sloprail/file-guard",
	)
	c := compileMany(t, project, plugin)

	allowed, _ := c.Allows("memories/tasks/eng/fix-bug/notes.md")
	assert.True(t, allowed, "the plugin's own allow covers a path its scope owns, "+
		"even though the project's allowlist never mentions memories/")

	allowed, _ = c.Allows("src/main.go")
	assert.True(t, allowed, "the project's own allow still works")
}

// A path outside EVERY covering gate's scope is unaffected by the plugin: no
// gate has an opinion, so nothing is refused for a reason the plugin invented.
func TestComposed_PathOutsideEveryScopeIsUnaffectedByPlugin(t *testing.T) {
	project := declaration.StructureGate{
		Allow: []declaration.StructureEntry{glob("src/**")},
	}
	plugin := scoped(
		[]declaration.StructureEntry{glob("memories/tasks/**")},
		[]declaration.StructureEntry{glob("memories/tasks/**")},
		nil,
		declaration.Origin{Plugin: "sloprail-tasks"},
		"plugin/.sloprail/file-guard",
	)
	c := compileMany(t, project, plugin)

	// docs/ is outside BOTH the project's allow-scope (unscoped, so it covers
	// docs/ too, but does not allow it) — pick a path outside the PROJECT's
	// coverage by using an unscoped project gate would still cover everything,
	// so use a SCOPED project instead to prove "no covering gate => no opinion".
	scopedProject := scoped(
		[]declaration.StructureEntry{glob("src/**")},
		[]declaration.StructureEntry{glob("src/**")},
		nil,
		declaration.Origin{},
		"project/.sloprail/file-guard",
	)
	c = compileMany(t, scopedProject, plugin)

	allowed, reason := c.Allows("docs/readme.md")
	assert.True(t, allowed, "a path outside every covering gate's scope has no opinion from any gate: %s", reason)
}

// A `deny` in a covering plugin refuses, even though the project's own gate
// (which does not cover this path) would have nothing to say.
func TestComposed_DenyInCoveringPluginRefuses(t *testing.T) {
	project := scoped(
		[]declaration.StructureEntry{glob("src/**")},
		[]declaration.StructureEntry{glob("src/**")},
		nil,
		declaration.Origin{},
		"project/.sloprail/file-guard",
	)
	plugin := scoped(
		[]declaration.StructureEntry{glob("memories/tasks/**")},
		[]declaration.StructureEntry{glob("memories/tasks/**")},
		[]declaration.StructureEntry{glob("memories/tasks/secret/**")},
		declaration.Origin{Plugin: "sloprail-tasks"},
		"plugin/.sloprail/file-guard",
	)
	c := compileMany(t, project, plugin)

	allowed, _ := c.Allows("memories/tasks/eng/fix-bug/notes.md")
	assert.True(t, allowed, "allowed by the plugin's own allow")

	allowed, reason := c.Allows("memories/tasks/secret/x.md")
	assert.False(t, allowed, "the plugin's own deny exception refuses a path under its scope")
	assert.Contains(t, reason, "sloprail-tasks/structure")
}

// Two covering gates: the UNION of their allows, minus the UNION of their
// denies. A path allowed by one covering gate and denied by ANOTHER covering
// gate is refused — deny from any covering file wins.
func TestComposed_UnionOfAllowsMinusUnionOfDenies(t *testing.T) {
	a := scoped(
		[]declaration.StructureEntry{glob("shared/**")},
		[]declaration.StructureEntry{glob("shared/**")},
		nil,
		declaration.Origin{Plugin: "plugin-a"},
		"a/.sloprail/file-guard",
	)
	b := scoped(
		[]declaration.StructureEntry{glob("shared/**")},
		nil,
		[]declaration.StructureEntry{glob("shared/blocked/**")},
		declaration.Origin{Plugin: "plugin-b"},
		"b/.sloprail/file-guard",
	)
	c := compileMany(t, a, b)

	allowed, _ := c.Allows("shared/ok.md")
	assert.True(t, allowed, "plugin a's allow covers it, and plugin b's deny does not match")

	allowed, reason := c.Allows("shared/blocked/x.md")
	assert.False(t, allowed, "plugin b's deny carves this path back out even though plugin a's allow matched it")
	assert.Contains(t, reason, "plugin-b/structure")
}

// The refusal reason names the covering file(s) so a reader knows where to add
// an allow.
func TestComposed_RefusalNamesCoveringFiles(t *testing.T) {
	plugin := scoped(
		[]declaration.StructureEntry{glob("memories/tasks/**")},
		[]declaration.StructureEntry{glob("memories/tasks/only-this/**")},
		nil,
		declaration.Origin{Plugin: "sloprail-tasks"},
		"plugin/.sloprail/file-guard",
	)
	c := compileMany(t, plugin)

	allowed, reason := c.Allows("memories/tasks/elsewhere/x.md")
	require.False(t, allowed)
	assert.Contains(t, reason, "sloprail-tasks/structure", "the refusal names the covering plugin's qualified key")
	assert.Contains(t, reason, "plugin/.sloprail/file-guard/structure.yaml", "the refusal names the file to edit")
}
