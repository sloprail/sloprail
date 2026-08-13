package filemod

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- a stat that cannot answer is not a fact about the tree (F1) -------------

// unreadable makes dir unreadable and unsearchable for the duration of the
// test, so a stat of anything inside it fails with EACCES rather than ENOENT.
func unreadable(t *testing.T, dir string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("permission bits do not work this way on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores the permission bits this depends on")
	}
	require.NoError(t, os.Chmod(dir, 0o000))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
}

func TestObserved_UnstattablePathIsNotReportedAsADelete(t *testing.T) {
	// The file is sitting right there. Only the parent's permissions stop the
	// stat from saying so, and "the machine cannot answer" collapsed into "not
	// there" turns an existing, modified file into PostFileDelete — an event
	// reporting a difference the tree does not have.
	root := tree(t, "locked/present.md")
	unreadable(t, filepath.Join(root, "locked"))

	events, err := observeErr(fakeObserved{
		root:   root,
		paths:  []string{"locked/present.md"},
		before: map[string]bool{"locked/present.md": true},
	})

	assert.Empty(t, events, "no event at all beats an event that says the file was deleted")
	assert.ErrorIs(t, err, ErrUnreadableTree)
}

func TestObserved_UnstattablePathDoesNotSilenceTheRest(t *testing.T) {
	// One path the machine cannot answer about is not a reason to withhold the
	// classification of the ones beside it.
	root := tree(t, "locked/present.md", "fine.md")
	unreadable(t, filepath.Join(root, "locked"))

	events, err := observeErr(fakeObserved{
		root:   root,
		paths:  []string{"locked/present.md", "fine.md", "gone.md"},
		before: map[string]bool{"locked/present.md": true, "fine.md": true, "gone.md": true},
	})

	require.ErrorIs(t, err, ErrUnreadableTree)
	require.Len(t, events, 2)
	assert.Equal(t, KindPostUpdate, events[0].Kind)
	assert.Equal(t, "fine.md", events[0].Fields[FieldPath])
	assert.Equal(t, KindPostDelete, events[1].Kind)
	assert.Equal(t, "gone.md", events[1].Fields[FieldPath])
}

func TestLookAt_DistinguishesAbsentFromUnanswerable(t *testing.T) {
	root := tree(t, "locked/present.md")

	p, err := lookAt("nothing-here.md", filepath.Join(root, "nothing-here.md"))
	require.NoError(t, err, "not being there is a fact, not a failure")
	assert.Equal(t, absent, p)

	unreadable(t, filepath.Join(root, "locked"))

	p, err = lookAt("locked/present.md", filepath.Join(root, "locked", "present.md"))
	assert.Equal(t, unknown, p)
	assert.ErrorIs(t, err, ErrUnreadableTree)
}

// --- the link is the thing git tracks, not what it points at (F2) ------------

// symlink puts a link at root/name pointing at target, which need not exist.
func symlink(t *testing.T, root, name, target string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need a privilege this test will not assume on windows")
	}
	full := filepath.Join(root, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.Symlink(target, full))
}

func TestObserved_DanglingSymlinkCreatedThisCycleIsACreate(t *testing.T) {
	// An agent links to something not generated yet. Following the link, the
	// path reads as absent and the create is swallowed by the no/no row — no
	// guardrail ever sees the new file.
	root := tree(t)
	symlink(t, root, "link.md", "target-that-does-not-exist.md")

	events := observe(t, fakeObserved{
		root:   root,
		paths:  []string{"link.md"},
		before: nil,
	})

	require.Len(t, events, 1)
	assert.Equal(t, KindPostCreate, events[0].Kind)
	assert.Equal(t, "link.md", events[0].Fields[FieldPath])
}

func TestObserved_DanglingSymlinkAtBaselineIsNotADelete(t *testing.T) {
	// Retargeting a link to a path that does not exist. The link is still
	// there, and git still tracks it, so this is an update — not a delete of a
	// file sitting in the tree.
	root := tree(t)
	symlink(t, root, "link.md", "gone-target.md")

	events := observe(t, fakeObserved{
		root:   root,
		paths:  []string{"link.md"},
		before: map[string]bool{"link.md": true},
	})

	require.Len(t, events, 1)
	assert.Equal(t, KindPostUpdate, events[0].Kind)
}

func TestObserved_SymlinkToOutsideTheRepositoryIsTheLinkItself(t *testing.T) {
	// The inverse: following the link would report on a file the repository
	// does not contain. The link is what changed and what the event is about.
	outside := filepath.Join(t.TempDir(), "elsewhere.md")
	require.NoError(t, os.WriteFile(outside, []byte("x\n"), 0o644))

	root := tree(t)
	symlink(t, root, "link.md", outside)

	events := observe(t, fakeObserved{
		root:   root,
		paths:  []string{"link.md"},
		before: nil,
	})

	require.Len(t, events, 1)
	assert.Equal(t, KindPostCreate, events[0].Kind)
}

func TestLookAt_SeesTheLinkNotItsTarget(t *testing.T) {
	root := tree(t)
	symlink(t, root, "dangling", "nowhere")

	p, err := lookAt("dangling", filepath.Join(root, "dangling"))
	require.NoError(t, err)
	assert.Equal(t, presentFile, p, "os.Stat would resolve the link and call this absent")
}

// --- a path that is not repository-relative is refused, not normalised (F4) --

func TestObserved_PathsOutsideTheRepositoryAreRefused(t *testing.T) {
	// filepath.Join folds "../outside.md" away and stats a real file outside
	// the repository, while the event still carries the raw spelling — which no
	// hook can resolve, and which escapes again the moment one joins it against
	// its own root.
	parent := t.TempDir()
	root := filepath.Join(parent, "repo")
	require.NoError(t, os.MkdirAll(root, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(parent, "outside.md"), []byte("x\n"), 0o644))

	for name, tc := range map[string]struct {
		path   string
		before bool
	}{
		"escapes through ..":     {path: "../outside.md"},
		"escapes and comes back": {path: "../repo/../outside.md"},
		"deep escape":            {path: "a/b/../../../outside.md"},
		"absolute":               {path: filepath.Join(parent, "outside.md")},
		"the root itself":        {path: "."},
		"the root with a slash":  {path: "./", before: true},
		"a bare separator":       {path: "/", before: true},
		"whitespace":             {path: " ", before: true},
		"just ..":                {path: "..", before: true},
	} {
		t.Run(name, func(t *testing.T) {
			before := map[string]bool{}
			if tc.before {
				before[tc.path] = true
			}

			events, err := observeErr(fakeObserved{root: root, paths: []string{tc.path}, before: before})

			assert.Empty(t, events, "%q must not become an event", tc.path)
			assert.ErrorIs(t, err, ErrPathNotRelativeToRoot)
		})
	}
}

func TestObserved_RefusedPathDoesNotSilenceTheRest(t *testing.T) {
	root := tree(t, "real.md")

	events, err := observeErr(fakeObserved{
		root:   root,
		paths:  []string{"../outside.md", "real.md"},
		before: map[string]bool{"real.md": true},
	})

	require.ErrorIs(t, err, ErrPathNotRelativeToRoot)
	require.Len(t, events, 1)
	assert.Equal(t, "real.md", events[0].Fields[FieldPath])
}

func TestObserved_ADirectoryIsNotAFile(t *testing.T) {
	// A path where a directory now sits stats fine. Reported, it hands every
	// rule written about a file something that is not one.
	root := tree(t, "pkg/inner.go")

	events, err := observeErr(fakeObserved{
		root:   root,
		paths:  []string{"pkg"},
		before: map[string]bool{"pkg": true},
	})

	assert.Empty(t, events)
	assert.ErrorIs(t, err, ErrPathIsNotAFile)
}

func TestObserved_PathIsReportedInOneCanonicalSpelling(t *testing.T) {
	// The event carries the cleaned path, so a matcher scoped to "a.md" and a
	// fingerprint keyed on it both see one file rather than two.
	root := tree(t, "dir/a.md")

	events := observe(t, fakeObserved{
		root:   root,
		paths:  []string{"./dir/./a.md"},
		before: map[string]bool{"./dir/./a.md": true},
	})

	require.Len(t, events, 1)
	assert.Equal(t, filepath.Join("dir", "a.md"), events[0].Fields[FieldPath])
}

// --- paths given with nothing to resolve them against ------------------------

func TestObserved_NoRootIsRefused(t *testing.T) {
	// Join("", "module.go") is "module.go", resolved against the process's
	// working directory — the exact dependence on the invocation site that
	// Root() exists to remove.
	for name, root := range map[string]string{"empty": "", "whitespace": "  "} {
		t.Run(name, func(t *testing.T) {
			events, err := observeErr(fakeObserved{
				root:   root,
				paths:  []string{"module.go"},
				before: map[string]bool{"module.go": true},
			})

			assert.Empty(t, events)
			assert.ErrorIs(t, err, ErrNoRoot)
		})
	}
}

// --- a producer that is not answering the baseline question (F5) -------------

func TestObserved_ProducerAlwaysAnsweringFalseSaysSo(t *testing.T) {
	// The lazy implementation: deletes vanish and updates become creates, and
	// no event is wrong on its face. The no/no row is the one place the tree
	// contradicts the claim, so it is where the degradation surfaces.
	root := tree(t, "kept.md")

	events, err := observeErr(fakeObserved{
		root:   root,
		paths:  []string{"kept.md", "removed.md"},
		before: nil, // always false
	})

	require.Len(t, events, 1, "the delete has nowhere to appear")
	assert.Equal(t, KindPostCreate, events[0].Kind)
	assert.ErrorIs(t, err, ErrNotADifference,
		"the vanished delete has to be audible somewhere")
}

func TestObserved_EveryNoNoRowIsReported(t *testing.T) {
	root := tree(t)

	events, err := observeErr(fakeObserved{
		root:   root,
		paths:  []string{"a.tmp", "b.tmp"},
		before: nil,
	})

	assert.Empty(t, events)
	require.ErrorIs(t, err, ErrNotADifference)
	assert.Contains(t, err.Error(), "a.tmp")
	assert.Contains(t, err.Error(), "b.tmp", "one bad path does not stop the audit of the next")
}

func TestObserved_ARepeatedPathIsJudgedOnceWhateverItIs(t *testing.T) {
	// Deduplication is on the path, not on the outcome: a path named three
	// times that turns out to be a producer error is one error, the same way a
	// path named three times that turns out to be a create is one event.
	root := tree(t)

	events, err := observeErr(fakeObserved{
		root:   root,
		paths:  []string{"scratch.tmp", "scratch.tmp", "./scratch.tmp"},
		before: nil,
	})

	assert.Empty(t, events)
	require.ErrorIs(t, err, ErrNotADifference)
	assert.Len(t, strings.Split(err.Error(), "\n"), 1, "one file, one complaint")
}

// --- nothing to report is nil, not an empty slice ----------------------------

func TestObserved_NothingToReportIsNil(t *testing.T) {
	// A caller appending to its own slice should not have to tell an empty
	// result from a present-but-empty one; a nil is unambiguous either way.
	events, err := observeErr(fakeObserved{root: tree(t), paths: nil})
	require.NoError(t, err)
	assert.Nil(t, events)

	events, err = observeErr(fakeObserved{root: tree(t), paths: []string{}})
	require.NoError(t, err)
	assert.Nil(t, events)

	// And when every path given was refused, so the slice was allocated and
	// then never appended to.
	events, err = observeErr(fakeObserved{root: tree(t), paths: []string{"../out.md", ""}})
	require.Error(t, err)
	assert.Nil(t, events, "an allocated-but-empty slice must not leak out")
}
