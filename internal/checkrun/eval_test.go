package checkrun

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/changeset"
	"github.com/sloprail/sloprail/internal/checkcache"
	"github.com/sloprail/sloprail/internal/checkstore"
	"github.com/sloprail/sloprail/internal/declaration"
	dispatchcore "github.com/sloprail/sloprail/internal/dispatch"
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/grounding"
	"github.com/sloprail/sloprail/internal/transcript"
)

// The evaluation of one file-guard over a real repository, a real check script and a real
// check-results store. Nothing is faked but the store's two failure modes, which a real one
// cannot be made to show on demand.

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "--initial-branch=main")
	runGit(t, dir, "config", "user.email", "test@example.invalid")
	runGit(t, dir, "config", "user.name", "Test")
	return dir
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "git %s: %s", strings.Join(args, " "), out)
	return strings.TrimSpace(string(out))
}

func commitFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "add "+name)
	return runGit(t, dir, "rev-parse", "HEAD")
}

// countingCheck appends a line to ledger every time it runs, and refuses — with a
// reason — when a file of the changeset holds the word FORBIDDEN.
func countingCheck(ledger string) string {
	return "#!/bin/sh\npayload=\"$(cat)\"\necho run >> '" + ledger + "'\n" +
		"if printf '%s' \"$payload\" | grep -q FORBIDDEN; then echo '{\"reason\":\"forbidden words\"}'; exit 1; fi\nexit 0\n"
}

type evalFixture struct {
	repo    string
	base    string
	ledger  string
	guard   declaration.FileGuard
	results checkstore.Store
}

// newEvalFixture is a repository with a seed commit, then a committed rule "docs"
// (match `docs/**`, one script check). The range judged is the seed commit..HEAD.
func newEvalFixture(t *testing.T, mutate func(*declaration.FileGuard)) *evalFixture {
	t.Helper()
	repo := initRepo(t)
	base := commitFile(t, repo, "seed.txt", "seed")
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
	results := checkstore.Open(checkcache.NewMemory(), false)
	t.Cleanup(func() { results.Close() })
	return &evalFixture{repo: repo, base: base, ledger: ledger, guard: g, results: results}
}

func (f *evalFixture) params(t *testing.T, results checkstore.Store) Params {
	t.Helper()
	rng, err := gitrepo.ResolveRange(f.repo, f.base, "HEAD")
	require.NoError(t, err)
	return Params{
		Guards: []declaration.FileGuard{f.guard}, Root: f.repo, Range: rng, Cwd: f.repo, SessionID: "s-eval",
		Store: results,
	}
}

// newEvaluation is an evaluation of the range as it stands now.
func (f *evalFixture) newEvaluation(t *testing.T, results checkstore.Store) *changesetEvaluation {
	t.Helper()
	p := f.params(t, results)
	ev := &changesetEvaluation{
		errw: &bytes.Buffer{}, diags: map[string]*bytes.Buffer{}, root: f.repo, params: p,
		store: results, rng: p.Range, batch: "b1",
	}
	ev.identity = ev.runIdentity()
	return ev
}

// evaluate judges the rule over the range as it stands now.
func (f *evalFixture) evaluate(t *testing.T, results checkstore.Store) (FileGuardResult, bool) {
	t.Helper()
	return f.newEvaluation(t, results).evaluate(f.guard)
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

func TestEvaluate_APassingRangeRunsItsCheck(t *testing.T) {
	f := newEvalFixture(t, nil)
	f.commitDoc(t, "docs/a.md", "clean")

	r, refused := f.evaluate(t, f.results)
	require.False(t, refused, r.Reason)
	assert.Equal(t, 1, f.runs(t))
}

func TestEvaluate_ARefusingCheckRefusesWithItsReason(t *testing.T) {
	f := newEvalFixture(t, nil)
	f.commitDoc(t, "docs/a.md", "FORBIDDEN")

	r, refused := f.evaluate(t, f.results)
	require.True(t, refused)
	assert.Contains(t, r.Reason, "forbidden words")
	assert.Equal(t, "docs", r.Name)
}

func TestEvaluate_MatchSelectingNothingIsAPass(t *testing.T) {
	f := newEvalFixture(t, nil)
	f.commitDoc(t, "notes/a.md", "FORBIDDEN but unguarded")

	_, refused := f.evaluate(t, f.results)
	assert.False(t, refused)
	assert.Equal(t, 0, f.runs(t), "no file selected, no check run")
}

func TestEvaluate_AMatchThatDoesNotCompileFailsClosedAndIsRecordedAsAnEngineFailure(t *testing.T) {
	f := newEvalFixture(t, func(g *declaration.FileGuard) { g.Match = `path ==` })
	f.commitDoc(t, "docs/a.md", "clean")

	r, refused := f.evaluate(t, f.results)
	require.True(t, refused)
	assert.Contains(t, r.Reason, "could not be evaluated")
	assert.Contains(t, r.Reason, "must not be read as approval")
	assert.Equal(t, 0, f.runs(t))
}

func TestEvaluate_ARuleFolderThatCannotBeHashedFailsClosed(t *testing.T) {
	f := newEvalFixture(t, nil)
	f.commitDoc(t, "docs/a.md", "clean")
	f.guard.Dir = filepath.Join(t.TempDir(), "gone", "file-guard", "docs") // its root does not exist

	r, refused := f.evaluate(t, f.results)
	require.True(t, refused)
	assert.Contains(t, r.Reason, "could not be evaluated")
}

// A range that holds no commit (base == head) is a computed, empty range: nothing to judge. A
// repository with no commit at all has no range, which gitrepo.ResolveRange refuses.
func TestEvaluate_ARangeWithNoCommitHasNothingToJudge(t *testing.T) {
	f := newEvalFixture(t, nil)
	p := f.params(t, f.results)
	p.Range = gitrepo.Range{Base: p.Range.Head, Head: p.Range.Head}

	got, _ := Evaluate(p)
	assert.Empty(t, got)
	assert.Equal(t, 0, f.runs(t))

	_, err := gitrepo.ResolveRange(initRepo(t), "HEAD", "HEAD")
	assert.Error(t, err)
}

// The run is recorded RUNNING and finished only once every check is stored. One
// that could not be finished is refused.
func TestEvaluate_ARunThatCannotBeFinishedIsRefused(t *testing.T) {
	f := newEvalFixture(t, nil)
	f.commitDoc(t, "docs/a.md", "clean")

	r, refused := f.evaluate(t, failingStore{Store: f.results, finish: errors.New("disk full")})
	require.True(t, refused)
	assert.Contains(t, r.Reason, "could not finish recording its run")
	assert.Contains(t, r.Reason, "disk full")

	// The next evaluation judges the same range again rather than trusting the lost run.
	_, refused = f.evaluate(t, f.results)
	require.False(t, refused)
	assert.Equal(t, 2, f.runs(t))
}

func TestEvaluate_ACheckThatCannotBeRecordedIsAnEngineFailureNotAPass(t *testing.T) {
	f := newEvalFixture(t, nil)
	f.commitDoc(t, "docs/a.md", "clean")

	r, refused := f.evaluate(t, failingStore{Store: f.results, check: errors.New("db locked")})
	require.True(t, refused)
	assert.Contains(t, r.Reason, "could not be evaluated")
	assert.Contains(t, r.Reason, "db locked")
}

func TestEvaluate_WithoutAStoreNothingIsRecorded(t *testing.T) {
	f := newEvalFixture(t, nil)
	f.commitDoc(t, "docs/a.md", "clean")

	_, refused := f.evaluate(t, nil)
	require.False(t, refused)
	_, refused = f.evaluate(t, nil)
	require.False(t, refused)
	assert.Equal(t, 2, f.runs(t), "a script is judged again every time")
}

func TestEvaluate_ARuleLaunchedByItsOwnCheckIsNotEnforced(t *testing.T) {
	f := newEvalFixture(t, nil)
	f.commitDoc(t, "docs/a.md", "FORBIDDEN")
	t.Setenv(LaunchedByEnv, "docs")

	got, _ := Evaluate(f.params(t, f.results))
	assert.Empty(t, got, "a session a rule's own judge launched does not judge itself")
	assert.Equal(t, 0, f.runs(t))
}

func TestEvaluate_NoGuardsNoWork(t *testing.T) {
	got, outcomes := Evaluate(Params{Root: t.TempDir()})
	assert.Nil(t, got)
	assert.Nil(t, outcomes)
}

func TestEvaluate_EachRuleIsEvaluatedAndOnlyTheRefusalsReturn(t *testing.T) {
	f := newEvalFixture(t, nil)
	f.commitDoc(t, "docs/a.md", "FORBIDDEN")
	passing := f.guard
	passing.Name, passing.Match = "other", "nothing-here/**"
	p := f.params(t, f.results)
	p.Guards = []declaration.FileGuard{passing, f.guard}

	got, _ := Evaluate(p)
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
	head := runGit(t, f.repo, "rev-parse", "HEAD")
	ev := f.newEvaluation(t, f.results)
	req := dispatchcore.Request{Nature: dispatchcore.NatureFileGuard, Dir: f.guard.Dir, Changeset: &changeset.Payload{}, Require: []declaration.Prerequisite{prereq}}
	require := func(cs changeset.Changeset) (dispatchcore.Verdict, error) {
		return ev.runRequirement(f.guard, req, prereq, "require:citation", changeset.NewPayload(cs, changeset.Whole(cs), ""), "", nil)
	}

	t.Run("every file grounded", func(t *testing.T) {
		cs := changeset.Changeset{
			Files:     []changeset.File{{Path: "a.md", Commits: []string{"c1"}}, {Path: "b.md", Commits: []string{"c2"}}},
			Citations: []changeset.Citation{cite("c1"), cite("c2")},
		}
		v, err := require(cs)
		assert.NoError(t, err)
		assert.False(t, v.Refused, v.Reason)
	})

	t.Run("one file uncited is refused by name, and only it", func(t *testing.T) {
		cs := changeset.Changeset{
			Files:     []changeset.File{{Path: "a.md", Commits: []string{"c1"}}, {Path: "b.md", Commits: []string{"c2"}}, {Path: "c.md", Commits: []string{"c3"}}},
			Citations: []changeset.Citation{cite("c1")},
		}
		v, err := require(cs)
		assert.NoError(t, err)
		assert.True(t, v.Refused)
		assert.Contains(t, v.Reason, "b.md, c.md")
		assert.NotContains(t, v.Reason, "a.md")
		assert.Contains(t, v.Reason, "Sloprail-Cites-User")
		assert.Contains(t, v.Reason, "an empty commit carrying only the trailer does not count")
	})

	followUp := func(file string) string {
		return "git add '" + file + "' && git commit -m '<what changed>' -m 'Sloprail-Cites-User: <exact quote>'"
	}
	headCs := func() changeset.Changeset {
		return changeset.Changeset{Base: "b0", Head: head,
			Commits:   []changeset.Commit{{SHA: "c1", Subject: "first"}, {SHA: head, Subject: "second"}},
			Files:     []changeset.File{{Path: "a.md", Commits: []string{"c1"}}, {Path: "b.md", Commits: []string{head}}},
			Citations: []changeset.Citation{cite("c1")},
		}
	}
	refuse := func(t *testing.T, cs changeset.Changeset, file string) string {
		t.Helper()
		v, err := require(cs)
		assert.NoError(t, err)
		if !assert.True(t, v.Refused) {
			t.FailNow()
		}
		assert.NotContains(t, v.Reason, "reset --soft", "the squash is never suggested")
		assert.NotContains(t, v.Reason, "reset --hard ", "a hard reset is never suggested")
		assert.NotContains(t, v.Reason, "4b825dc", "a non-commit sha (the empty tree) is never suggested")
		assert.Contains(t, v.Reason, "git revert", "undoing is a revert")
		assert.Contains(t, v.Reason, "an empty commit carrying only the trailer does not count")
		assert.Contains(t, v.Reason, followUp(file), "the recommended fix is a follow-up commit")
		assert.NotContains(t, v.Reason, "sr-file write", "no restating the content through a cited write")
		assert.Contains(t, v.Reason, "Never wash a change", "a citation is never carried by a whitespace-only or restated commit")
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
		cs.Citations = []changeset.Citation{cite(head)}
		assert.NotContains(t, refuse(t, cs, "a.md"), "--amend")
	})

	t.Run("HEAD is pushed: no amend is suggested", func(t *testing.T) {
		runGit(t, f.repo, "update-ref", "refs/remotes/origin/main", "HEAD")
		t.Cleanup(func() { runGit(t, f.repo, "update-ref", "-d", "refs/remotes/origin/main") })
		assert.NotContains(t, refuse(t, headCs(), "b.md"), "--amend")
	})

	t.Run("the tree is dirty: no amend is suggested", func(t *testing.T) {
		assert.NoError(t, os.WriteFile(filepath.Join(f.repo, "dirty.txt"), []byte("x"), 0o644))
		t.Cleanup(func() { _ = os.Remove(filepath.Join(f.repo, "dirty.txt")) })
		assert.NotContains(t, refuse(t, headCs(), "b.md"), "--amend")
	})

	t.Run("an uncited change on top of a cited one is uncited", func(t *testing.T) {
		cs := changeset.Changeset{
			Files:     []changeset.File{{Path: "a.md", Commits: []string{"c1", "c2"}}},
			Citations: []changeset.Citation{cite("c1")},
		}
		v, err := require(cs)
		assert.NoError(t, err)
		assert.True(t, v.Refused)
	})

	t.Run("a citation in another pool grounds nothing", func(t *testing.T) {
		tool := changeset.Citation{Citation: transcript.Citation{Quote: "q", SourceTypes: []transcript.SourceType{transcript.SourceToolResult}, Path: "/t.jsonl", Line: 3}, Commits: []string{"c1"}}
		cs := changeset.Changeset{Files: []changeset.File{{Path: "a.md", Commits: []string{"c1"}}}, Citations: []changeset.Citation{tool}}
		v, err := require(cs)
		assert.NoError(t, err)
		assert.True(t, v.Refused)
	})

	// A refusal hands back the quotes the session already recorded (sr-file --cite), as the
	// exact trailer lines to paste, in place of the placeholder.
	t.Run("a quote the session already recorded is handed back as the trailer to paste", func(t *testing.T) {
		ev.params.Recorded = map[string][]transcript.Citation{
			"b.md": {
				{Quote: "  the   user\nsaid it ", SourceTypes: user},
				{Quote: "the user said it", SourceTypes: user}, // a repeat once whitespace is normalised
				{Quote: "wrong pool", SourceTypes: []transcript.SourceType{transcript.SourceToolResult}},
			},
			"other.md": {{Quote: "not asked about", SourceTypes: user}},
		}
		t.Cleanup(func() { ev.params.Recorded = nil })
		v, err := require(headCs())
		assert.NoError(t, err)
		assert.True(t, v.Refused)
		assert.Contains(t, v.Reason, "Quotes already recorded for these files this session")
		assert.Contains(t, v.Reason, "b.md: Sloprail-Cites-User: the user said it")
		assert.Equal(t, 1, strings.Count(v.Reason, "b.md: Sloprail-Cites-User: the user said it"), "listed once")
		assert.NotContains(t, v.Reason, "wrong pool")
		assert.NotContains(t, v.Reason, "not asked about")
		assert.Contains(t, v.Reason, "-m 'Sloprail-Cites-User: the user said it'", "the recommended commit carries it")
		assert.Contains(t, v.Reason, "--trailer 'Sloprail-Cites-User: the user said it'", "so does the amend")
	})
}

func TestRecordedQuotes_AreFromThePoolsTheRequirementAccepts(t *testing.T) {
	both := []transcript.SourceType{transcript.SourceUser, transcript.SourceToolResult}
	ev := &changesetEvaluation{params: Params{Recorded: map[string][]transcript.Citation{
		"a.md": {
			{Quote: "from the user", SourceTypes: []transcript.SourceType{transcript.SourceUser}},
			{Quote: "from a tool", SourceTypes: []transcript.SourceType{transcript.SourceToolResult}},
			{Quote: "in both", SourceTypes: both},
			{Quote: "", SourceTypes: both},
			{Quote: "in none", SourceTypes: nil},
		},
	}}}

	got := ev.recordedQuotes([]string{"a.md", "missing.md"}, []transcript.SourceType{transcript.SourceUser})
	assert.Equal(t, []recordedQuote{
		{Path: "a.md", Quote: "from the user", Trailer: changeset.TrailerCitesUser},
		{Path: "a.md", Quote: "in both", Trailer: changeset.TrailerCitesUser},
	}, got)

	got = ev.recordedQuotes([]string{"a.md"}, []transcript.SourceType{transcript.SourceToolResult})
	assert.Equal(t, []recordedQuote{
		{Path: "a.md", Quote: "from a tool", Trailer: changeset.TrailerCitesTool},
		{Path: "a.md", Quote: "in both", Trailer: changeset.TrailerCitesTool},
	}, got)

	tr, ok := trailerOf(transcript.Citation{SourceTypes: both}, both)
	assert.True(t, ok)
	assert.Equal(t, changeset.TrailerCitesUser, tr, "the user's wins when both resolve")
	_, ok = trailerOf(transcript.Citation{SourceTypes: both}, nil)
	assert.False(t, ok)
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

// Every refusal of a file-guard names the file(s) it is about, whatever wording the
// check gave its reason: a check that names none gets the files it judged listed, one
// that names a file of the changeset is left as it is.
func TestEvaluate_ARefusalAlwaysNamesTheFilesItIsAbout(t *testing.T) {
	f := newEvalFixture(t, nil)
	f.commitDoc(t, "docs/a.md", "FORBIDDEN")
	f.commitDoc(t, "docs/b.md", "fine")

	r, refused := f.evaluate(t, f.results)
	require.True(t, refused)
	assert.Contains(t, r.Reason, "forbidden words", "the check's own reason is kept")
	assert.Contains(t, r.Reason, "docs/a.md")
	assert.Contains(t, r.Reason, "docs/b.md")
}

func TestNamingFiles(t *testing.T) {
	files := []changeset.File{{Path: "docs/a.md"}, {Path: "docs/b.md"}}
	assert.Equal(t, "docs/b.md: wrong", namingFiles("docs/b.md: wrong", files), "a reason that names a file is not repeated")
	assert.Equal(t, "wrong", namingFiles("wrong", nil))

	var many []changeset.File
	for i := 0; i < maxNamedFiles+3; i++ {
		many = append(many, changeset.File{Path: fmt.Sprintf("f%02d.md", i)})
	}
	got := namingFiles("wrong", many)
	assert.Contains(t, got, "f00.md")
	assert.NotContains(t, got, fmt.Sprintf("f%02d.md", maxNamedFiles))
	assert.Contains(t, got, "(and 3 more)")
}

// An engine failure (the range could not be read) still names the files it is about.
func TestEvaluate_AnEngineFailureNamesTheFiles(t *testing.T) {
	f := newEvalFixture(t, nil)
	f.commitDoc(t, "docs/a.md", "clean")

	r, refused := f.evaluate(t, failingStore{Store: f.results, check: errors.New("db locked")})
	require.True(t, refused)
	assert.Contains(t, r.Reason, "could not be evaluated")
	assert.Contains(t, r.Reason, "docs/a.md")
}

// Every check kind is cached by the guard's content: the same input is a hit (pass or fail,
// a fail replayed), and `verify` only ever reads what `run` stored.
func TestEvaluate_AScriptIsCachedByContentAndAFailIsReplayed(t *testing.T) {
	f := newEvalFixture(t, nil)
	f.commitDoc(t, "docs/a.md", "clean")

	_, refused := f.evaluate(t, f.results)
	require.False(t, refused)
	require.Equal(t, 1, f.runs(t), "a miss runs the script")
	_, refused = f.evaluate(t, f.results)
	require.False(t, refused)
	assert.Equal(t, 1, f.runs(t), "a hit does not run it again")

	f.commitDoc(t, "docs/a.md", "FORBIDDEN")
	r, refused := f.evaluate(t, f.results)
	require.True(t, refused)
	assert.Contains(t, r.Reason, "forbidden words")
	require.Equal(t, 2, f.runs(t), "changed content is a miss")
	r, refused = f.evaluate(t, f.results)
	require.True(t, refused, "the stored fail is replayed")
	assert.Contains(t, r.Reason, "forbidden words")
	assert.Equal(t, 2, f.runs(t), "a replayed fail does not run the script")
}

func TestEvaluate_VerifyNeverExecutesAnythingAndOnlyReadsStoredVerdicts(t *testing.T) {
	f := newEvalFixture(t, nil)
	f.commitDoc(t, "docs/a.md", "clean")
	verify := func() ([]FileGuardResult, []CheckOutcome) {
		p := f.params(t, f.results)
		p.Verify = true
		return Evaluate(p)
	}

	got, outcomes := verify()
	require.Len(t, got, 1)
	assert.Contains(t, got[0].Reason, "not judged yet")
	assert.Contains(t, got[0].Reason, "sr-checks run --base")
	assert.Equal(t, "missing", outcomes[0].Status)
	assert.Equal(t, 0, f.runs(t), "verify did not run the script")

	_, refused := f.evaluate(t, f.results)
	require.False(t, refused)
	require.Equal(t, 1, f.runs(t))
	got, _ = verify()
	assert.Empty(t, got, "the stored pass is read")
	assert.Equal(t, 1, f.runs(t), "verify did not run the script")

	f.commitDoc(t, "docs/a.md", "FORBIDDEN")
	got, _ = verify()
	require.Len(t, got, 1)
	assert.Contains(t, got[0].Reason, "not judged yet", "new content has no stored verdict")
	_, _ = f.evaluate(t, f.results)
	got, _ = verify()
	require.Len(t, got, 1)
	assert.Contains(t, got[0].Reason, "forbidden words", "the stored fail's reasons")
	assert.Equal(t, 2, f.runs(t), "verify did not run the script")
}

// An engine error is no verdict: the next run starts the guard again.
func TestEvaluate_AnEngineErrorStoresNoVerdict(t *testing.T) {
	f := newEvalFixture(t, nil)
	f.commitDoc(t, "docs/a.md", "clean")
	_, refused := f.evaluate(t, failingStore{Store: f.results, check: errors.New("db locked")})
	require.True(t, refused)
	p := f.params(t, f.results)
	p.Verify = true
	got, _ := Evaluate(p)
	require.Len(t, got, 1)
	assert.Contains(t, got[0].Reason, "not judged yet")
}

// `subjects:` splits the selected files into units, each cached on its own; its fingerprint is
// part of the key, so a change in what a subject depends on re-runs only that subject.
func TestEvaluate_SubjectsScriptKeysEachSubjectOnItsOwn(t *testing.T) {
	f := newEvalFixture(t, func(g *declaration.FileGuard) { g.Subjects = "./subjects.sh" })
	dep := filepath.Join(t.TempDir(), "dep")
	require.NoError(t, os.WriteFile(dep, []byte("1"), 0o644))
	script := "#!/bin/sh\ncat >/dev/null\n" +
		"printf '[{\"id\":\"a\",\"files\":[\"docs/a.md\"],\"fingerprint\":\"%s\"},{\"id\":\"b\",\"files\":[\"docs/b.md\"]}]' \"$(cat '" + dep + "')\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(f.guard.Dir, "subjects.sh"), []byte(script), 0o755))
	f.commitDoc(t, "docs/a.md", "clean")
	f.commitDoc(t, "docs/b.md", "clean")

	_, refused := f.evaluate(t, f.results)
	require.False(t, refused)
	require.Equal(t, 2, f.runs(t), "one run per subject")
	_, refused = f.evaluate(t, f.results)
	require.False(t, refused)
	assert.Equal(t, 2, f.runs(t), "both subjects are hits")

	require.NoError(t, os.WriteFile(dep, []byte("2"), 0o644))
	_, refused = f.evaluate(t, f.results)
	require.False(t, refused)
	assert.Equal(t, 3, f.runs(t), "only the subject whose fingerprint moved is run again")

	p := f.params(t, f.results)
	p.Verify = true
	got, _ := Evaluate(p)
	assert.Empty(t, got, "verify computes the same keys, without a session")
	assert.Equal(t, 3, f.runs(t))
}

// A check whose prepare refuses (no transcript to read, fail-closed) has a verdict, a FAIL: it
// is stored, so verify reads it instead of "not judged yet", and run replays it.
func TestEvaluate_APrepareRefusalIsAStoredFailNotAnEngineError(t *testing.T) {
	f := newEvalFixture(t, func(g *declaration.FileGuard) {
		require.NoError(t, os.WriteFile(filepath.Join(g.Dir, "prepare.sh"),
			[]byte("#!/bin/sh\ncat >/dev/null\necho '{\"reason\":\"no standard to judge\"}'\nexit 1\n"), 0o755))
		g.Checks = []declaration.Check{{Script: "./check.sh", Prepare: "./prepare.sh"}}
	})
	f.commitDoc(t, "docs/a.md", "clean")

	r, refused := f.evaluate(t, f.results)
	require.True(t, refused)
	assert.Contains(t, r.Reason, "no standard to judge")

	p := f.params(t, f.results)
	p.Verify = true
	got, _ := Evaluate(p)
	require.Len(t, got, 1)
	assert.Contains(t, got[0].Reason, "no standard to judge")
	assert.NotContains(t, got[0].Reason, "not judged yet")
}

// citedFixture is a rule that requires a user citation, over a doc committed with a citation
// trailer: the key is the one a real session computes.
func citedFixture(t *testing.T) *evalFixture {
	t.Helper()
	f := newEvalFixture(t, func(g *declaration.FileGuard) {
		g.Require = []declaration.Prerequisite{{Citation: &declaration.CitationPrerequisite{SourceTypes: []string{"user"}}}}
	})
	require.NoError(t, os.MkdirAll(filepath.Join(f.repo, "docs"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(f.repo, "docs", "a.md"), []byte("clean"), 0o644))
	runGit(t, f.repo, "add", "-A")
	runGit(t, f.repo, "commit", "-m", "doc", "-m", changeset.TrailerCitesUser+": the user said so")
	return f
}

func (f *evalFixture) verifyReasons(t *testing.T) []FileGuardResult {
	t.Helper()
	p := f.params(t, f.results)
	p.Verify = true
	got, _ := Evaluate(p)
	return got
}

// A `run` with no session cannot ground the trailers: it refuses saying so, and the refusal is
// no verdict about the key (a real session computes the same key), so nothing is stored.
func TestEvaluate_ARunWithoutASessionStoresNoCitationVerdict(t *testing.T) {
	f := citedFixture(t)
	r, refused := f.evaluate(t, f.results)
	require.True(t, refused)
	assert.Contains(t, r.Reason, "needs a session to judge")
	assert.Equal(t, 0, f.runs(t), "no check ran")
	got := f.verifyReasons(t)
	require.Len(t, got, 1)
	assert.Contains(t, got[0].Reason, "not judged yet", "no FAIL was stored under the key")
	assert.NotContains(t, got[0].Reason, "needs a session")
}

// A quote that does not resolve in a present session is a real refusal: stored as a FAIL.
func TestEvaluate_AnUnresolvedCitationIsStoredAsFail(t *testing.T) {
	f := citedFixture(t)
	record := filepath.Join(t.TempDir(), "s-eval.jsonl")
	require.NoError(t, os.WriteFile(record, []byte(""), 0o644))
	p := f.params(t, f.results)
	p.Transcript = record
	ev := &changesetEvaluation{errw: &bytes.Buffer{}, diags: map[string]*bytes.Buffer{}, root: f.repo, params: p, store: f.results, rng: p.Range, batch: "b1"}
	ev.identity = ev.runIdentity()
	_, refused := ev.evaluate(f.guard)
	require.True(t, refused)
	got := f.verifyReasons(t)
	require.Len(t, got, 1)
	assert.NotContains(t, got[0].Reason, "not judged yet", "the refusal is stored")
}

// A guard's stored steps are its own subject's: another subject of the same rule contributes none.
func TestStepsOf_AreFilteredByRuleAndSubject(t *testing.T) {
	ev := &changesetEvaluation{}
	g := declaration.FileGuard{Name: "r"}
	ev.note(CheckOutcome{Rule: g.Qualified(), Subject: "a", Kind: "check[0]:script:x", Status: "pass"})
	ev.note(CheckOutcome{Rule: g.Qualified(), Subject: "b", Kind: "check[0]:script:x", Status: "fail", Reason: "b's"})
	ev.note(CheckOutcome{Rule: g.Qualified(), Subject: "docs/a.md", Kind: "require:citation", Status: "pass"})
	ev.note(CheckOutcome{Rule: "other", Subject: "a", Kind: "k", Status: "pass"})
	rr := &ruleRun{g: g, subject: changeset.Subject{ID: "a", Files: []string{"docs/a.md"}}}
	rows := ev.stepsOf(rr)
	require.Len(t, rows, 2)
	assert.Equal(t, "a", rows[0].Subject)
	assert.Equal(t, "docs/a.md", rows[1].Subject)
}

// A stored citation refusal that offered to amend HEAD is replayed without the offer once HEAD
// has been pushed: the advice depends on the repository now, the verdict on the key's input.
func TestCurrentAdvice_AStoredAmendOfferIsDroppedOncePushed(t *testing.T) {
	f := citedFixture(t)
	ev := f.newEvaluation(t, f.results)
	head := ev.rng.Head
	cs := changeset.Changeset{Base: ev.rng.Base, Head: head, Files: []changeset.File{{Path: "docs/a.md", Commits: []string{head}}}}
	stored := citeHowToFix(cs, []string{"docs/a.md"}, changeset.TrailerCitesUser, true, nil)
	require.Contains(t, stored, "--amend")

	assert.Equal(t, stored, ev.currentAdvice(stored), "unpushed and clean: the offer stands")
	runGit(t, f.repo, "update-ref", "refs/remotes/origin/main", "HEAD")
	got := ev.currentAdvice(stored)
	assert.NotContains(t, got, "--amend")
	assert.Contains(t, got, "To undo the whole range", "the rest of the advice stays")
	assert.Contains(t, got, "FOLLOW-UP commit")
}
