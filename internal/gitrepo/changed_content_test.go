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

// The tests here are about what the differ says for file SHAPES rather than for
// repository states — a mode change with no content change, an empty file, a
// file with no trailing newline, binary content — and about the two error paths
// Changed has that no test reached.
//
// They matter because the differ's output is what decides whether a rule is
// asked about a file at all. A shape that classifies wrong is a guardrail that
// does not run, and the shapes below are the ones where git's own answer is
// least like the obvious guess.

// TestChanged_AModeChangeWithNoContentChangeIsAnUpdate.
//
// `chmod +x` produces `M` from git with the blob OID identical on both sides:
// the file's CONTENT is untouched, and the only difference is the mode bit. The
// path was in the baseline and is in the tree, so it is an update.
//
// Worth pinning because it is the case where "changed" and "different bytes"
// come apart, and either direction of getting it wrong is a real failure. Read
// as no change, a rule about scripts becoming executable never fires — and that
// is a security-relevant change a guardrail is exactly the thing to catch. Read
// as a create, every rule bound to creation fires for a file that has been
// there all along.
//
// It also constrains the fingerprint story rather than contradicting it: the
// fingerprint of this file is unchanged, because the fingerprint is of content
// and the content did not change. A revalidation keyed on the fingerprint would
// therefore treat the file as already-judged content, which is correct — the
// bytes a rule reads are the same bytes. The differ reporting it and the
// fingerprint not distinguishing it are both right, about different questions.
func TestChanged_AModeChangeWithNoContentChangeIsAnUpdate(t *testing.T) {
	dir := initRepo(t)
	write(t, dir, "script.sh", "#!/bin/sh\necho hi\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "base")
	base := git(t, dir, "rev-parse", "HEAD")

	require.NoError(t, os.Chmod(filepath.Join(dir, "script.sh"), 0o755))

	changes := changedMap(t, dir, base)
	existed, reported := changes["script.sh"]
	require.True(t, reported,
		"a file becoming executable is a change to it; unreported, no rule about it can fire")
	assert.True(t, existed,
		"the file was in the baseline and is in the tree — an update, not a create")
}

// TestChanged_AnEmptyFileIsAChange. A created file with no bytes in it is
// still a created file, and git tracks it.
//
// The shape to be careful of is a differ that decides what changed by looking
// at content: nothing to diff reads as nothing changed. An empty file is
// exactly the output an agent produces when a generation fails, which is a case
// a guardrail wants to see rather than the one it should be blind to.
func TestChanged_AnEmptyFileIsAChange(t *testing.T) {
	dir := initRepo(t)
	write(t, dir, "seed.md", "x")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "base")
	base := git(t, dir, "rev-parse", "HEAD")

	// One empty and untracked, one empty and staged: the two halves of Changed
	// answer these separately, so both routes are exercised.
	write(t, dir, "untracked-empty.md", "")
	write(t, dir, "staged-empty.md", "")
	git(t, dir, "add", "staged-empty.md")

	changes := changedMap(t, dir, base)
	existed, reported := changes["untracked-empty.md"]
	require.True(t, reported, "an empty untracked file is a file the cycle created")
	assert.False(t, existed)

	existed, reported = changes["staged-empty.md"]
	require.True(t, reported, "and so is an empty staged one")
	assert.False(t, existed)
}

// TestChanged_TruncatingAFileToEmptyIsAnUpdate is the other end of the same
// concern: a file whose content is REMOVED still exists, so it is an update and
// not a delete.
//
// Reported as a delete, every rule bound to deletion runs against a file still
// sitting in the tree, and every rule that would have judged the now-empty
// content does not run at all.
func TestChanged_TruncatingAFileToEmptyIsAnUpdate(t *testing.T) {
	dir := initRepo(t)
	write(t, dir, "notes.md", "a good deal of text\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "base")
	base := git(t, dir, "rev-parse", "HEAD")

	write(t, dir, "notes.md", "")

	changes := changedMap(t, dir, base)
	existed, reported := changes["notes.md"]
	require.True(t, reported)
	assert.True(t, existed,
		"the file is still there with nothing in it — an update, and its content is what a rule should now judge")
}

// TestChanged_AMissingTrailingNewlineIsStillAChange.
//
// git prints "\ No newline at end of file" into its diff output for this, and
// that line is a real hazard for a reader: it appears in the human-readable
// format. It must not appear in `--name-status -z`, and if a future flag
// change ever let it in it would be read as a STATUS field, shifting every
// subsequent entry and turning the rest of the diff into nonsense.
//
// So this asserts the parse stays intact around such a file, with entries on
// both sides of it, rather than merely that the file is reported.
func TestChanged_AMissingTrailingNewlineIsStillAChange(t *testing.T) {
	dir := initRepo(t)
	write(t, dir, "a-before.md", "one\n")
	write(t, dir, "b-nonewline.md", "one\n")
	write(t, dir, "c-after.md", "one\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "base")
	base := git(t, dir, "rev-parse", "HEAD")

	write(t, dir, "a-before.md", "two\n")
	write(t, dir, "b-nonewline.md", "no trailing newline")
	write(t, dir, "c-after.md", "two\n")

	changes := changedMap(t, dir, base)
	for _, path := range []string{"a-before.md", "b-nonewline.md", "c-after.md"} {
		existed, reported := changes[path]
		require.Truef(t, reported, "%q must be reported — a no-newline entry must not derail the parse around it", path)
		assert.Truef(t, existed, "%q was in the baseline", path)
	}
	assert.Len(t, changes, 3, "and nothing else may appear from a misread field")
}

// TestChanged_BinaryContentIsReportedLikeAnyOtherFile.
//
// git treats binary files specially in its diff OUTPUT — "Binary files differ"
// in place of a hunk — and the concern is the same as above: a reader that saw
// any of that would misparse. `--name-status` should emit only the status and
// the path whatever the content is.
//
// The bytes include a NUL, which is the field separator under -z. That is the
// interesting part: a path is NUL-terminated but content never reaches this
// output, so a NUL in the CONTENT must not split anything. If it ever did, one
// binary file would shift the whole remainder of the diff.
func TestChanged_BinaryContentIsReportedLikeAnyOtherFile(t *testing.T) {
	dir := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "img.bin"), []byte{0x89, 'P', 'N', 'G', 0x00, 0x1a}, 0o644))
	write(t, dir, "z-after.md", "one\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "base")
	base := git(t, dir, "rev-parse", "HEAD")

	require.NoError(t, os.WriteFile(filepath.Join(dir, "img.bin"), []byte{0x89, 'P', 'N', 'G', 0x00, 0x2b, 0x00}, 0o644))
	write(t, dir, "z-after.md", "two\n")

	changes := changedMap(t, dir, base)
	existed, reported := changes["img.bin"]
	require.True(t, reported, "a binary file that changed is a file that changed")
	assert.True(t, existed)

	_, reported = changes["z-after.md"]
	assert.True(t, reported,
		"and the entry after it survives — a NUL in the content must not be read as a field separator")
	assert.Len(t, changes, 2)
}

// TestChanged_AFileThatBecameABinaryFileIsAnUpdate. Text to bytes is a content
// change like any other, and the path was there before and is there now.
func TestChanged_AFileThatBecameABinaryFileIsAnUpdate(t *testing.T) {
	dir := initRepo(t)
	write(t, dir, "data", "plain text\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "base")
	base := git(t, dir, "rev-parse", "HEAD")

	require.NoError(t, os.WriteFile(filepath.Join(dir, "data"), []byte{0x00, 0x01, 0x02, 0x00}, 0o644))

	changes := changedMap(t, dir, base)
	existed, reported := changes["data"]
	require.True(t, reported)
	assert.True(t, existed, "the path was in the baseline, whatever its content became")
}

// TestChanged_TheFourWaysWorkCanBeOutstandingAreAllReportedTogether is the
// claim in Changed's own doc — committed, staged, merely written, and never
// added — stated as one tree with all four in it at once.
//
// Each is covered separately elsewhere. Together is a different assertion: the
// two git commands behind this must UNION rather than shadow one another, and a
// tree holding only one kind at a time cannot show that. A differ that asked
// only one question would pass three of the four existing tests.
func TestChanged_TheFourWaysWorkCanBeOutstandingAreAllReportedTogether(t *testing.T) {
	dir := initRepo(t)
	write(t, dir, "will-be-committed.md", "one")
	write(t, dir, "will-be-staged.md", "one")
	write(t, dir, "will-be-written.md", "one")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "base")
	base := git(t, dir, "rev-parse", "HEAD")

	// Committed since the baseline.
	write(t, dir, "will-be-committed.md", "two")
	git(t, dir, "add", "will-be-committed.md")
	git(t, dir, "commit", "-m", "mid-cycle commit")

	// Staged but not committed.
	write(t, dir, "will-be-staged.md", "two")
	git(t, dir, "add", "will-be-staged.md")

	// Written but not staged.
	write(t, dir, "will-be-written.md", "two")

	// Never added to git at all.
	write(t, dir, "never-added.md", "new")

	changes := changedMap(t, dir, base)
	assert.Equal(t, map[string]bool{
		"will-be-committed.md": true,
		"will-be-staged.md":    true,
		"will-be-written.md":   true,
		"never-added.md":       false,
	}, changes,
		"a cycle's difference spans committed and uncommitted work alike; an agent that commits mid-cycle has not changed nothing")
}

// TestRoot_ADirectoryThatIsNotARepositoryIsAnError covers Root's error return,
// which no test reached.
//
// It is not a cosmetic path. Root is what a consumer joins every reported path
// against, so a Root that returned "" with no error would have every consumer
// resolving repository-relative paths against the process's working directory —
// files stat as absent, and every modified file classifies as a DELETE. That is
// F1 arrived at from the consumer's side, and an empty string is exactly what a
// swallowed error leaves behind.
func TestRoot_ADirectoryThatIsNotARepositoryIsAnError(t *testing.T) {
	root, err := Root(t.TempDir())
	require.Error(t, err, "a directory outside any repository has no root to report")
	assert.Empty(t, root,
		"and it must not come back as \"\" with no error — a consumer would join every path against its own cwd")
}

// TestRoot_FromASubdirectoryReportsTheTopOfTheTree is Root's whole reason for
// existing, stated where it differs from the obvious answer.
//
// Change.Path is relative to the ROOT while a hook holds the directory it was
// invoked in, and those coincide only when the hook ran at the top of the tree.
// A Root that answered "the directory I was asked about" would look right in
// every test run from the root and be wrong for every hook that is not.
func TestRoot_FromASubdirectoryReportsTheTopOfTheTree(t *testing.T) {
	dir := initRepo(t)
	write(t, dir, "sub/nested/a.md", "x")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "base")

	realDir, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)

	for _, from := range []string{dir, filepath.Join(dir, "sub"), filepath.Join(dir, "sub", "nested")} {
		root, err := Root(from)
		require.NoErrorf(t, err, "asked from %q", from)
		realRoot, err := filepath.EvalSymlinks(root)
		require.NoError(t, err)
		assert.Equalf(t, realDir, realRoot,
			"asked from %q, Root must name the top of the tree — that is what reported paths resolve against", from)
	}
}

// TestChanged_APathReportedFromASubdirectoryStillResolvesAgainstRoot is the
// consumer-side pairing of the two facts above, and it is the assertion that
// actually catches the bug they exist to prevent.
//
// Root() and Changed() are separately correct in the tests around them. What
// matters operationally is that joining ONE onto the OTHER reaches the file:
// that is what every consumer does, and it is the step where a cwd-relative
// path silently reads as absent and turns a modification into a deletion.
func TestChanged_APathReportedFromASubdirectoryStillResolvesAgainstRoot(t *testing.T) {
	dir := initRepo(t)
	write(t, dir, "top.md", "one")
	write(t, dir, "sub/deep/inner.md", "one")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "base")
	base := git(t, dir, "rev-parse", "HEAD")

	write(t, dir, "top.md", "two")
	write(t, dir, "sub/deep/inner.md", "two")
	write(t, dir, "sub/deep/fresh.md", "new")

	from := filepath.Join(dir, "sub", "deep")
	root, err := Root(from)
	require.NoError(t, err)

	changes, err := Changed(from, base)
	require.NoError(t, err)
	require.NotEmpty(t, changes)

	for _, c := range changes {
		joined := filepath.Join(root, c.Path)
		_, statErr := os.Lstat(joined)
		assert.NoErrorf(t, statErr,
			"%q joined onto Root() must reach the file; a path that does not stat reads as absent and a modification becomes a deletion", c.Path)
	}
}

// TestChanged_AnUnreadableRepositoryIsReportedRatherThanCalledUnchanged covers
// the second uncovered return in Changed: the untracked listing failing.
//
// The direction is the whole point. Changed returning `nil, nil` for a
// repository it could not read is indistinguishable from a clean tree, so every
// rule for that cycle is skipped and the run looks green — the silence this
// engine exists to prevent, reached by the quietest possible route. An error is
// the only honest answer.
func TestChanged_AnUnreadableRepositoryIsReportedRatherThanCalledUnchanged(t *testing.T) {
	dir := initRepo(t)
	base := commit(t, dir, "a.md", "one")

	// The repository's object store removed underneath it: git can still tell
	// this is a repository, and can no longer answer about it.
	require.NoError(t, os.RemoveAll(filepath.Join(dir, ".git", "objects")))

	changes, err := Changed(dir, base)
	require.Error(t, err,
		"a repository that cannot be read is not a repository with nothing in it; reported as unchanged, every rule is skipped and the run looks green")
	assert.Empty(t, changes)
}

// gitShimEmittingDiff puts a `git` earlier on PATH that answers
// `diff --name-status` with a canned payload and forwards everything else to
// the real binary.
//
// A shim rather than a seam in the package, because the behaviour under test is
// what Changed does with a diff it cannot fully classify, and there is no other
// way to produce one: git does not emit an unrecognised status on request, and
// the whole package's test convention is real repositories against real git.
// Everything except the one command stays real, so the untracked half, Root and
// the repository itself are unaffected.
func gitShimEmittingDiff(t *testing.T, payload string) {
	t.Helper()
	realGit, err := exec.LookPath("git")
	require.NoError(t, err)

	bin := t.TempDir()
	script := "#!/bin/sh\n" +
		"for a in \"$@\"; do\n" +
		"  case \"$a\" in --name-status) printf '%s'; exit 0;; esac\n" +
		"done\n" +
		"exec " + realGit + " \"$@\"\n"
	script = strings.Replace(script, "'%s'", "'"+payload+"'", 1)
	require.NoError(t, os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestChanged_AnUnclassifiableStatusIsReportedWithoutDroppingTheRest is the
// contract Changed's own doc states and that nothing asserted through Changed:
// the changes and the problem come back TOGETHER.
//
// parseNameStatus is tested for this directly, but the propagation was not, and
// the two halves fail differently. The comment in the source is explicit about
// the stake — returning the error alone makes the caller turn it into zero
// events, so "a single status nobody had seen before silently disabled every
// file rule for that turn". One file nobody could classify becomes every file
// nobody judged.
//
// Measured: with `return changes, unclassified` weakened to `return changes,
// nil`, the entire existing suite stayed green. That is the mutation this test
// exists to kill, and it is why the assertion is on BOTH returns rather than on
// either.
func TestChanged_AnUnclassifiableStatusIsReportedWithoutDroppingTheRest(t *testing.T) {
	dir := initRepo(t)
	base := commit(t, dir, "a.md", "one")

	// One status this package does not know, and two ordinary entries around it
	// so the "without dropping the rest" half is not vacuous.
	gitShimEmittingDiff(t, `M\0a.md\0X\0weird.md\0D\0gone.md\0`)

	changes, err := Changed(dir, base)

	require.Error(t, err,
		"a status that cannot be classified must be reported; unreported, the path is dropped and nobody knows")
	assert.Contains(t, err.Error(), "weird.md", "and the diagnostic must name the path nobody could classify")

	byPath := make(map[string]bool, len(changes))
	for _, c := range changes {
		byPath[c.Path] = c.ExistedAtBaseline
	}
	assert.Equal(t, map[string]bool{"a.md": true, "gone.md": true}, byPath,
		"the classifiable paths are real changes and must survive — dropping ninety-nine because a hundredth was unreadable is the silence this engine exists to prevent")
}

// TestChanged_AnUnclassifiableStatusStillLetsTheUntrackedHalfThrough. The two
// questions are independent, and a partially-unreadable diff must not suppress
// files the diff never had an opinion about in the first place.
func TestChanged_AnUnclassifiableStatusStillLetsTheUntrackedHalfThrough(t *testing.T) {
	dir := initRepo(t)
	base := commit(t, dir, "a.md", "one")
	write(t, dir, "brand-new.md", "written this cycle")

	gitShimEmittingDiff(t, `X\0weird.md\0`)

	changes, err := Changed(dir, base)
	require.Error(t, err)

	var found bool
	for _, c := range changes {
		if c.Path == "brand-new.md" {
			found = true
			assert.False(t, c.ExistedAtBaseline, "untracked means in no commit")
		}
	}
	assert.True(t, found,
		"a file the agent wrote and never staged is not the diff's to classify, so a diff problem must not hide it")
}

// TestChanged_ARepositoryWithNoCommitsAndNoBaselineIsSilentNotAnError.
//
// The pairing with the test above is the point: absence of a baseline is an
// ORDINARY state and must be quiet, while inability to read the tree is a fault
// and must not be. Collapsing the two in either direction is a defect — errors
// on every fresh repository, or silence on every broken one.
func TestChanged_ARepositoryWithNoCommitsAndNoBaselineIsSilentNotAnError(t *testing.T) {
	dir := initRepo(t)
	write(t, dir, "a.md", "one")

	changes, err := Changed(dir, "")
	require.NoError(t, err,
		"a repository with no commit yet is an ordinary state, and the caller has already been told the baseline is unavailable")
	assert.Empty(t, changes, "there is nothing to measure from, so nothing is measured")
}
