package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/sessionstate"
)

// countGit puts a `git` shim first on PATH that logs every invocation, whoever starts it
// (gitrepo.run or a direct exec), and returns a func reporting how many ran since the last call.
func countGit(t *testing.T) func() int {
	t.Helper()
	real, err := exec.LookPath("git")
	require.NoError(t, err)
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	shim := fmt.Sprintf("#!/bin/sh\necho \"$*\" >> %q\nexec %q \"$@\"\n", log, real)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "git"), []byte(shim), 0o755))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() int {
		b, _ := os.ReadFile(log)
		_ = os.Remove(log)
		if os.Getenv("SR_SHOW_GIT") != "" {
			t.Logf("calls:\n%s", b)
		}
		return strings.Count(string(b), "\n")
	}
}

// pretoolRepo is a repository with `branches` extra local branches and `worktrees` sub-agent
// worktrees, each on its own branch with its own tracked row, observed once.
func pretoolRepo(t *testing.T, worktrees, branches int) (sessionstate.Store, rootSession) {
	t.Helper()
	proj, reg, rs := ruledAndObserved(t, func(proj string) { withOrigin(t, proj) })
	base := runGit(t, proj, "rev-parse", "HEAD")
	var refs strings.Builder
	for i := 0; i < branches; i++ {
		fmt.Fprintf(&refs, "create refs/heads/old-%d %s\n", i, base)
	}
	cmd := exec.Command("git", "update-ref", "--stdin")
	cmd.Dir = proj
	cmd.Stdin = strings.NewReader(refs.String())
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "%s", out)
	for i := 0; i < worktrees; i++ {
		d := filepath.Join(t.TempDir(), fmt.Sprintf("wt%d", i))
		runGit(t, proj, "worktree", "add", "-q", "-b", fmt.Sprintf("worktree-agent-%d", i), d)
		_, err := reg.RegisterFolder(sessionstate.Folder{SessionID: rs.ID, Path: d, Role: sessionstate.FolderSubagentWorktree, GitRoot: d, BaseRef: base, AgentID: fmt.Sprintf("agent-%d", i)})
		require.NoError(t, err)
		require.NoError(t, ensureTracked(reg, rs.ID, d, fmt.Sprintf("agent-%d", i), base))
	}
	require.NoError(t, trackMissing(reg, rs, HookPayload{}))
	return reg, rs
}

// What a hook costs in git processes must not grow with branches x worktrees: a branch checked out
// in another worktree is decided from the registry, and a decision is remembered until a tip or a
// base changes.
func TestPreToolGitCallsDoNotGrowWithBranchesTimesWorktrees(t *testing.T) {
	count := countGit(t)
	type repo struct {
		reg sessionstate.Store
		rs  rootSession
	}
	build := func(worktrees, branches int) repo {
		reg, rs := pretoolRepo(t, worktrees, branches)
		return repo{reg, rs}
	}
	measure := func(r repo, agent string, ownOnly bool) int {
		require.NoError(t, trackMissingOf(r.reg, r.rs, HookPayload{AgentID: agent}, ownOnly)) // the decisions are remembered
		count()
		require.NoError(t, trackMissingOf(r.reg, r.rs, HookPayload{AgentID: agent}, ownOnly))
		return count()
	}
	small, big := build(3, 8), build(8, 80)
	for _, tc := range []struct{ name, agent string }{{"sub-agent", "agent-1"}, {"root", ""}} {
		s, b := measure(small, tc.agent, true), measure(big, tc.agent, true)
		t.Logf("%s tool call: %d git calls (3 wt x 8 br), %d (8 wt x 80 br)", tc.name, s, b)
		require.LessOrEqual(t, b, s+4, "git calls per %s tool call grow with branches x worktrees", tc.name)
	}
	// The Stop observes every folder: linear in worktrees (a few calls each), never in branches x worktrees.
	s, b := measure(small, "", false), measure(big, "", false)
	t.Logf("root stop: %d git calls (3 wt x 8 br), %d (8 wt x 80 br)", s, b)
	require.LessOrEqual(t, b, s+5*6, "git calls per Stop grow faster than a few per worktree")
}
