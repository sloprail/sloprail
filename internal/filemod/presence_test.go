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

func TestObserved_DiagnosticsNameTheRepositoryRelativePath(t *testing.T) {
	// Every error this module raises is read by someone debugging a producer,
	// and they all name the path the producer gave — except this one, which
	// named the absolute path it happened to stat. In a module whose premise is
	// that paths are repository-relative, the odd one out reads as a different
	// kind of thing than it is.
	root := tree(t, "locked/present.md")
	unreadable(t, filepath.Join(root, "locked"))

	_, err := observeErr(fakeObserved{
		root:   root,
		paths:  []string{"locked/present.md"},
		before: map[string]bool{filepath.FromSlash("locked/present.md"): true},
	})

	require.ErrorIs(t, err, ErrUnreadableTree)
	// The sentinel's own line names the relative path. The wrapped *PathError
	// still carries the absolute one, which is why the whole message is not
	// searched for the root.
	line, _, _ := strings.Cut(err.Error(), ": lstat")
	require.Contains(t, line, filepath.FromSlash("locked/present.md"))
	assert.NotContains(t, line, root, "the diagnostic leads with the relative path, as the others do")
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

// --- a symlinked parent directory is an escape too ---------------------------

// escapeTree builds a repository containing a symlink "escape" pointing at a
// directory outside it, with a secret sitting in that directory. Returns the
// repository root.
func escapeTree(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need a privilege this test will not assume on windows")
	}
	parent := t.TempDir()
	outside := filepath.Join(parent, "outside")
	require.NoError(t, os.MkdirAll(outside, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "id_rsa"), []byte("PRIVATE KEY\n"), 0o600))

	root := filepath.Join(parent, "repo")
	require.NoError(t, os.MkdirAll(root, 0o755))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "escape")))
	return root
}

func TestObserved_SymlinkedParentDirectoryIsRefused(t *testing.T) {
	// The worst version of the escape, and the one no string check can see.
	// "escape/id_rsa" is relative, has no "..", and cleans to itself — yet
	// filepath.Join(root, it) reads a private key the repository does not
	// contain. Worse than "../outside.md": that one at least emitted a spelling
	// no hook could resolve, so a hook joining it would fail. This one emits a
	// path that JOINS, and every consumer downstream reads the outside file
	// through it while believing it is looking inside the repository.
	root := escapeTree(t)

	events, err := observeErr(fakeObserved{root: root, paths: []string{"escape/id_rsa"}})

	assert.Empty(t, events, "a path resolving outside the repository must not become an event")
	assert.ErrorIs(t, err, ErrPathNotRelativeToRoot)
}

func TestObserved_SymlinkedParentEscapeIsRefusedAtEveryDepth(t *testing.T) {
	root := escapeTree(t)
	// The link's own directory, nested below it, and a path under it that is
	// not even on disk — a delete through the link escapes just as far.
	for name, path := range map[string]string{
		"directly under the link": "escape/id_rsa",
		"nested below the link":   "escape/deeper/still/secret.pem",
		"gone from behind it":     "escape/removed.md",
	} {
		t.Run(name, func(t *testing.T) {
			events, err := observeErr(fakeObserved{
				root:   root,
				paths:  []string{path},
				before: map[string]bool{path: true},
			})

			assert.Empty(t, events)
			assert.ErrorIs(t, err, ErrPathNotRelativeToRoot)
		})
	}
}

func TestObserved_SymlinkedParentEscapeDoesNotSilenceTheRest(t *testing.T) {
	root := escapeTree(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "real.md"), []byte("x\n"), 0o644))

	events, err := observeErr(fakeObserved{
		root:   root,
		paths:  []string{"escape/id_rsa", "real.md"},
		before: map[string]bool{"real.md": true},
	})

	require.ErrorIs(t, err, ErrPathNotRelativeToRoot)
	require.Len(t, events, 1)
	assert.Equal(t, "real.md", events[0].Fields[FieldPath])
}

func TestObserved_AHardLinkToAnOutsideInodeIsContained(t *testing.T) {
	// The decision, written down as a test because the doc's framing is broader
	// than what a path check can deliver.
	//
	// repo/hardlink and an outside file are ONE inode under two names, and the
	// name under the root is a real entry the repository holds and git tracks.
	// There is no link to follow and nothing in the path that leaves — so this
	// is contained, an event is emitted, and reading through it reads the shared
	// content. That is the same reach a symlinked parent had, arrived at by a
	// mechanism no path check can see: containment here is over PATHS, not over
	// inodes, and a check that refused this would be refusing an ordinary file
	// for a property it cannot observe, producing the silence instead.
	//
	// Out of scope on purpose rather than overlooked: an agent that can make a
	// hard link to a file outside the repository could as easily copy it in.
	if runtime.GOOS == "windows" {
		t.Skip("hard links need a privilege this test will not assume on windows")
	}
	parent := t.TempDir()
	root := filepath.Join(parent, "repo")
	require.NoError(t, os.MkdirAll(root, 0o755))
	outside := filepath.Join(parent, "id_rsa")
	require.NoError(t, os.WriteFile(outside, []byte("PRIVATE KEY\n"), 0o600))
	require.NoError(t, os.Link(outside, filepath.Join(root, "hardlink")))

	events := observe(t, fakeObserved{
		root:   root,
		paths:  []string{"hardlink"},
		before: nil,
	})

	require.Len(t, events, 1, "an entry the repository holds, under a name with nothing to follow")
	assert.Equal(t, KindPostCreate, events[0].Kind)
	assert.Equal(t, "hardlink", events[0].Fields[FieldPath])
}

func TestObserved_ALinkOutOfTheRepositoryIsStillAFileInIt(t *testing.T) {
	// The boundary the containment check must not cross. Where the link POINTS
	// is not the question — "escape" itself lives in the repository, git tracks
	// it, and changing it is a change to the repository. Only paths whose
	// PARENTS leave are refused, which is why lookAt keeps its Lstat.
	root := escapeTree(t)

	events := observe(t, fakeObserved{
		root:   root,
		paths:  []string{"escape"},
		before: nil,
	})

	require.Len(t, events, 1)
	assert.Equal(t, KindPostCreate, events[0].Kind)
	assert.Equal(t, "escape", events[0].Fields[FieldPath])
}

func TestObserved_ASymlinkedDirectoryStayingInsideIsFine(t *testing.T) {
	// Containment, not a ban on symlinked directories. A link to a directory
	// that is itself inside the repository resolves back under the root, so
	// nothing left and nothing is refused.
	root := tree(t, "real/inner.md")
	symlink(t, root, "alias", filepath.Join(root, "real"))

	events := observe(t, fakeObserved{
		root:   root,
		paths:  []string{"alias/inner.md"},
		before: map[string]bool{filepath.Join("alias", "inner.md"): true},
	})

	require.Len(t, events, 1)
	assert.Equal(t, KindPostUpdate, events[0].Kind)
}

func TestObserved_ARootReachedThroughASymlinkIsNotAnEscape(t *testing.T) {
	// The false positive a naive resolved-child-against-raw-root comparison
	// produces. /tmp is a link to /private/tmp on darwin, so a repository under
	// a symlinked ancestor is entirely ordinary — every path in it would be
	// called an escape if the root were not resolved too.
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need a privilege this test will not assume on windows")
	}
	actual := tree(t, "a.md")
	link := filepath.Join(t.TempDir(), "repo-link")
	require.NoError(t, os.Symlink(actual, link))

	events := observe(t, fakeObserved{
		root:   link,
		paths:  []string{"a.md"},
		before: map[string]bool{"a.md": true},
	})

	require.Len(t, events, 1)
	assert.Equal(t, KindPostUpdate, events[0].Kind)
}

func TestObserved_ASiblingSharingTheRootsPrefixIsNotInside(t *testing.T) {
	// Containment compares path elements, not string prefixes: "/x/repo-backup"
	// starts with "/x/repo" and is not under it.
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need a privilege this test will not assume on windows")
	}
	parent := t.TempDir()
	root := filepath.Join(parent, "repo")
	sibling := filepath.Join(parent, "repo-backup")
	require.NoError(t, os.MkdirAll(root, 0o755))
	require.NoError(t, os.MkdirAll(sibling, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sibling, "secret.md"), []byte("x\n"), 0o644))
	require.NoError(t, os.Symlink(sibling, filepath.Join(root, "near")))

	events, err := observeErr(fakeObserved{root: root, paths: []string{"near/secret.md"}})

	assert.Empty(t, events)
	assert.ErrorIs(t, err, ErrPathNotRelativeToRoot)
}

func TestResolve_TheLexicalChecksStillDoTheirOwnWork(t *testing.T) {
	// Containment now rejects "..", "." and the rest as a side effect, so the
	// lexical checks above it stopped being what refuses them and nothing
	// noticed. Deleting either one left every test passing.
	//
	// Which is not the same as their being redundant. Containment answers a
	// different question and answers these badly: it reports "." — the root
	// itself — as resolving OUTSIDE the repository, which is false and would
	// send whoever read it looking for an escape that is not there. It also
	// names an absolute path, the exact leak F4 fixed everywhere else. So the
	// messages are what this pins, not just the refusal: these spellings are
	// wrong on their face, they are settled before any syscall, and they say
	// what is actually wrong with them.
	root := tree(t, "a.md")

	for name, tc := range map[string]struct {
		path, says string
		// echoesRoot: the input itself is an absolute path, so the diagnostic
		// quoting it back necessarily contains the root. Nothing leaked.
		echoesRoot bool
	}{
		"escapes through ..":    {path: "../outside.md", says: "escapes it"},
		"just ..":               {path: "..", says: "escapes it"},
		"deep escape":           {path: "a/b/../../../outside.md", says: "escapes it"},
		"the root itself":       {path: ".", says: "names the root"},
		"the root with a slash": {path: "./", says: "names the root"},
		"a bare separator":      {path: "/", says: "is absolute"},
		"an absolute path":      {path: filepath.Join(root, "a.md"), says: "is absolute", echoesRoot: true},
		"names no file":         {path: "", says: "names no file"},
		"whitespace only":       {path: " ", says: "names no file"},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := resolve(root, tc.path)

			require.ErrorIs(t, err, ErrPathNotRelativeToRoot)
			assert.Contains(t, err.Error(), tc.says,
				"the lexical check names what is wrong; containment's generic message does not")
			if !tc.echoesRoot {
				assert.NotContains(t, err.Error(), root,
					"settled without the filesystem, so no absolute path in the diagnostic")
			}
		})
	}
}

func TestObserved_ADanglingSymlinkedParentIsRefused(t *testing.T) {
	// The escape the walk-up let through, and the reason "the ancestors that do
	// not exist cannot be symlinks" was false: a dangling symlink is an ancestor
	// that does not exist and IS a symlink.
	//
	// EvalSymlinks answers ENOENT for it exactly as it does for a name with
	// nothing at it, so a walk reading that as "keep going up" strips "escape"
	// into the unresolved remainder, resolves the root, and re-joins the
	// remainder — having followed nothing. The path comes out contained.
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need a privilege this test will not assume on windows")
	}
	parent := t.TempDir()
	root := filepath.Join(parent, "repo")
	require.NoError(t, os.MkdirAll(root, 0o755))

	outside := filepath.Join(parent, "outside")
	// Deliberately NOT created yet. That is the whole attack: absent at
	// classification time, so the ancestor looks like one of the missing
	// parents a delete legitimately names.
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "escape")))

	events, err := observeErr(fakeObserved{
		root:   root,
		paths:  []string{"escape/id_rsa"},
		before: map[string]bool{filepath.FromSlash("escape/id_rsa"): true},
	})

	assert.Empty(t, events, "the emitted path joins onto the root and reads whatever lands at the target")
	require.ErrorIs(t, err, ErrPathNotRelativeToRoot)

	// And the target arriving is what makes it live. An agent that creates a
	// link creates its target moments later; the event was already out by then,
	// carrying a path that now resolves to the secret.
	require.NoError(t, os.MkdirAll(outside, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "id_rsa"), []byte("PRIVATE KEY\n"), 0o600))

	for _, e := range events {
		p, ok := e.Fields[FieldPath].(string)
		require.True(t, ok)
		b, readErr := os.ReadFile(filepath.Join(root, p))
		require.Error(t, readErr, "no emitted path may read %q", string(b))
	}
}

func TestResolve_ADanglingSymlinkedParentIsRefusedAtEveryDepth(t *testing.T) {
	// Nested below the dangling link, and several links deep. Every one of these
	// reaches contained's ENOENT branch with a symlink sitting in the walk.
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need a privilege this test will not assume on windows")
	}
	parent := t.TempDir()
	root := filepath.Join(parent, "repo")
	require.NoError(t, os.MkdirAll(root, 0o755))
	require.NoError(t, os.Symlink(filepath.Join(parent, "outside"), filepath.Join(root, "escape")))
	// A link to the dangling link: resolving it fails one hop further along.
	require.NoError(t, os.Symlink(filepath.Join(root, "escape"), filepath.Join(root, "hop")))

	for name, path := range map[string]string{
		"directly under the dangling link": "escape/id_rsa",
		"nested below it":                  "escape/deeper/still/secret.pem",
		"through a link to the link":       "hop/id_rsa",
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := resolve(root, path)
			require.ErrorIs(t, err, ErrPathNotRelativeToRoot,
				"a link whose target does not exist yet still has to be followed before it is skipped")
		})
	}
}

func TestResolve_AGenuinelyMissingParentIsStillWalkedPast(t *testing.T) {
	// The other side of the Lstat. Refusing every unresolvable ancestor would be
	// easy and would break the case the walk exists for: a delete names a file
	// that is gone, and a removed directory takes its parents with it. Nothing
	// is at these names at all — no link, nothing to follow — so the walk goes
	// up and containment is settled against the ancestor that does exist.
	root := tree(t)

	events := observe(t, fakeObserved{
		root:   root,
		paths:  []string{"removed/deeply/nested/a.md"},
		before: map[string]bool{filepath.FromSlash("removed/deeply/nested/a.md"): true},
	})

	require.Len(t, events, 1)
	assert.Equal(t, KindPostDelete, events[0].Kind)
	assert.Equal(t, filepath.FromSlash("removed/deeply/nested/a.md"), events[0].Fields[FieldPath])
}

func TestResolve_ContainmentComesFromTheResolvedAncestorItself(t *testing.T) {
	// What replaced the unresolved remainder, and why it could go.
	//
	// The walk used to carry the skipped ancestors along and re-join them onto
	// the one that resolved. That could never change an answer: full arrives
	// cleaned, so the skipped elements are ordinary names and the rejoin only
	// DESCENDS — which neither leaves a contained directory nor re-enters one
	// that escapes. Both ways of getting it wrong were therefore invisible, so
	// what is pinned here is the property that made them invisible, rather than
	// a remainder no behaviour depends on.
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need a privilege this test will not assume on windows")
	}
	parent := t.TempDir()
	root := filepath.Join(parent, "repo")
	outside := filepath.Join(parent, "outside")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "real"), 0o755))
	require.NoError(t, os.MkdirAll(outside, 0o755))
	require.NoError(t, os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "alias")))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "away")))

	realRoot, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	realOutside, err := filepath.EvalSymlinks(outside)
	require.NoError(t, err)

	// Descending from a contained ancestor stays contained, at any depth.
	assert.NoError(t, under(realRoot, realRoot))
	assert.NoError(t, under(realRoot, filepath.Join(realRoot, "real")))
	assert.NoError(t, under(realRoot, filepath.Join(realRoot, "real", "one", "two", "three")))

	// And descending from one that escapes never comes back.
	assert.Error(t, under(realRoot, realOutside))
	assert.Error(t, under(realRoot, filepath.Join(realOutside, "one", "two", "three")))

	// Which is what lets the walk answer from the resolved ancestor alone: a
	// missing path below a contained link is contained however deep it goes,
	// and one below an escaping link escapes however deep it goes.
	_, _, err = resolve(root, "alias/one/two/a.md")
	assert.NoError(t, err, "missing, but under a link that stays inside")

	_, _, err = resolve(root, "away/one/two/a.md")
	assert.ErrorIs(t, err, ErrPathNotRelativeToRoot, "missing, and under a link that leaves")
}

func TestUnder_ADotDotPrefixedDirectoryIsNotAnEscape(t *testing.T) {
	// The same lexical anchor as resolve's, in a second copy that resolve's test
	// never reaches: that one's files sit at the repository root, so under() is
	// only ever asked about paths whose first element has no dots. Here the
	// ".."-prefixed element is a real DIRECTORY, which is what puts it into the
	// rel under() computes.
	//
	// Loosened to HasPrefix(rel, ".."), this refuses "..hidden/a.md" — a real
	// file changing in the tree, producing no event at all.
	root := tree(t, "..hidden/a.md", "...cache/b.md")

	for name, path := range map[string]string{
		"two dots then a name": "..hidden/a.md",
		"three dots":           "...cache/b.md",
	} {
		t.Run(name, func(t *testing.T) {
			events := observe(t, fakeObserved{
				root:   root,
				paths:  []string{path},
				before: map[string]bool{filepath.FromSlash(path): true},
			})

			require.Len(t, events, 1, "%q is a directory name, not an escape", path)
			assert.Equal(t, KindPostUpdate, events[0].Kind)
		})
	}

	// And directly, so the anchor is pinned on the function that holds it
	// rather than only through everything above it.
	realRoot, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	assert.NoError(t, under(realRoot, filepath.Join(realRoot, "..hidden")),
		`"..hidden" is one element that begins with dots, not a step above the root`)
	assert.Error(t, under(realRoot, filepath.Dir(realRoot)),
		"the anchor still has to catch the real thing")
}

func TestResolve_DotDotIsFoldedBeforeAnySymlinkIsFollowed(t *testing.T) {
	// The Clean-versus-symlink mismatch, stated as behaviour rather than left
	// as a surprise. Clean folds ".." lexically: "sub/escape/../id_rsa" becomes
	// "sub/id_rsa" before the filesystem is consulted, while a kernel open()
	// follows "escape" out and steps up from where it LANDED — two different
	// files for one spelling.
	//
	// What is pinned is that the folded path is the one classified AND the one
	// emitted, so every consumer reads the file this module judged, and that
	// file is inside the repository. The cost is a missed event for the file
	// the kernel would have reached, never a path pointing out.
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need a privilege this test will not assume on windows")
	}
	parent := t.TempDir()
	root := filepath.Join(parent, "repo")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "sub"), 0o755))
	outside := filepath.Join(parent, "outside")
	require.NoError(t, os.MkdirAll(outside, 0o755))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "sub", "escape")))

	// What the kernel would reach through the link, and what the fold reaches.
	require.NoError(t, os.WriteFile(filepath.Join(parent, "id_rsa"), []byte("PRIVATE KEY\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "sub", "id_rsa"), []byte("inside\n"), 0o644))

	clean, _, err := resolve(root, "sub/escape/../id_rsa")

	require.NoError(t, err)
	assert.Equal(t, filepath.Join("sub", "id_rsa"), clean,
		`".." is folded lexically, so the link is never followed`)

	// And the emitted path reads the in-repository file — the fold points
	// inward here, which is why this is a wrong-file bug and not a disclosure.
	b, readErr := os.ReadFile(filepath.Join(root, clean))
	require.NoError(t, readErr)
	assert.Equal(t, "inside\n", string(b), "never the outside file the kernel would have opened")
}

func TestUnder_AnUnanswerableComparisonIsRefusedNotAllowed(t *testing.T) {
	// The Rel error branch, which no input reaching under() through contained()
	// can produce — both arguments come from EvalSymlinks, which preserves
	// absoluteness, so they never disagree. It is therefore called directly.
	//
	// The point is the DIRECTION of the answer. A comparison that cannot be made
	// is not a comparison that succeeded, and the one thing containment must
	// never do is let a path through on a check that did not answer.
	err := under("/absolute/root", "relative/path")

	require.Error(t, err, "an unanswerable comparison is not containment")
	assert.NotContains(t, err.Error(), "relative/path", "and it does not echo the path back")
}

func TestUnder_ComparisonIsCaseSensitive(t *testing.T) {
	// A documented false negative, pinned so it is a decision rather than a
	// surprise. On darwin's case-insensitive default, /parent/REPO and
	// /parent/repo are one directory the kernel cannot tell apart, and this
	// refuses the second as an escape — costing an event for a real change.
	//
	// It is the direction chosen on purpose: erring toward refusal never emits
	// a path that should not have been emitted, while folding case would let
	// "REPO" pass for "repo" on a case-SENSITIVE filesystem where they are two
	// different directories. Both are wrong somewhere; only one is wrong in the
	// direction that discloses.
	assert.Error(t, under("/parent/REPO", "/parent/repo/sub"),
		"case is not folded, and the cost is a missed event rather than a leaked one")
	assert.NoError(t, under("/parent/repo", "/parent/repo/sub"))
}

func TestContained_AnUnresolvableRootIsRefused(t *testing.T) {
	// The root gets the same rule as the ancestors: containment that cannot be
	// established is refused. Substituting the raw root for the resolved one
	// makes every path under an unresolvable root come out contained, which is
	// a containment claim made from nothing.
	root := filepath.Join(t.TempDir(), "not-a-repository")

	err := contained(root, filepath.Join(root, "a.md"))

	require.Error(t, err, "a root that does not resolve cannot contain anything")
	assert.Contains(t, err.Error(), "cannot resolve the repository root")
}

func TestResolve_ContainmentDiagnosticsNameNoAbsolutePath(t *testing.T) {
	// F4's rule, which the containment errors were exempt from: every
	// diagnostic this module raises names the repository-relative path the
	// producer gave. These named the resolved absolute path instead — the
	// spelling no hook can use, in the module whose premise is that paths are
	// relative to a root, and it discloses the filesystem layout OUTSIDE the
	// repository to whoever reads the log.
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need a privilege this test will not assume on windows")
	}
	root := escapeTree(t)
	outside := filepath.Join(filepath.Dir(root), "outside")

	// A dangling link, an unreadable ancestor and a nonexistent root alongside
	// the resolved escape, so every branch that formats a message is covered.
	require.NoError(t, os.Symlink(filepath.Join(filepath.Dir(root), "not-yet"), filepath.Join(root, "dangling")))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "locked", "inner"), 0o755))
	unreadable(t, filepath.Join(root, "locked"))

	for name, tc := range map[string]struct{ root, path string }{
		"resolves outside":       {root: root, path: "escape/id_rsa"},
		"dangling link ancestor": {root: root, path: "dangling/id_rsa"},
		"unresolvable ancestor":  {root: root, path: "locked/inner/a.md"},
		"unresolvable root":      {root: filepath.Join(root, "no-such-root"), path: "a.md"},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := resolve(tc.root, tc.path)

			require.ErrorIs(t, err, ErrPathNotRelativeToRoot)
			assert.Contains(t, err.Error(), tc.path, "the diagnostic names the path the producer gave")

			// This module's own sentences are what it controls, and they are what
			// this pins — the same split TestObserved_DiagnosticsNameTheRepositoryRelativePath
			// draws around lookAt's wrapped *PathError. A syscall error that
			// this code wraps rather than writes still carries the absolute path
			// it failed on, and dropping it would leave someone debugging a
			// producer with an error naming nothing they could act on.
			line, _, _ := strings.Cut(err.Error(), ": lstat")
			assert.NotContains(t, line, outside,
				"this module's own sentence must not disclose the layout outside the repository")
			assert.NotContains(t, line, root,
				"the relative path is the spelling every other diagnostic here uses")
		})
	}
}

func TestResolve_TheOutsideTargetIsNeverNamedAtAll(t *testing.T) {
	// The disclosure that matters most, and the one no wrapped syscall error
	// excuses: where a link POINTS. "escape/id_rsa" resolves cleanly — there is
	// no *PathError here, only this module's own sentence — and naming the
	// resolved target in it hands a log reader the filesystem layout outside
	// the repository, which is the thing the refusal exists to keep out.
	root := escapeTree(t)
	outside := filepath.Join(filepath.Dir(root), "outside")

	_, _, err := resolve(root, "escape/id_rsa")

	require.ErrorIs(t, err, ErrPathNotRelativeToRoot)
	assert.NotContains(t, err.Error(), outside, "the whole message, not just its first line")
	assert.NotContains(t, err.Error(), root)
	assert.Contains(t, err.Error(), "escape/id_rsa")
}

func TestResolve_ADanglingLinksTargetIsNeverNamed(t *testing.T) {
	// The same disclosure, reached through the branch that has a syscall error
	// in hand and could excuse itself by wrapping it. EvalSymlinks fails on a
	// dangling link by naming the link's TARGET — which is the escape's
	// destination, chosen by whoever made the link, and the one string here
	// that is both outside the repository and attacker-controlled. Wrapping it
	// prints the escape's destination as the reward for refusing it.
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need a privilege this test will not assume on windows")
	}
	parent := t.TempDir()
	root := filepath.Join(parent, "repo")
	require.NoError(t, os.MkdirAll(root, 0o755))
	target := filepath.Join(parent, "secret-location", "deeper")
	require.NoError(t, os.Symlink(target, filepath.Join(root, "escape")))

	_, _, err := resolve(root, "escape/id_rsa")

	require.ErrorIs(t, err, ErrPathNotRelativeToRoot)
	assert.NotContains(t, err.Error(), target, "the whole message")
	assert.NotContains(t, err.Error(), "secret-location",
		"not even the directory name the link was pointed at")
	assert.NotContains(t, err.Error(), root)
	assert.Contains(t, err.Error(), "escape", "the ancestor that is the link is named; it is inside")
}

func TestResolve_ContainmentRefusesWhatItCannotEstablish(t *testing.T) {
	// A parent that cannot be resolved is not a parent that is contained.
	//
	// The unreadable directory has to be the GRANDparent: chmod 000 on a
	// directory blocks reading its contents, not lstat'ing the directory
	// itself, so a link sitting directly inside it still resolves fine and
	// containment is legitimately established. One level up is what actually
	// stops EvalSymlinks from answering about the parent.
	//
	// Whether that parent is a symlink out of the repository is now
	// unknowable, and unknowable is not contained. Letting it through would
	// mean a path whose containment was never established going on to become
	// an event — the one outcome this check exists to prevent.
	root := tree(t)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "locked", "inner"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "locked", "inner", "a.md"), []byte("x\n"), 0o644))
	unreadable(t, filepath.Join(root, "locked"))

	_, _, err := resolve(root, "locked/inner/a.md")

	require.ErrorIs(t, err, ErrPathNotRelativeToRoot,
		"containment that cannot be established is refused, not assumed")
	assert.Contains(t, err.Error(), "cannot resolve")
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

func TestObserved_ADotDotPrefixIsNotAnEscape(t *testing.T) {
	// The escape check is anchored to the separator, and it has to be: a leading
	// ".." is only an escape when it is the WHOLE first element. "..hidden.md"
	// and "...config" are ordinary filenames, and a check loosened to
	// HasPrefix(clean, "..") refuses them — a real file changing in the tree
	// producing no event at all, which is the silence this module exists to
	// prevent, reached from the opposite direction.
	root := tree(t, "..hidden.md", "...config", "dir/..also-fine")

	for name, path := range map[string]string{
		"two dots then a name":   "..hidden.md",
		"three dots":             "...config",
		"nested, dots in a name": "dir/..also-fine",
	} {
		t.Run(name, func(t *testing.T) {
			events := observe(t, fakeObserved{
				root:   root,
				paths:  []string{path},
				before: map[string]bool{filepath.FromSlash(path): true},
			})

			require.Len(t, events, 1, "%q is a filename, not an escape", path)
			assert.Equal(t, KindPostUpdate, events[0].Kind)
			assert.Equal(t, filepath.FromSlash(path), events[0].Fields[FieldPath])
		})
	}
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
		before: map[string]bool{filepath.Join("dir", "a.md"): true},
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

// --- the canonical-spelling contract is checked, not trusted -----------------

func TestObserved_ABaselineKeyedOnTheRawSpellingSaysSo(t *testing.T) {
	// The contract term that had no enforcement and no detection. The producer
	// keyed its baseline on its own uncleaned paths, so asked about the
	// canonical "dir/a.md" it answers false for a file that WAS at the baseline
	// — and the update ships as a create with the tree agreeing completely.
	//
	// ErrNotADifference does not reach it: these paths are on disk, so the no/no
	// row never comes up. The one guard against silent baseline degradation did
	// not cover the failure mode the canonical-spelling rule itself created.
	root := tree(t, "dir/a.md")

	events, err := observeErr(fakeObserved{
		root:   root,
		paths:  []string{"./dir/./a.md"},
		before: map[string]bool{"./dir/./a.md": true}, // uncleaned key
	})

	assert.Empty(t, events, "which of the two answers is true is exactly what is in doubt")
	require.ErrorIs(t, err, ErrBaselineKeyedOnRawSpelling)
	assert.Contains(t, err.Error(), "dir/a.md")
}

func TestObserved_EveryDeleteVanishingIsTheSameBreach(t *testing.T) {
	// The shape the breach actually takes in a producer: a whole cycle's worth
	// of paths keyed raw. Without the check the deletes simply do not appear
	// and the updates come out as creates — no error anywhere.
	root := tree(t, "kept.md")

	events, err := observeErr(fakeObserved{
		root:  root,
		paths: []string{"./kept.md", "./gone.md"},
		before: map[string]bool{
			"./kept.md": true,
			"./gone.md": true,
		},
	})

	assert.Empty(t, events)
	require.ErrorIs(t, err, ErrBaselineKeyedOnRawSpelling)
	assert.Contains(t, err.Error(), "kept.md")
	assert.Contains(t, err.Error(), "gone.md", "one breached path does not stop the audit of the next")
}

func TestObserved_ACleanedBaselineIsNotAccused(t *testing.T) {
	// The check costs a second call on non-canonical spellings and must cost
	// nothing else. A producer that cleans its keys, as the contract asks, gets
	// agreement both ways and is classified normally — including when the raw
	// spelling it gave was not canonical.
	root := tree(t, "dir/a.md")

	events := observe(t, fakeObserved{
		root:   root,
		paths:  []string{"./dir/./a.md"},
		before: map[string]bool{filepath.Join("dir", "a.md"): true}, // cleaned, as asked
	})

	require.Len(t, events, 1)
	assert.Equal(t, KindPostUpdate, events[0].Kind)
	assert.Equal(t, filepath.Join("dir", "a.md"), events[0].Fields[FieldPath])
}

func TestObserved_AgreementOnBothSpellingsIsNotABreach(t *testing.T) {
	// Both spellings answering false is agreement, not a breach: a genuinely new
	// file named with a non-canonical spelling is a create, and the check must
	// not turn every one of those into an error.
	root := tree(t, "dir/new.md")

	events := observe(t, fakeObserved{
		root:   root,
		paths:  []string{"./dir/./new.md"},
		before: nil,
	})

	require.Len(t, events, 1)
	assert.Equal(t, KindPostCreate, events[0].Kind)
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
