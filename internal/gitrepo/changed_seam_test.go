package gitrepo

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The seam this file attacks is the union of two git commands that answer
// different questions in different vocabularies: `git diff --name-status -z -M
// <commit>`, which names paths from the repository ROOT and covers everything
// git tracks, and `git ls-files -z --others --exclude-standard --full-name`,
// which names untracked paths and — without that last flag — names them from
// the CURRENT DIRECTORY.
//
// Every test here builds a real repository and drives real git. A stub would
// only prove this package agrees with a second copy of its own assumptions
// about git's output, and the whole risk in this code is that those assumptions
// are wrong.
//
// Where a test asserts what git itself emitted, it says so with a `require` on
// the raw status BEFORE asserting on Changed's answer. Otherwise a change in
// git's behaviour turns the test into one that passes while measuring something
// else — the failure mode these tests exist to prevent, arriving in the tests.

// changedErr is changedMap's counterpart for the cases whose whole point is
// what got reported. changedMap requires NoError, so it cannot be used to ask
// what a broken repository does.
func changedErr(t *testing.T, dir, commit string) ([]Change, error) {
	t.Helper()
	return Changed(dir, commit)
}

// rawDiff is what git actually emitted, NUL-separated, for the tests that need
// to prove which status letter arrived before asserting on its handling.
func rawDiff(t *testing.T, dir, commit string) []string {
	t.Helper()
	cmd := exec.Command("git", "diff", "--name-status", "-z", "-M", commit)
	cmd.Dir = dir
	out, err := cmd.Output()
	require.NoError(t, err)
	var fields []string
	for _, f := range strings.Split(string(out), "\x00") {
		if f != "" {
			fields = append(fields, f)
		}
	}
	return fields
}

// --- the two commands overlapping on one path -------------------------------

// TestChanged_APathBothCommandsNameIsReportedOnceWithTheDiffsAnswer is the
// seam's defining case: the one path both questions answer about.
//
// A tracked file removed from the index and then written back to disk is
// reported by the diff as a DELETION (it is gone from the index, so the tree no
// longer tracks it) and by the untracked listing as a NEW file (nothing tracks
// it, and there it is on disk). Two answers, one path, and they disagree about
// the fact that matters — whether it was at the baseline.
//
// The diff's answer has to win, because it is the only one of the two that
// knows anything about the baseline commit; the untracked listing's `false` is
// not a finding but the absence of one. Folded the other way, a file that was
// at the baseline classifies as a create, and every rule bound to updates or
// deletions is silent about it.
//
// Both halves are pinned: that git really does name the path in both commands
// (or the test is about something else), and that Changed reports it once.
func TestChanged_APathBothCommandsNameIsReportedOnceWithTheDiffsAnswer(t *testing.T) {
	dir := initRepo(t)
	write(t, dir, "both.md", "at the baseline")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "base")
	base := git(t, dir, "rev-parse", "HEAD")

	// Untrack it, then put it back on disk. Now both commands see it.
	git(t, dir, "rm", "--cached", "-q", "both.md")
	write(t, dir, "both.md", "rewritten on disk")

	// The premise, proven rather than assumed.
	tracked, err := diffAgainst(dir, base)
	require.NoError(t, err)
	require.Contains(t, tracked, "both.md",
		"the diff no longer names this path, so the overlap this test is about does not exist")
	require.True(t, tracked["both.md"],
		"the diff must report it as having been at the baseline, or there is no disagreement to resolve")

	untracked, err := untrackedPaths(dir)
	require.NoError(t, err)
	require.Contains(t, untracked, "both.md",
		"the untracked listing no longer names this path, so the overlap does not exist")

	// The union reports it once, and with the diff's answer.
	got := changedMap(t, dir, base) // changedMap itself fails on a duplicate path
	require.Contains(t, got, "both.md")
	assert.Truef(t, got["both.md"],
		"the untracked listing's `false` overwrote the diff's `true`: a file that WAS at the "+
			"baseline now classifies as a create, and every update and delete rule goes silent")
}

// TestChanged_StagedThenDeletedFromDiskEndsTheCycleAsADeletion.
//
// A file that exists at the baseline, is staged with new content, and is then
// removed from disk before the cycle ends. The index says one thing, the tree
// says another, and what this reports is about the TREE: the path was at the
// baseline and is not there now, which is a deletion.
//
// Distinct from TestChanged_StagedThenDeletedIsReportedAsNoChange, which is
// about a file CREATED during the cycle — that one nets out to nothing because
// the baseline never had it. Here the baseline did, so the difference is real
// and must survive the staging.
func TestChanged_StagedThenDeletedFromDiskEndsTheCycleAsADeletion(t *testing.T) {
	dir := initRepo(t)
	write(t, dir, "doomed.md", "at the baseline")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "base")
	base := git(t, dir, "rev-parse", "HEAD")

	write(t, dir, "doomed.md", "staged content")
	git(t, dir, "add", "doomed.md")
	require.NoError(t, os.Remove(filepath.Join(dir, "doomed.md")))

	got := changedMap(t, dir, base)

	require.Contains(t, got, "doomed.md",
		"a file that was at the baseline and is now gone is a difference, whatever the index holds")
	assert.True(t, got["doomed.md"],
		"it was at the baseline — false here dispatches a create for a file that is not on disk")
}

// --- renames, chains, copies ------------------------------------------------

// TestChanged_ARenameOntoAnExistingFileIsAnUpdateAndADeletion.
//
// `git mv -f src dst` over an existing dst is NOT reported as R. Git emits `M
// dst` and `D src`, because dst was already in the baseline — it was
// overwritten, not created.
//
// That distinction is the whole test. Reported as a rename, dst would carry
// ExistedAtBaseline=false and classify as a CREATE, when in fact a file that
// was there was overwritten — so a rule bound to updates never sees the
// overwrite of a file the project already had.
//
// The status letters are asserted first, because this test's premise is a claim
// about what git emits.
func TestChanged_ARenameOntoAnExistingFileIsAnUpdateAndADeletion(t *testing.T) {
	dir := initRepo(t)
	// Long and distinctive so rename detection would fire if git chose to.
	write(t, dir, "src.md", "a body long enough and distinctive enough for rename detection to consider it\n")
	write(t, dir, "dst.md", "an entirely different body that was already here at the baseline\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "base")
	base := git(t, dir, "rev-parse", "HEAD")

	git(t, dir, "mv", "-f", "src.md", "dst.md")

	fields := rawDiff(t, dir, base)
	require.Equalf(t, []string{"M", "dst.md", "D", "src.md"}, fields,
		"git no longer reports an overwriting move as M+D, so this test is measuring something else")

	got := changedMap(t, dir, base)

	assert.Truef(t, got["dst.md"],
		"the overwritten destination WAS at the baseline; false here makes it a create and "+
			"no update rule ever sees a file being clobbered")
	assert.True(t, got["src.md"], "the source was at the baseline and is gone")
	assert.Len(t, got, 2)
}

// TestChanged_ARenameChainAcrossCommitsIsItsEndpoints.
//
// a -> b in one commit, b -> c in the next. Measured from before either, the
// intermediate name never existed at the baseline and does not exist now, so it
// is not a difference and must not be reported: an event for `b.md` would name
// a file the project never had at either end of the cycle.
//
// Git collapses the chain to a single R100 a -> c, which is the answer to
// assert against.
func TestChanged_ARenameChainAcrossCommitsIsItsEndpoints(t *testing.T) {
	dir := initRepo(t)
	write(t, dir, "a.md", "content stable across the whole chain so detection collapses it\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "base")
	base := git(t, dir, "rev-parse", "HEAD")

	git(t, dir, "mv", "a.md", "b.md")
	git(t, dir, "commit", "-m", "first move")
	git(t, dir, "mv", "b.md", "c.md")
	git(t, dir, "commit", "-m", "second move")

	fields := rawDiff(t, dir, base)
	require.Equalf(t, []string{"R100", "a.md", "c.md"}, fields,
		"git no longer collapses the chain to one rename, so the assertions below are about a different shape")

	got := changedMap(t, dir, base)

	assert.True(t, got["a.md"], "the chain's origin was at the baseline and is gone")
	assert.False(t, got["c.md"], "the chain's destination was not at the baseline")
	assert.NotContainsf(t, got, "b.md",
		"the intermediate name existed at neither end of the cycle — reporting it hands a rule "+
			"a file the project never had")
	assert.Len(t, got, 2)
}

// TestChanged_ACopyReportsOnlyItsDestination, against real git rather than a
// hand-written status.
//
// The C status needs --find-copies-harder to appear at all, which Changed does
// NOT pass — so this drives parseNameStatus through git's real C output
// obtained separately, and then pins what Changed itself does with the same
// tree. Both are worth having: the parser must handle C because a future flag
// or a git default could produce it, and today's answer must be right too.
//
// Either way the source is untouched and must stay silent. Naming it would
// report a file that did not change, which is the flood untouched_stays_silent
// exists to prevent.
func TestChanged_ACopyReportsOnlyItsDestination(t *testing.T) {
	dir := initRepo(t)
	write(t, dir, "orig.md", "a body long enough to be recognised when it appears twice over\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "base")
	base := git(t, dir, "rev-parse", "HEAD")

	body, err := os.ReadFile(filepath.Join(dir, "orig.md"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "dup.md"), body, 0o644))
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "copy")

	// git's own C output, which needs a flag Changed does not pass.
	cmd := exec.Command("git", "diff", "--name-status", "-z", "-C", "--find-copies-harder", base)
	cmd.Dir = dir
	out, cerr := cmd.Output()
	require.NoError(t, cerr)
	require.Truef(t, strings.HasPrefix(string(out), "C"),
		"git did not report this as a copy even when asked, so the C arm is not being exercised: %q", out)

	fromCopy, err := parseNameStatus(string(out))
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{"dup.md": false}, fromCopy,
		"a copy is a new destination and an untouched source; naming the source reports a file that did not change")

	// And what Changed actually answers for the same tree, which is an A.
	got := changedMap(t, dir, base)
	assert.Equal(t, map[string]bool{"dup.md": false}, got,
		"however git spells it, the copy's destination is the only difference")
}

// --- statuses the diff can carry --------------------------------------------

// TestChanged_AConflictedFileIsReportedAsAnUpdateNotAsU pins what the U arm's
// own doc comment claims: that `git diff --name-status <commit>` reports a
// conflicted file as M and never as U.
//
// The claim is load-bearing — it is why the U arm is documented as unreachable
// from Changed — and it is a claim about git rather than about this code, so it
// is worth a real merge conflict rather than a comment.
//
// A conflicted file must be reported. It is a file the cycle changed, sitting
// in the tree with conflict markers in it, and dropping it means no rule ever
// looks at a half-merged file.
func TestChanged_AConflictedFileIsReportedAsAnUpdateNotAsU(t *testing.T) {
	dir := initRepo(t)
	write(t, dir, "shared.md", "the common ancestor\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "base")
	base := git(t, dir, "rev-parse", "HEAD")

	git(t, dir, "checkout", "-q", "-b", "other")
	write(t, dir, "shared.md", "the other side's take\n")
	git(t, dir, "commit", "-qam", "other")

	git(t, dir, "checkout", "-q", "main")
	write(t, dir, "shared.md", "main's own take\n")
	git(t, dir, "commit", "-qam", "main")

	// Expected to fail: that is what leaves the conflict in the tree.
	cmd := exec.Command("git", "merge", "other")
	cmd.Dir = dir
	_ = cmd.Run()
	require.FileExists(t, filepath.Join(dir, ".git", "MERGE_HEAD"),
		"the merge did not leave a conflict in progress, so this test is not about a conflicted tree")

	fields := rawDiff(t, dir, base)
	require.Equalf(t, []string{"M", "shared.md"}, fields,
		"the U arm's doc claims a conflicted file arrives as M from this command; git now says otherwise")

	got := changedMap(t, dir, base)
	assert.True(t, got["shared.md"],
		"a conflicted file was at the baseline and is in the tree — dropping it means "+
			"nothing ever judges a half-merged file")
}

// TestParseNameStatus_UnmergedIsAnUpdate reaches the U arm directly, since
// Changed's own command cannot produce it. The arm is kept precisely because
// the alternative for an unknown status is to drop the path, and a conflicted
// file dropped is a file changed and never judged.
func TestParseNameStatus_UnmergedIsAnUpdate(t *testing.T) {
	got, err := parseNameStatus("U\x00conflicted.md\x00")
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{"conflicted.md": true}, got,
		"a conflicted path is present on both sides — an update, not a create and not a delete")
}

// TestChanged_ATrackedFileThatGitignoreAlsoNamesIsStillReported.
//
// --exclude-standard filters the UNTRACKED listing and nothing else. A file git
// already tracks stays tracked whatever .gitignore says about it, so a change to
// it is a change to the project's work and must be reported.
//
// The failure this guards is the plausible over-reach: applying the ignore
// rules to the diff as well, which would silence real edits to files that
// happen to match an ignore pattern — a config file added with `add -f` and
// then edited is the ordinary shape of it.
func TestChanged_ATrackedFileThatGitignoreAlsoNamesIsStillReported(t *testing.T) {
	dir := initRepo(t)
	write(t, dir, ".gitignore", "tracked-anyway.md\nbuild/\n")
	write(t, dir, "tracked-anyway.md", "v1")
	// -f is what tracks a file the ignore rules name.
	git(t, dir, "add", "-f", ".gitignore", "tracked-anyway.md")
	git(t, dir, "commit", "-m", "base")
	base := git(t, dir, "rev-parse", "HEAD")

	write(t, dir, "tracked-anyway.md", "v2") // an edit to a tracked-but-ignored file
	write(t, dir, "build/output.o", "junk")  // untracked AND ignored — must stay silent

	got := changedMap(t, dir, base)

	assert.Truef(t, got["tracked-anyway.md"],
		"git tracks this file, so an edit to it is the project's work — the ignore rules filter "+
			"the untracked listing, not the diff")
	assert.NotContains(t, got, "build/output.o",
		"an untracked file the project ignores is not the cycle's work")
}

// --- submodules -------------------------------------------------------------

// TestChanged_ADirtySubmoduleReportsTheGitlinkAsAnUpdate.
//
// This contradicts Changed's own doc comment, which states that "changing a
// file INSIDE the submodule reports nothing, because the parent repository's
// diff only notices a submodule when the commit it points at moves".
//
// Git does not behave that way. A submodule whose worktree is merely DIRTY —
// contents edited, nothing committed, the gitlink pointing exactly where it did
// — is reported by the parent's diff as `M <gitlink>`. The commit it points at
// has not moved, and git reports it anyway, because the recorded gitlink and
// the submodule's actual HEAD-plus-dirt no longer agree.
//
// The existing TestChanged_SubmoduleIsAGitlinkNotItsContents does not catch
// this: it dirties the submodule while the gitlink is still an uncommitted `A`,
// and `A` masks the difference — the entry reads `A vendor/sub` before and
// after, so its `assert.Equal(got, after)` holds for a reason that has nothing
// to do with the claim. Here the gitlink is COMMITTED first, which is the only
// arrangement in which the M can appear.
//
// The consequence downstream is the point. The gitlink is handed to file rules
// as a path, and on disk it is a DIRECTORY — so filemod classifies it
// presentNotAFile and reports ErrPathIsNotAFile. Every `git add` inside a
// submodule therefore produces a path that is guaranteed to fail
// classification, which is worth knowing whether or not it is worth changing.
func TestChanged_ADirtySubmoduleReportsTheGitlinkAsAnUpdate(t *testing.T) {
	inner := initRepo(t)
	write(t, inner, "lib.md", "v1")
	git(t, inner, "add", ".")
	git(t, inner, "commit", "-m", "init")

	dir := initRepo(t)
	write(t, dir, "seed.md", "seed")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "seed")

	// file:// between local repositories is refused by default in modern git.
	git(t, dir, "-c", "protocol.file.allow=always", "submodule", "add", inner, "vendor/sub")
	// COMMITTED, unlike the existing test — an uncommitted gitlink reads as A
	// and hides every subsequent difference behind it.
	git(t, dir, "commit", "-m", "add submodule")
	base := git(t, dir, "rev-parse", "HEAD")

	// Nothing has changed yet.
	require.Empty(t, changedMap(t, dir, base),
		"the baseline must be a clean tree, or the M below cannot be attributed to the dirtying")

	// Dirty the submodule's WORKTREE only. No commit, so the gitlink still
	// points exactly where the parent recorded it.
	write(t, filepath.Join(dir, "vendor", "sub"), "lib.md", "v2")

	fields := rawDiff(t, dir, base)
	require.Equalf(t, []string{"M", "vendor/sub"}, fields,
		"git no longer reports a merely-dirty submodule, so the doc comment would now be right")

	got := changedMap(t, dir, base)

	assert.Truef(t, got["vendor/sub"],
		"the gitlink was at the baseline and git reports it modified — this is the case the "+
			"doc comment says produces nothing")
	assert.NotContains(t, got, "vendor/sub/lib.md",
		"the file inside the submodule is still not this repository's to report")

	// The consequence, stated as a fact about the tree rather than left implied:
	// the reported path is a directory, so no file event can honestly be about it.
	info, err := os.Lstat(filepath.Join(dir, "vendor", "sub"))
	require.NoError(t, err)
	assert.Truef(t, info.IsDir(),
		"the gitlink resolves to a directory on disk, so filemod classifies it presentNotAFile "+
			"and reports ErrPathIsNotAFile for every dirty submodule")
}

// TestChanged_AnUntrackedFileInsideASubmoduleIsNotThisRepositorysToReport.
//
// The untracked listing stops at the submodule boundary: `ls-files --others` in
// the parent does not descend into another repository's worktree. Worth pinning
// separately from the diff half, because it is the other command in the union
// and it is the one that would flood — a submodule with a build directory in it
// would otherwise put every file it holds in front of every guardrail.
func TestChanged_AnUntrackedFileInsideASubmoduleIsNotThisRepositorysToReport(t *testing.T) {
	inner := initRepo(t)
	write(t, inner, "lib.md", "v1")
	git(t, inner, "add", ".")
	git(t, inner, "commit", "-m", "init")

	dir := initRepo(t)
	write(t, dir, "seed.md", "seed")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "seed")
	git(t, dir, "-c", "protocol.file.allow=always", "submodule", "add", inner, "vendor/sub")
	git(t, dir, "commit", "-m", "add submodule")
	base := git(t, dir, "rev-parse", "HEAD")

	write(t, filepath.Join(dir, "vendor", "sub"), "brand-new.md", "written inside the submodule")

	untracked, err := untrackedPaths(dir)
	require.NoError(t, err)
	assert.NotContains(t, untracked, "vendor/sub/brand-new.md",
		"the untracked listing must not descend into another repository's worktree")

	got := changedMap(t, dir, base)
	assert.NotContains(t, got, "vendor/sub/brand-new.md")
}

// --- repository states ------------------------------------------------------

// TestChanged_ARepositoryWithNoCommitsReportsItsUntrackedFiles.
//
// A freshly initialised repository has no commit to measure from, so Changed is
// called with an empty baseline and answers nothing at all — which is the
// documented contract and is right: there is no point to compare against.
//
// Asserted alongside what the untracked half WOULD have said, so the silence is
// shown to be the empty-baseline decision rather than the untracked question
// happening to fail in an empty repository.
func TestChanged_ARepositoryWithNoCommitsReportsItsUntrackedFiles(t *testing.T) {
	dir := initRepo(t) // initialised, never committed
	write(t, dir, "scratch.md", "written before any commit exists")

	// The untracked question answers fine here — the silence below is a choice,
	// not a failure.
	untracked, err := untrackedPaths(dir)
	require.NoError(t, err)
	require.Contains(t, untracked, "scratch.md")

	changes, err := Changed(dir, "")
	require.NoError(t, err, "a repository with no commit yet is an ordinary state, not an error")
	assert.Nil(t, changes, "with no baseline there is nothing to measure a difference from")
}

// TestChanged_ABaselineCommitLeftBehindByAResetIsStillMeasurable.
//
// A commit that is no longer reachable from HEAD — a reset dropped it, a rebase
// replaced it — is still an OBJECT in the repository until it is collected, and
// `git diff` against it works exactly as before. So the difference is still
// measurable, and it is measured in the direction that matters: a file that the
// dropped commit had and the tree no longer does is a DELETION.
//
// This is the state a session is in after the user rebases mid-cycle, and it is
// the one where getting the direction backwards is invisible — every file the
// abandoned line of history added would classify as a create.
func TestChanged_ABaselineCommitLeftBehindByAResetIsStillMeasurable(t *testing.T) {
	dir := initRepo(t)
	write(t, dir, "kept.md", "one")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "first")

	write(t, dir, "only-on-the-dropped-commit.md", "two")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "second")
	dropped := git(t, dir, "rev-parse", "HEAD")

	// Abandon it. The object survives; nothing reaches it.
	git(t, dir, "reset", "-q", "--hard", "HEAD~1")
	require.NoFileExists(t, filepath.Join(dir, "only-on-the-dropped-commit.md"))

	got := changedMap(t, dir, dropped)

	assert.Truef(t, got["only-on-the-dropped-commit.md"],
		"the file was on the baseline commit and is gone from the tree — a deletion; "+
			"false here reports a create for a file that is not there")
	assert.Len(t, got, 1)
}

// TestChanged_ABaselineCommitTheRepositoryDoesNotHaveIsReportedNotSilent.
//
// An object git has never heard of makes `git diff` fail outright. That must
// surface as an error: turned into "no changes", a session measuring from a
// commit that is not in this repository would report a clean tree forever and
// every guardrail would look satisfied.
func TestChanged_ABaselineCommitTheRepositoryDoesNotHaveIsReportedNotSilent(t *testing.T) {
	dir := initRepo(t)
	write(t, dir, "a.md", "one")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "base")
	write(t, dir, "b.md", "written this cycle")

	changes, err := changedErr(t, dir, strings.Repeat("0", 40))

	require.Errorf(t, err,
		"a baseline this repository does not have must be reported; silence here makes every "+
			"cycle look clean forever")
	assert.Nil(t, changes, "nothing was measured, so nothing is claimed")
}

// TestChanged_ABareRepositoryIsReportedRatherThanCalledUnchanged.
//
// A bare repository has no working tree, so neither command can run. Both fail,
// and the failure must reach the caller: reported as no changes, a misconfigured
// path pointing at a bare repository would silently disable every file rule.
func TestChanged_ABareRepositoryIsReportedRatherThanCalledUnchanged(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q", "--bare", ".")

	changes, err := changedErr(t, dir, "HEAD")

	require.Error(t, err, "a bare repository cannot answer either question, and saying nothing "+
		"would make it indistinguishable from a clean tree")
	assert.Nil(t, changes)
}

// TestUntrackedPaths_AFailureIsReturnedRatherThanReadAsAnEmptyListing.
//
// The untracked half's error must reach Changed's caller, and this pins it at
// the function itself rather than through Changed.
//
// Asked through Changed it cannot be pinned at all: every state that breaks
// `ls-files` — a bare repository, a missing directory, a corrupt index —
// breaks `git diff` too, and the diff runs FIRST, so Changed returns on the
// diff's error and the untracked half is never reached. Dropping `if err !=
// nil { return nil, err }` from untrackedPaths therefore survives every test
// that goes through Changed, which is exactly what the mutation run showed.
//
// It still has to be right. An error read as an empty listing is the silence
// this package exists to prevent, arriving through the half that reports work
// the diff cannot see: every never-staged file the agent wrote would vanish
// from the cycle while the answer still looked complete. So the direction is
// asserted where it is observable, on the function that owns it.
//
// A bare repository is the state used because it is a genuine "this command
// cannot answer here" rather than a corruption — no working tree exists, so
// there is nothing for `--others` to list, and git says so on stderr.
func TestUntrackedPaths_AFailureIsReturnedRatherThanReadAsAnEmptyListing(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q", "--bare", ".")

	paths, err := untrackedPaths(dir)

	require.Errorf(t, err,
		"a command that could not run must not be read as a listing with nothing in it — "+
			"every never-staged file the cycle wrote would vanish and the answer would still look complete")
	assert.Nil(t, paths, "nothing was listed, so nothing is claimed")
}

// TestChanged_InALinkedWorktreeMeasuresThatWorktree.
//
// A linked worktree is a second checkout sharing one object store, and its .git
// is a FILE rather than a directory. Both commands must answer about the
// worktree they are run in rather than about the main checkout, and Root() must
// name that worktree — a consumer joining paths onto the main checkout's root
// would stat files that are not there and dispatch deletions for all of them.
func TestChanged_InALinkedWorktreeMeasuresThatWorktree(t *testing.T) {
	dir := initRepo(t)
	write(t, dir, "shared.md", "one")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "base")
	base := git(t, dir, "rev-parse", "HEAD")

	linked := filepath.Join(t.TempDir(), "linked")
	git(t, dir, "worktree", "add", "-q", "-b", "feature", linked)

	// The premise: it really is a linked worktree, whose .git is a file.
	info, err := os.Lstat(filepath.Join(linked, ".git"))
	require.NoError(t, err)
	require.Truef(t, info.Mode().IsRegular(),
		"a linked worktree's .git is a file; this one is not, so the test is about an ordinary checkout")

	write(t, linked, "shared.md", "changed in the worktree")
	write(t, linked, "sub/fresh.md", "new in the worktree")

	got := changedMap(t, linked, base)

	assert.Equal(t, map[string]bool{
		"shared.md":    true,
		"sub/fresh.md": false,
	}, got)

	// Root names the worktree, and every path resolves against it.
	root, err := Root(linked)
	require.NoError(t, err)
	for p := range got {
		_, err := os.Stat(filepath.Join(root, p))
		assert.NoErrorf(t, err, "%q does not resolve against the worktree's own root", p)
	}
}

// TestChanged_OnADetachedHeadMeasuresFromTheGivenCommit.
//
// Detaching HEAD changes which commit the tree sits on and nothing about how a
// difference is measured: the baseline is an argument, not HEAD. Worth pinning
// because a detached HEAD is what a rebase and a bisect both leave, and those
// are ordinary mid-session states.
func TestChanged_OnADetachedHeadMeasuresFromTheGivenCommit(t *testing.T) {
	dir := initRepo(t)
	write(t, dir, "a.md", "one")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "base")
	base := git(t, dir, "rev-parse", "HEAD")

	git(t, dir, "checkout", "-q", "--detach", "HEAD")
	require.Equal(t, "HEAD", git(t, dir, "rev-parse", "--abbrev-ref", "HEAD"),
		"HEAD is not detached, so this test is about an attached checkout")

	write(t, dir, "a.md", "two")
	write(t, dir, "b.md", "written while detached")

	got := changedMap(t, dir, base)

	assert.Equal(t, map[string]bool{"a.md": true, "b.md": false}, got,
		"the baseline is the commit given, not wherever HEAD happens to point")
}

// --- path spellings ---------------------------------------------------------

// TestChanged_ExoticFilenamesSurviveBothCommandsIntact.
//
// A path is bytes, not a line. Git QUOTES and escapes any path holding a
// newline, a quote, a backslash or a non-ASCII byte in its default output — so a
// line-oriented reader mis-splits "a\nb.md" into two entries, and a
// quote-unaware one carries the literal quotes into the path. Under -z the
// paths come out raw, which is why both commands pass it.
//
// The newline case is the one that matters most, and it is why this asserts the
// exact set rather than each name in turn: a mis-split produces MORE entries,
// and only comparing the whole set catches that.
//
// Both halves of the union are driven, since the two commands quote
// independently: the tracked names go through the diff, the untracked ones
// through ls-files.
func TestChanged_ExoticFilenamesSurviveBothCommandsIntact(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows will not take most of these names")
	}
	dir := initRepo(t)
	write(t, dir, "seed.md", "seed")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "seed")

	// Tracked at the baseline, so these arrive through the DIFF.
	tracked := []string{
		"tracked-café.md",        // non-ASCII: quoted as octal escapes without -z
		"tracked-qu\"ote.md",     // a quote: the character git wraps paths in
		"tracked-back\\slash.md", // a backslash: the escape character itself
		"tracked-new\nline.md",   // a newline: splits the record for a line reader
	}
	for _, name := range tracked {
		write(t, dir, name, "v1")
	}
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-m", "base")
	base := git(t, dir, "rev-parse", "HEAD")

	for _, name := range tracked {
		write(t, dir, name, "v2") // modified: M through the diff
	}

	// Never staged, so these arrive through the UNTRACKED listing.
	untracked := []string{
		"untracked-café.md",
		"untracked-qu\"ote.md",
		"untracked-back\\slash.md",
		"untracked-new\nline.md",
	}
	for _, name := range untracked {
		write(t, dir, name, "fresh")
	}

	want := map[string]bool{}
	for _, name := range tracked {
		want[name] = true
	}
	for _, name := range untracked {
		want[name] = false
	}

	assert.Equal(t, want, changedMap(t, dir, base),
		"a mis-split or an unquoted read changes the SET, which is why the whole map is compared")
}

// TestChanged_QuotepathDoesNotChangeTheAnswer.
//
// core.quotepath is a user configurable that turns non-ASCII bytes into octal
// escapes in git's default output. Under -z it is inert — the paths come out raw
// either way — and that inertness is what makes -z the right flag rather than an
// unquoting step this code would otherwise have to get right.
//
// Pinned because the setting is in the user's config, not this code's: a
// repository with quotepath=true would otherwise emit "caf\303\251.md" as a
// literal path, which resolves against nothing and classifies as a deletion of a
// file that is sitting right there.
func TestChanged_QuotepathDoesNotChangeTheAnswer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the names this needs are not portable to windows")
	}
	both := func(t *testing.T, quotepath string) map[string]bool {
		t.Helper()
		dir := initRepo(t)
		git(t, dir, "config", "core.quotepath", quotepath)
		write(t, dir, "seed.md", "seed")
		write(t, dir, "tracked-café.md", "v1")
		git(t, dir, "add", "-A")
		git(t, dir, "commit", "-m", "base")
		base := git(t, dir, "rev-parse", "HEAD")

		write(t, dir, "tracked-café.md", "v2")
		write(t, dir, "untracked-naïve.md", "fresh")
		return changedMap(t, dir, base)
	}

	want := map[string]bool{
		"tracked-café.md":    true,
		"untracked-naïve.md": false,
	}
	assert.Equal(t, want, both(t, "true"), "quotepath=true must not leak octal escapes into the paths")
	assert.Equal(t, want, both(t, "false"))
}

// TestChanged_FromACwdThatIsASymlinkToTheRepository.
//
// Reaching the repository through a symlink is ordinary — /tmp is a link to
// /private/tmp on darwin, which every test here runs under. Git resolves it, so
// both commands answer about the real repository and the paths are
// repository-relative as always.
//
// The assertion that carries the weight is the resolution one: every reported
// path must stat against Root(). That is what a consumer does, and it is where a
// path in the wrong convention turns into a phantom deletion.
func TestChanged_FromACwdThatIsASymlinkToTheRepository(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need a privilege this test cannot assume on windows")
	}
	dir := initRepo(t)
	write(t, dir, "top.md", "one")
	write(t, dir, "sub/inner.md", "one")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "base")
	base := git(t, dir, "rev-parse", "HEAD")

	write(t, dir, "top.md", "two")
	write(t, dir, "sub/fresh.md", "new")

	link := filepath.Join(t.TempDir(), "link-to-repo")
	require.NoError(t, os.Symlink(dir, link))

	got := changedMap(t, link, base)
	assert.Equal(t, map[string]bool{"top.md": true, "sub/fresh.md": false}, got)

	root, err := Root(link)
	require.NoError(t, err)
	for p := range got {
		_, err := os.Stat(filepath.Join(root, p))
		assert.NoErrorf(t, err, "%q does not resolve against the root reached through the link", p)
	}
}

// TestChanged_FromASubdirectoryTheUntrackedHalfIsAlsoRootRelative.
//
// The existing subdirectory test covers the union's answer. This one isolates
// the command that was actually wrong — `ls-files` without --full-name names
// paths from the CWD — so that removing the flag fails a test about that flag
// rather than only a test about the union.
//
// Both directions are pinned: the path is the root-relative one, and it is NOT
// the cwd-relative spelling. Asserting only the former would pass for an
// implementation that reported both.
func TestChanged_FromASubdirectoryTheUntrackedHalfIsAlsoRootRelative(t *testing.T) {
	dir := initRepo(t)
	write(t, dir, "seed.md", "seed")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "base")

	write(t, dir, "sub/deep/fresh.md", "written this cycle")

	untracked, err := untrackedPaths(filepath.Join(dir, "sub", "deep"))
	require.NoError(t, err)

	assert.Equal(t, []string{"sub/deep/fresh.md"}, untracked,
		"asked from a subdirectory, the untracked listing must still name paths from the repository root")
	assert.NotContains(t, untracked, "fresh.md",
		"the cwd-relative spelling resolves against nothing and classifies a present file as deleted")
}

// --- volume -----------------------------------------------------------------

// TestChanged_TenThousandChangedFilesAreAllReportedAndOrdered.
//
// Scale, and the two properties that only scale can threaten. Git's own output
// for ten thousand paths is far past any pipe buffer, so a reader that did not
// consume it whole would truncate; and the ordering contract — a cycle
// dispatching its events in the same order twice over the same tree — is
// asserted over a set big enough that map iteration order would visibly break
// it, which at three paths it would not.
//
// Half tracked and half untracked, so both commands carry a large answer and the
// union is exercised at size rather than one half of it.
func TestChanged_TenThousandChangedFilesAreAllReportedAndOrdered(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a ten-thousand-file repository")
	}
	const half = 5000

	dir := initRepo(t)
	for i := 0; i < half; i++ {
		write(t, dir, "tracked/f"+strconv.Itoa(i)+".md", "v1")
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "base")
	base := git(t, dir, "rev-parse", "HEAD")

	for i := 0; i < half; i++ {
		write(t, dir, "tracked/f"+strconv.Itoa(i)+".md", "v2")
		write(t, dir, "untracked/f"+strconv.Itoa(i)+".md", "fresh")
	}

	changes, err := Changed(dir, base)
	require.NoError(t, err)
	require.Lenf(t, changes, 2*half,
		"paths were lost at scale — a truncated read of git's output looks exactly like this")

	// Ordered, and strictly so: a duplicate would satisfy a non-strict check.
	for i := 1; i < len(changes); i++ {
		require.Truef(t, changes[i-1].Path < changes[i].Path,
			"changes are not in strictly increasing path order at %d: %q then %q",
			i, changes[i-1].Path, changes[i].Path)
	}

	// And the same order twice over the same tree, which is the contract the
	// sort exists for.
	again, err := Changed(dir, base)
	require.NoError(t, err)
	assert.Equal(t, changes, again, "two reads of one tree must dispatch in the same order")

	// Both halves survived, with the right baseline answer.
	byPath := make(map[string]bool, len(changes))
	for _, c := range changes {
		byPath[c.Path] = c.ExistedAtBaseline
	}
	assert.True(t, byPath["tracked/f0.md"], "a modified tracked file was at the baseline")
	assert.True(t, byPath["tracked/f"+strconv.Itoa(half-1)+".md"])
	assert.False(t, byPath["untracked/f0.md"], "an untracked file was not at the baseline")
	assert.False(t, byPath["untracked/f"+strconv.Itoa(half-1)+".md"])
}
