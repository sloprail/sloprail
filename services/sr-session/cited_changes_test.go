package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/filemod"
	"github.com/sloprail/sloprail/internal/grounding"
	"github.com/sloprail/sloprail/internal/sessionstate"
	"github.com/sloprail/sloprail/internal/transcript"
)

func openTestStore(t *testing.T) sessionstate.Store {
	t.Helper()
	store, err := sessionstate.Open(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	return store
}

var userCite = []transcript.Citation{{Quote: "q", SourceTypes: []transcript.SourceType{transcript.SourceUser}, Path: "/r.jsonl", Line: 1}}

func st(store sessionstate.Store, content string) historyState {
	return putState(store, true, content)
}

// A permitted call's cited change is kept only when the file holds what it
// produces by the next hook: a call that failed, was denied, or never ran
// leaves the file as it was, and its citation grounds nothing.
func TestSettleKeepsOnlyChangesThatLanded(t *testing.T) {
	root := t.TempDir()
	store := openTestStore(t)
	landed := filepath.Join(root, "landed.md")
	require.NoError(t, os.WriteFile(landed, []byte("new"), 0o644))
	failed := filepath.Join(root, "failed.md")
	require.NoError(t, os.WriteFile(failed, []byte("old"), 0o644))
	deleted := filepath.Join(root, "deleted.md")

	require.NoError(t, recordPending(store, []pendingChange{
		{Path: "landed.md", Abs: landed, Point: historyPoint{Cites: userCite, Before: st(store, "old"), After: st(store, "new"), At: 1}},
		{Path: "failed.md", Abs: failed, Point: historyPoint{Cites: userCite, Before: st(store, "old"), After: st(store, "new"), At: 2}},
		{Path: "deleted.md", Abs: deleted, Point: historyPoint{Cites: userCite, Before: st(store, "x"), Whole: true, At: 3}},
	}))
	require.NoError(t, settleCitedChanges(store))

	got := historyIn(store, true)
	assert.Contains(t, got, "landed.md")
	assert.Contains(t, got, "deleted.md", "a delete that happened landed")
	assert.NotContains(t, got, "failed.md", "a change that never landed was kept")

	// Settled means gone from the pending list: a later write of the same
	// bytes, uncited, does not revive it.
	require.NoError(t, os.WriteFile(failed, []byte("new"), 0o644))
	require.NoError(t, settleCitedChanges(store))
	assert.NotContains(t, historyIn(store, true), "failed.md")
}

func citedPostEvent(kind, path, oldContent, newContent string) event.Event {
	fe := filemod.FileEvent{Path: path, OldContent: oldContent, NewContent: newContent}
	return fe.Event(kind)
}

// A Post event carries the citations of every cited change that landed on its
// path; its history carries the points, by content hash, with each content
// retrievable once.
func TestAttachHistoriesCarriesCitationsAndContent(t *testing.T) {
	store := openTestStore(t)
	pt := historyPoint{Cites: userCite, Before: st(store, "base"), After: st(store, "cited"), At: 1}
	require.NoError(t, swapJSON(store, sessionstate.MetaCitations, func(all *map[string][]historyPoint) {
		*all = map[string][]historyPoint{"a.md": {pt}}
	}))
	events := []event.Event{
		citedPostEvent(filemod.KindPostUpdate, "a.md", "base", "cited and more"),
		citedPostEvent(filemod.KindPostUpdate, "d.md", "base", "never cited"),
	}
	hs := attachHistories(store, events, nil, nil)

	assert.Len(t, grounding.FromWire(events[0].Fields[grounding.FieldCitations]), 1)
	assert.Empty(t, grounding.FromWire(events[1].Fields[grounding.FieldCitations]))
	require.Contains(t, hs, "a.md")
	assert.NotContains(t, hs, "d.md", "no citation, nothing to narrow: the rule sees no citation at all")

	h := hs["a.md"]
	require.Len(t, h.Points, 1)
	assert.Equal(t, []transcript.SourceType{transcript.SourceUser}, h.Points[0].Pools, "each change keeps the pools it was cited in")
	for _, hash := range []string{h.Baseline.Hash, h.Current.Hash, h.Points[0].Before.Hash, h.Points[0].After.Hash} {
		_, ok := h.Content(hash)
		assert.True(t, ok, "content of %s", hash)
	}

	// The history names hashes, not contents: the stored record stays small.
	raw, _, err := store.Meta(sessionstate.MetaCitations)
	require.NoError(t, err)
	assert.NotContains(t, raw, `"cited"`)
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)...).CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
}

func historyOf(t *testing.T, store sessionstate.Store) map[string][]historyPoint {
	t.Helper()
	return historyIn(store, true)
}

// The first hook of a cycle records, as FOREIGN, each file the agent did not
// leave that way: at the session's first hook, each file already dirty; later,
// each file that differs from how the agent left it at its last Stop. A cycle
// already open records nothing — the agent's own changes are its own.
func TestBeginCycleRecordsOnlyWhatTheAgentDidNotDo(t *testing.T) {
	repo := t.TempDir()
	gitRun(t, repo, "init", "-q")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "a.md"), []byte("base\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "b.md"), []byte("base\n"), 0o644))
	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, "commit", "-qm", "base")
	head, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	require.NoError(t, err)
	store := openTestStore(t)
	require.NoError(t, store.SetMeta(sessionstate.MetaBaselineCommit, string(head[:len(head)-1])))

	// Dirty before the session began.
	require.NoError(t, os.WriteFile(filepath.Join(repo, "a.md"), []byte("dirty\n"), 0o644))
	require.NoError(t, beginCycle(store, repo, 10, nil, nil))
	h := historyOf(t, store)
	require.Len(t, h["a.md"], 1)
	assert.True(t, h["a.md"][0].Foreign)
	assert.Nil(t, h["a.md"][0].From, "at the session's first hook, where it came from is not the agent's either")
	assert.NotContains(t, h, "b.md", "a clean file is the baseline")

	// The agent's own change during the open cycle is not recorded.
	require.NoError(t, os.WriteFile(filepath.Join(repo, "b.md"), []byte("agent\n"), 0o644))
	require.NoError(t, beginCycle(store, repo, 11, nil, nil))
	assert.NotContains(t, historyOf(t, store), "b.md")

	// Stop: the agent leaves b.md as "agent". Then the user edits it.
	require.NoError(t, endCycle(store, []event.Event{
		citedPostEvent(filemod.KindPostUpdate, "a.md", "base\n", "dirty\n"),
		citedPostEvent(filemod.KindPostUpdate, "b.md", "base\n", "agent\n"),
	}, nil, backgroundReport{}))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "b.md"), []byte("user\n"), 0o644))
	require.NoError(t, beginCycle(store, repo, 20, nil, nil))
	h = historyOf(t, store)
	require.Len(t, h["b.md"], 1)
	assert.True(t, h["b.md"][0].Foreign)
	require.NotNil(t, h["b.md"][0].From)
	assert.Equal(t, hashOf("agent\n"), h["b.md"][0].From.Hash, "the user's edit starts where the agent left the file")
	assert.Len(t, h["a.md"], 1, "a file the agent left unchanged since gets no new point")
}

// When the tree leaves the history the baseline was on, the points before the
// current cycle go: they describe a line the tree no longer has.
func TestPruneHistoryKeepsOnlyTheCurrentCycle(t *testing.T) {
	store := openTestStore(t)
	from := st(store, "old branch")
	require.NoError(t, swapJSON(store, sessionstate.MetaCitations, func(all *map[string][]historyPoint) {
		*all = map[string][]historyPoint{
			"old.md": {{Cites: userCite, After: st(store, "x"), At: 5}},
			"a.md": {
				{Cites: userCite, After: st(store, "x"), At: 5},
				{Foreign: true, From: &from, After: st(store, "y"), At: 20},
				{Cites: userCite, Before: st(store, "y"), After: st(store, "z"), At: 21},
			},
		}
	}))
	raw, _ := json.Marshal(cycleMeta{State: "open", StartedAt: 20})
	require.NoError(t, store.SetMeta(sessionstate.MetaCitedCycle, string(raw)))

	require.NoError(t, pruneHistory(store, cycleStartedAt(store)))
	h := historyOf(t, store)
	assert.NotContains(t, h, "old.md")
	require.Len(t, h["a.md"], 2)
	assert.Nil(t, h["a.md"][0].From, "the move itself is not the agent's")

	// Between turns (SessionStart), everything so far goes, and how the agent
	// left each file is forgotten.
	raw, _ = json.Marshal(cycleMeta{State: "ended", StartedAt: 20, End: map[string]historyState{"a.md": st(store, "z")}})
	require.NoError(t, store.SetMeta(sessionstate.MetaCitedCycle, string(raw)))
	require.NoError(t, pruneHistory(store, 100))
	assert.Empty(t, historyOf(t, store))
	assert.Empty(t, readCycle(store).End)
}

// A dry-run failure is quoted beside its own file, in the order the line ran;
// the refusal of one target never carries what sr-file said about another.
func TestResolveNotesAreKeyedByFile(t *testing.T) {
	n := resolveNotes{failed: []resolveFailure{{"b.md", "B-ERROR"}, {"a.md", "A-ERROR"}}, line: "B-ERROR\nA-ERROR"}
	assert.Equal(t, "A-ERROR", n.For("a.md"))
	assert.NotContains(t, n.For("a.md"), "B-ERROR")
	assert.Equal(t, "B-ERROR", n.For("b.md"))

	// A target the dry run never reached is told which call stopped the line:
	// the last to fail, not the alphabetically first.
	stopped := resolveNotes{failed: []resolveFailure{{"b.md", "B-ERROR"}, {"z.md", "Z-ERROR"}}}
	assert.Contains(t, stopped.For("c.md"), "z.md")
	assert.Contains(t, stopped.For("c.md"), "Z-ERROR")
	assert.NotContains(t, stopped.For("c.md"), "B-ERROR")

	// A failure sr-file tied to no file is the line's.
	assert.Equal(t, "unparseable", resolveNotes{line: "unparseable"}.For("a.md"))
}

func commitRepo(t *testing.T, files map[string]string) (string, sessionstate.Store) {
	t.Helper()
	repo := t.TempDir()
	gitRun(t, repo, "init", "-q")
	for p, c := range files {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(repo, p)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(repo, p), []byte(c), 0o644))
	}
	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, "commit", "-qm", "base")
	head, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	require.NoError(t, err)
	store := openTestStore(t)
	require.NoError(t, store.SetMeta(sessionstate.MetaBaselineCommit, strings.TrimSpace(string(head))))
	return repo, store
}

// Once the agent detached work, what changes between its Stop and its next
// hook may be that work landing: it is not set aside as foreign but recorded
// as a between-turns change naming the work, and charged.
func TestBeginCycleChargesWhatChangedAfterBackgroundWork(t *testing.T) {
	repo, store := commitRepo(t, map[string]string{"a.md": "base\n"})
	require.NoError(t, beginCycle(store, repo, 10, nil, nil))
	require.NoError(t, markDetached(store, "nohup sh -c 'sleep 3'"))
	require.NoError(t, endCycle(store, []event.Event{citedPostEvent(filemod.KindPostUpdate, "a.md", "base\n", "cited\n")}, nil, backgroundReport{Known: true}))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "a.md"), []byte("EVIL UNCITED REWRITE"), 0o644))
	require.NoError(t, beginCycle(store, repo, 20, nil, nil))
	pts := historyOf(t, store)["a.md"]
	require.Len(t, pts, 1)
	assert.False(t, pts[0].Foreign, "a change landing after detached work was set aside as not the agent's")
	assert.True(t, pts[0].BetweenTurns)
	assert.Contains(t, pts[0].By, "nohup")
	assert.True(t, readCycle(store).Detached, "a detached job's mark lasts the session")
}

// Work another session sharing the tree started (a sub-agent's detached job)
// charges the between-turns change the same way.
func TestBeginCycleHonoursAnotherSessionsMark(t *testing.T) {
	repo, store := commitRepo(t, map[string]string{"a.md": "base\n"})
	require.NoError(t, beginCycle(store, repo, 10, nil, nil))
	require.NoError(t, endCycle(store, []event.Event{citedPostEvent(filemod.KindPostUpdate, "a.md", "base\n", "cited\n")}, nil, backgroundReport{Known: true}))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "a.md"), []byte("rewritten"), 0o644))
	require.NoError(t, beginCycle(store, repo, 20, nil, []string{"a sub-agent's nohup job"}))
	pts := historyOf(t, store)["a.md"]
	require.Len(t, pts, 1)
	assert.True(t, pts[0].BetweenTurns)
	assert.Contains(t, pts[0].By, "sub-agent")
}

// The harness's own report of what still runs sets and clears the task mark;
// a harness that reports nothing falls back to what the cycle ran in the
// background, and keeps it.
func TestTaskMarkFollowsTheStopReport(t *testing.T) {
	store := openTestStore(t)
	running := backgroundOf(HookPayload{BackgroundTasks: json.RawMessage(
		`[{"id":"b1","type":"shell","status":"running","description":"tests","command":"make watch"},` +
			`{"id":"a1","type":"subagent","status":"running","description":"research","agent_type":"general-purpose"},` +
			`{"id":"b2","type":"shell","status":"completed","command":"done already"}]`),
		SessionCrons: json.RawMessage(`[]`)})
	assert.True(t, running.Known)
	assert.Equal(t, []string{"make watch", "subagent research"}, running.Running)

	require.NoError(t, endCycle(store, nil, nil, running))
	c := readCycle(store)
	assert.True(t, c.Tasks)
	assert.Equal(t, []string{"make watch", "subagent research"}, c.TasksBy)

	require.NoError(t, endCycle(store, nil, nil, backgroundOf(HookPayload{BackgroundTasks: json.RawMessage(`[]`)})))
	assert.False(t, readCycle(store).Tasks, "a Stop reporting nothing running clears the mark")

	crons := backgroundOf(HookPayload{BackgroundTasks: json.RawMessage(`[]`), SessionCrons: json.RawMessage(`[{"id":"c1","cron":"*/5 * * * *"}]`)})
	assert.Len(t, crons.Running, 1, "a session cron keeps work running between turns")

	// No report at all: what this cycle ran in the background stands.
	require.NoError(t, markLaunched(store, "sleep 30 && touch x"))
	require.NoError(t, endCycle(store, nil, nil, backgroundOf(HookPayload{})))
	c = readCycle(store)
	assert.True(t, c.Tasks)
	assert.Contains(t, c.TasksBy, "sleep 30 && touch x")
	assert.Empty(t, c.Launched)
}

// The cycle record is updated by swaps: many hooks marking it at once lose no
// mark.
func TestCycleMarksSurviveConcurrentUpdates(t *testing.T) {
	db := filepath.Join(t.TempDir(), "state.db")
	s0, err := sessionstate.Open(db)
	require.NoError(t, err)
	defer s0.Close()
	require.NoError(t, updateCycle(s0, func(c *cycleMeta) { c.State = "ended" }))
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s, err := sessionstate.Open(db)
			if err != nil {
				t.Error(err)
				return
			}
			defer s.Close()
			if i%2 == 0 {
				assert.NoError(t, markDetached(s, fmt.Sprintf("job %d", i)))
			} else {
				assert.NoError(t, beginCycle(s, t.TempDir(), int64(i), nil, nil))
			}
		}(i)
	}
	wg.Wait()
	c := readCycle(s0)
	assert.True(t, c.Detached, "a concurrent cycle opening overwrote the mark")
	assert.Len(t, c.DetachedBy, 4)
	assert.Equal(t, "open", c.State)
}

// A cited change that lands clears the earlier "could not be computed" note
// for its file, and every Stop clears the set.
func TestCitedUnknownClears(t *testing.T) {
	root := t.TempDir()
	store := openTestStore(t)
	unknown := filemod.FileEvent{Path: "a.md"}.Event(filemod.KindPreCreate)
	unknown.Fields[grounding.FieldCitations] = grounding.ToWire(userCite)
	require.NoError(t, markCitedUnknown(store, []event.Event{unknown}))
	require.NotEmpty(t, citedUnknownNote(store, "a.md"))

	abs := filepath.Join(root, "a.md")
	require.NoError(t, os.WriteFile(abs, []byte("cited"), 0o644))
	require.NoError(t, recordPending(store, []pendingChange{{Path: "a.md", Abs: abs,
		Point: historyPoint{Cites: userCite, After: putState(store, true, "cited"), Whole: true, At: 1}}}))
	require.NoError(t, settleCitedChanges(store))
	assert.Empty(t, citedUnknownNote(store, "a.md"), "a computed cited write left the note standing")

	require.NoError(t, markCitedUnknown(store, []event.Event{unknown}))
	require.NoError(t, clearCitedUnknown(store))
	assert.Empty(t, citedUnknownNote(store, "a.md"), "the Stop did not clear the set")
}

// Only files a citation rule selects are snapshotted, and a content nothing
// names any more is deleted.
func TestSnapshotsOnlyWhatACitationRuleSelects(t *testing.T) {
	repo, store := commitRepo(t, map[string]string{"memories/a.md": "base\n", "other.txt": "base\n"})
	require.NoError(t, os.WriteFile(filepath.Join(repo, "memories/a.md"), []byte("dirty memory\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "other.txt"), []byte("dirty other\n"), 0o644))
	selects := citedPathsOf([]declaration.FileGuard{{Match: `memories/**`,
		Require: []declaration.Prerequisite{{Citation: &declaration.CitationPrerequisite{}}}}})
	require.NoError(t, beginCycle(store, repo, 10, selects, nil))
	h := historyOf(t, store)
	assert.Contains(t, h, "memories/a.md")
	assert.NotContains(t, h, "other.txt")
	_, ok, err := store.Meta(contentKey(hashOf("dirty other\n")))
	require.NoError(t, err)
	assert.False(t, ok, "the content of a file no citation rule selects was stored")

	// A content no point names any more goes at the next Stop.
	orphan := putState(store, true, "orphan")
	require.NoError(t, endCycle(store, nil, selects, backgroundReport{}))
	_, ok, err = store.Meta(contentKey(orphan.Hash))
	require.NoError(t, err)
	assert.False(t, ok, "an unreferenced content was kept")
	_, ok, err = store.Meta(contentKey(hashOf("dirty memory\n")))
	require.NoError(t, err)
	assert.True(t, ok, "a content a point names was deleted")
}
