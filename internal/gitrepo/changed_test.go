package gitrepo

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Real repositories throughout, for the reason the rest of this package's tests
// give: what is being checked is the reading of git's own output, and a stub
// would only prove this file agrees with a second copy of its own assumptions.

// changedMap is the changes as a lookup, for assertions that name one path.
func changedMap(t *testing.T, dir, commit string) map[string]bool {
	t.Helper()
	changes, err := Changed(dir, commit)
	require.NoError(t, err)
	m := make(map[string]bool, len(changes))
	for _, c := range changes {
		_, dup := m[c.Path]
		require.Falsef(t, dup, "path %q reported twice — one file must be one event", c.Path)
		m[c.Path] = c.ExistedAtBaseline
	}
	return m
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	full := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(body), 0o644))
}

// TestChanged_ClassifiesCreateUpdateDelete is the base case: the three kinds a
// Post event can be, from one real tree against one real baseline.
//
// The assertion is on ExistedAtBaseline rather than on a status letter, because
// that is the single fact the module cannot recover for itself — whether the
// file is there NOW is a stat away, and whether it was there BEFORE is a fact
// about a commit the tree may have left behind.
func TestChanged_ClassifiesCreateUpdateDelete(t *testing.T) {
	dir := initRepo(t)
	write(t, dir, "keep.md", "one")
	write(t, dir, "gone.md", "one")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "base")
	base := git(t, dir, "rev-parse", "HEAD")

	write(t, dir, "keep.md", "two")                              // update
	require.NoError(t, os.Remove(filepath.Join(dir, "gone.md"))) // delete
	write(t, dir, "fresh.md", "new")                             // create

	got := changedMap(t, dir, base)

	assert.True(t, got["keep.md"], "a modified file was at the baseline")
	assert.True(t, got["gone.md"], "a deleted file was at the baseline — false here makes every delete vanish")
	assert.False(t, got["fresh.md"], "a new file was not at the baseline")
	assert.Len(t, got, 3)
}

// TestChanged_SpansCommittedAndUncommitted is difference_spans_both.
//
// The committed half is the one a naive implementation loses: an agent that
// commits its work leaves a tree with nothing outstanding, and a comparison
// reading only what is outstanding reports that the cycle changed nothing —
// precisely wrong, and silently so.
//
// Both halves in one tree, so the test cannot pass by covering either alone.
func TestChanged_SpansCommittedAndUncommitted(t *testing.T) {
	dir := initRepo(t)
	write(t, dir, "seed.md", "seed")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "base")
	base := git(t, dir, "rev-parse", "HEAD")

	// Committed during the cycle: nothing outstanding in the tree afterwards.
	write(t, dir, "committed.md", "done")
	git(t, dir, "add", "committed.md")
	git(t, dir, "commit", "-m", "mid-cycle")

	// Staged but not committed.
	write(t, dir, "staged.md", "pending")
	git(t, dir, "add", "staged.md")

	// Written and not staged.
	write(t, dir, "dirty.md", "loose")

	got := changedMap(t, dir, base)

	assert.Contains(t, got, "committed.md", "work committed mid-cycle fell out of the difference")
	assert.Contains(t, got, "staged.md")
	assert.Contains(t, got, "dirty.md")
	assert.False(t, got["committed.md"], "it was created during the cycle, so it was not at the baseline")
}

// TestChanged_UntrackedFileCounts: a file the agent created and never staged is
// a change the baseline does not have.
//
// `git diff --name-status` alone does not report it — it compares tracked
// content — so this is the case a diff-only implementation misses entirely, and
// it is the ordinary shape of an agent writing scratch output.
//
// Pinned in both directions: the file must be reported, AND the diff must
// genuinely be silent about it, or the test would pass against an
// implementation that never asked the second question.
func TestChanged_UntrackedFileCounts(t *testing.T) {
	dir := initRepo(t)
	write(t, dir, "seed.md", "seed")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "base")
	base := git(t, dir, "rev-parse", "HEAD")

	write(t, dir, "never-staged.md", "scratch")

	// The control: git's own diff says nothing about it.
	tracked, err := diffAgainst(dir, base)
	require.NoError(t, err)
	require.NotContains(t, tracked, "never-staged.md",
		"if the diff reported it, this test would pass without the untracked question being asked at all")

	got := changedMap(t, dir, base)
	require.Contains(t, got, "never-staged.md", "an untracked file is work the baseline does not have")
	assert.False(t, got["never-staged.md"])
}

// TestChanged_IgnoredFilesStaySilent: an untracked file the project ignores is
// not the cycle's work.
//
// Without --exclude-standard the first cycle after a dependency install puts
// every file in node_modules in front of every guardrail — the flood
// untouched_stays_silent exists to prevent, arriving through the untracked door
// rather than the diff.
func TestChanged_IgnoredFilesStaySilent(t *testing.T) {
	dir := initRepo(t)
	write(t, dir, ".gitignore", "build/\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "base")
	base := git(t, dir, "rev-parse", "HEAD")

	write(t, dir, "build/artifact.o", "binary")
	write(t, dir, "src.md", "real work")

	got := changedMap(t, dir, base)

	assert.Contains(t, got, "src.md")
	assert.NotContains(t, got, "build/artifact.o", "an ignored file is not the cycle's work")
}

// TestChanged_RenameIsADeleteAndACreate.
//
// Git reports a rename as ONE entry naming TWO paths. Both halves have to
// survive: a rule bound to deletion must be told the old path is gone, and a
// rule bound to creation must be told the new one arrived.
//
// This is also the parser's alignment case. A reader treating the -z stream as
// status/path pairs consumes one path here and then reads the SECOND path as
// the next status, misaligning the whole remainder of the diff — which is why
// the assertions below include a file that comes after the rename.
func TestChanged_RenameIsADeleteAndACreate(t *testing.T) {
	dir := initRepo(t)
	write(t, dir, "dir/old.md", "stable content that will move unchanged")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "base")
	base := git(t, dir, "rev-parse", "HEAD")

	git(t, dir, "mv", "dir/old.md", "dir/new.md")
	// Sorts after the rename's paths, so a misaligned parse loses or mangles it.
	write(t, dir, "zzz-after.md", "written after")

	got := changedMap(t, dir, base)

	assert.True(t, got["dir/old.md"], "the rename's source was at the baseline and is gone — a delete")
	assert.False(t, got["dir/new.md"], "the rename's destination was not at the baseline — a create")
	assert.Contains(t, got, "zzz-after.md",
		"an entry after the rename was lost — the two-path status was read as one")
}

// TestParseNameStatus_RenameAndSplitShapeAgree.
//
// Rename detection is a git CONFIGURABLE, not a constant: `diff.renames=false`
// makes git report the same move as a separate delete and add. The events must
// not depend on which shape arrived, so both are parsed here and compared.
//
// This is what the -M flag's doc comment claims, stated as a test rather than
// asserted in prose — the flag itself changes nothing under git's current
// default, so a test of the flag would pass with it removed.
func TestParseNameStatus_RenameAndSplitShapeAgree(t *testing.T) {
	asRename, err := parseNameStatus("R100\x00dir/old.md\x00dir/new.md\x00")
	require.NoError(t, err)
	asSplit, err := parseNameStatus("A\x00dir/new.md\x00D\x00dir/old.md\x00")
	require.NoError(t, err)

	assert.Equal(t, asSplit, asRename,
		"a move must classify identically whether git reported it as one rename or as a delete plus an add")
	assert.True(t, asRename["dir/old.md"])
	assert.False(t, asRename["dir/new.md"])
}

// TestChanged_ChangedAndChangedBackIsSilent is untouched_stays_silent at its
// sharpest.
//
// The file was written to during the cycle and then written back. Its mtime has
// moved and its content has not, and git compares content — so there is no
// difference from the baseline and no event. An implementation reaching for
// timestamps instead would report it, and hand every rule a file identical to
// the one the project already had.
func TestChanged_ChangedAndChangedBackIsSilent(t *testing.T) {
	dir := initRepo(t)
	write(t, dir, "flip.md", "original")
	write(t, dir, "other.md", "untouched")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "base")
	base := git(t, dir, "rev-parse", "HEAD")

	write(t, dir, "flip.md", "TEMPORARILY DIFFERENT")
	write(t, dir, "flip.md", "original") // back to exactly what was committed

	got := changedMap(t, dir, base)

	assert.NotContains(t, got, "flip.md", "a file reverted to its baseline content differs from nothing")
	assert.NotContains(t, got, "other.md", "a file never touched must never appear")
	assert.Empty(t, got)
}

// TestChanged_UntouchedTreeReportsNothing: the whole repository is not the
// session's work.
//
// The first cycle in an established project is the case that matters. Reporting
// everything the project has ever contained would bury whatever the agent
// actually did under the rest of the repository.
func TestChanged_UntouchedTreeReportsNothing(t *testing.T) {
	dir := initRepo(t)
	for _, name := range []string{"a.md", "b/c.md", "d/e/f.md"} {
		write(t, dir, name, "content of "+name)
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "base")
	base := git(t, dir, "rev-parse", "HEAD")

	changes, err := Changed(dir, base)
	require.NoError(t, err)
	assert.Empty(t, changes, "nothing changed since the baseline, so nothing may be reported")
}

// TestChanged_DeletedThenRecreatedDifferentIsOneEntry.
//
// A tracked file removed from the index and then written back on disk is
// reported by the diff (as a deletion) and by the untracked listing (as a new
// file). One file must not become two events, and the diff's answer — the one
// that knows the baseline — must win, or the delete would be recorded as a
// create and the file would look new to every rule.
func TestChanged_DeletedThenRecreatedDifferentIsOneEntry(t *testing.T) {
	dir := initRepo(t)
	write(t, dir, "twice.md", "original")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "base")
	base := git(t, dir, "rev-parse", "HEAD")

	git(t, dir, "rm", "--cached", "twice.md")
	write(t, dir, "twice.md", "rewritten")

	// The control: both questions really do name this path, so the
	// deduplication below is being exercised rather than assumed.
	untracked, err := untrackedPaths(dir)
	require.NoError(t, err)
	require.Contains(t, untracked, "twice.md", "the untracked listing must name it for this test to mean anything")
	tracked, err := diffAgainst(dir, base)
	require.NoError(t, err)
	require.Contains(t, tracked, "twice.md", "the diff must name it too")

	got := changedMap(t, dir, base) // changedMap fails on a duplicate
	assert.True(t, got["twice.md"], "the baseline knew this file — the diff's answer must win over the untracked guess")
}

// TestChanged_PathsAreCleanAndRelative.
//
// The consumer keys a map on these and hands them to a module that refuses
// anything absolute or escaping. Git's own spelling is already clean, and this
// is what says so rather than assuming it — including for a nested path, where
// a "./" prefix would be easiest to acquire.
func TestChanged_PathsAreCleanAndRelative(t *testing.T) {
	dir := initRepo(t)
	write(t, dir, "seed.md", "seed")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "base")
	base := git(t, dir, "rev-parse", "HEAD")

	write(t, dir, "deep/nested/file.md", "x")

	changes, err := Changed(dir, base)
	require.NoError(t, err)
	require.NotEmpty(t, changes)
	for _, c := range changes {
		assert.Equal(t, filepath.Clean(c.Path), c.Path, "git's spelling must already be canonical")
		assert.False(t, filepath.IsAbs(c.Path), "paths are repository-relative")
	}
	assert.Equal(t, "deep/nested/file.md", changes[0].Path)
}

// TestChanged_IsOrdered: the same tree twice gives the same order.
//
// Map iteration is random in Go, so without the sort a hook writing a ledger
// would have a different one to assert against on every run — a flake that
// looks like the engine being non-deterministic.
func TestChanged_IsOrdered(t *testing.T) {
	dir := initRepo(t)
	write(t, dir, "seed.md", "seed")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "base")
	base := git(t, dir, "rev-parse", "HEAD")

	for _, n := range []string{"c.md", "a.md", "b.md", "d/e.md"} {
		write(t, dir, n, "x")
	}

	first, err := Changed(dir, base)
	require.NoError(t, err)
	for i := 0; i < 5; i++ {
		again, err := Changed(dir, base)
		require.NoError(t, err)
		assert.Equal(t, first, again, "the same tree must produce the same order")
	}
	assert.Equal(t, []string{"a.md", "b.md", "c.md", "d/e.md"},
		[]string{first[0].Path, first[1].Path, first[2].Path, first[3].Path})
}

// TestChanged_NoBaselineReportsNothing: a repository with no commit yet is an
// ordinary state — the first session in a new project is in it — and it is not
// a point anything can be measured from.
func TestChanged_NoBaselineReportsNothing(t *testing.T) {
	dir := initRepo(t)
	write(t, dir, "a.md", "x")

	changes, err := Changed(dir, "")
	require.NoError(t, err, "no baseline is not a failure")
	assert.Empty(t, changes)
}

// TestChanged_UnreadableRepositoryIsReported: a directory that is not a
// repository is an error rather than an empty difference.
//
// Silently reporting "nothing changed" would be the engine claiming it looked
// and found nothing, when it never managed to look.
func TestChanged_NotARepositoryIsReported(t *testing.T) {
	dir := t.TempDir()
	_, err := Changed(dir, "0000000000000000000000000000000000000000")
	assert.Error(t, err, "a tree with no repository cannot be compared, and must say so")
}

// TestParseNameStatus_RejectsATruncatedRenameEntry.
//
// The parser is a cursor over NUL-separated fields, and a rename consumes two
// paths. A stream ending after the source path must be reported rather than
// read past the end of the slice or silently treated as a single-path status.
func TestParseNameStatus_RejectsATruncatedRenameEntry(t *testing.T) {
	_, err := parseNameStatus("R100\x00only-one-path.md\x00")
	assert.Error(t, err, "a rename naming one path is malformed output, not a delete")
}

// TestParseNameStatus_RejectsAnUnknownStatus.
//
// An unrecognised verb must not be guessed at. Defaulting it to "existed" or
// "did not exist" would classify a file on a status nothing understood, and the
// wrong answer here is invisible — it becomes an ordinary-looking event.
// TestParseNameStatus_ReportsAnUnknownStatusWithoutDroppingTheRest.
//
// An unrecognised letter is still reported — the path cannot be classified, so
// it must not be silently treated as unchanged. What changed is the BLAST
// RADIUS: it used to fail the whole parse, and the caller turns a failed parse
// into zero events for the entire cycle. One status nobody had seen before
// therefore disabled every file rule for that turn, which is a far larger
// silence than the one unclassifiable path it was reporting.
func TestParseNameStatus_ReportsAnUnknownStatusWithoutDroppingTheRest(t *testing.T) {
	got, err := parseNameStatus("M\x00before.md\x00X\x00weird.md\x00A\x00after.md\x00")

	assert.Error(t, err, "a status that cannot be classified must be reported")
	assert.Contains(t, err.Error(), "weird.md", "the error names the path it could not classify")

	assert.True(t, got["before.md"], "a change read before the unknown status survives it")
	assert.False(t, got["after.md"], "a change read after the unknown status survives it")
	assert.NotContains(t, got, "weird.md", "the path that could not be classified is not guessed at")
	assert.Len(t, got, 2)
}

// TestParseNameStatus_EveryUnknownStatusIsReported: a second unreadable status
// does not hide the first, which is why they are collected rather than
// returned at the first one.
func TestParseNameStatus_EveryUnknownStatusIsReported(t *testing.T) {
	_, err := parseNameStatus("X\x00one.md\x00Y\x00two.md\x00")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "one.md")
	assert.Contains(t, err.Error(), "two.md")
}

// TestParseNameStatus_HandlesAPathHoldingANewline.
//
// The reason for -z. In git's default output this path is quoted and escaped,
// and a line-oriented reader splits it into two entries. Under -z the bytes
// arrive raw, and the parser must carry them through untouched — a path is
// bytes, not a line.
func TestParseNameStatus_HandlesAPathHoldingANewline(t *testing.T) {
	got, err := parseNameStatus("M\x00od\nd.md\x00A\x00after.md\x00")
	require.NoError(t, err)
	assert.True(t, got["od\nd.md"], "the embedded newline must not split one path into two entries")
	assert.False(t, got["after.md"], "the entry after it must still align")
	assert.Len(t, got, 2)
}

// TestParseNameStatus_CopyReportsOnlyTheDestination.
//
// A copy leaves its source exactly as the baseline had it. Reporting the source
// would announce a file that has not changed, which untouched_stays_silent
// forbids.
func TestParseNameStatus_CopyReportsOnlyTheDestination(t *testing.T) {
	got, err := parseNameStatus("C75\x00src.md\x00dst.md\x00")
	require.NoError(t, err)
	assert.NotContains(t, got, "src.md", "a copy does not change its source")
	assert.False(t, got["dst.md"])
	assert.Len(t, got, 1)
}

// TestParseNameStatus_EmptyOutputIsNoChanges: the ordinary answer for a cycle
// that changed nothing tracked, and it must not be read as one empty record.
func TestParseNameStatus_EmptyOutputIsNoChanges(t *testing.T) {
	for _, out := range []string{"", "\x00"} {
		got, err := parseNameStatus(out)
		require.NoErrorf(t, err, "output %q", out)
		assert.Emptyf(t, got, "output %q", out)
	}
}

// TestChanged_FromASubdirectoryReportsRepositoryRelativePaths is F1: the two
// git commands do not speak the same path language.
//
// `git diff --name-status` names paths from the REPOSITORY ROOT wherever it is
// run from. `git ls-files --others` names them from the CURRENT DIRECTORY. Run
// anywhere but the root, the union of the two carries both conventions at once
// and nothing downstream can tell which is which.
//
// What that costs is not a cosmetic mis-spelling. The consumer stats Root()
// joined to the path to decide whether a file exists NOW, so a path in the
// wrong convention does not resolve, reads as absent, and a file that was at
// the baseline and is still on disk classifies as a DELETE. Every modified file
// outside the invocation subdirectory is dispatched as a deletion of a file
// that is still there.
//
// The cwd is a subdirectory two levels down and the tree holds a modified file
// at the root, a modified file in that subdirectory, and a new file in it —
// which is the shape that makes both conventions appear in one answer.
func TestChanged_FromASubdirectoryReportsRepositoryRelativePaths(t *testing.T) {
	dir := initRepo(t)
	write(t, dir, "top.md", "one")
	write(t, dir, "sub/deep/inner.md", "one")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "base")
	base := git(t, dir, "rev-parse", "HEAD")

	write(t, dir, "top.md", "two")
	write(t, dir, "sub/deep/inner.md", "two")
	write(t, dir, "sub/deep/new.md", "fresh")

	// Asked from the subdirectory, the way a hook invoked with a cwd below the
	// root asks it.
	got := changedMap(t, filepath.Join(dir, "sub", "deep"), base)

	assert.Equal(t, map[string]bool{
		"top.md":            true,
		"sub/deep/inner.md": true,
		"sub/deep/new.md":   false,
	}, got, "every path is repository-relative regardless of which directory the question was asked from")

	// Stated separately, because the map assertion above would still pass if
	// the untracked file were reported cwd-relative AND the test's expectation
	// were written to match. This is the property the consumer depends on: the
	// path resolves against the repository root.
	for p := range got {
		_, err := os.Stat(filepath.Join(dir, p))
		assert.NoErrorf(t, err, "%q does not resolve against the repository root, so it will classify as a delete", p)
	}
}

// TestChanged_TypechangeIsAnUpdate is F2, against real git rather than a
// hand-written status string.
//
// Replacing a regular file with a symlink changes neither its presence nor its
// path — only its mode — and git reports that as `T`. The file was at the
// baseline and is there now, so it is an update.
//
// Written against a real tree because the point is that git actually emits this
// letter. A parser test alone would only assert that the code agrees with the
// test's own guess about git's alphabet, and the arm survived a full suite
// precisely because nothing ever produced a T.
//
// What removing the arm costs is out of all proportion to the case: an
// unrecognised status is an error for the WHOLE diff, and the caller turns that
// error into zero events for the entire cycle. One symlink would silently
// disable every file rule for that turn — see
// TestChanged_OneUnreadableStatusDoesNotSilenceTheRest.
func TestChanged_TypechangeIsAnUpdate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need a privilege this test cannot assume on windows")
	}
	dir := initRepo(t)
	write(t, dir, "thing.md", "a regular file")
	write(t, dir, "target.md", "the target")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "base")
	base := git(t, dir, "rev-parse", "HEAD")

	require.NoError(t, os.Remove(filepath.Join(dir, "thing.md")))
	require.NoError(t, os.Symlink(filepath.Join(dir, "target.md"), filepath.Join(dir, "thing.md")))

	// The status really is T, or this test is proving something else.
	require.Contains(t, git(t, dir, "diff", "--name-status", base), "T\tthing.md",
		"git no longer reports a typechange as T, so this test is no longer about the T arm")

	got := changedMap(t, dir, base)
	assert.True(t, got["thing.md"], "a file whose type changed was still at the baseline")
	assert.Len(t, got, 1)
}

// TestParseNameStatus_TypechangeIsAnUpdate pins the same arm at the parser,
// where the letter can be stated outright.
func TestParseNameStatus_TypechangeIsAnUpdate(t *testing.T) {
	got, err := parseNameStatus("T\x00thing.md\x00")
	require.NoError(t, err)
	assert.True(t, got["thing.md"], "a typechange is present on both sides, so it is an update")
	assert.Len(t, got, 1)
}

// TestChanged_StagedThenDeletedIsReportedAsNoChange is F3: the one shape the
// two-command union is structurally blind to, pinned so it is a decision rather
// than an accident.
//
// A file created, staged, and then removed from disk before the cycle ends.
// `git status` calls it "AD". Neither question this asks can see it:
//
//	git diff --name-status <baseline>   nothing: absent from the worktree AND
//	                                    from the baseline commit, so there is
//	                                    no difference between those two
//	git ls-files --others               nothing: the path is TRACKED, having
//	                                    been added to the index
//
// So the answer is zero changes, and that is defensible rather than merely
// convenient: what a Post event describes is what the cycle did to the TREE,
// and the tree ends the cycle exactly as the baseline had it. The file has no
// content to judge and no path a rule could look at. classify's own no/no row
// says the same thing — not there before, not there now, so nothing happened.
//
// What is lost is the intermediate state: a rule that wants to object to a file
// having existed at all, however briefly, cannot see it here. Reading the index
// as a third question would surface it, and would also start reporting paths
// that are not in the tree the other rules are judging. That trade is not taken;
// this test is what makes the choice visible if it is ever revisited.
func TestChanged_StagedThenDeletedIsReportedAsNoChange(t *testing.T) {
	dir := initRepo(t)
	write(t, dir, "seed.md", "seed")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "base")
	base := git(t, dir, "rev-parse", "HEAD")

	write(t, dir, "staged.md", "written, added, then removed")
	git(t, dir, "add", "staged.md")
	require.NoError(t, os.Remove(filepath.Join(dir, "staged.md")))

	// The state really is the one this test is about.
	require.Contains(t, git(t, dir, "status", "--short"), "AD staged.md",
		"git no longer calls this AD, so this test is no longer about that state")

	changes, err := Changed(dir, base)
	require.NoError(t, err)
	assert.Empty(t, changes,
		"a file staged and then deleted leaves the tree as the baseline had it, so the cycle changed nothing")
}

// TestChanged_SubmoduleIsAGitlinkNotItsContents is F4, pinned as a decision.
//
// A submodule is one entry in the parent's tree — a GITLINK, recording which
// commit of another repository this one points at. Two consequences, both of
// which this test states:
//
//	Adding one reports .gitmodules and the submodule's PATH. The path is not a
//	file, but it is what git names and it is handed to file rules as though it
//	were one. A rule bound to a path pattern will match on it.
//
//	Changing a file INSIDE the submodule reports NOTHING. The parent only
//	notices a submodule when the commit it points at moves, and editing a file
//	in the submodule's worktree does not move it — that needs a commit in the
//	submodule and then staging the new pointer in the parent.
//
// Left as it is rather than "fixed". Recursing into submodules would mean
// judging another repository's files against this project's rules, with its own
// baseline, its own ignore rules and its own history — a different question from
// the one a cycle asks. What the engine owes here is to say so rather than to
// let a project discover it by having a rule silently never fire.
func TestChanged_SubmoduleIsAGitlinkNotItsContents(t *testing.T) {
	inner := initRepo(t)
	write(t, inner, "lib.md", "v1")
	git(t, inner, "add", ".")
	git(t, inner, "commit", "-m", "init")

	dir := initRepo(t)
	write(t, dir, "seed.md", "seed")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "base")
	base := git(t, dir, "rev-parse", "HEAD")

	// file:// transport between local repositories is refused by default in
	// modern git; this is a local fixture, not a fetch from anywhere.
	git(t, dir, "-c", "protocol.file.allow=always", "submodule", "add", inner, "vendor/sub")

	got := changedMap(t, dir, base)
	assert.False(t, got[".gitmodules"], "the file recording the submodule is itself a new file")
	assert.False(t, got["vendor/sub"], "the gitlink is reported under the submodule's PATH, not its contents")
	assert.NotContains(t, got, "vendor/sub/lib.md", "a file inside a submodule is not this repository's to report")

	// Dirtying the submodule's worktree moves nothing the parent records.
	write(t, filepath.Join(dir, "vendor", "sub"), "lib.md", "v2")
	after := changedMap(t, dir, base)
	assert.NotContains(t, after, "vendor/sub/lib.md",
		"editing a file inside a submodule is invisible to the parent's diff")
	assert.Equal(t, got, after, "dirtying a submodule's contents changes nothing the parent reports")
}
