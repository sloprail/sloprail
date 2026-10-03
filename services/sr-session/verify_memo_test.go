package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/sessionstate"
)

// A range already verified keeps its answer (a pass and a refusal alike), and every input of the
// answer invalidates it: the commits, the rules, the results ref, who asks, the wording.
func TestVerifyMemoHitsAndEveryInvalidation(t *testing.T) {
	proj := initRepo(t)
	ruleDir := filepath.Join(proj, ".sloprail", "file-guard", "g")
	require.NoError(t, os.MkdirAll(ruleDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(ruleDir, "guard.yaml"), []byte("match: '*.md'\n"), 0o644))
	runGit(t, proj, "add", "-A")
	runGit(t, proj, "commit", "-m", "rule")
	guards := []declaration.FileGuard{{Name: "g", Dir: ruleDir}}
	r := sessionstate.TrackedRange{Folder: proj, Head: "main"}
	rng := gitrepo.Range{Base: "b1", Head: "h1"}
	p := HookPayload{AgentID: "a1"}

	memo := &verifyMemo{store: openStore(t)}
	key := func(p HookPayload, where string, rng gitrepo.Range, tip string) string {
		memo = &verifyMemo{store: memo.store} // a fresh Stop: nothing hashed yet
		return memo.key(r, p, where, rng, guards, nil, tip)
	}
	k := key(p, "w", rng, "t1")
	require.NotEmpty(t, k)

	_, hit := memo.get(k)
	assert.False(t, hit, "nothing is remembered before the first verify")
	memo.put(k, "")
	got, hit := memo.get(k)
	assert.True(t, hit)
	assert.Equal(t, "", got, "a pass is remembered as a pass")

	assert.Equal(t, k, key(p, "w", rng, "t1"), "the same inputs name the same answer")
	refusal := "In x: refused (file-guard g)"
	k2 := key(p, "w", gitrepo.Range{Base: "b1", Head: "h2"}, "t1")
	assert.NotEqual(t, k, k2, "a moved tip")
	memo.put(k2, refusal)
	got, hit = memo.get(k2)
	assert.True(t, hit)
	assert.Equal(t, refusal, got, "a refusal is remembered with its text")

	assert.NotEqual(t, k, key(p, "w", gitrepo.Range{Base: "b2", Head: "h1"}, "t1"), "a moved base")
	assert.NotEqual(t, k, key(p, "w", rng, "t2"), "a new `sr-checks run` moves the results ref")
	assert.NotEqual(t, k, key(HookPayload{AgentID: "a2"}, "w", rng, "t1"), "another agent")
	assert.NotEqual(t, k, key(p, "w2", rng, "t1"), "another wording")

	require.NoError(t, os.WriteFile(filepath.Join(ruleDir, "guard.yaml"), []byte("match: '*.go'\n"), 0o644))
	assert.NotEqual(t, k, key(p, "w", rng, "t1"), "an edited rule")

	other := &verifyMemo{store: memo.store}
	assert.NotEqual(t, other.key(r, p, "w", rng, nil, nil, "t1"), other.key(r, p, "w", rng, nil, []string{"broke"}, "t1"), "a rule that failed to load")
}

func TestNilVerifyMemoRemembersNothing(t *testing.T) {
	var m *verifyMemo
	assert.Equal(t, "", m.key(sessionstate.TrackedRange{}, HookPayload{}, "", gitrepo.Range{}, nil, nil, ""))
	m.put("k", "x")
	_, hit := m.get("k")
	assert.False(t, hit)
}

// Worktrees of one repository share every branch: a branch registered once per worktree is one
// range, the one with an explicit base preferred.
func TestCollapseByRepoPicksOneRowPerBranch(t *testing.T) {
	proj := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(proj, "a"), []byte("a"), 0o644))
	runGit(t, proj, "add", "-A")
	runGit(t, proj, "commit", "-m", "a")
	runGit(t, proj, "branch", "feat")
	wt := filepath.Join(t.TempDir(), "wt")
	runGit(t, proj, "worktree", "add", "--detach", wt)
	ranges := []sessionstate.TrackedRange{
		{Folder: proj, Head: "feat", AddedBy: sessionstate.RangeAuto},
		{Folder: wt, Head: "feat", AddedBy: sessionstate.RangeAuto},
		{Folder: wt, Head: "feat", AddedBy: sessionstate.RangeAgent, Base: "main"},
		{Folder: proj, Head: "main", AddedBy: sessionstate.RangeAuto},
		{Folder: wt, Head: "gone", HeadSHA: "abc", AddedBy: sessionstate.RangeAuto, AgentID: "", UntrackedReason: ""},
	}
	got := collapseByRepo(ranges, "", newCoverMemo())
	assert.Equal(t, []bool{false, false, true, true}, got[:4], "one row per branch: feat's explicit row, then main")
	assert.True(t, got[4], "a row of a branch that is gone is its own range")
}
