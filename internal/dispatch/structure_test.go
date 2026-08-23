package dispatch

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/declaration"
)

// The structure gate is deny-by-default over the tree, with `deny` carving
// exceptions out of `allow`. These pin each half: an allowed path passes, an
// unlisted path is refused, a deny exception subtracts from allow, and both entry
// kinds (glob and regex) work.

func compile(t *testing.T, allow, deny []declaration.StructureEntry) *StructureGate {
	t.Helper()
	sg, err := CompileStructureGate(declaration.StructureGate{Allow: allow, Deny: deny})
	require.NoError(t, err)
	return sg
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
