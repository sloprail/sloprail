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

// currentBranch is the branch HEAD names, or "" when HEAD is detached.
//
// Its own helper because a detached HEAD makes `symbolic-ref` exit non-zero,
// which is the answer rather than a failure — runGit would call it one.
func currentBranch(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "symbolic-ref", "-q", "--short", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
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

// divergentBranch creates a branch off the repository's first commit, so that
// switching to it genuinely LEAVES the history the tree is on rather than
// extending it. Returns to the branch it started on.
//
// A helper because the distinction is the whole subject: `git checkout -b`
// from where the tree already is does not leave anything, and a test that used
// it to mean "switched branches" would never reach the case it names.
func divergentBranch(t *testing.T, dir, name string) string {
	t.Helper()
	was := runGit(t, dir, "rev-parse", "--abbrev-ref", "HEAD")
	root := runGit(t, dir, "rev-list", "--max-parents=0", "HEAD")
	runGit(t, dir, "checkout", "-b", name, root)
	tip := commitFile(t, dir, name+".txt", "on "+name)
	runGit(t, dir, "checkout", was)
	return tip
}

func TestEnsureBaseline_BranchSwitchRetakesThePoint(t *testing.T) {
	// A point recorded on the line the tree left describes a history it no
	// longer has, and the difference against it would be every commit between
	// the two — an entire branch delta arriving at one cycle as though this
	// session had written it.
	dir := initRepo(t)
	commitFile(t, dir, "a.txt", "one")
	mainCommit := commitFile(t, dir, "b.txt", "two")
	featureCommit := divergentBranch(t, dir, "feature")
	s := openStore(t)

	outcome, err := ensureBaseline(s, dir)
	require.NoError(t, err)
	require.Equal(t, baselineRecorded, outcome)

	// Onto a line that does not contain the recorded point.
	runGit(t, dir, "checkout", "feature")

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
	mainCommit := commitFile(t, dir, "b.txt", "two")
	divergentBranch(t, dir, "feature")
	s := openStore(t)

	_, err := ensureBaseline(s, dir)
	require.NoError(t, err)

	runGit(t, dir, "checkout", "feature")
	_, err = ensureBaseline(s, dir)
	require.NoError(t, err)

	runGit(t, dir, "checkout", "main")
	outcome, err := ensureBaseline(s, dir)
	require.NoError(t, err)
	assert.Equal(t, baselineMoved, outcome)

	commit, branch := baseline(t, s)
	assert.Equal(t, "main", branch)
	assert.Equal(t, mainCommit, commit)
}

func TestEnsureBaseline_NewBranchOffTheSessionsOwnWorkDoesNotMoveThePoint(t *testing.T) {
	// F3. `git checkout -b` changes the name and moves no history: everything
	// the session committed is still reachable, so it is all still the
	// session's own work and must stay inside the difference.
	//
	// Re-taking the point here would push the agent's committed work out
	// through the branch door — the same loss the record-once rule exists to
	// prevent, reached by an operation that changed no history at all.
	dir := initRepo(t)
	start := commitFile(t, dir, "a.txt", "one")
	s := openStore(t)

	_, err := ensureBaseline(s, dir)
	require.NoError(t, err)

	// The agent commits its work, then branches off it.
	agentCommit := commitFile(t, dir, "agent.txt", "the agent's work")
	runGit(t, dir, "checkout", "-b", "feature")
	require.Equal(t, agentCommit, runGit(t, dir, "rev-parse", "HEAD"),
		"checkout -b must not have moved HEAD, or this tests something else")

	outcome, err := ensureBaseline(s, dir)
	require.NoError(t, err)
	assert.Equal(t, baselineUnchanged, outcome)

	commit, _ := baseline(t, s)
	assert.Equal(t, start, commit,
		"the point must still be where the session began, so the agent's own commit stays in the difference")
}

func TestEnsureBaseline_RenamingTheBranchDoesNotMoveThePoint(t *testing.T) {
	// F3. `git branch -m` is a pure rename: same commit, same history, nothing
	// left. Treating it as a switch re-takes the point past the session's own
	// commits for no reason at all.
	dir := initRepo(t)
	start := commitFile(t, dir, "a.txt", "one")
	s := openStore(t)

	_, err := ensureBaseline(s, dir)
	require.NoError(t, err)
	commitFile(t, dir, "agent.txt", "the agent's work")

	runGit(t, dir, "branch", "-m", "main", "trunk")

	outcome, err := ensureBaseline(s, dir)
	require.NoError(t, err)
	assert.Equal(t, baselineUnchanged, outcome)

	commit, _ := baseline(t, s)
	assert.Equal(t, start, commit, "a rename is not a change of history")
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
	// A second commit, so "feature" off the root really is another line rather
	// than a descendant of where the point will be taken.
	commitFile(t, dir, "b.txt", "two")
	divergentBranch(t, dir, "feature")
	s := openStore(t)

	_, err := ensureBaseline(s, dir)
	require.NoError(t, err)

	// A guardrail refused this content during the session, and nobody fixed it.
	refused := sessionstate.Verdict{Fingerprint: "content-of-broken-file", Passed: false}
	require.NoError(t, s.RecordFileCheck("broken.txt", "no-slop", refused))

	runGit(t, dir, "checkout", "feature")
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
	// A second commit, so "feature" off the root really is another line rather
	// than a descendant of where the point will be taken.
	commitFile(t, dir, "b.txt", "two")
	divergentBranch(t, dir, "feature")
	s := openStore(t)

	_, err := ensureBaseline(s, dir)
	require.NoError(t, err)
	require.NoError(t, s.RecordFileCheck("ok.txt", "no-slop",
		sessionstate.Verdict{Fingerprint: "judged", Passed: true}))

	runGit(t, dir, "checkout", "feature")
	outcome, err := ensureBaseline(s, dir)
	require.NoError(t, err)
	require.Equal(t, baselineMoved, outcome, "the point must have moved for this to test anything")

	skippable, err := s.Skippable("ok.txt", "no-slop", "judged")
	require.NoError(t, err)
	assert.True(t, skippable)
}

func TestEnsureBaseline_DetachedHeadStayingStillDoesNotRebaseline(t *testing.T) {
	// A session that starts detached and does not move is not re-baselined on
	// every cycle. HEAD really does hold still here, which is the only thing
	// this claims.
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

func TestEnsureBaseline_DetachingSomewhereElseIsLeavingTheHistory(t *testing.T) {
	// F2, and the case the old detached-HEAD test never reached because it
	// never moved HEAD between calls.
	//
	// Two different detached HEADs both report no branch. Compared on the
	// branch alone they are the same place, and the point is left on a commit
	// the tree has walked away from — the whole delta between the two arriving
	// at the next cycle as though this session had written it.
	dir := initRepo(t)
	commitFile(t, dir, "a.txt", "one")
	first := commitFile(t, dir, "b.txt", "two")
	// A second line, three commits along, that does not contain the first.
	last := divergentBranch(t, dir, "other")
	require.NotEqual(t, first, last)

	runGit(t, dir, "checkout", "--detach", first)
	s := openStore(t)

	outcome, err := ensureBaseline(s, dir)
	require.NoError(t, err)
	require.Equal(t, baselineRecorded, outcome)
	commit, branch := baseline(t, s)
	require.Equal(t, first, commit)
	require.Empty(t, branch, "detached, so there is no branch to tell these apart by")

	// HEAD actually moves — to a commit on another line, still detached, still
	// reporting no branch.
	runGit(t, dir, "checkout", "--detach", last)
	require.Empty(t, currentBranch(t, dir),
		"still detached, so the branch cannot be what tells these apart")

	outcome, err = ensureBaseline(s, dir)
	require.NoError(t, err)
	assert.Equal(t, baselineMoved, outcome,
		"two different detached HEADs are not the same place")

	commit, _ = baseline(t, s)
	assert.Equal(t, last, commit, "the point must be where the tree actually is")
}

func TestEnsureBaseline_DetachedHeadMovingBackwardsIsLeavingTheHistory(t *testing.T) {
	// The other direction. Detaching at an ANCESTOR of the recorded point means
	// the recorded commit is no longer reachable, so it is no longer a place
	// this tree can measure from.
	dir := initRepo(t)
	first := commitFile(t, dir, "a.txt", "one")
	last := commitFile(t, dir, "b.txt", "two")

	runGit(t, dir, "checkout", "--detach", last)
	s := openStore(t)
	_, err := ensureBaseline(s, dir)
	require.NoError(t, err)

	runGit(t, dir, "checkout", "--detach", first)
	outcome, err := ensureBaseline(s, dir)
	require.NoError(t, err)
	assert.Equal(t, baselineMoved, outcome)

	commit, _ := baseline(t, s)
	assert.Equal(t, first, commit)
}

func TestEnsureBaseline_ARebaseDoesNotRebaselineAtEveryStep(t *testing.T) {
	// The case the branch-only rule was written to protect, made to actually
	// work rather than merely asserted.
	//
	// An interactive rebase stopped at `edit` IS a detached HEAD walking a
	// different commit at each step, and each of those commits reaches nothing
	// that was recorded. On reachability alone the point would be re-taken
	// several times over one rebase, each time onto a commit the rebase is
	// about to replace. The branch the operation is running on holds still for
	// as long as it runs, and that is what the point is held by.
	dir := initRepo(t)
	commitFile(t, dir, "a.txt", "one")
	for _, n := range []string{"b", "c", "d"} {
		commitFile(t, dir, n+".txt", n)
	}
	start := runGit(t, dir, "rev-parse", "HEAD")
	s := openStore(t)

	outcome, err := ensureBaseline(s, dir)
	require.NoError(t, err)
	require.Equal(t, baselineRecorded, outcome)

	// Stop at every step, the way a person editing history does.
	rebase := exec.Command("git", "rebase", "-i", "HEAD~3")
	rebase.Dir = dir
	rebase.Env = append(os.Environ(),
		"GIT_SEQUENCE_EDITOR=sed -i.bak s/^pick/edit/",
		"GIT_EDITOR=true")
	out, err := rebase.CombinedOutput()
	require.NoErrorf(t, err, "git rebase -i: %s", out)

	moved := 0
	for range 3 {
		require.Empty(t, currentBranch(t, dir),
			"a rebase stopped at edit must be detached, or this proves nothing")
		outcome, err := ensureBaseline(s, dir)
		require.NoError(t, err)
		if outcome == baselineMoved {
			moved++
		}
		cont := exec.Command("git", "rebase", "--continue")
		cont.Dir = dir
		cont.Env = append(os.Environ(), "GIT_EDITOR=true")
		if out, err := cont.CombinedOutput(); err != nil {
			t.Fatalf("git rebase --continue: %v\n%s", err, out)
		}
	}

	assert.Zero(t, moved, "a rebase must not re-take the point at every step")
	commit, _ := baseline(t, s)
	assert.Equal(t, start, commit, "the point stays where the session began")
}

func TestEnsureBaseline_LeavingADetachedHeadOntoAnotherLineIsASwitch(t *testing.T) {
	// Attaching to a branch that does not contain the recorded point is
	// leaving, the same as any other departure from the history.
	dir := initRepo(t)
	commitFile(t, dir, "a.txt", "one")
	featureTip := divergentBranch(t, dir, "feature")
	detached := commitFile(t, dir, "b.txt", "two")

	runGit(t, dir, "checkout", "--detach", detached)
	s := openStore(t)

	_, err := ensureBaseline(s, dir)
	require.NoError(t, err)

	runGit(t, dir, "checkout", "feature")
	outcome, err := ensureBaseline(s, dir)
	require.NoError(t, err)
	assert.Equal(t, baselineMoved, outcome)

	commit, branch := baseline(t, s)
	assert.Equal(t, "feature", branch)
	assert.Equal(t, featureTip, commit)
}

func TestEnsureBaseline_AttachingToABranchThatContainsThePointDoesNotMoveIt(t *testing.T) {
	// Detaching and then checking out the branch the commit is on again is a
	// round trip, not a departure. The point is still reachable, so everything
	// between it and HEAD is still inside the difference where it belongs.
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
	assert.Equal(t, baselineUnchanged, outcome)

	commit, _ := baseline(t, s)
	assert.Equal(t, sha, commit, "a commit still reachable is still a point to measure from")
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

func TestEnsureBaseline_AFinishedRebaseRetakesThePoint(t *testing.T) {
	// The other half of the rebase rule, and the one a same-branch guard alone
	// gets wrong.
	//
	// A rebase rewrites the commits it walks. Once it finishes, the branch reads
	// exactly as it did before while the history under it has been replaced, and
	// the recorded point is on a commit that no longer exists on this line.
	// Holding the point on the strength of the branch name would leave the
	// session measuring from a discarded commit for the rest of its life.
	dir := initRepo(t)
	commitFile(t, dir, "a.txt", "one")
	for _, n := range []string{"b", "c", "d"} {
		commitFile(t, dir, n+".txt", n)
	}
	start := runGit(t, dir, "rev-parse", "HEAD")
	s := openStore(t)

	_, err := ensureBaseline(s, dir)
	require.NoError(t, err)

	// Reword every commit, which rewrites all three and runs to completion.
	rebase := exec.Command("git", "rebase", "-i", "HEAD~3")
	rebase.Dir = dir
	// The message editor must actually change something: `reword` with a no-op
	// editor keeps the message, and git then leaves the commit alone.
	rebase.Env = append(os.Environ(),
		"GIT_SEQUENCE_EDITOR=sed -i.bak s/^pick/reword/",
		"GIT_EDITOR=sed -i.bak 1s/.*/reworded/")
	out, err := rebase.CombinedOutput()
	require.NoErrorf(t, err, "git rebase -i: %s", out)

	require.Equal(t, "main", currentBranch(t, dir), "the rebase finished and HEAD is attached again")
	require.NotEqual(t, start, runGit(t, dir, "rev-parse", "HEAD"),
		"the rebase must have rewritten history, or this proves nothing")

	outcome, err := ensureBaseline(s, dir)
	require.NoError(t, err)
	assert.Equal(t, baselineMoved, outcome,
		"the branch name is unchanged but the history under it was replaced")

	commit, _ := baseline(t, s)
	assert.NotEqual(t, start, commit, "the point must not stay on a commit the rebase discarded")
	assert.Equal(t, runGit(t, dir, "rev-parse", "HEAD"), commit)
}
