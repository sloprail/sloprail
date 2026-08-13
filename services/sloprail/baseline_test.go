package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/sessionstate"
)

// A real repository and a real store. What is being tested is the pairing of
// the two — a point taken from git and held in the store, and the rule about
// when it moves — so faking either half would leave the pairing untested.

func openStore(t *testing.T) sessionstate.Store {
	t.Helper()
	s, err := sessionstate.Open(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s
}

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

func baseline(t *testing.T, s sessionstate.Store) (commit, branch string) {
	t.Helper()
	commit, _, err := s.Meta(sessionstate.MetaBaselineCommit)
	require.NoError(t, err)
	branch, _, err = s.Meta(sessionstate.MetaBaselineBranch)
	require.NoError(t, err)
	return commit, branch
}

func TestEnsureBaseline_RecordsTheCommitAndTheBranch(t *testing.T) {
	dir := initRepo(t)
	want := commitFile(t, dir, "a.txt", "one")
	s := openStore(t)

	outcome, err := ensureBaseline(s, dir)
	require.NoError(t, err)
	assert.Equal(t, baselineRecorded, outcome)

	commit, branch := baseline(t, s)
	assert.Equal(t, want, commit)
	assert.Equal(t, "main", branch)
}

func TestEnsureBaseline_RecordedOnceAndNotRevised(t *testing.T) {
	// The point is taken once and does not follow HEAD. An agent that commits
	// mid-session must not push its own work — including work a hook refused —
	// out of the difference, which is exactly what following HEAD would do.
	dir := initRepo(t)
	first := commitFile(t, dir, "a.txt", "one")
	s := openStore(t)

	outcome, err := ensureBaseline(s, dir)
	require.NoError(t, err)
	require.Equal(t, baselineRecorded, outcome)

	second := commitFile(t, dir, "b.txt", "two")
	require.NotEqual(t, first, second)

	outcome, err = ensureBaseline(s, dir)
	require.NoError(t, err)
	assert.Equal(t, baselineUnchanged, outcome)

	commit, _ := baseline(t, s)
	assert.Equal(t, first, commit, "the point must still be where the session began")
}

func TestEnsureBaseline_IsIdempotentAcrossManyCycles(t *testing.T) {
	// Called at session start and at the end of every cycle. Nothing moves
	// while the tree stays on the line the point was taken on.
	dir := initRepo(t)
	want := commitFile(t, dir, "a.txt", "one")
	s := openStore(t)

	for range 5 {
		_, err := ensureBaseline(s, dir)
		require.NoError(t, err)
	}
	commit, branch := baseline(t, s)
	assert.Equal(t, want, commit)
	assert.Equal(t, "main", branch)
}

func TestEnsureBaseline_BranchSwitchRetakesThePoint(t *testing.T) {
	// A point recorded on the line the tree left describes a history it no
	// longer has, and the difference against it would be every commit between
	// the two — an entire branch delta arriving at one cycle as though this
	// session had written it.
	dir := initRepo(t)
	mainCommit := commitFile(t, dir, "a.txt", "one")
	s := openStore(t)

	outcome, err := ensureBaseline(s, dir)
	require.NoError(t, err)
	require.Equal(t, baselineRecorded, outcome)

	runGit(t, dir, "checkout", "-b", "feature")
	featureCommit := commitFile(t, dir, "b.txt", "two")

	outcome, err = ensureBaseline(s, dir)
	require.NoError(t, err)
	assert.Equal(t, baselineMoved, outcome)

	commit, branch := baseline(t, s)
	assert.Equal(t, featureCommit, commit)
	assert.Equal(t, "feature", branch)
	assert.NotEqual(t, mainCommit, commit)
}

func TestEnsureBaseline_SwitchingBackIsAlsoASwitch(t *testing.T) {
	// Returning to the line the session began on is still a change of history
	// from the line the point was last taken on, and the point must follow.
	dir := initRepo(t)
	commitFile(t, dir, "a.txt", "one")
	s := openStore(t)

	_, err := ensureBaseline(s, dir)
	require.NoError(t, err)

	runGit(t, dir, "checkout", "-b", "feature")
	commitFile(t, dir, "b.txt", "two")
	_, err = ensureBaseline(s, dir)
	require.NoError(t, err)

	runGit(t, dir, "checkout", "main")
	outcome, err := ensureBaseline(s, dir)
	require.NoError(t, err)
	assert.Equal(t, baselineMoved, outcome)

	_, branch := baseline(t, s)
	assert.Equal(t, "main", branch)
}

func TestEnsureBaseline_UnfixedRefusalSurvivesARebaseline(t *testing.T) {
	// The half that makes moving the point safe at all.
	//
	// A failing verdict is kept in file_checks in its own right, keyed by path
	// and guardrail rather than against the point. Were the two tied together,
	// switching branches would drop an unfixed violation out of view and leave
	// the file broken with nothing left to say so.
	dir := initRepo(t)
	commitFile(t, dir, "a.txt", "one")
	s := openStore(t)

	_, err := ensureBaseline(s, dir)
	require.NoError(t, err)

	// A guardrail refused this content during the session, and nobody fixed it.
	refused := sessionstate.Verdict{Fingerprint: "content-of-broken-file", Passed: false}
	require.NoError(t, s.RecordFileCheck("broken.txt", "no-slop", refused))

	runGit(t, dir, "checkout", "-b", "feature")
	commitFile(t, dir, "b.txt", "two")
	outcome, err := ensureBaseline(s, dir)
	require.NoError(t, err)
	require.Equal(t, baselineMoved, outcome, "the point must have moved for this to test anything")

	// The refusal is still there, unchanged.
	v, found, err := s.FileCheck("broken.txt", "no-slop")
	require.NoError(t, err)
	require.True(t, found, "the refusal must survive the point moving")
	assert.False(t, v.Passed)
	assert.Equal(t, "content-of-broken-file", v.Fingerprint)

	// And it is still a refusal in the sense that matters: the guardrail may
	// not skip the file, so the violation surfaces again on the next cycle.
	skippable, err := s.Skippable("broken.txt", "no-slop", "content-of-broken-file")
	require.NoError(t, err)
	assert.False(t, skippable, "an unfixed refusal must keep the hook running after a re-baseline")
}

func TestEnsureBaseline_PassingVerdictAlsoSurvivesARebaseline(t *testing.T) {
	// The other side of the same independence. A pass is not re-earned by a
	// branch switch either — the content it was given is the content it judged,
	// and nothing about the line of history changes that.
	dir := initRepo(t)
	commitFile(t, dir, "a.txt", "one")
	s := openStore(t)

	_, err := ensureBaseline(s, dir)
	require.NoError(t, err)
	require.NoError(t, s.RecordFileCheck("ok.txt", "no-slop",
		sessionstate.Verdict{Fingerprint: "judged", Passed: true}))

	runGit(t, dir, "checkout", "-b", "feature")
	commitFile(t, dir, "b.txt", "two")
	_, err = ensureBaseline(s, dir)
	require.NoError(t, err)

	skippable, err := s.Skippable("ok.txt", "no-slop", "judged")
	require.NoError(t, err)
	assert.True(t, skippable)
}

func TestEnsureBaseline_DetachedHeadDoesNotRebaselineEveryCycle(t *testing.T) {
	// A detached HEAD has no branch, and an empty branch compares equal to the
	// next empty branch. Without that, a session doing a rebase or a bisect
	// would take a new point on every single cycle and never measure anything.
	dir := initRepo(t)
	sha := commitFile(t, dir, "a.txt", "one")
	commitFile(t, dir, "b.txt", "two")
	runGit(t, dir, "checkout", "--detach", sha)
	s := openStore(t)

	outcome, err := ensureBaseline(s, dir)
	require.NoError(t, err)
	require.Equal(t, baselineRecorded, outcome)

	outcome, err = ensureBaseline(s, dir)
	require.NoError(t, err)
	assert.Equal(t, baselineUnchanged, outcome)

	commit, branch := baseline(t, s)
	assert.Equal(t, sha, commit)
	assert.Empty(t, branch)
}

func TestEnsureBaseline_LeavingADetachedHeadIsASwitch(t *testing.T) {
	dir := initRepo(t)
	sha := commitFile(t, dir, "a.txt", "one")
	commitFile(t, dir, "b.txt", "two")
	runGit(t, dir, "checkout", "--detach", sha)
	s := openStore(t)

	_, err := ensureBaseline(s, dir)
	require.NoError(t, err)

	runGit(t, dir, "checkout", "main")
	outcome, err := ensureBaseline(s, dir)
	require.NoError(t, err)
	assert.Equal(t, baselineMoved, outcome)

	_, branch := baseline(t, s)
	assert.Equal(t, "main", branch)
}

func TestEnsureBaseline_NotARepositoryIsNotAFailure(t *testing.T) {
	// A project without git is a project the engine guards with everything
	// except the difference, rather than one it refuses to start a session in.
	dir := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	s := openStore(t)

	outcome, err := ensureBaseline(s, dir)
	require.NoError(t, err)
	assert.Equal(t, baselineUnavailable, outcome)

	_, found, err := s.Meta(sessionstate.MetaBaselineCommit)
	require.NoError(t, err)
	assert.False(t, found)
}

func TestEnsureBaseline_RepositoryWithNoCommitRecordsNothingYet(t *testing.T) {
	// The first session in a fresh project. Nothing to measure from until a
	// commit exists, and the next cycle asks again.
	dir := initRepo(t)
	s := openStore(t)

	outcome, err := ensureBaseline(s, dir)
	require.NoError(t, err)
	assert.Equal(t, baselineUnavailable, outcome)

	_, found, err := s.Meta(sessionstate.MetaBaselineCommit)
	require.NoError(t, err)
	assert.False(t, found)

	// Once there is a commit, the point is taken.
	want := commitFile(t, dir, "a.txt", "one")
	outcome, err = ensureBaseline(s, dir)
	require.NoError(t, err)
	assert.Equal(t, baselineRecorded, outcome)
	commit, _ := baseline(t, s)
	assert.Equal(t, want, commit)
}

func TestEnsureBaseline_HalfWrittenPointIsRetaken(t *testing.T) {
	// The two rows are not written atomically. A commit recorded without its
	// branch is not a point that can be trusted — there is nothing to say which
	// line of history it describes — so it is taken again rather than believed.
	dir := initRepo(t)
	commitFile(t, dir, "a.txt", "one")
	s := openStore(t)
	require.NoError(t, s.SetMeta(sessionstate.MetaBaselineCommit, "stale-commit"))

	outcome, err := ensureBaseline(s, dir)
	require.NoError(t, err)
	assert.Equal(t, baselineRecorded, outcome)

	commit, branch := baseline(t, s)
	assert.NotEqual(t, "stale-commit", commit)
	assert.Equal(t, "main", branch)
}

func TestEnsureBaseline_ClosedStoreReportsRatherThanMoving(t *testing.T) {
	// A store that cannot be read is not a branch that changed. Every failure
	// path leaves the recorded point alone, because moving it on a transient
	// fault would push unfixed work out of the difference.
	dir := initRepo(t)
	commitFile(t, dir, "a.txt", "one")
	s := openStore(t)
	require.NoError(t, s.Close())

	_, err := ensureBaseline(s, dir)
	assert.ErrorIs(t, err, sessionstate.ErrClosed)
}
