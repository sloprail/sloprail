package filemod

import (
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/module"
)

// mkfifoOrSkip makes a named pipe, or says why the platform will not.
//
// A FIFO is the cheapest object whose open() BLOCKS rather than failing, which
// is the only property these tests need from it.
func mkfifoOrSkip(t *testing.T, path string) {
	t.Helper()
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("cannot create a FIFO here: %v", err)
	}
}

// timeoutAfterASecond is the deadline these tests use to tell "returned the
// wrong answer" from "did not return".
//
// A second is three orders of magnitude past what any of these calls costs, so
// it cannot flake on a loaded machine; the distinction being drawn is between
// terminating and not.
func timeoutAfterASecond() <-chan time.Time { return time.After(time.Second) }

// pendingInput is a pre-phase Write naming a path, against a workspace root.
func pendingInput(path, content, root string) module.Input {
	args, err := json.Marshal(map[string]string{"file_path": path, "content": content})
	if err != nil {
		panic(err)
	}
	return module.Input{
		module.InputPhase:   module.PhasePre,
		module.InputPayload: fakePending{tool: "Write", args: args, root: root},
	}
}

// The tests here are about reportable — the absolute-to-relative step that
// decides what spelling a matcher is handed — and about the containment walk it
// delegates to. They are written against the function rather than through
// Extract wherever the distinction being drawn is inside it, because the
// interesting cases are the ones where several branches produce the same
// EVENT and differ only in which of them answered.

// symlinkOrSkip makes a link, or says why the platform will not.
func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}

// TestReportable_AnExistingFileUnderASymlinkedWorkspaceRelativizes covers the
// branch that no test reached: reportable's SECOND attempt, where both the root
// and the path are resolved and asked again.
//
// The existing TestExtractPending_SymlinkedWorkspaceStillRelativizes stages the
// same /tmp-versus-/private/tmp arrangement, but its file does not EXIST — it
// is a PreFileCreate — so EvalSymlinks on the path fails and the answer comes
// from the third branch, the deepest-existing-parent walk. The second branch
// was uncovered, which means the ordinary case of an UPDATE to a file that is
// already there, in a workspace reached through a link, rested on nothing.
//
// That is the macOS default arrangement, not an exotic one: `mktemp -d` and
// every `$TMPDIR` path go through /tmp, git reports the root as /private/tmp,
// and this is the branch that has to notice they are one directory. When it
// does not, the path keeps its absolute spelling, no project-relative matcher
// admits it, and the rule goes inert — which is what was measured against real
// guardrails and is the reason the branch exists at all.
func TestReportable_AnExistingFileUnderASymlinkedWorkspaceRelativizes(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	symlinkOrSkip(t, real, link)

	// The file EXISTS, which is what routes this through the resolve-both
	// branch rather than the missing-parent one.
	require.NoError(t, os.MkdirAll(filepath.Join(real, "memories"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(real, "memories", "a.md"), []byte("x"), 0o644))

	got := reportable(filepath.Join(link, "memories", "a.md"), real)
	assert.Equal(t, "memories/a.md", got,
		"an existing file in a workspace reached through a link is in the workspace; a rule must not go inert on the spelling")
}

// TestReportable_TheWorkspaceSpelledThroughTheLinkIsTheSameWorkspace is the
// mirror of the case above, and it is the one that actually happens.
//
// A harness reports its cwd — which on macOS is routinely the /tmp spelling —
// while git resolves the root to /private/tmp, or the reverse depending on
// which of the two the session started from. Both directions have to answer the
// same, because which spelling lands in Root() is not something a rule author
// controls or can even see.
func TestReportable_TheWorkspaceSpelledThroughTheLinkIsTheSameWorkspace(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	symlinkOrSkip(t, real, link)
	require.NoError(t, os.MkdirAll(filepath.Join(real, "memories"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(real, "memories", "a.md"), []byte("x"), 0o644))

	for name, tc := range map[string]struct{ path, root string }{
		"root real, path through the link": {filepath.Join(link, "memories", "a.md"), real},
		"root through the link, path real": {filepath.Join(real, "memories", "a.md"), link},
		"both through the link":            {filepath.Join(link, "memories", "a.md"), link},
		"both real":                        {filepath.Join(real, "memories", "a.md"), real},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, "memories/a.md", reportable(tc.path, tc.root),
				"one file, one workspace, one reported spelling — whichever way each was spelled")
		})
	}
}

// TestReportable_AnEscapeThroughASymlinkedParentKeepsItsAbsoluteSpelling is the
// second of the two bugs found in this code today, pinned at the level of
// reportable itself and with the file EXISTING.
//
// The regression test that exists covers the pending/create shape. This covers
// the shape where the outside file is really there, which is both the dangerous
// one — there is something to read at the other end — and the one that takes a
// different route through reportable, since EvalSymlinks succeeds on the path
// and the second branch is the one that must refuse it.
//
// The requirement is not merely "not memories/id_rsa". It is that the result
// carries no clean relative spelling AT ALL: a hook joins whatever it is given
// against its own root, so any relative answer here is a hook reading a file
// outside the repository. Absolute is the honest answer — no project-relative
// matcher admits it, which correctly says the write is outside the rule's
// subject.
func TestReportable_AnEscapeThroughASymlinkedParentKeepsItsAbsoluteSpelling(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "id_rsa"), []byte("KEY"), 0o600))
	symlinkOrSkip(t, outside, filepath.Join(root, "escape"))

	got := reportable(filepath.Join(root, "escape", "id_rsa"), root)

	assert.True(t, filepath.IsAbs(got),
		"a path whose parent links out of the repository must not be given a relative spelling: got %q, which a hook would join onto its own root", got)
	assert.NotEqual(t, "escape/id_rsa", got,
		"this is the exact string the bug produced — clean, relative, and naming a file outside the repository")
}

// TestReportable_ASymlinkIntoTheRepositoryFromOutsideIsReportedAsInside is the
// direction the escape tests do not cover, and it is the one where refusing
// would be the defect.
//
// outside/in -> repo/sub. A harness that happens to name the file through that
// link is naming a file the repository genuinely holds, at a path inside it.
// Leaving it absolute would mean no matcher admits a write to a file that IS
// the project's — a rule going inert, which is the failure this module treats
// as worse than the noisy one.
//
// It is the containment walk rather than any string test that gets this right:
// lexically, `outside/in/a.md` shares no prefix with the root at all.
func TestReportable_ASymlinkIntoTheRepositoryFromOutsideIsReportedAsInside(t *testing.T) {
	root := t.TempDir()
	realRoot, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "sub", "a.md"), []byte("x"), 0o644))

	outside := t.TempDir()
	symlinkOrSkip(t, filepath.Join(root, "sub"), filepath.Join(outside, "in"))

	assert.Equal(t, "sub/a.md", reportable(filepath.Join(outside, "in", "a.md"), realRoot),
		"a link INTO the repository names a file the repository holds; refusing it makes a rule about that file inert")
}

// TestReportable_ARelativePathIsCleanedButNotResolved pins the first branch:
// folded lexically, never resolved against a filesystem.
//
// This test used to assert the opposite — that `./memories/a.md` is returned
// exactly as given — and that assertion was the bypass. A matcher is a prefix
// test, so `path startsWith "memories/"` did not admit `./memories/a.md`, the
// hook was never asked, and the write landed. One character, typed with no
// intent to evade, defeating every prefix-narrowed rule in every project.
//
// The old argument was that cleaning "would resolve the path against this
// process's working directory, which is not the workspace". That confuses two
// operations. filepath.Clean is LEXICAL: it folds `.` and `..` as string
// arithmetic and never touches a filesystem, so no working directory is
// consulted and nothing is resolved. What it buys is that one file has one
// spelling, which is what a prefix test needs to mean anything.
//
// `../outside.md` is the case that shows cleaning is not laundering: it stays
// `../outside.md`, still refused by every project-relative matcher, because
// there is nothing lexically above a relative root to fold it into.
func TestReportable_ARelativePathIsCleanedButNotResolved(t *testing.T) {
	root := t.TempDir()
	for given, want := range map[string]string{
		"memories/a.md":   "memories/a.md",
		"./memories/a.md": "memories/a.md",
		"a.md":            "a.md",
		"secret/./keys.md": "secret/keys.md",
		// An escape stays an escape. Folding it into something a matcher would
		// admit is the one thing cleaning must not do.
		"../outside.md": "../outside.md",
	} {
		assert.Equal(t, want, reportable(given, root),
			"one file must have one spelling, and an escape must stay one")
	}
}

// TestReportable_WithNoRootAnAbsolutePathKeepsItsAbsoluteSpelling. There is
// nothing to be relative TO, and guessing a workspace would be worse than
// reporting what the harness said.
func TestReportable_WithNoRootAnAbsolutePathKeepsItsAbsoluteSpelling(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.md")
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o644))

	got := reportable(path, "")
	assert.True(t, filepath.IsAbs(got), "no root means no relative answer is available")
	assert.Equal(t, filepath.ToSlash(path), got)
}

// TestReportable_TwoSpellingsOfOnePathProduceOneReportedPath is the canonical
// spelling requirement stated where it bites.
//
// `a.md` and `./a.md` and `sub/../a.md` are one file. Reported differently they
// are two events with different `path` values, so a matcher scoped to `a.md`
// catches one and misses the others, and a fingerprint keyed on path records
// independent verdicts for one file. The guarantee is not "each is reasonable"
// but that they are all the SAME string.
func TestReportable_TwoSpellingsOfOnePathProduceOneReportedPath(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "sub", "a.md"), []byte("x"), 0o644))

	spellings := []string{
		filepath.Join(root, "sub", "a.md"),
		filepath.Join(root, ".", "sub", "a.md"),
		filepath.Join(root, "sub", ".", "a.md"),
		root + "/sub//a.md",
		root + "/other/../sub/a.md",
		root + "/./sub/../sub/a.md",
	}
	want := reportable(spellings[0], root)
	assert.Equal(t, "sub/a.md", want)
	for _, s := range spellings[1:] {
		assert.Equalf(t, want, reportable(s, root),
			"%q must report as the same path as %q — two spellings of one file are two events and two fingerprints", s, spellings[0])
	}
}

// TestResolve_EverySpellingOfOnePathCanonicalisesIdentically is the same
// requirement one level down, over the relative spellings resolve itself takes.
//
// resolve is what reportable delegates to and what extractObserved uses
// directly, so the guarantee has to hold here or it holds nowhere. Separate
// from the test above because these inputs never pass through the
// absolute-path branches at all.
func TestResolve_EverySpellingOfOnePathCanonicalisesIdentically(t *testing.T) {
	root := t.TempDir()
	realRoot, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "sub", "a.md"), []byte("x"), 0o644))

	for _, spelling := range []string{
		"sub/a.md",
		"./sub/a.md",
		"sub/./a.md",
		"sub//a.md",
		"other/../sub/a.md",
		"./sub/../sub/a.md",
		"sub/nested/../a.md",
	} {
		clean, full, err := resolve(realRoot, spelling)
		require.NoErrorf(t, err, "%q names a file inside the repository", spelling)
		assert.Equalf(t, filepath.Join("sub", "a.md"), clean,
			"%q is one file and must have one canonical spelling", spelling)
		assert.Equalf(t, filepath.Join(realRoot, "sub", "a.md"), full,
			"%q must stat the one file it names", spelling)
	}
}

// TestResolve_APathNamingTheRootItselfIsRefusedInEverySpelling.
//
// The root is not a file within the repository, and joined against the root
// every one of these spellings stats the root — which EXISTS, and is a
// directory. Left through, it becomes an update of nothing, or an
// ErrPathIsNotAFile per cycle for a path that named no file to begin with.
//
// This asserts the CONTRACT rather than any one guard, and the distinction is
// worth stating because resolve refuses these twice over: the lexical check
// before the join, and then under() after the containment walk. Deleting either
// alone leaves the other refusing every input here, so this test does not kill
// a mutation to one of them — measured, not assumed. It kills when the property
// itself goes, which is what it claims. The layering is deliberate in the source
// (the lexical checks are documented as the cheap path, not the only one), so a
// test that pinned one layer would be asserting an implementation detail the
// module reserves the right to move.
func TestResolve_APathNamingTheRootItselfIsRefusedInEverySpelling(t *testing.T) {
	root := t.TempDir()
	realRoot, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)

	for name, path := range map[string]string{
		"empty":                  "",
		"whitespace":             "   ",
		"a single dot":           ".",
		"dot slash":              "./",
		"dot slash dot":          "./.",
		"a path folding to root": "sub/..",
		"separator alone":        string(filepath.Separator),
	} {
		t.Run(name, func(t *testing.T) {
			clean, full, err := resolve(realRoot, path)
			require.Error(t, err, "%q names the root, not a file within it", path)
			assert.ErrorIs(t, err, ErrPathNotRelativeToRoot)
			assert.Empty(t, clean, "a refused path yields no spelling to report")
			assert.Empty(t, full, "and nothing to stat")
		})
	}
}

// TestResolve_ClimbingOutIsRefusedAtEveryDepth. ".." is folded lexically by
// Clean, so what reaches the escape check is already reduced — and the check is
// anchored to the separator, which is a thing that can be got wrong in a way
// that only shows at one particular depth.
//
// Backstopped the same way as the test above, and for the same reason it is
// still written against the contract: removing resolve's lexical ".." check
// alone leaves under() refusing all of these after the walk, so this kills only
// when BOTH go. What it is really pinning is that no depth slips through
// whichever layer is doing the work.
func TestResolve_ClimbingOutIsRefusedAtEveryDepth(t *testing.T) {
	root := t.TempDir()
	realRoot, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)

	for _, path := range []string{
		"..",
		"../a.md",
		"../../a.md",
		"../../../../../../../../etc/passwd",
		"sub/../../a.md",
		"./../a.md",
		"a/b/c/../../../../out.md",
	} {
		_, _, err := resolve(realRoot, path)
		require.Errorf(t, err, "%q climbs out of the repository", path)
		assert.ErrorIsf(t, err, ErrPathNotRelativeToRoot, "%q", path)
	}
}

// TestResolve_APathThatDoesNotExistIsStillResolved is the ordinary
// PreFileCreate case, and it is the reason the containment walk climbs to the
// first ancestor that resolves rather than requiring the path to be there.
//
// A create names a file that is not on disk yet, and so may name several
// directories that are not either. Refusing those would mean no create under a
// new directory ever produced an event.
func TestResolve_APathThatDoesNotExistIsStillResolved(t *testing.T) {
	root := t.TempDir()
	realRoot, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)

	for _, path := range []string{
		"new.md",
		"newdir/new.md",
		"a/b/c/d/e/f/new.md",
	} {
		clean, full, err := resolve(realRoot, path)
		require.NoErrorf(t, err, "%q is a create under the repository and must resolve", path)
		assert.Equal(t, filepath.FromSlash(path), clean)
		assert.Equal(t, filepath.Join(realRoot, path), full)
	}
}

// TestResolveAsFarAsItGoes_APathWhoseWholePrefixIsMissingComesBackWhole is the
// answer for a path with nothing on disk below the filesystem root — the target
// of a dangling link under a directory not created yet, which is the
// generated-output case danglingLinkStaysInside exists to allow.
//
// It does NOT reach the `parent == dir` line, and saying so is the point of
// this comment. That line is unreachable on any real filesystem:
// EvalSymlinks("/") succeeds, as does EvalSymlinks("."), so the climb always
// terminates on the err == nil return one iteration earlier and the answer here
// is filepath.Join("/", remainder) rather than the bare `return path`. A
// mutation replacing that line with `return ""` survives this test and every
// other — measured, not assumed — so no test in this file claims to kill it.
//
// The behaviour below is still worth pinning, because it is what the dangling-
// link judgement rests on: a target with no existing prefix must come back
// spelled as it stands, so that the lexical comparison against the root is the
// whole answer. An implementation that dropped the remainder would return "/"
// for every such target and place every not-yet-created link inside every root.
func TestResolveAsFarAsItGoes_APathWhoseWholePrefixIsMissingComesBackWhole(t *testing.T) {
	missingRoot := filepath.Join(string(filepath.Separator), "sloprail-no-such-directory-zzz")
	_, statErr := os.Lstat(missingRoot)
	require.Error(t, statErr, "the test needs this name to be absent")

	deep := filepath.Join(missingRoot, "a", "b.md")
	assert.Equal(t, deep, resolveAsFarAsItGoes(deep),
		"the remainder must be re-joined onto the deepest resolving ancestor — here \"/\" — not dropped")

	rel := filepath.Join("no-such-dir-zzz", "a", "b.md")
	assert.Equal(t, rel, resolveAsFarAsItGoes(rel),
		"and a relative path resolves against \".\", which also exists, so it too comes back whole")
}

// TestResolveAsFarAsItGoes_TheDeepestExistingParentIsWhatResolves is the other
// half, and it is what makes the function worth having: the EXISTING prefix
// must actually be resolved through its symlinks, and the missing remainder
// re-joined onto the result.
//
// A version that returned the input unchanged whenever the full path was
// missing would pass every containment test that only asks "is it inside",
// because the lexical answer usually agrees. It fails here, where the existing
// prefix is a link and the resolved spelling differs from the given one.
func TestResolveAsFarAsItGoes_TheDeepestExistingParentIsWhatResolves(t *testing.T) {
	dir := t.TempDir()
	realDir, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(realDir, "real"), 0o755))
	symlinkOrSkip(t, filepath.Join(realDir, "real"), filepath.Join(realDir, "link"))

	// "link" exists and resolves to "real"; "missing/x.md" does not exist.
	got := resolveAsFarAsItGoes(filepath.Join(realDir, "link", "missing", "x.md"))
	assert.Equal(t, filepath.Join(realDir, "real", "missing", "x.md"), got,
		"the existing prefix is resolved through its links and the missing remainder re-joined onto it")
}

// TestResolve_ASymlinkChainOfDirectoriesStayingInsideIsContained. Every link in
// the chain is followed by EvalSymlinks in one call, so the chain length is not
// itself the question — what is, is that a chain landing INSIDE is allowed.
//
// The escape tests all end outside, so a containment check that simply refused
// any symlinked ancestor would pass all of them and fail here. That version is
// the silence: an entirely ordinary layout — a `current -> releases/v3` link, a
// linked docs directory — would produce no events at all.
func TestResolve_ASymlinkChainOfDirectoriesStayingInsideIsContained(t *testing.T) {
	root := t.TempDir()
	realRoot, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(realRoot, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(realRoot, "sub", "a.md"), []byte("x"), 0o644))

	// l1 -> l2 -> sub, all inside.
	symlinkOrSkip(t, filepath.Join(realRoot, "l2"), filepath.Join(realRoot, "l1"))
	symlinkOrSkip(t, filepath.Join(realRoot, "sub"), filepath.Join(realRoot, "l2"))

	clean, _, err := resolve(realRoot, "l1/a.md")
	require.NoError(t, err, "a chain of links that lands inside the repository is inside it")
	assert.Equal(t, filepath.Join("l1", "a.md"), clean,
		"and it is reported by the path that was named, not by where the chain led — the link is what the repository holds")
}

// TestResolve_ASymlinkChainThatLeavesTheRepositoryIsRefusedHoweverLong. The
// mirror of the case above: an escape reached through two hops rather than one
// must not be waved through because the first hop looked local.
func TestResolve_ASymlinkChainThatLeavesTheRepositoryIsRefusedHoweverLong(t *testing.T) {
	root := t.TempDir()
	realRoot, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	outside := t.TempDir()
	realOutside, err := filepath.EvalSymlinks(outside)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(realOutside, "id_rsa"), []byte("KEY"), 0o600))

	// hop1 -> hop2 -> outside. The first hop names something inside.
	symlinkOrSkip(t, filepath.Join(realRoot, "hop2"), filepath.Join(realRoot, "hop1"))
	symlinkOrSkip(t, realOutside, filepath.Join(realRoot, "hop2"))

	_, _, err = resolve(realRoot, "hop1/id_rsa")
	require.Error(t, err, "the chain ends outside the repository, whatever the first hop looked like")
	assert.ErrorIs(t, err, ErrPathNotRelativeToRoot)
	assert.NotContains(t, err.Error(), realOutside,
		"and the outside target is never named — the log must not be the reward for the escape")
}

// TestResolve_ASymlinkLoopAmongAncestorsIsRefusedRatherThanLoopedOn.
//
// EvalSymlinks fails ELOOP, which is NOT ENOENT, so the walk takes the
// refuse-what-cannot-be-established branch rather than the dangling-link one.
//
// A loop is refused TWICE over, and the test is written against the verdict
// rather than either route because both are real. Make the ELOOP branch
// skippable and the walk falls into the dangling-link path instead, follows the
// cycle hop by hop, reaches maxSymlinkHops and refuses there — measured, so the
// message changes and the answer does not. That is why this asserts the
// sentinel and not the sentence: pinning one of the two would fail the moment
// the other did the work, while the property that matters is that a cycle never
// resolves to a contained path.
//
// Termination is asserted alongside the verdict, since the failure being
// guarded against is a walk that does not come back at all, and a hung test
// reports nothing.
func TestResolve_ASymlinkLoopAmongAncestorsIsRefusedRatherThanLoopedOn(t *testing.T) {
	root := t.TempDir()
	realRoot, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	symlinkOrSkip(t, filepath.Join(realRoot, "b"), filepath.Join(realRoot, "a"))
	symlinkOrSkip(t, filepath.Join(realRoot, "a"), filepath.Join(realRoot, "b"))

	done := make(chan error, 1)
	go func() {
		_, _, err := resolve(realRoot, "a/x.md")
		done <- err
	}()
	select {
	case err := <-done:
		require.Error(t, err, "a loop establishes no containment, and unestablished containment is refused")
		assert.ErrorIs(t, err, ErrPathNotRelativeToRoot)
	case <-timeoutAfterASecond():
		t.Fatal("resolve did not return on a symlink loop — the walk must terminate, not spin")
	}
}

// TestLookAt_AFifoIsPresentAndNotAFile. lookAt is the guard that keeps every
// blocking-open in this module unreached: markersOnDisk uses os.ReadFile, which
// blocks on a FIFO exactly as fingerprint.OfFile did, and it is only ever called
// on the presentFile branch.
//
// So this is not a taxonomy test. It is the reason a FIFO in the tree does not
// wedge the pre phase, and a mutation folding presentNotAFile into presentFile
// would hang rather than fail — which is why the assertion is on the state
// rather than on any downstream event.
func TestLookAt_AFifoIsPresentAndNotAFile(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "pipe")
	mkfifoOrSkip(t, fifo)

	p, err := lookAt("pipe", fifo)
	assert.Equal(t, presentNotAFile, p,
		"a FIFO is there, so it is not absent; nothing can be written to it as a file, so it is not a file")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPathIsNotAFile)
	assert.Contains(t, err.Error(), "pipe", "and the diagnostic names the repository-relative path")
}

// TestExtractPending_AWriteAimedAtAFifoProducesNoEventAndDoesNotBlock is the
// same fact carried through the module, and it is the one that matters
// operationally.
//
// markersOnDisk would block forever on a FIFO, so if presentNotAFile ever
// stopped being its own state the pre phase would stop returning. Asserting the
// silence alone would not catch that — a hung call produces no event either.
// The deadline is what tells "no event" from "no answer".
func TestExtractPending_AWriteAimedAtAFifoProducesNoEventAndDoesNotBlock(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "pipe")
	mkfifoOrSkip(t, fifo)

	type result struct {
		events int
		err    error
	}
	got := make(chan result, 1)
	go func() {
		events, err := New().Extract(pendingInput(fifo, "body", dir))
		got <- result{len(events), err}
	}()

	select {
	case r := <-got:
		require.NoError(t, r.err, "a harness aiming a write at a FIFO is a filesystem condition, not a producer breach")
		assert.Zero(t, r.events, "no file write can land on a FIFO, so there is no file modification to report")
	case <-timeoutAfterASecond():
		t.Fatal("the pre phase blocked on a FIFO — markersOnDisk waits for a writer, so presentNotAFile must never reach it")
	}
}

// TestReportable_AnEscapeThroughASymlinkedParentIsRefusedInItsRelativeSpellingToo
// is the same escape as the test above, named the other way.
//
// The absolute branch was made non-lexical precisely so `<root>/escape/id_rsa`,
// where `escape` links out of the repository, could not come back as a clean
// repository-relative path. The RELATIVE branch was left purely lexical, and
// the identical file named as `escape/id_rsa` went straight through it: no
// `..` to fold, already clean, so Clean returned it untouched and the event
// carried a clean relative path naming a file outside the repository — which is
// verbatim what the absolute test calls "the exact string the bug produced".
//
// Which spelling the harness happens to use is not a property of the file, so
// the two branches must not disagree about it. Measured before the fix:
// absolute was left absolute and refused by every project-relative matcher,
// while relative came back as `escape/id_rsa`, admitted by `path startsWith
// "escape/"`, and a hook joining it onto its own root reads the outside file.
func TestReportable_AnEscapeThroughASymlinkedParentIsRefusedInItsRelativeSpellingToo(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "id_rsa"), []byte("KEY"), 0o600))
	symlinkOrSkip(t, outside, filepath.Join(root, "escape"))

	got := reportable("escape/id_rsa", root)

	assert.NotEqual(t, "escape/id_rsa", got,
		"the relative spelling of an escape must not stay a clean repository-relative path — a matcher admits it and a hook joins it onto its own root")
	assert.True(t, filepath.IsAbs(got),
		"an outside file is reported absolute, the same answer the absolute branch gives it: got %q", got)
}

// TestReportable_AWriteThroughASymlinkedParentCarriesNoRepositoryRelativePath
// is the same defect at the level a rule actually sees, rather than at the
// helper.
//
// A guardrail narrowed on a folder is a prefix test over the event's `path`. So
// the question that matters is not what reportable returns but what reaches the
// matcher, and this pins it there: a write the harness announces relatively,
// through a parent that links out of the repository, must not arrive as a
// spelling a project-relative rule would admit.
func TestReportable_AWriteThroughASymlinkedParentCarriesNoRepositoryRelativePath(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "id_rsa"), []byte("KEY"), 0o600))
	symlinkOrSkip(t, outside, filepath.Join(root, "escape"))

	args, err := json.Marshal(map[string]any{
		"file_path": "escape/id_rsa",
		"content":   "REPLACED",
	})
	require.NoError(t, err)

	m := &Module{}
	events, err := m.extractPending(module.Input{
		module.InputPayload: Pending(fakePending{tool: "Write", args: args, root: root}),
	})
	require.NoError(t, err)
	require.Len(t, events, 1)

	got, _ := events[0].Fields[FieldPath].(string)
	assert.NotEqual(t, "escape/id_rsa", got,
		"the event handed to every matcher must not name an outside file as though the project held it")
	assert.True(t, filepath.IsAbs(got),
		"outside is reported absolute, which no project-relative matcher admits: got %q", got)
}

// TestReportable_ContainmentOnTheRelativeBranchDoesNotStrandTheOrdinaryCases
// guards the fix above from the direction that would be worse than the hole.
//
// Routing the relative branch through resolve() means an outside path is now
// reported absolute — and the failure mode of any containment check is that it
// says "outside" about something inside, at which point no project-relative
// matcher admits it and the rule goes SILENTLY INERT. That is the direction this
// module treats as worse than a noisy one, so the ordinary cases are pinned here
// rather than left to follow from the escape test passing.
//
// Four cases, each a way the check could over-refuse:
//
//   - a file that does not exist yet, which is the ordinary PreFileCreate and
//     cannot be resolved at all;
//   - a workspace reached through a symlink, which is every macOS repo under
//     /tmp, where the root and the path resolve into different-looking trees;
//   - a link INTO the repository, which names a file the project genuinely
//     holds and must not be pushed out by the same walk that ejects an escape;
//   - a dangling link whose target is inside — a directory not generated yet,
//     which presence.go's containment walk deliberately judges rather than
//     refuses.
func TestReportable_ContainmentOnTheRelativeBranchDoesNotStrandTheOrdinaryCases(t *testing.T) {
	t.Run("a create of a file that is not there yet stays relative", func(t *testing.T) {
		root := t.TempDir()
		for _, given := range []string{"new.md", "deep/nested/new.md", "./new.md"} {
			got := reportable(given, root)
			assert.False(t, filepath.IsAbs(got),
				"a create cannot be resolved and must not be ejected for it: %q -> %q", given, got)
		}
	})

	t.Run("a symlinked workspace still relativises", func(t *testing.T) {
		real := t.TempDir()
		link := filepath.Join(t.TempDir(), "link")
		symlinkOrSkip(t, real, link)
		require.NoError(t, os.WriteFile(filepath.Join(real, "a.md"), []byte("x"), 0o644))

		for _, root := range []string{real, link} {
			assert.Equal(t, "a.md", reportable("a.md", root),
				"the repo spelled through a link is the same repo, and a rule about a.md must still fire")
		}
	})

	t.Run("a link into the repository is reported as inside", func(t *testing.T) {
		root := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(root, "sub"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, "sub", "a.md"), []byte("x"), 0o644))
		symlinkOrSkip(t, filepath.Join(root, "sub"), filepath.Join(root, "alias"))

		got := reportable("alias/a.md", root)
		assert.False(t, filepath.IsAbs(got),
			"the link's target is inside the repository, so refusing it makes a rule about that file inert: %q", got)
	})

	t.Run("a dangling link whose target is inside is still inside", func(t *testing.T) {
		root := t.TempDir()
		symlinkOrSkip(t, filepath.Join(root, "generated"), filepath.Join(root, "pending"))

		got := reportable("pending/out.md", root)
		assert.False(t, filepath.IsAbs(got),
			"a link made before the directory it names is the ordinary generated-output case: %q", got)
	})
}
