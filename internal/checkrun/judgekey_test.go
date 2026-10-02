package checkrun

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/changeset"
	"github.com/sloprail/sloprail/internal/declaration"
	dispatchcore "github.com/sloprail/sloprail/internal/dispatch"
	"github.com/sloprail/sloprail/internal/transcript"
)

// The key a judge's verdict is stored under is computed by `run` before the judge and by
// `verify` afterwards, each over a snapshot in its own temp directory: both must reach the
// same key, and it must move only with what the judge is asked.

func keyPayload() changeset.Payload {
	cs := changeset.Changeset{
		Base: "b0", Head: "h0",
		Commits: []changeset.Commit{{SHA: "c1", Subject: "split", Trailers: map[string][]string{"Cites-User": {"q"}}}},
		Files:   []changeset.File{{Path: "a.go", Status: "M", Commits: []string{"c1"}, NewContent: "2", Diff: "@@ x"}},
		Citations: []changeset.Citation{{Citation: transcript.Citation{Quote: "q", Path: "/t", Line: 3, Call: "Bash: one"},
			Commits: []string{"c1"}, Files: []string{"a.go"}}},
	}
	return changeset.NewPayload(cs, changeset.Whole(cs), "/t.jsonl", nil)
}

func keyRule(t *testing.T, citation bool) (declaration.FileGuard, declaration.Check, string) {
	t.Helper()
	dir := t.TempDir()
	// The prompt renders the slice and prepare's context (which may carry the snapshot path),
	// and never the commits or the citations.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "j.md.j2"), []byte("Judge:\n{{ change }}\nctx: {{ additionalContext.where }}\n"), 0o644))
	g := declaration.FileGuard{Name: "r", Dir: dir}
	if citation {
		g.Require = []declaration.Prerequisite{{Citation: &declaration.CitationPrerequisite{}}}
	}
	return g, declaration.Check{Judge: "j.md.j2", Model: "m"}, dir
}

func key(t *testing.T, g declaration.FileGuard, c declaration.Check, p changeset.Payload, tree, prepFP string) string {
	t.Helper()
	req := dispatchcore.Request{Dir: g.Dir, GuardName: g.Name, ProjectRoot: tree, Changeset: &p}
	prep := dispatchcore.Prepared{Context: declaration.PreparedContext{"where": tree + "/a.go"}, Fingerprint: prepFP}
	fp, refusal, err := judgeKey(dispatchcore.Runner{}, g, req, p, c, prep)
	require.NoError(t, err)
	require.Empty(t, refusal)
	return fp
}

func TestJudgeKey_SameCheckInTwoSnapshotDirsKeysTheSame(t *testing.T) {
	g, c, _ := keyRule(t, true)
	a := key(t, g, c, keyPayload(), "/tmp/sr-tree-111", "x:/tmp/sr-tree-111")
	b := key(t, g, c, keyPayload(), "/tmp/sr-tree-222", "x:/tmp/sr-tree-222")
	assert.Equal(t, a, b)
	g, c, _ = keyRule(t, false)
	assert.Equal(t, key(t, g, c, keyPayload(), "/tmp/sr-tree-111", ""), key(t, g, c, keyPayload(), "/tmp/sr-tree-222", ""))
}

func TestJudgeKey_PrepareFingerprintChangesTheKey(t *testing.T) {
	g, c, _ := keyRule(t, false)
	assert.NotEqual(t, key(t, g, c, keyPayload(), "/t1", ""), key(t, g, c, keyPayload(), "/t1", "v1"))
	assert.NotEqual(t, key(t, g, c, keyPayload(), "/t1", "v1"), key(t, g, c, keyPayload(), "/t1", "v2"))
}

func TestJudgeKey_ChangingTheSliceChangesTheKey(t *testing.T) {
	g, c, _ := keyRule(t, false)
	other := keyPayload()
	other.Changeset.Files[0].NewContent = "3"
	other.Changeset.Files[0].Diff = "@@ y"
	// `change` is derived from the files' diffs.
	assert.NotEqual(t, key(t, g, c, keyPayload(), "/t1", ""), key(t, g, c, other, "/t1", ""))
}

// A citation reword is an input only for a rule that requires a citation (its prompt need not
// render them); for any other rule it is not, and cached passes survive it. The volatile
// Call field and SHAs never are.
func TestJudgeKey_ACitationRewordChangesTheKeyOnlyForCitationRules(t *testing.T) {
	reword := func(p *changeset.Payload) {
		p.Changeset.Commits[0].Trailers["Cites-User"] = []string{"another"}
		p.Changeset.Citations[0].Citation.Quote = "another"
	}
	volatile := func(p *changeset.Payload) {
		p.Changeset.Citations[0].Citation.Call = "Bash: two"
		p.Changeset.Commits[0].SHA = "rewritten"
	}
	for _, citation := range []bool{true, false} {
		g, c, _ := keyRule(t, citation)
		base := key(t, g, c, keyPayload(), "/t1", "")
		r, v := keyPayload(), keyPayload()
		reword(&r)
		volatile(&v)
		assert.Equal(t, base, key(t, g, c, v, "/t1", ""), "Call and SHAs are never input")
		if citation {
			assert.NotEqual(t, base, key(t, g, c, r, "/t1", ""))
		} else {
			assert.Equal(t, base, key(t, g, c, r, "/t1", ""))
		}
	}
}
