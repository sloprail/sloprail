package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/sessionstate"
)

// boundedRepo is a repository with a.md committed, a store whose baseline is that commit, and a
// helper that opens the next cycle the way a Stop followed by a hook does: how the agent left a.md
// at its last Stop is `left`, and a.md now holds `now`.
func boundedRepo(t *testing.T) (repo string, store sessionstate.Store, cycle func(i int, left, now string, running ...string)) {
	t.Helper()
	repo = t.TempDir()
	gitRun(t, repo, "init", "-q")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "a.md"), []byte("base\n"), 0o644))
	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, "commit", "-qm", "base")
	head, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	require.NoError(t, err)
	store = openTestStore(t)
	require.NoError(t, store.SetMeta(sessionstate.MetaBaselineCommit, strings.TrimSpace(string(head))))
	cycle = func(i int, left, now string, running ...string) {
		t.Helper()
		require.NoError(t, updateCycle(store, func(c *cycleMeta) {
			c.State, c.EndedAt = "ended", int64(i*10-1)
			c.End = map[string]historyState{"a.md": st(store, left)}
			c.Tasks, c.TasksBy = len(running) > 0, running
		}))
		require.NoError(t, os.WriteFile(filepath.Join(repo, "a.md"), []byte(now), 0o644))
		require.NoError(t, beginCycle(store, repo, int64(i*10), nil, nil))
	}
	return repo, store, cycle
}

func storedCitations(t *testing.T, store sessionstate.Store) string {
	t.Helper()
	raw, _, err := store.Meta(sessionstate.MetaCitations)
	require.NoError(t, err)
	return raw
}

// A file the agent never touched that is not as the agent left it (a background job writes it) is
// found again at the first hook of every cycle. It is one stretch: the history holds it once, however
// many cycles the session runs, and every hook of the session stays as cheap as the first.
func TestHistoryStaysOneStretchWhileTheFileStaysAsAnotherLeftIt(t *testing.T) {
	_, store, cycle := boundedRepo(t)
	long := strings.Repeat("sleep 1000 && echo done ", 400)
	for i := 1; i <= 300; i++ {
		cycle(i, "left\n", "changed by a job\n", fmt.Sprintf("nohup job-%d %s", i, long))
	}
	pts := historyIn(store, true)["a.md"]
	require.Len(t, pts, 2, "the first sighting and the newest")
	assert.True(t, pts[0].BetweenTurns)
	assert.Equal(t, int64(10), pts[0].At, "the first sighting stays")
	assert.Equal(t, int64(3000), pts[1].At)
	assert.LessOrEqual(t, len(pts[1].By), 150, "a refusal names what ran; it does not quote its script")
	assert.Contains(t, pts[1].By, "job-300", "and names the newest work")
	assert.Less(t, len(storedCitations(t, store)), 2_000)
}

// A file that keeps CHANGING gives a history of as many stretches as it changed, up to a bound:
// the newest stay, and the record stays small.
func TestHistoryOfAFileThatKeepsChangingIsBounded(t *testing.T) {
	_, store, cycle := boundedRepo(t)
	for i := 1; i <= 400; i++ {
		cycle(i, fmt.Sprintf("v%d\n", i-1), fmt.Sprintf("v%d\n", i), "nohup a-long-running-job")
	}
	pts := historyIn(store, true)["a.md"]
	assert.Len(t, pts, sessionstate.MaxUncitedPoints+1, "the newest stretches and the anchor standing for the rest")
	assert.True(t, pts[0].Foreign && pts[0].From == nil, "what was dropped leaves a point from nowhere")
	assert.Equal(t, int64(4000), pts[len(pts)-1].At, "the newest stretch is kept")
	assert.Less(t, len(storedCitations(t, store)), 40_000)
}

// What is owed — a cited change that landed — is never pushed out by uncited stretches around it.
func TestACitedChangeSurvivesAnyNumberOfUncitedStretches(t *testing.T) {
	repo, store, cycle := boundedRepo(t)
	abs := filepath.Join(repo, "a.md")
	require.NoError(t, os.WriteFile(abs, []byte("cited\n"), 0o644))
	require.NoError(t, recordPending(store, []pendingChange{{Path: "a.md", Abs: abs,
		Point: historyPoint{Cites: userCite, Before: st(store, "base\n"), After: st(store, "cited\n"), At: 1}}}))
	require.NoError(t, settleCitedChanges(store))
	for i := 1; i <= 200; i++ {
		cycle(i, fmt.Sprintf("v%d\n", i-1), fmt.Sprintf("v%d\n", i), "nohup job")
	}
	var cites int
	for _, p := range historyIn(store, true)["a.md"] {
		if len(p.Cites) > 0 {
			cites++
			assert.Equal(t, userCite, p.Cites)
		}
	}
	assert.Equal(t, 1, cites)
}

// The names a cycle record keeps of work that may still run are bounded the same way.
func TestTheCycleRecordNamesAtMostAFewCommandsEachCut(t *testing.T) {
	store := openTestStore(t)
	long := strings.Repeat("x", 5000)
	for i := 0; i < 100; i++ {
		require.NoError(t, markDetached(store, fmt.Sprintf("nohup %d %s", i, long)))
		require.NoError(t, markLaunched(store, fmt.Sprintf("run %d %s", i, long)))
	}
	c := readCycle(store)
	assert.Len(t, c.DetachedBy, sessionstate.MaxByEntries)
	assert.Len(t, c.Launched, sessionstate.MaxByEntries)
	for _, s := range append(c.DetachedBy, c.Launched...) {
		assert.LessOrEqual(t, len(s), sessionstate.MaxByEntryLen+len("…"))
	}
	assert.True(t, strings.HasPrefix(c.DetachedBy[len(c.DetachedBy)-1], "nohup 99 "), "the newest are kept")
}
