package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/checkstore"
	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/gitrepo"
)

// A long session leaves thousands of passed runs. Deciding, for every recorded tip and every
// rule, whether the rule already passed it (and where each range starts) once asked a git
// process for every pair of tip and passed head: a live session's Stop spent nineteen minutes
// on it. The plan must cost one pass over the commit graph, not one git process per pair.
func TestStopPlan_ThousandsOfPassedRunsAreAnsweredFromOneGraph(t *testing.T) {
	repo := initRepo(t)
	const commits = 1500
	var script strings.Builder
	for i := 1; i <= commits; i++ {
		fmt.Fprintf(&script, "commit refs/heads/main\nmark :%d\ncommitter t <t@e.x> %d +0000\ndata 2\nc%d\n", i, 1_700_000_000+i, i%10)
		if i > 1 {
			fmt.Fprintf(&script, "from :%d\n", i-1)
		}
		fmt.Fprintf(&script, "M 100644 inline f%d\ndata 2\nv\n\n", i)
	}
	imp := exec.Command("git", "fast-import", "--quiet")
	imp.Dir = repo
	imp.Stdin = strings.NewReader(script.String())
	out, err := imp.CombinedOutput()
	require.NoError(t, err, string(out))
	runGit(t, repo, "reset", "--hard", "main")
	shas := strings.Fields(runGit(t, repo, "rev-list", "--reverse", "main"))
	require.Len(t, shas, commits)

	// A thousand more commits on a side line cut early: heads that passed there, and that
	// contain none of main's later tips. Newest first, they are what every question meets first.
	const sideCommits = 1000
	var side strings.Builder
	for i := 1; i <= sideCommits; i++ {
		fmt.Fprintf(&side, "commit refs/heads/side\nmark :%d\ncommitter t <t@e.x> %d +0000\ndata 2\ns%d\n", i, 1_800_000_000+i, i%10)
		if i == 1 {
			fmt.Fprintf(&side, "from %s\n", shas[100])
		} else {
			fmt.Fprintf(&side, "from :%d\n", i-1)
		}
		fmt.Fprintf(&side, "M 100644 inline s%d\ndata 2\nv\n\n", i)
	}
	imp2 := exec.Command("git", "fast-import", "--quiet")
	imp2.Dir = repo
	imp2.Stdin = strings.NewReader(side.String())
	out2, err := imp2.CombinedOutput()
	require.NoError(t, err, string(out2))
	sideShas := strings.Fields(runGit(t, repo, "rev-list", "--reverse", shas[100]+"..side"))
	require.Len(t, sideShas, sideCommits)

	var guards []declaration.FileGuard
	for i := 0; i < 6; i++ {
		guards = append(guards, declaration.FileGuard{Name: fmt.Sprintf("r%d", i), Dir: t.TempDir(), Checks: []declaration.Check{{Script: "./c.sh"}}})
	}
	results, err := checkstore.Open(filepath.Join(t.TempDir(), "checks.db"))
	require.NoError(t, err)
	defer results.Close()
	runs := 0
	for _, g := range guards {
		for i := 0; i < commits; i += 3 {
			id, err := results.RecordRun(checkstore.CheckRun{
				RunIdentity: checkstore.RunIdentity{RepoID: "r", Branch: "main", SessionID: "s"},
				BatchID:     "b", CheckID: g.Qualified(), BaseRef: shas[0], HeadRef: shas[i],
				Metadata: map[string]any{"ruleHash": "h"},
			})
			require.NoError(t, err)
			require.NoError(t, results.FinishRun(id))
			runs++
		}
	}
	for _, g := range guards {
		for i := 0; i < sideCommits; i += 2 {
			id, err := results.RecordRun(checkstore.CheckRun{
				RunIdentity: checkstore.RunIdentity{RepoID: "r", Branch: "side", SessionID: "s"},
				BatchID:     "b", CheckID: g.Qualified(), BaseRef: shas[100], HeadRef: sideShas[i],
				Metadata: map[string]any{"ruleHash": "h"},
			})
			require.NoError(t, err)
			require.NoError(t, results.FinishRun(id))
			runs++
		}
	}
	require.Greater(t, runs, 3000)

	results = &familyResults{Store: results, heads: map[string][]string{}}
	started := time.Now()
	primeGraph(repo, guards, results)
	settled, tips := 0, 0
	for tipAt := 200; tipAt < commits; tipAt += 100 { // a dozen tips, all on main past the fork
		tips++
		tip := shas[tipAt]
		if judgedByEvery(repo, tip, guards, results) {
			settled++
		}
		if tips%4 == 0 { // a range costs a handful of git calls of its own; a few of them suffice
			for _, g := range guards {
				_, err := resolveRuleRangeAt(repo, tip, "", g, results, nil, nil)
				require.NoError(t, err)
			}
		}
	}
	took := time.Since(started)

	assert.Equal(t, tips, settled, "every tip lies below a later passed head of every rule (on main)")
	t.Logf("planned %d tips over %d passed runs in %s", tips, runs, took)
	assert.Less(t, took, 20*time.Second,
		"deciding what thousands of passed runs cover must not cost a git process per pair (took %s)", took)

	// The answer is the one git gives.
	g := gitrepo.LoadGraph(repo)
	ok, err := gitrepo.IsAncestorFast(repo, g, shas[5], shas[900])
	require.NoError(t, err)
	assert.True(t, ok)
	ok, err = gitrepo.IsAncestorFast(repo, g, shas[900], shas[5])
	require.NoError(t, err)
	assert.False(t, ok)
}
