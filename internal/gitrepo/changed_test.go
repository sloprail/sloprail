package gitrepo

import (
	"os"
	"path/filepath"
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
func TestParseNameStatus_RejectsAnUnknownStatus(t *testing.T) {
	_, err := parseNameStatus("X\x00weird.md\x00")
	assert.Error(t, err)
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
