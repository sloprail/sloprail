package gitrepo

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// initRepo makes a real repository in a temporary directory.
//
// A real one rather than a fake: what is being tested is the reading of git's
// own output, and a stub would only prove this package agrees with a second
// copy of the assumptions it already holds.
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "--initial-branch=main")
	git(t, dir, "config", "user.email", "test@example.invalid")
	git(t, dir, "config", "user.name", "Test")
	return dir
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "git %s: %s", strings.Join(args, " "), out)
	return strings.TrimSpace(string(out))
}

func commit(t *testing.T, dir, name, body string) string {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "add "+name)
	return git(t, dir, "rev-parse", "HEAD")
}

func TestHead_ReportsCommitAndBranch(t *testing.T) {
	dir := initRepo(t)
	want := commit(t, dir, "a.txt", "one")

	pos, err := Head(dir)
	require.NoError(t, err)
	assert.Equal(t, want, pos.Commit)
	assert.Equal(t, "main", pos.Branch)
}

func TestHead_BranchChangesWithCheckout(t *testing.T) {
	// The branch is what makes a switch to another line of history noticeable.
	// If it did not change here, nothing downstream could ever detect one.
	dir := initRepo(t)
	commit(t, dir, "a.txt", "one")

	git(t, dir, "checkout", "-b", "feature")
	featureCommit := commit(t, dir, "b.txt", "two")

	pos, err := Head(dir)
	require.NoError(t, err)
	assert.Equal(t, "feature", pos.Branch)
	assert.Equal(t, featureCommit, pos.Commit)

	git(t, dir, "checkout", "main")
	pos, err = Head(dir)
	require.NoError(t, err)
	assert.Equal(t, "main", pos.Branch)
	assert.NotEqual(t, featureCommit, pos.Commit)
}

func TestHead_CommitMovesAlongTheSameBranch(t *testing.T) {
	// Committing is not switching. The branch stays put and only the commit
	// moves, which is what lets a caller tell "the agent committed" apart from
	// "the agent changed lines of history".
	dir := initRepo(t)
	first := commit(t, dir, "a.txt", "one")

	before, err := Head(dir)
	require.NoError(t, err)

	second := commit(t, dir, "b.txt", "two")
	after, err := Head(dir)
	require.NoError(t, err)

	assert.Equal(t, before.Branch, after.Branch)
	assert.Equal(t, first, before.Commit)
	assert.Equal(t, second, after.Commit)
}

func TestHead_DetachedHeadHasNoBranch(t *testing.T) {
	// A rebase, a bisect, or a checkout of a bare commit all produce one. It is
	// a real state rather than a failure, and reporting it as an empty branch
	// means a session that starts detached and stays detached compares equal to
	// itself rather than being re-baselined on every cycle.
	dir := initRepo(t)
	sha := commit(t, dir, "a.txt", "one")
	commit(t, dir, "b.txt", "two")

	git(t, dir, "checkout", "--detach", sha)

	pos, err := Head(dir)
	require.NoError(t, err)
	assert.Equal(t, sha, pos.Commit)
	assert.Empty(t, pos.Branch)
}

func TestHead_RepositoryWithNoCommitIsAnAbsentPosition(t *testing.T) {
	// The very first session in a new project is in this state. There is
	// nothing wrong and nothing to measure from, so it is reported as an empty
	// position rather than as an error.
	dir := initRepo(t)

	pos, err := Head(dir)
	require.NoError(t, err)
	assert.Empty(t, pos.Commit)
}

func TestHead_NotARepositoryIsASentinel(t *testing.T) {
	// A project that is not a repository is an ordinary thing for a person to
	// have. The caller recognises it by the sentinel and carries on, rather
	// than matching on whatever git happened to print.
	dir := t.TempDir()
	// A repository anywhere above the temp directory would make this a
	// repository too; git's own ceiling stops the search here.
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))

	_, err := Head(dir)
	assert.ErrorIs(t, err, ErrNotARepository)
}

func TestHead_UntrackedFilesDoNotAffectThePosition(t *testing.T) {
	// The position is about history, not about the working tree's contents.
	// Asking for untracked files would make this walk the whole tree on every
	// hook run for an answer it does not use.
	dir := initRepo(t)
	want := commit(t, dir, "a.txt", "one")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("x"), 0o644))

	pos, err := Head(dir)
	require.NoError(t, err)
	assert.Equal(t, want, pos.Commit)
	assert.Equal(t, "main", pos.Branch)
}

func TestParseBranchHeaders_IgnoresEntryLines(t *testing.T) {
	// Porcelain v2 interleaves entry lines with the headers, and a path
	// containing spaces splits like a header would. Only the "# name value"
	// shape is read, and the `field == "#"` switch is the whole of what enforces
	// it.
	//
	// The data below is chosen so that guard is the ONLY thing standing between
	// the input and a wrong answer. An earlier version of this test used
	// `? untracked branch.head evil`, which cannot reach the assertion: the two
	// Cuts take "?" as the field and "untracked" as the name, so the name is
	// never "branch.head" and the line is inert whether or not the guard is
	// there. Deleting the guard — accepting "?" and "1" lines as headers —
	// passed that version green. The property the test is named for was not
	// being measured.
	//
	// What actually splits like a header is a path whose FIRST word is the
	// header name, since the status letter takes the field slot and the path
	// begins immediately after it. `? branch.head evil` is an untracked file
	// named "branch.head evil", which git emits in exactly this shape.
	for _, attack := range []struct {
		name string
		line string
	}{
		// An untracked path whose name begins with the branch header's.
		{"untracked path named like the branch header", "? branch.head evil"},
		// The same against the commit, which is the more damaging of the two:
		// a poisoned oid is a baseline pointing at a commit nothing recorded.
		{"untracked path named like the oid header", "? branch.oid deadbeef"},
		// A changed-entry line, which begins with "1" and is otherwise the
		// same shape.
		{"changed entry named like the branch header", "1 branch.head evil"},
		// The original line, kept so the ordinary interleaving stays covered —
		// it is inert, and now it is inert alongside data that is not.
		{"ordinary untracked path", "? untracked branch.head evil"},
		{"ordinary changed entry", "1 .M N... 100644 100644 100644 aaa bbb some file.txt"},
	} {
		t.Run(attack.name, func(t *testing.T) {
			pos, err := parseBranchHeaders(strings.Join([]string{
				"# branch.oid abc123",
				"# branch.head main",
				attack.line,
			}, "\n"))
			require.NoError(t, err)
			assert.Equal(t, "abc123", pos.Commit, "an entry line must not supply the commit")
			assert.Equal(t, "main", pos.Branch, "an entry line must not supply the branch")
		})
	}

	// All of them at once, since a real status carries many entries and the
	// headers come first — a later line overwriting an earlier header is the
	// shape that survives a per-line test.
	pos, err := parseBranchHeaders(strings.Join([]string{
		"# branch.oid abc123",
		"# branch.head main",
		"1 .M N... 100644 100644 100644 aaa bbb some file.txt",
		"? untracked branch.head evil",
		"? branch.head evil",
		"? branch.oid deadbeef",
		"1 branch.head evil",
	}, "\n"))
	require.NoError(t, err)
	assert.Equal(t, "abc123", pos.Commit)
	assert.Equal(t, "main", pos.Branch)
}

// TestParseBranchHeaders_AValuelessHeaderDoesNotEraseTheOneAlreadyRead pins the
// second Cut's `ok`, which is load-bearing and was not being measured.
//
// A header line carrying a NAME and no value — "# branch.oid" on its own —
// splits to name="branch.oid", v="". Without the `ok` guard the empty string is
// written straight over a commit that was read correctly a line earlier, and
// parseBranchHeaders then finds Commit == "" and returns the ABSENT position.
//
// That is the silent direction. An absent position is the documented, ordinary
// answer for a repository with no commit yet, so Head returns it with no error:
// the caller records no baseline, the session measures nothing, and nothing
// anywhere says why. It is the same shape as F4 — a fault degrading into the
// "there is simply nothing here" answer — reached through the parser instead of
// through the not-a-repository check.
//
// Both headers are covered because they fail differently. A wiped branch is
// recoverable (Contains compares on the commit); a wiped commit is the baseline
// itself.
func TestParseBranchHeaders_AValuelessHeaderDoesNotEraseTheOneAlreadyRead(t *testing.T) {
	for _, tc := range []struct {
		name      string
		truncated string
	}{
		{"valueless oid", "# branch.oid"},
		{"valueless head", "# branch.head"},
		// A trailing space is the near miss, and it lands on the same guard
		// rather than around it: the TrimSpace on the way in removes it, so the
		// line reaches the second Cut as the valueless case above and `ok` is
		// what stops it. Kept because it is the spelling that looks like it
		// should slip past — the Cut would succeed with an empty value if the
		// trim were ever dropped, and then the guard would not be reached at
		// all.
		{"oid with a trailing space", "# branch.oid "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pos, err := parseBranchHeaders(strings.Join([]string{
				"# branch.oid abc123",
				"# branch.head main",
				tc.truncated,
			}, "\n"))
			require.NoError(t, err)
			assert.Equal(t, "abc123", pos.Commit,
				"a valueless header must not erase the commit already read — an empty commit is reported as an absent position, and the session then measures nothing while saying nothing")
			assert.Equal(t, "main", pos.Branch,
				"a valueless header must not erase the branch already read")
		})
	}
}

func TestHead_TwoDetachedHeadsAreTellableApart(t *testing.T) {
	// F2. Both report no branch, so the branch cannot be what distinguishes
	// them — the commit has to, and Contains is what asks.
	dir := initRepo(t)
	first := commit(t, dir, "a.txt", "one")
	second := commit(t, dir, "b.txt", "two")

	git(t, dir, "checkout", "--detach", first)
	a, err := Head(dir)
	require.NoError(t, err)

	git(t, dir, "checkout", "--detach", second)
	b, err := Head(dir)
	require.NoError(t, err)

	require.Equal(t, a.Branch, b.Branch, "both detached, so the branch is the same empty value")
	assert.NotEqual(t, a.Commit, b.Commit, "and the commit is the only thing that differs")
}

func TestHead_DuringARebaseReportsTheBranchBeingRebased(t *testing.T) {
	// F2's other half. A rebase detaches HEAD and moves it at every step, but
	// the line of history being worked on has not changed — and reading each
	// step as a new line would re-baseline a caller several times over one
	// rebase.
	dir := initRepo(t)
	for _, n := range []string{"a", "b", "c", "d"} {
		commit(t, dir, n+".txt", n)
	}

	rebase := exec.Command("git", "rebase", "-i", "HEAD~3")
	rebase.Dir = dir
	rebase.Env = append(os.Environ(),
		"GIT_SEQUENCE_EDITOR=sed -i.bak s/^pick/edit/",
		"GIT_EDITOR=true")
	out, err := rebase.CombinedOutput()
	require.NoErrorf(t, err, "git rebase -i: %s", out)
	t.Cleanup(func() {
		abort := exec.Command("git", "rebase", "--abort")
		abort.Dir = dir
		_ = abort.Run()
	})

	pos, err := Head(dir)
	require.NoError(t, err)
	assert.Equal(t, "main", pos.Branch,
		"a rebase stopped at edit is detached, but the branch being rebased is known")
}

func TestHead_DuringABisectReportsTheBranchBisectFrom(t *testing.T) {
	// The same recovery for the other operation that walks a detached HEAD.
	dir := initRepo(t)
	root := commit(t, dir, "a.txt", "one")
	commit(t, dir, "b.txt", "two")
	commit(t, dir, "c.txt", "three")

	git(t, dir, "bisect", "start", "HEAD", root)
	t.Cleanup(func() {
		reset := exec.Command("git", "bisect", "reset")
		reset.Dir = dir
		_ = reset.Run()
	})

	pos, err := Head(dir)
	require.NoError(t, err)
	assert.Equal(t, "main", pos.Branch, "a bisect records the branch it started from")
}

func TestHead_ACorruptRepositoryIsAFaultRatherThanAnAbsence(t *testing.T) {
	// F4. git says "fatal: not a git repository" for a corrupted HEAD as well
	// as for a directory with no repository at all, so the message alone cannot
	// tell them apart.
	//
	// Reported as a fault because a caller that reads this as an absence stops
	// measuring anything and says nothing about why — the recorded baseline is
	// abandoned in silence, which is the unsafe direction, not the safe one.
	dir := initRepo(t)
	commit(t, dir, "a.txt", "one")
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".git", "HEAD"),
		[]byte("garbage-not-a-ref\n"), 0o644))

	_, err := Head(dir)
	require.Error(t, err, "a repository that cannot be read is not a repository that is absent")
	assert.NotErrorIs(t, err, ErrNotARepository,
		"reported as ErrNotARepository, the caller silently abandons the baseline it had")
}

func TestContains_ReachableAndUnreachable(t *testing.T) {
	// What tells a history the tree is still on from one it has left, in the
	// two cases that matter.
	dir := initRepo(t)
	first := commit(t, dir, "a.txt", "one")
	commit(t, dir, "b.txt", "two")

	got, err := Contains(dir, first)
	require.NoError(t, err)
	assert.True(t, got, "an ancestor of HEAD is reachable")

	// Another line, off the root, that does not contain the tip of main.
	tip := git(t, dir, "rev-parse", "HEAD")
	git(t, dir, "checkout", "-b", "other", first)
	commit(t, dir, "z.txt", "elsewhere")

	got, err = Contains(dir, tip)
	require.NoError(t, err)
	assert.False(t, got, "a commit on the line that was left is not reachable")
}

func TestContains_AnAbsentCommitIsNotReachable(t *testing.T) {
	// A commit git does not have is a real state — a rebase dropped it, a reset
	// discarded it — and it is one where the recorded point is no longer a
	// place this tree can measure from. Not an error to refuse over.
	dir := initRepo(t)
	commit(t, dir, "a.txt", "one")

	got, err := Contains(dir, "0123456789012345678901234567890123456789")
	require.NoError(t, err)
	assert.False(t, got)
}

func TestContains_ABrokenRepositoryIsReportedRatherThanCalledUnreachable(t *testing.T) {
	// The same shape as F4, one level down. "Not reachable" would be a claim
	// about a history nothing managed to read, and a caller acting on it would
	// move its point on the strength of a failure.
	dir := initRepo(t)
	c := commit(t, dir, "a.txt", "one")
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".git", "HEAD"),
		[]byte("garbage-not-a-ref\n"), 0o644))

	_, err := Contains(dir, c)
	assert.Error(t, err, "a repository that cannot be read must not answer the question")
}

func TestHead_RebaseInALinkedWorktreeFindsItsOwnOperation(t *testing.T) {
	// A linked worktree keeps its operation files under .git/worktrees/<name>/
	// rather than in .git/, so joining ".git" onto the directory would look in
	// the wrong place and report a bare detached HEAD. The location comes from
	// `git rev-parse --git-path`, which resolves it per worktree.
	dir := initRepo(t)
	for _, n := range []string{"a", "b", "c", "d"} {
		commit(t, dir, n+".txt", n)
	}
	linked := filepath.Join(t.TempDir(), "linked")
	git(t, dir, "worktree", "add", "-b", "wtbranch", linked)

	rebase := exec.Command("git", "rebase", "-i", "HEAD~3")
	rebase.Dir = linked
	rebase.Env = append(os.Environ(),
		"GIT_SEQUENCE_EDITOR=sed -i.bak s/^pick/edit/",
		"GIT_EDITOR=true")
	out, err := rebase.CombinedOutput()
	require.NoErrorf(t, err, "git rebase -i: %s", out)
	t.Cleanup(func() {
		abort := exec.Command("git", "rebase", "--abort")
		abort.Dir = linked
		_ = abort.Run()
	})

	pos, err := Head(linked)
	require.NoError(t, err)
	assert.Equal(t, "wtbranch", pos.Branch,
		"the operation belongs to this worktree, and is where this worktree keeps it")
}

func TestHead_ADanglingLinkedWorktreeIsNotARepository(t *testing.T) {
	// F7. A linked worktree whose parent has been deleted is an everyday thing
	// to find on disk — someone removed the checkout the worktree hung off. git
	// says "not a git repository" about it, and so must this: judging the .git
	// NAME alone reported a fault, so ensureBaseline errored on a normal state
	// instead of quietly recording no baseline.
	parent := initRepo(t)
	commit(t, parent, "a.txt", "one")
	linked := filepath.Join(t.TempDir(), "linked")
	git(t, parent, "worktree", "add", "-b", "side", linked)

	// The .git file survives; what it points at does not.
	require.NoError(t, os.RemoveAll(parent))

	_, err := Head(linked)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotARepository,
		"a .git file pointing at a gitdir that is gone is an absence, the same way git reads it")
}

func TestHead_AnEmptyGitDirectoryIsNotARepository(t *testing.T) {
	// The other half of F7, and what a half-finished copy leaves behind. The
	// name is there and nothing else is, which is not a repository git can find
	// and not one this package may call broken.
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, ".git"), 0o755))
	// So the walk cannot climb out of the temporary directory into a real
	// repository above it and answer about that one instead.
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))

	_, err := Head(dir)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotARepository)
}

func TestHead_AGitFilePointingNowhereIsNotARepository(t *testing.T) {
	// A submodule's .git file left behind after the submodule was removed. Same
	// shape as the dangling worktree, reached a different way.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".git"),
		[]byte("gitdir: "+filepath.Join(dir, "nowhere")+"\n"), 0o644))
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))

	_, err := Head(dir)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotARepository)
}

func TestHead_ACorruptGitDirIsStillAFaultNotAnAbsence(t *testing.T) {
	// The line F7's fix must not cross, and the case hasGitDir exists for.
	//
	// A gitdir carrying HEAD, objects and refs IS a repository — a garbage HEAD
	// makes it a broken one, and git says "not a git repository" about that too.
	// Following the .git name to what it points at must check that the gitdir is
	// THERE, never that its contents are healthy: read as an absence, the
	// session silently stops measuring and says nothing about why.
	dir := initRepo(t)
	commit(t, dir, "a.txt", "one")
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".git", "HEAD"),
		[]byte("garbage-not-a-ref\n"), 0o644))

	_, err := Head(dir)
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNotARepository,
		"a repository that cannot be read is a fault to report, not an absence to skip past")
}

func TestIsGitDirAt_TheThreeWaysAGitNameFailsToBeAGitDir(t *testing.T) {
	// isGitDirAt's three remaining directions, none of which had a test — every
	// one of them survived a mutation flipping it. They are reachable from an
	// ordinary tree, and each is a case the function's own doc comment names, so
	// the absence was coverage rather than unreachability.
	//
	// The direction matters for the same reason as everywhere else in hasGitDir:
	// answering "present" makes an absence look like a fault (noise on stderr
	// about a directory with no repository in it), and answering "absent" makes
	// a fault look like an absence (the session stops measuring and says
	// nothing). The first two below must be absent and the third present, and
	// they are asserted together so a function that always answers one way
	// cannot satisfy them.
	base := t.TempDir()

	t.Run("a gitdir line naming nothing is absent", func(t *testing.T) {
		// What a half-finished copy or an interrupted clone leaves behind. The
		// name is there, the pointer is empty, and there is no repository.
		path := filepath.Join(base, "empty-pointer", ".git")
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte("gitdir:\n"), 0o644))

		assert.False(t, isGitDirAt(path),
			"a .git file whose gitdir: line is empty points at no repository")
	})

	t.Run("a gitdir pointing at a non-directory is absent", func(t *testing.T) {
		// The pointer resolves, but not to a gitdir. A gitdir is a directory;
		// anything else at the far end is not a repository however real it is.
		target := filepath.Join(base, "not-a-directory")
		require.NoError(t, os.WriteFile(target, []byte("x"), 0o644))
		path := filepath.Join(base, "points-at-a-file", ".git")
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte("gitdir: "+target+"\n"), 0o644))

		assert.False(t, isGitDirAt(path),
			"a .git file resolving to a regular file names no gitdir")
	})

	t.Run("a .git file that cannot be read is present", func(t *testing.T) {
		// The other direction, and the one that must NOT fold into absence: a
		// .git file we were refused permission to read is a repository we could
		// not look at. Called absent, it becomes ErrNotARepository and the
		// session quietly stops measuring.
		if os.Geteuid() == 0 {
			t.Skip("root reads regardless of the permission bits this depends on")
		}
		path := filepath.Join(base, "unreadable", ".git")
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte("gitdir: /somewhere\n"), 0o000))
		t.Cleanup(func() { _ = os.Chmod(path, 0o644) })

		assert.True(t, isGitDirAt(path),
			"a .git file that cannot be read is unestablished, and unestablished errs towards present")
	})
}

func TestIsGitDirContents_AnUnreadableGitDirErrsTowardsPresent(t *testing.T) {
	// The third way a gitdir can fail to answer, and the only one with no test
	// on it: not absent, not corrupt, but unreadable. isGitDirContents chooses
	// "present" for both of its unreadable branches — the Stat of the directory
	// and the Stat of each marker inside it — and nothing pinned that direction,
	// so a mutation flipping either to false survived the suite.
	//
	// The direction is the whole point of the function. hasGitDir exists to tell
	// "there is no repository" from "there is one and it is broken", and only
	// the first is an ordinary state the engine carries on past. Answering false
	// for a repository it merely could not read collapses the fault into the
	// absence: run turns it into ErrNotARepository, the caller records no
	// baseline, and the session measures nothing while printing nothing — the
	// same silent stop TestHead_ACorruptGitDirIsStillAFaultNotAnAbsence forbids,
	// reached by permissions instead of by a garbage HEAD.
	//
	// Both branches are exercised, because they fail at different depths and a
	// test on one leaves the other free to flip.
	if os.Geteuid() == 0 {
		t.Skip("root reads regardless of the permission bits this depends on")
	}

	t.Run("the markers inside cannot be stat'd", func(t *testing.T) {
		// The gitdir resolves and is a directory; only the Stat of HEAD,
		// objects and refs fails. This is the marker loop's unreadable arm.
		gitdir := filepath.Join(t.TempDir(), "gitdir")
		require.NoError(t, os.MkdirAll(filepath.Join(gitdir, "objects"), 0o755))
		require.NoError(t, os.MkdirAll(filepath.Join(gitdir, "refs"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(gitdir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644))
		require.True(t, isGitDirContents(gitdir), "a healthy gitdir must read as present, or the case below proves nothing")

		require.NoError(t, os.Chmod(gitdir, 0o000))
		t.Cleanup(func() { _ = os.Chmod(gitdir, 0o755) })

		assert.True(t, isGitDirContents(gitdir),
			"a gitdir whose markers cannot be read is a repository we could not look at, not one that is absent")
	})

	t.Run("the gitdir itself cannot be stat'd", func(t *testing.T) {
		// The parent is unreadable, so the Stat of the gitdir fails with EACCES
		// rather than ENOENT. This is the `!os.IsNotExist(err)` arm.
		parent := filepath.Join(t.TempDir(), "parent")
		gitdir := filepath.Join(parent, "gitdir")
		require.NoError(t, os.MkdirAll(filepath.Join(gitdir, "objects"), 0o755))
		require.NoError(t, os.MkdirAll(filepath.Join(gitdir, "refs"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(gitdir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644))

		require.NoError(t, os.Chmod(parent, 0o000))
		t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })

		assert.True(t, isGitDirContents(gitdir),
			"a gitdir behind an unreadable parent is unestablished, and unestablished errs towards present")
	})

	t.Run("a gitdir that is genuinely not there is absent", func(t *testing.T) {
		// The other direction, so the assertions above cannot be satisfied by a
		// function that simply always answers true.
		assert.False(t, isGitDirContents(filepath.Join(t.TempDir(), "nowhere")),
			"ENOENT is a fact about the tree and must stay distinguishable from a failure to look")
	})
}

func TestHead_ALiveLinkedWorktreeIsStillARepository(t *testing.T) {
	// The fix follows a .git FILE to its target, so the ordinary linked worktree
	// — where the target is very much there — must keep working.
	parent := initRepo(t)
	want := commit(t, parent, "a.txt", "one")
	linked := filepath.Join(t.TempDir(), "linked")
	git(t, parent, "worktree", "add", "--detach", linked, "HEAD")

	pos, err := Head(linked)
	require.NoError(t, err)
	assert.Equal(t, want, pos.Commit)
}
