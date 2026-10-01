package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/changeset"
	"github.com/sloprail/sloprail/internal/checkstore"
	"github.com/sloprail/sloprail/internal/declaration"
	dispatchcore "github.com/sloprail/sloprail/internal/dispatch"
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/grounding"
	"github.com/sloprail/sloprail/internal/natures"
	"github.com/sloprail/sloprail/internal/transcript"
)

// The Stop evaluation of one file-guard over a real repository, a real check
// script and a real check-results store. Nothing is faked but the store's two
// failure modes, which a real one cannot be made to show on demand.

// countingCheck appends a line to ledger every time it runs, and refuses — with a
// reason — when a file of the changeset holds the word FORBIDDEN.
func countingCheck(ledger string) string {
	return "#!/bin/sh\npayload=\"$(cat)\"\necho run >> '" + ledger + "'\n" +
		"if printf '%s' \"$payload\" | grep -q FORBIDDEN; then echo '{\"reason\":\"forbidden words\"}'; exit 1; fi\nexit 0\n"
}

type evalFixture struct {
	repo    string
	ledger  string
	guard   declaration.FileGuard
	results checkstore.Store
	ev      *changesetEvaluation
}

// newEvalFixture is a repository with docs/seed.md, then a committed rule "docs"
// (match `docs/**`, one script check), so the rule's floor is the seed commit.
func newEvalFixture(t *testing.T, mutate func(*declaration.FileGuard)) *evalFixture {
	t.Helper()
	repo := initRepo(t)
	commitFile(t, repo, "seed.txt", "seed")
	ledger := filepath.Join(t.TempDir(), "ledger")
	dir := filepath.Join(repo, ".sloprail", "file-guard", "docs")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "check.sh"), []byte(countingCheck(ledger)), 0o755))
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-m", "the rule")

	g := declaration.FileGuard{Name: "docs", Match: "docs/**", Dir: dir, Checks: []declaration.Check{{Script: "./check.sh"}}}
	if mutate != nil {
		mutate(&g)
	}
	results, err := checkstore.Open(filepath.Join(t.TempDir(), "checks.db"))
	require.NoError(t, err)
	t.Cleanup(func() { results.Close() })
	f := &evalFixture{repo: repo, ledger: ledger, guard: g, results: results}
	f.ev = f.newEvaluation(results)
	return f
}

func (f *evalFixture) newEvaluation(results checkstore.Store) *changesetEvaluation {
	ev := &changesetEvaluation{
		cmd: discard(), root: f.repo, p: HookPayload{Cwd: f.repo}, scope: hookScope{SessionID: "s-eval"},
		contextMap: map[string]natures.ContextState{}, context: map[string]any{},
		results: results, batch: "b1",
	}
	ev.identity = ev.runIdentity()
	return ev
}

func (f *evalFixture) hash(t *testing.T) string {
	t.Helper()
	h, err := changeset.RuleHash(f.guard.Root())
	require.NoError(t, err)
	return h
}

func (f *evalFixture) passedHeads(t *testing.T) []string {
	t.Helper()
	heads, err := f.results.PassedHeads(f.guard.Qualified())
	require.NoError(t, err)
	return heads
}

func (f *evalFixture) runs(t *testing.T) int {
	t.Helper()
	body, err := os.ReadFile(f.ledger)
	if os.IsNotExist(err) {
		return 0
	}
	require.NoError(t, err)
	return strings.Count(string(body), "run\n")
}

func (f *evalFixture) commitDoc(t *testing.T, name, body string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(f.repo, filepath.Dir(name)), 0o755))
	return commitFile(t, f.repo, name, body)
}

func TestEvaluate_APassingRangeIsRecordedCompleteAndMovesTheWatermark(t *testing.T) {
	f := newEvalFixture(t, nil)
	head := f.commitDoc(t, "docs/a.md", "clean")

	r, refused := f.ev.evaluate(f.guard)
	require.False(t, refused, r.Reason)
	assert.Equal(t, 1, f.runs(t))
	assert.Equal(t, []string{head}, f.passedHeads(t), "the watermark is the head it passed at")

	// Nothing new: the range is empty, selects nothing, and runs no check.
	_, refused = f.ev.evaluate(f.guard)
	assert.False(t, refused)
	assert.Equal(t, 1, f.runs(t))
}

func TestEvaluate_ARefusingCheckRefusesAndMovesNothing(t *testing.T) {
	f := newEvalFixture(t, nil)
	f.commitDoc(t, "docs/a.md", "FORBIDDEN")

	r, refused := f.ev.evaluate(f.guard)
	require.True(t, refused)
	assert.Contains(t, r.Reason, "forbidden words")
	assert.Equal(t, "docs", r.Name)
	assert.Empty(t, f.passedHeads(t), "a refused range is never partly passed")

	// And it is judged again over the whole range once fixed.
	head := f.commitDoc(t, "docs/a.md", "clean")
	_, refused = f.ev.evaluate(f.guard)
	require.False(t, refused)
	assert.Equal(t, []string{head}, f.passedHeads(t))
	assert.Equal(t, 2, f.runs(t))
}

func TestEvaluate_MatchSelectingNothingIsAPassThatAdvancesTheWatermark(t *testing.T) {
	f := newEvalFixture(t, nil)
	head := f.commitDoc(t, "notes/a.md", "FORBIDDEN but unguarded")

	_, refused := f.ev.evaluate(f.guard)
	assert.False(t, refused)
	assert.Equal(t, 0, f.runs(t), "no file selected, no check run")
	assert.Equal(t, []string{head}, f.passedHeads(t))
}

func TestEvaluate_AMatchThatDoesNotCompileFailsClosedAndIsRecordedAsAnEngineFailure(t *testing.T) {
	f := newEvalFixture(t, func(g *declaration.FileGuard) { g.Match = `path ==` })
	f.commitDoc(t, "docs/a.md", "clean")

	r, refused := f.ev.evaluate(f.guard)
	require.True(t, refused)
	assert.Contains(t, r.Reason, "could not be evaluated")
	assert.Contains(t, r.Reason, "must not be read as approval")
	assert.Empty(t, f.passedHeads(t), "an engine failure passes nothing")
	assert.Equal(t, 0, f.runs(t))
}

func TestEvaluate_ARuleFolderThatCannotBeHashedFailsClosed(t *testing.T) {
	f := newEvalFixture(t, nil)
	f.guard.Dir = filepath.Join(t.TempDir(), "gone", "file-guard", "docs") // its root does not exist

	r, refused := f.ev.evaluate(f.guard)
	require.True(t, refused)
	assert.Contains(t, r.Reason, "could not be evaluated")
}

func TestEvaluate_ARepositoryWithNoCommitHasNothingToJudge(t *testing.T) {
	f := newEvalFixture(t, nil)
	empty := initRepo(t)
	f.ev.root = empty

	r, refused := f.ev.evaluate(f.guard)
	assert.False(t, refused, r.Reason)
	assert.Equal(t, 0, f.runs(t))
}

// The run is recorded RUNNING and finished only once every check is stored. One
// that could not be finished is refused, and is no watermark.
func TestEvaluate_ARunThatCannotBeFinishedIsRefusedAndIsNoWatermark(t *testing.T) {
	f := newEvalFixture(t, nil)
	f.commitDoc(t, "docs/a.md", "clean")
	ev := f.newEvaluation(failingStore{Store: f.results, finish: errors.New("disk full")})

	r, refused := ev.evaluate(f.guard)
	require.True(t, refused)
	assert.Contains(t, r.Reason, "could not finish recording its run")
	assert.Contains(t, r.Reason, "disk full")
	assert.Empty(t, f.passedHeads(t), "a run left RUNNING is never a watermark")

	// The next Stop judges the same range again rather than trusting the lost run.
	_, refused = f.ev.evaluate(f.guard)
	require.False(t, refused)
	assert.Equal(t, 2, f.runs(t))
	assert.Len(t, f.passedHeads(t), 1)
}

func TestEvaluate_ACheckThatCannotBeRecordedIsAnEngineFailureNotAPass(t *testing.T) {
	f := newEvalFixture(t, nil)
	f.commitDoc(t, "docs/a.md", "clean")
	ev := f.newEvaluation(failingStore{Store: f.results, check: errors.New("db locked")})

	r, refused := ev.evaluate(f.guard)
	require.True(t, refused)
	assert.Contains(t, r.Reason, "could not be evaluated")
	assert.Contains(t, r.Reason, "db locked")
	assert.Empty(t, f.passedHeads(t), "a run holding fewer checks than it ran must not read as passed")
}

// A crash between recording the run and finishing it (a kill, a judge that never
// came back) leaves a RUNNING row. It is never a watermark, whatever head it names.
func TestEvaluate_ACrashThatLeftARunUnfinishedIsNotAWatermark(t *testing.T) {
	f := newEvalFixture(t, nil)
	head := f.commitDoc(t, "docs/a.md", "clean")
	_, err := f.results.RecordRun(checkstore.CheckRun{
		RunIdentity: f.ev.identity, BatchID: "crashed", CheckID: f.guard.Qualified(), BaseRef: "x", HeadRef: head,
		Metadata: map[string]any{"ruleHash": f.hash(t)},
	})
	require.NoError(t, err)
	assert.Empty(t, f.passedHeads(t))

	// So the Stop after the crash judges the range instead of finding it empty.
	_, refused := f.ev.evaluate(f.guard)
	require.False(t, refused)
	assert.Equal(t, 1, f.runs(t), "the check ran: the unfinished run did not stand in for a pass")
	assert.Equal(t, []string{head}, f.passedHeads(t))
}

func TestEvaluate_WithoutAStoreNothingIsRecordedAndEveryRunStartsAtTheFloor(t *testing.T) {
	f := newEvalFixture(t, nil)
	f.commitDoc(t, "docs/a.md", "clean")
	ev := f.newEvaluation(nil)

	_, refused := ev.evaluate(f.guard)
	require.False(t, refused)
	_, refused = ev.evaluate(f.guard)
	require.False(t, refused)
	assert.Equal(t, 2, f.runs(t), "no watermark without a store: the same range is judged again")
}

func TestEvaluateChangesets_ARuleLaunchedByItsOwnCheckIsNotEnforced(t *testing.T) {
	f := newEvalFixture(t, nil)
	f.commitDoc(t, "docs/a.md", "FORBIDDEN")
	t.Setenv(LaunchedByEnv, "docs")

	got := evaluateChangesets(discard(), []declaration.FileGuard{f.guard}, HookPayload{Cwd: f.repo}, hookScope{SessionID: "s-eval"}, f.repo,
		map[string]natures.ContextState{}, nil, f.results)
	assert.Empty(t, got, "a session a rule's own judge launched does not judge itself")
	assert.Equal(t, 0, f.runs(t))
}

func TestEvaluateChangesets_NoGuardsNoWork(t *testing.T) {
	assert.Nil(t, evaluateChangesets(discard(), nil, HookPayload{}, hookScope{}, t.TempDir(), nil, nil, nil))
}

func TestEvaluateChangesets_EachRuleIsEvaluatedAndOnlyTheRefusalsReturn(t *testing.T) {
	f := newEvalFixture(t, nil)
	f.commitDoc(t, "docs/a.md", "FORBIDDEN")
	passing := f.guard
	passing.Name, passing.Match = "other", "nothing-here/**"

	got := evaluateChangesets(discard(), []declaration.FileGuard{passing, f.guard}, HookPayload{Cwd: f.repo}, hookScope{SessionID: "s-eval"}, f.repo,
		map[string]natures.ContextState{}, nil, f.results)
	require.Len(t, got, 1)
	assert.Equal(t, "docs", got[0].Name)
}

// Citations are attributed per file: a file is grounded by a citation quoted in
// the commit that last changed it, and every file that is not is named.
func TestRunRequirement_CitationPerFile(t *testing.T) {
	user := []transcript.SourceType{transcript.SourceUser}
	cite := func(commit string) changeset.Citation {
		return changeset.Citation{Citation: transcript.Citation{Quote: "q", SourceTypes: user, Path: "/t.jsonl", Line: 3}, Commits: []string{commit}}
	}
	prereq := declaration.Prerequisite{Citation: &declaration.CitationPrerequisite{SourceTypes: []string{"user"}}}
	f := newEvalFixture(t, nil)
	req := dispatchcore.Request{Nature: dispatchcore.NatureFileGuard, Dir: f.guard.Dir, Changeset: &changeset.Payload{}, Require: []declaration.Prerequisite{prereq}}

	t.Run("every file grounded", func(t *testing.T) {
		cs := changeset.Changeset{
			Files:     []changeset.File{{Path: "a.md", Commits: []string{"c1"}}, {Path: "b.md", Commits: []string{"c2"}}},
			Citations: []changeset.Citation{cite("c1"), cite("c2")},
		}
		v, err := f.ev.runRequirement(f.guard, req, prereq, "require:citation", changeset.NewPayload(cs, changeset.Whole(cs), "", nil), "", nil)
		require.NoError(t, err)
		assert.False(t, v.Refused, v.Reason)
	})

	t.Run("one file uncited is refused by name, and only it", func(t *testing.T) {
		cs := changeset.Changeset{
			Files:     []changeset.File{{Path: "a.md", Commits: []string{"c1"}}, {Path: "b.md", Commits: []string{"c2"}}, {Path: "c.md", Commits: []string{"c3"}}},
			Citations: []changeset.Citation{cite("c1")},
		}
		v, err := f.ev.runRequirement(f.guard, req, prereq, "require:citation", changeset.NewPayload(cs, changeset.Whole(cs), "", nil), "", nil)
		require.NoError(t, err)
		require.True(t, v.Refused)
		assert.Contains(t, v.Reason, "b.md, c.md")
		assert.NotContains(t, v.Reason, "a.md")
		assert.Contains(t, v.Reason, "Sloprail-Cites-User")
		assert.Contains(t, v.Reason, "an empty commit carrying only the trailer does not count")
	})

	followUp := func(file string) string {
		return "git add '" + file + "' && git commit -m '<what changed>' -m 'Sloprail-Cites-User: <exact quote>'"
	}
	headCs := func() changeset.Changeset {
		return changeset.Changeset{Base: "b0", Head: "c2",
			Commits:   []changeset.Commit{{SHA: "c1", Subject: "first"}, {SHA: "c2", Subject: "second"}},
			Files:     []changeset.File{{Path: "a.md", Commits: []string{"c1"}}, {Path: "b.md", Commits: []string{"c2"}}},
			Citations: []changeset.Citation{cite("c1")},
		}
	}
	refuse := func(t *testing.T, cs changeset.Changeset, file string) string {
		t.Helper()
		v, err := f.ev.runRequirement(f.guard, req, prereq, "require:citation", changeset.NewPayload(cs, changeset.Whole(cs), "", nil), "", nil)
		require.NoError(t, err)
		require.True(t, v.Refused)
		assert.NotContains(t, v.Reason, "reset --soft", "the squash is never suggested")
		assert.Contains(t, v.Reason, "an empty commit carrying only the trailer does not count")
		assert.Contains(t, v.Reason, followUp(file), "the recommended fix is a follow-up commit")
		assert.Contains(t, v.Reason, "sr-file write", "no change needed: restate the content through a cited write")
		return v.Reason
	}
	amend := "git commit --amend --no-edit --trailer 'Sloprail-Cites-User: <exact quote>'"

	t.Run("HEAD last changed the file, is unpushed and the tree is clean: a follow-up commit is recommended and the amend offered", func(t *testing.T) {
		reason := refuse(t, headCs(), "b.md")
		assert.Contains(t, reason, amend)
		assert.Less(t, strings.Index(reason, followUp("b.md")), strings.Index(reason, amend), "the follow-up commit comes first")
	})

	t.Run("an uncited file last changed earlier gets the follow-up commit and no amend", func(t *testing.T) {
		cs := headCs()
		cs.Citations = []changeset.Citation{cite("c2")}
		cs.Files = []changeset.File{{Path: "a.md", Commits: []string{"c1"}}, {Path: "b.md", Commits: []string{"c2"}}}
		reason := refuse(t, cs, "a.md")
		assert.NotContains(t, reason, "--amend")
	})

	t.Run("HEAD is pushed: no amend is suggested", func(t *testing.T) {
		runGit(t, f.repo, "update-ref", "refs/remotes/origin/main", "HEAD")
		t.Cleanup(func() { runGit(t, f.repo, "update-ref", "-d", "refs/remotes/origin/main") })
		assert.NotContains(t, refuse(t, headCs(), "b.md"), "--amend")
	})

	t.Run("the tree is dirty: no amend is suggested", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(f.repo, "dirty.txt"), []byte("x"), 0o644))
		t.Cleanup(func() { _ = os.Remove(filepath.Join(f.repo, "dirty.txt")) })
		assert.NotContains(t, refuse(t, headCs(), "b.md"), "--amend")
	})

	t.Run("an uncited change on top of a cited one is uncited", func(t *testing.T) {
		cs := changeset.Changeset{
			Files:     []changeset.File{{Path: "a.md", Commits: []string{"c1", "c2"}}},
			Citations: []changeset.Citation{cite("c1")},
		}
		v, err := f.ev.runRequirement(f.guard, req, prereq, "require:citation", changeset.NewPayload(cs, changeset.Whole(cs), "", nil), "", nil)
		require.NoError(t, err)
		assert.True(t, v.Refused)
	})

	t.Run("a citation in another pool grounds nothing", func(t *testing.T) {
		tool := changeset.Citation{Citation: transcript.Citation{Quote: "q", SourceTypes: []transcript.SourceType{transcript.SourceToolResult}, Path: "/t.jsonl", Line: 3}, Commits: []string{"c1"}}
		cs := changeset.Changeset{Files: []changeset.File{{Path: "a.md", Commits: []string{"c1"}}}, Citations: []changeset.Citation{tool}}
		v, err := f.ev.runRequirement(f.guard, req, prereq, "require:citation", changeset.NewPayload(cs, changeset.Whole(cs), "", nil), "", nil)
		require.NoError(t, err)
		assert.True(t, v.Refused)
	})
}

func TestRequireAndCheckKinds(t *testing.T) {
	assert.Equal(t, "require:citation", requireKind(declaration.Prerequisite{Citation: &declaration.CitationPrerequisite{}}))
	assert.Equal(t, "require:skill:x", requireKind(declaration.Prerequisite{Skill: "x"}))
	assert.Equal(t, "require:context:goal", requireKind(declaration.Prerequisite{Context: "goal"}))
	assert.Equal(t, "check[0]:script:./a.sh", checkKind(0, declaration.Check{Script: "./a.sh"}))
	assert.Equal(t, "check[2]:judge:rubric.md.j2", checkKind(2, declaration.Check{Judge: "rubric.md.j2"}))
}

func TestCitationItemsAndUnresolvedNote(t *testing.T) {
	assert.Empty(t, unresolvedNote(nil))
	missed := []changeset.Unresolved{{Commit: "abcdef0123456789", Trailer: changeset.TrailerCitesUser, Quote: "nobody said it", Err: errors.New("does not resolve")}}
	note := unresolvedNote(missed)
	assert.Contains(t, note, "nobody said it")
	assert.Contains(t, note, "abcdef012345")

	items := citationItems(eventWithCitations(transcript.Citation{Quote: "yes", Path: "/t", Line: 4, SourceTypes: []transcript.SourceType{transcript.SourceUser}}), missed)
	require.Len(t, items, 2)
	assert.True(t, items[0].Passed)
	assert.Equal(t, "yes", items[0].Key)
	assert.False(t, items[1].Passed)
	assert.Equal(t, "nobody said it", items[1].Key)
}

func TestOpenChecksStore_NoSessionNoStore(t *testing.T) {
	assert.Nil(t, openChecksStore(discard(), HookPayload{Cwd: t.TempDir()}, hookScope{}))
}

// failingStore is a real store whose finishing or recording of a check can be made to fail.
type failingStore struct {
	checkstore.Store
	finish error
	check  error
}

func (s failingStore) FinishRun(id string) error {
	if s.finish != nil {
		return s.finish
	}
	return s.Store.FinishRun(id)
}

func (s failingStore) RecordCheck(runID string, c checkstore.CheckRecord) (string, error) {
	if s.check != nil {
		return "", s.check
	}
	return s.Store.RecordCheck(runID, c)
}

func eventWithCitations(cs ...transcript.Citation) event.Event {
	return event.Event{Kind: changeset.Kind, Fields: map[string]any{grounding.FieldCitations: grounding.ToWire(cs)}}
}
