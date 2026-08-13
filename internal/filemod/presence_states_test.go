package filemod

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The presence oracle answers one question — what is at a path — on two
// independent axes: whether the lookup produced a FACT at all, and what KIND of
// object is there when something is. Four states carry both, and collapsing
// either axis loses a defect the other cannot catch.
//
// The tests here attack the states one object kind at a time, and then the
// transitions between them. Every one drives lookAt itself rather than a copy
// of its logic — TestLookAt_IsTheOnlyPresenceOracle exists to keep a second
// path-presence stat from appearing in the package, and a test that grew its own
// would be exactly that.

// --- object kinds that are not files ----------------------------------------

// TestLookAt_ASocketIsNotAFile.
//
// isRegular rather than !IsDir, taken past the fifo the existing tests cover. A
// unix socket exists, no file write can land on it, and a check naming only
// directories reports it as a file — so a PreFileUpdate goes out for a path the
// write will fail on.
func TestLookAt_ASocketIsNotAFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix sockets do not work this way on windows")
	}
	root := tree(t)
	// Kept short: the sun_path limit is around 100 bytes and t.TempDir() names
	// are long, so a socket under a deep temp path fails to bind for a reason
	// that has nothing to do with what is being tested.
	sock := filepath.Join(root, "s")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Skipf("cannot bind a unix socket here: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })

	p, err := lookAt("s", sock)
	assert.Equal(t, presentNotAFile, p, "a socket exists and is not a file this module's kinds are about")
	require.ErrorIs(t, err, ErrPathIsNotAFile)
}

// TestLookAt_ADeviceIsNotAFile uses a device the system already has, since
// creating one needs privileges a test must not assume.
//
// /dev/null is a character device: it exists, and a create or update event
// naming it would claim a file changed where no file is.
func TestLookAt_ADeviceIsNotAFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("/dev/null is not a device node on windows")
	}
	info, err := os.Lstat("/dev/null")
	if err != nil || info.Mode()&os.ModeDevice == 0 {
		t.Skip("/dev/null is not a device node on this system")
	}

	p, err := lookAt("dev/null", "/dev/null")
	assert.Equal(t, presentNotAFile, p, "a device exists and is not a file")
	require.ErrorIs(t, err, ErrPathIsNotAFile)
}

// TestLookAt_ADirectoryAndItsSymlinkAnswerDifferently is the isRegular
// asymmetry, stated as the pair it is.
//
// A directory is presentNotAFile. A SYMLINK to that same directory is
// presentFile — deliberately, and the doc comment argues it at length: git
// tracks the link as a blob holding a path, so the link is a file in the
// repository whatever sits at the other end. Deciding its kind by following it
// would make a tracked file's event depend on an object outside the repository,
// and the link would flip between "file" and "not a file" as its target came
// and went.
//
// The two are asserted together because the claim is about the DIFFERENCE. Each
// alone would pass under an implementation that used Stat and got the other one
// wrong.
func TestLookAt_ADirectoryAndItsSymlinkAnswerDifferently(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need a privilege this test cannot assume on windows")
	}
	root := tree(t)
	require.NoError(t, os.Mkdir(filepath.Join(root, "adir"), 0o755))
	symlink(t, root, "link-to-dir", "adir")

	p, err := lookAt("adir", filepath.Join(root, "adir"))
	require.ErrorIs(t, err, ErrPathIsNotAFile)
	assert.Equal(t, presentNotAFile, p, "the directory itself is not a file")

	p, err = lookAt("link-to-dir", filepath.Join(root, "link-to-dir"))
	require.NoError(t, err,
		"a symlink is the file git tracks; following it would make this answer depend on "+
			"something the repository does not contain")
	assert.Equal(t, presentFile, p)
}

// TestLookAt_ABrokenSymlinkIsPresentNotAbsent.
//
// The Lstat-not-Stat decision, at its sharpest. Under Stat a dangling link
// reads ENOENT and therefore ABSENT — so an agent that creates a link to a
// not-yet-generated target gets no event at all, and retargeting an existing
// link to a missing path reads as a DELETE of a file that is sitting right
// there.
//
// Lstat sees the link itself, which is the thing that changed.
func TestLookAt_ABrokenSymlinkIsPresentNotAbsent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need a privilege this test cannot assume on windows")
	}
	root := tree(t)
	symlink(t, root, "dangling.md", "nothing-is-here.md")
	full := filepath.Join(root, "dangling.md")

	// The premise: following it really does read as absent.
	_, serr := os.Stat(full)
	require.Truef(t, os.IsNotExist(serr),
		"the link's target exists, so this test is not about a dangling link")

	p, err := lookAt("dangling.md", full)
	require.NoError(t, err)
	assert.Equalf(t, presentFile, p,
		"a dangling link is a file in the repository — absent here swallows the create "+
			"and turns a retarget into a delete of a file that is still there")
}

// TestLookAt_ASymlinkChainIsTheFirstLinkAndNothingFurther.
//
// Lstat does not follow, so a chain's length is irrelevant and its END is
// irrelevant: the answer is about the link at the named path. Asserted over a
// chain whose end is a DIRECTORY, so an implementation that followed would
// answer presentNotAFile and be visibly wrong rather than accidentally right.
func TestLookAt_ASymlinkChainIsTheFirstLinkAndNothingFurther(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need a privilege this test cannot assume on windows")
	}
	root := tree(t)
	require.NoError(t, os.Mkdir(filepath.Join(root, "end-dir"), 0o755))
	symlink(t, root, "hop2", "end-dir")
	symlink(t, root, "hop1", "hop2")

	p, err := lookAt("hop1", filepath.Join(root, "hop1"))
	require.NoError(t, err,
		"following the chain would land on a directory and report ErrPathIsNotAFile")
	assert.Equal(t, presentFile, p, "the answer is about the link at the named path")
}

// TestLookAt_ASymlinkLoopIsStillJustALink.
//
// A cycle is where a following implementation fails hardest — Stat returns
// ELOOP, which is not ENOENT, so the path would report unknown and the module
// would refuse to classify a file that is sitting right there. Lstat never
// follows, so the loop is invisible to it and the link is a file like any
// other.
//
// The ELOOP is proven rather than assumed, so the test cannot pass by the loop
// having failed to form.
func TestLookAt_ASymlinkLoopIsStillJustALink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need a privilege this test cannot assume on windows")
	}
	root := tree(t)
	symlink(t, root, "a-loop", "b-loop")
	symlink(t, root, "b-loop", "a-loop")
	full := filepath.Join(root, "a-loop")

	// The premise: following it really does fail, and NOT with ENOENT.
	_, serr := os.Stat(full)
	require.Error(t, serr)
	require.Falsef(t, os.IsNotExist(serr),
		"the loop resolves to a plain absence, so this test is not about ELOOP")

	p, err := lookAt("a-loop", full)
	require.NoErrorf(t, err,
		"a loop is only visible to something that follows; Lstat does not, so this must not "+
			"report the machine as unable to answer about a link that is right there")
	assert.Equal(t, presentFile, p)
}

// --- the fact/no-fact axis --------------------------------------------------

// TestLookAt_AnUnreadableParentIsUnknownForEveryKindBehindIt.
//
// The unknown state is a fact about the LOOKUP, not about the object, so it
// must not depend on what is actually behind the unreadable parent. A file, a
// directory and a dangling link all sit behind one chmod 000 here, and all
// three must answer unknown — an implementation that somehow reported the true
// kind would be reading something it cannot see.
//
// This is the axis whose collapse announces PostFileDelete for a file the
// project still has.
func TestLookAt_AnUnreadableParentIsUnknownForEveryKindBehindIt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits do not work this way on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores the permission bits this depends on")
	}
	root := tree(t, "locked/a-file.md")
	require.NoError(t, os.Mkdir(filepath.Join(root, "locked", "a-dir"), 0o755))
	symlink(t, root, "locked/a-dangling-link", "nowhere.md")

	unreadable(t, filepath.Join(root, "locked"))

	for _, name := range []string{"a-file.md", "a-dir", "a-dangling-link"} {
		rel := "locked/" + name
		p, err := lookAt(rel, filepath.Join(root, "locked", name))
		assert.Equalf(t, unknown, p,
			"%s: an unreadable parent is the machine declining to answer, whatever is behind it", rel)
		assert.ErrorIsf(t, err, ErrUnreadableTree, "%s", rel)
		// And specifically NOT the fact-shaped answer.
		assert.NotEqualf(t, absent, p,
			"%s: folded into absent, this announces a delete for a file that is still there", rel)
	}
}

// TestLookAt_AnAbsentPathAndAnUnreadableOneAreNotTheSameAnswer states the two
// apart in one place, since the whole of the fact/no-fact axis is that they
// differ. One is a finding about the tree and carries no error; the other is
// the absence of a finding and carries one.
func TestLookAt_AnAbsentPathAndAnUnreadableOneAreNotTheSameAnswer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits do not work this way on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores the permission bits this depends on")
	}
	root := tree(t, "locked/behind.md")

	gone, gerr := lookAt("gone.md", filepath.Join(root, "gone.md"))
	require.NoError(t, gerr, "not being there is a fact, and a fact is not an error")
	require.Equal(t, absent, gone)

	unreadable(t, filepath.Join(root, "locked"))
	blocked, berr := lookAt("locked/behind.md", filepath.Join(root, "locked", "behind.md"))
	require.ErrorIs(t, berr, ErrUnreadableTree)

	assert.NotEqualf(t, gone, blocked,
		"the two collapsed into one state is how a file behind a locked directory "+
			"gets announced as deleted")
}

// TestLookAt_TheNotAFileDiagnosticNamesTheRepositoryRelativePath.
//
// The sibling of the ErrUnreadableTree diagnostic, and it was the unpinned one:
// swapping `path` for `full` in the ErrPathIsNotAFile message survived the whole
// suite. Every error this module raises is read by someone debugging a producer,
// and they all name the path the producer gave — a lone absolute path among them,
// in a module whose premise is that paths are repository-relative, reads as a
// different kind of thing than it is, and discloses the filesystem layout around
// the repository besides.
//
// Unlike the unreadable case there is no wrapped *PathError here, so the whole
// message is this module's own sentence and the whole of it is fair game: it
// must name the relative path and must not contain the root anywhere.
func TestLookAt_TheNotAFileDiagnosticNamesTheRepositoryRelativePath(t *testing.T) {
	root := tree(t)
	require.NoError(t, os.Mkdir(filepath.Join(root, "adir"), 0o755))

	_, err := lookAt("adir", filepath.Join(root, "adir"))

	require.ErrorIs(t, err, ErrPathIsNotAFile)
	assert.Contains(t, err.Error(), "adir", "the diagnostic names the path the producer gave")
	assert.NotContainsf(t, err.Error(), root,
		"this message is entirely this module's own — it wraps no *PathError — so an absolute "+
			"path in it is a leak with nothing to excuse it: %q", err.Error())
}

// TestObserved_TheNotAFileDiagnosticStaysRelativeThroughTheModule is the same
// claim at the module's front door, where a producer actually reads it.
//
// Worth stating separately from the lookAt-level test because the observed
// phase is the caller that surfaces this error, and it passes lookAt the CLEAN
// path alongside the absolute one — so a swap of the two arguments anywhere on
// that route shows up here.
func TestObserved_TheNotAFileDiagnosticStaysRelativeThroughTheModule(t *testing.T) {
	root := tree(t)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "sub", "adir"), 0o755))

	_, err := observeErr(fakeObserved{
		root:   root,
		paths:  []string{"sub/adir"},
		before: map[string]bool{filepath.FromSlash("sub/adir"): true},
	})

	require.ErrorIs(t, err, ErrPathIsNotAFile)
	assert.Contains(t, err.Error(), filepath.FromSlash("sub/adir"))
	assert.NotContainsf(t, err.Error(), root,
		"the producer reads this message and joins the paths in it against its own root; "+
			"an absolute one resolves against nothing: %q", err.Error())
}

// --- transitions ------------------------------------------------------------

// TestLookAt_APathThatChangesTypeBetweenTwoLookupsAnswersAboutEachMoment.
//
// The oracle is a reading of the tree at an instant, and it says so: nothing
// here is cached, memoised, or carried between calls. A path that is a file,
// then a directory, then gone must answer presentFile, presentNotAFile, absent
// in that order.
//
// What this pins is the absence of any state between calls. An oracle that
// remembered its first answer would report a file long after the tree stopped
// having one, and every event built on it would describe a tree that no longer
// exists.
func TestLookAt_APathThatChangesTypeBetweenTwoLookupsAnswersAboutEachMoment(t *testing.T) {
	root := tree(t)
	full := filepath.Join(root, "shifting")

	require.NoError(t, os.WriteFile(full, []byte("a file, for now"), 0o644))
	p, err := lookAt("shifting", full)
	require.NoError(t, err)
	require.Equal(t, presentFile, p)

	require.NoError(t, os.Remove(full))
	require.NoError(t, os.Mkdir(full, 0o755))
	p, err = lookAt("shifting", full)
	require.ErrorIs(t, err, ErrPathIsNotAFile)
	assert.Equalf(t, presentNotAFile, p,
		"the second lookup answered about the first one's tree — nothing may be carried between calls")

	require.NoError(t, os.Remove(full))
	p, err = lookAt("shifting", full)
	require.NoError(t, err)
	assert.Equal(t, absent, p)
}

// TestLookAt_EveryStateIsReachableAndDistinct walks the whole enum in one test,
// so that a collapse of ANY pair fails here rather than only in whichever
// single-state test happened to cover it.
//
// The four are compared against each other rather than against their expected
// values alone: an implementation returning one constant for everything would
// satisfy four separate equality assertions written in four separate tests if
// any one of them were wrong, and cannot satisfy the distinctness check below.
func TestLookAt_EveryStateIsReachableAndDistinct(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits do not work this way on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores the permission bits this depends on")
	}
	root := tree(t, "a-file.md", "locked/behind.md")
	require.NoError(t, os.Mkdir(filepath.Join(root, "a-dir"), 0o755))
	unreadable(t, filepath.Join(root, "locked"))

	got := map[string]presence{}
	for name, rel := range map[string]string{
		"absent":          "gone.md",
		"presentFile":     "a-file.md",
		"presentNotAFile": "a-dir",
		"unknown":         "locked/behind.md",
	} {
		p, _ := lookAt(rel, filepath.Join(root, filepath.FromSlash(rel)))
		got[name] = p
	}

	assert.Equal(t, absent, got["absent"])
	assert.Equal(t, presentFile, got["presentFile"])
	assert.Equal(t, presentNotAFile, got["presentNotAFile"])
	assert.Equal(t, unknown, got["unknown"])

	// All four distinct, which no single-state assertion can establish.
	seen := map[presence]string{}
	for name, p := range got {
		if prior, dup := seen[p]; dup {
			t.Errorf("%s and %s collapsed into the same state %d — each collapse is a defect "+
				"one axis or the other was written to prevent", prior, name, p)
		}
		seen[p] = name
	}
}

// --- the error and the value carry the same fact ----------------------------

// TestLookAt_TheValueAndTheErrorNeverDisagree.
//
// The two returns carry one fact in two forms on purpose: the observed phase
// reads the error and reports it, while the pre phase has no producer to blame
// and simply emits nothing. A bool could not serve both — that asymmetry is
// exactly how a write onto a directory became PreFileUpdate.
//
// Which makes their AGREEMENT load-bearing, and unasserted anywhere else: a
// state that returned presentNotAFile with a nil error would silently reach the
// pre phase's presentFile handling in any caller that branched on the error
// instead of the value.
func TestLookAt_TheValueAndTheErrorNeverDisagree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits do not work this way on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores the permission bits this depends on")
	}
	root := tree(t, "a-file.md", "locked/behind.md")
	require.NoError(t, os.Mkdir(filepath.Join(root, "a-dir"), 0o755))
	require.NoError(t, syscall.Mkfifo(filepath.Join(root, "a-fifo"), 0o600))
	unreadable(t, filepath.Join(root, "locked"))

	for _, tc := range []struct {
		rel      string
		want     presence
		wantErr  error
		nilError bool
	}{
		{rel: "gone.md", want: absent, nilError: true},
		{rel: "a-file.md", want: presentFile, nilError: true},
		{rel: "a-dir", want: presentNotAFile, wantErr: ErrPathIsNotAFile},
		{rel: "a-fifo", want: presentNotAFile, wantErr: ErrPathIsNotAFile},
		{rel: "locked/behind.md", want: unknown, wantErr: ErrUnreadableTree},
	} {
		p, err := lookAt(tc.rel, filepath.Join(root, filepath.FromSlash(tc.rel)))
		assert.Equalf(t, tc.want, p, "%s: wrong state", tc.rel)
		if tc.nilError {
			assert.NoErrorf(t, err, "%s: a state that is a fact carries no error", tc.rel)
			continue
		}
		assert.ErrorIsf(t, err, tc.wantErr,
			"%s: the value says %d and the error disagrees — a caller branching on one "+
				"would take the other's path", tc.rel, p)
	}
}

// --- the breach audit, counted -----------------------------------------------

// TestObserved_ABreachIsReportedOncePerFileNotOncePerSpelling.
//
// baselinesFor carries an explicit guard — "Reported once per file, not once per
// spelling of it" — and nothing pinned the COUNT it is about. Removing the guard
// left every existing test green, because each of them names a breached file
// under exactly one non-canonical spelling, and one spelling cannot produce a
// duplicate.
//
// So this names ONE file under THREE spellings that all clean to it, and every
// one of them is a spelling the raw-keyed baseline answers true for. Without the
// guard the same file is accused three times.
//
// The count is the only thing that separates the mistake from the fix, which is
// why the assertion is on how many times the sentinel appears rather than on
// whether it appears at all. A producer reading three identical complaints about
// one file cannot tell whether it has one bug or three.
func TestObserved_ABreachIsReportedOncePerFileNotOncePerSpelling(t *testing.T) {
	root := tree(t, "dir/a.md")

	// Three raw spellings, one canonical file. The baseline is keyed RAW — the
	// breach — so it answers true for each raw spelling and false for the clean
	// one, which is precisely the disagreement the check is looking for.
	events, err := observeErr(fakeObserved{
		root:  root,
		paths: []string{"./dir/a.md", "dir/./a.md", "./dir/./a.md"},
		before: map[string]bool{
			"./dir/a.md":   true,
			"dir/./a.md":   true,
			"./dir/./a.md": true,
		},
	})

	require.ErrorIs(t, err, ErrBaselineKeyedOnRawSpelling)
	assert.Empty(t, events, "which of the two answers is true is exactly what is in doubt")

	// The premise: the three spellings really are one file.
	for _, raw := range []string{"./dir/a.md", "dir/./a.md", "./dir/./a.md"} {
		require.Equal(t, filepath.FromSlash("dir/a.md"), filepath.Clean(raw),
			"this test needs all three spellings to clean to one file")
	}

	got := strings.Count(err.Error(), ErrBaselineKeyedOnRawSpelling.Error())
	assert.Equalf(t, 1, got,
		"one file must be accused once however many spellings named it; got %d complaints:\n%s",
		got, err.Error())
}

// --- containment, at the states the walk can meet ---------------------------

// TestResolve_AParentThatIsUnreadableIsRefusedRatherThanContained.
//
// contained walks up to the first ancestor that RESOLVES. A parent whose
// permissions stop EvalSymlinks is not an ancestor that resolved and not one
// that is absent — it is a lookup that established nothing, and unestablished
// containment is refused.
//
// The direction is the whole point: letting the path through on a failed check
// is the one outcome the function exists to prevent, and the refusal is
// reported rather than turned into an event.
func TestResolve_AParentThatIsUnreadableIsRefusedRatherThanContained(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits do not work this way on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores the permission bits this depends on")
	}
	root := tree(t, "locked/inner/deep.md")
	unreadable(t, filepath.Join(root, "locked"))

	_, _, err := resolve(root, "locked/inner/deep.md")

	require.ErrorIsf(t, err, ErrPathNotRelativeToRoot,
		"an ancestor that could not be resolved leaves containment unestablished, and "+
			"unestablished is refused rather than allowed")
	assert.NotContains(t, err.Error(), root,
		"the refusal names the ancestor relatively, never by its absolute path")
}

// TestObserved_ADeepChainOfDanglingLinksTerminates.
//
// The hop bound's live job is termination on a chain the kernel has not already
// rejected. This drives a chain past the bound through the module's front door
// and asserts that it comes back refused rather than hanging — the property no
// single Lstat can establish.
//
// A count well past maxSymlinkHops, so the bound is genuinely crossed rather
// than approached.
func TestObserved_ADeepChainOfDanglingLinksTerminates(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need a privilege this test cannot assume on windows")
	}
	root := tree(t)
	const hops = maxSymlinkHops + 10
	for i := 0; i < hops; i++ {
		symlink(t, root, "hop"+strconv.Itoa(i), "hop"+strconv.Itoa(i+1))
	}
	// hop<hops> is never created, so the whole chain dangles.

	events, err := observeErr(fakeObserved{
		root:   root,
		paths:  []string{"hop0/file.md"},
		before: nil,
	})

	require.Error(t, err, "a chain that does not settle must be refused, not followed forever")
	assert.ErrorIs(t, err, ErrPathNotRelativeToRoot)
	assert.Empty(t, events)
}
