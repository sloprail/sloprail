package filemod

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/module"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scannerTree lays out root/scanners/x/{scanner.yaml,notes/extra.md} — a
// directory holding a guarded file at depth one and another one level deeper —
// and returns root.
func scannerTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "scanners", "x", "notes"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "scanners", "x", "scanner.yaml"), []byte("active: true\nkeywords:\n  - a\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "scanners", "x", "notes", "extra.md"), []byte("# extra\n"), 0o644))
	return root
}

// TestExtractCommand_RemovingADirectoryRecursivelyDeletesEveryFileInIt is the
// hole this closes, measured on a real run: refused by a coverage gate over
// scanners/x/scanner.yaml, an agent ran `rm -rf scanners/x` and the rule
// guarding that file never saw a deletion, because the line named a directory
// and a directory is not a file. Every file under it now gets its own
// PreFileDelete, carrying the bytes about to be lost, at its canonical path.
// sr:proves events/recursive-removal-per-file
func TestExtractCommand_RemovingADirectoryRecursivelyDeletesEveryFileInIt(t *testing.T) {
	root := scannerTree(t)
	dir := filepath.Join(root, "scanners", "x")

	for _, command := range []string{
		"rm -rf " + dir,
		"rm -r " + dir,
		"rm -R " + dir,
		"rm -fR " + dir,
		"rm --recursive --force " + dir,
		// GNU getopt accepts any unambiguous abbreviation of a long option, and
		// --recursive is the only rm long option starting with r.
		"rm --rec -f " + dir,
		"rm --recur " + dir,
		"rm --r " + dir,
		// git rm -r deletes the directory's files from the working tree too.
		"git rm -r " + dir,
		"git rm -rf " + dir,
		"git mv " + dir + " " + filepath.Join(root, "moved"),
		"rm -rf " + dir + "/",
		"mv " + dir + " " + filepath.Join(root, "elsewhere"),
	} {
		events, err := extractForIn(t, command, root)
		require.NoError(t, err, "command %q", command)
		got := kindsByPath(events)
		assert.Equal(t, KindPreDelete, got["scanners/x/scanner.yaml"], "command %q: %v", command, got)
		assert.Equal(t, KindPreDelete, got["scanners/x/notes/extra.md"], "command %q: a nested file goes too: %v", command, got)
		assert.NotContains(t, got, "scanners/x", "command %q: the directory itself is not a file", command)

		for _, e := range events {
			if e.Fields[FieldPath] == "scanners/x/scanner.yaml" {
				assert.Equal(t, "active: true\nkeywords:\n  - a\n", e.Fields[FieldOldContent],
					"command %q: a delete carries the bytes about to be lost", command)
			}
		}
	}
}

// TestExtractCommand_ACdPrefixedDirectoryRemovalIsResolved holds the form an
// agent actually writes — `cd scanners && rm -rf x` — to the same answer.
func TestExtractCommand_ACdPrefixedDirectoryRemovalIsResolved(t *testing.T) {
	root := scannerTree(t)
	events, err := extractForIn(t, "cd "+filepath.Join(root, "scanners")+" && rm -rf x", root)
	require.NoError(t, err)
	assert.Equal(t, KindPreDelete, kindsByPath(events)["scanners/x/scanner.yaml"], "%v", kindsByPath(events))
}

// TestExtractCommand_ANonRecursiveRemoveOfADirectoryDeletesNothing is the
// reverse: plain `rm dir` fails on a directory and removes no file, so it must
// not be reported as removing the files inside — a rule refusing it would be
// refusing a command that changes nothing.
// sr:proves events/recursive-removal-per-file
func TestExtractCommand_ANonRecursiveRemoveOfADirectoryDeletesNothing(t *testing.T) {
	root := scannerTree(t)
	for _, command := range []string{
		"rm " + filepath.Join(root, "scanners", "x"),
		"rm -f " + filepath.Join(root, "scanners", "x"),
		"rm -- -r " + filepath.Join(root, "scanners", "x"),
	} {
		events, err := extractForIn(t, command, root)
		require.NoError(t, err, "command %q", command)
		assert.NotContains(t, kindsByPath(events), "scanners/x/scanner.yaml", "command %q", command)
	}
}

// TestExtractCommand_ARecursiveRemoveDoesNotFollowALinkedDirectory holds the
// walk to what rm -r itself does: a link to a directory is removed as a link,
// and nothing in what it points at is reported.
// sr:proves events/recursive-removal-per-file
func TestExtractCommand_ARecursiveRemoveDoesNotFollowALinkedDirectory(t *testing.T) {
	root := scannerTree(t)
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "kept.md"), []byte("kept\n"), 0o644))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "scanners", "x", "link")))

	events, err := extractForIn(t, "rm -rf "+filepath.Join(root, "scanners", "x"), root)
	require.NoError(t, err)
	got := kindsByPath(events)
	assert.Equal(t, KindPreDelete, got["scanners/x/scanner.yaml"])
	assert.NotContains(t, got, "scanners/x/link/kept.md", "a linked directory's contents are not removed by rm -r")
}

// TestExtractCommand_ATooLargeDirectoryIsNamedNotExpanded bounds the walk: one
// removal of a huge tree must not read every file inside a hook. Past the bound
// the directory is a named problem and predicts nothing.
// sr:proves events/recursive-removal-per-file
func TestExtractCommand_ATooLargeDirectoryIsNamedNotExpanded(t *testing.T) {
	root := t.TempDir()
	big := filepath.Join(root, "big")
	require.NoError(t, os.Mkdir(big, 0o755))
	for i := 0; i <= maxRemovedDirectoryFiles; i++ {
		require.NoError(t, os.WriteFile(filepath.Join(big, fmt.Sprintf("f%04d", i)), nil, 0o644))
	}

	events, err := extractForIn(t, "rm -rf "+big, root)
	assert.Empty(t, events)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrRemovedDirectoryTooLarge), "%v", err)
}

// TestExtractCommand_AnUnreadableSubdirectoryHidesOnlyItself: one subdirectory
// the walk cannot read must not hide every other file in the removed
// directory. `rm -rf scanners/x` still deletes scanners/x/scanner.yaml when
// scanners/x/zz is unreadable, so the prediction keeps what it found, and the
// part it could not read is named as a problem.
// sr:proves events/recursive-removal-per-file
func TestExtractCommand_AnUnreadableSubdirectoryHidesOnlyItself(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a 0o000 directory anyway")
	}
	root := scannerTree(t)
	locked := filepath.Join(root, "scanners", "x", "zz")
	require.NoError(t, os.Mkdir(locked, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(locked, "hidden.md"), []byte("h\n"), 0o644))
	require.NoError(t, os.Chmod(locked, 0o000))
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	events, err := extractForIn(t, "rm -rf "+filepath.Join(root, "scanners", "x"), root)
	got := kindsByPath(events)
	assert.Equal(t, KindPreDelete, got["scanners/x/scanner.yaml"], "the readable file is still predicted: %v", got)
	assert.Equal(t, KindPreDelete, got["scanners/x/notes/extra.md"], "a readable sibling subtree is still predicted: %v", got)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrUnreadableTree), "the unreadable subtree is named: %v", err)
	assert.Contains(t, err.Error(), "zz")
}

// TestExtractCommand_ExactlyTheFileBoundStillExpands is the boundary of the
// count bound: a directory holding exactly maxRemovedDirectoryFiles files is
// predicted in full; one more is not (TestExtractCommand_ATooLargeDirectoryIsNamedNotExpanded).
func TestExtractCommand_ExactlyTheFileBoundStillExpands(t *testing.T) {
	root := t.TempDir()
	big := filepath.Join(root, "big")
	require.NoError(t, os.Mkdir(big, 0o755))
	for i := 0; i < maxRemovedDirectoryFiles; i++ {
		require.NoError(t, os.WriteFile(filepath.Join(big, fmt.Sprintf("f%04d", i)), nil, 0o644))
	}

	events, err := extractForIn(t, "rm -rf "+big, root)
	require.NoError(t, err)
	assert.Len(t, events, maxRemovedDirectoryFiles)
}

// countReads swaps the module's one content read for a counting wrapper, and
// returns the count.
func countReads(t *testing.T) *int {
	t.Helper()
	n := 0
	orig := readContent
	readContent = func(path string, limit int64) (string, bool) {
		n++
		return orig(path, limit)
	}
	t.Cleanup(func() { readContent = orig })
	return &n
}

// deleteEvents indexes a removal's PreFileDelete events by path.
func deleteEvents(t *testing.T, events []event.Event) map[string]FileEvent {
	t.Helper()
	out := map[string]FileEvent{}
	for _, e := range events {
		if e.Kind != KindPreDelete {
			continue
		}
		f, err := FromEvent(e)
		require.NoError(t, err)
		out[f.Path] = f
	}
	return out
}

// TestExtractCommand_PastTheByteBudgetEveryFileIsPredictedUnread bounds what the
// expansion READS without dropping what it PREDICTS. Every file of a directory
// is still a PreFileDelete — a rule matching the path must still fire — but a
// file that does not fit in what is left of the byte budget carries no bytes
// and oldContentKnown false, is never read, and charges nothing: the smaller
// files after it are still read. Dropping the whole directory, as this once
// did, let one padding file hide the removal from every PreFileDelete gate;
// and stopping every read once a big file had spent the budget blinded content
// rules to the small guarded files sorting after it.
// sr:proves events/recursive-removal-per-file
func TestExtractCommand_PastTheByteBudgetEveryFileIsPredictedUnread(t *testing.T) {
	old := maxRemovedDirectoryBytes
	maxRemovedDirectoryBytes = 64
	t.Cleanup(func() { maxRemovedDirectoryBytes = old })
	reads := countReads(t)

	root := t.TempDir()
	dir := filepath.Join(root, "big")
	require.NoError(t, os.Mkdir(dir, 0o755))
	// Walked in lexical order: a.md fits, b.bin does not, c.md fits after it.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.md"), []byte("small\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.bin"), make([]byte, 100), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "c.md"), []byte("after\n"), 0o644))

	events, err := extractForIn(t, "rm -rf "+dir, root)
	require.NoError(t, err)
	got := deleteEvents(t, events)
	require.Len(t, got, 3, "every file is predicted: %v", got)
	assert.True(t, got["big/a.md"].OldContentKnown)
	assert.Equal(t, "small\n", got["big/a.md"].OldContent)
	assert.False(t, got["big/b.bin"].OldContentKnown, "b.bin does not fit the budget")
	assert.Empty(t, got["big/b.bin"].OldContent)
	assert.True(t, got["big/c.md"].OldContentKnown, "a small file after the big one is still read")
	assert.Equal(t, "after\n", got["big/c.md"].OldContent)
	assert.Equal(t, 2, *reads, "the two files that fit are read; the big one never is")

	// Against the real budget: a 9 MiB sparse asset sorting FIRST, beside a
	// small spec — the spec is still read.
	maxRemovedDirectoryBytes = old
	*reads = 0
	pad := filepath.Join(root, "pad")
	require.NoError(t, os.Mkdir(pad, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pad, "0big.bin"), nil, 0o644))
	require.NoError(t, os.Truncate(filepath.Join(pad, "0big.bin"), 9<<20))
	require.NoError(t, os.WriteFile(filepath.Join(pad, "spec.md"), []byte("# spec\n"), 0o644))
	events, err = extractForIn(t, "rm -rf "+pad, root)
	require.NoError(t, err)
	got = deleteEvents(t, events)
	require.Contains(t, got, "pad/spec.md", "the spec's removal is predicted: %v", got)
	assert.False(t, got["pad/0big.bin"].OldContentKnown)
	assert.True(t, got["pad/spec.md"].OldContentKnown, "the spec after the big asset is read")
	assert.Equal(t, "# spec\n", got["pad/spec.md"].OldContent)
	assert.Equal(t, 1, *reads)
}

// TestExtractCommand_AnOversizeFileIsPredictedUnread: one file larger than a
// delete read takes is predicted with oldContentKnown false, not read whole.
// sr:proves events/unknown-bytes-are-flagged
func TestExtractCommand_AnOversizeFileIsPredictedUnread(t *testing.T) {
	reads := countReads(t)
	root := t.TempDir()
	big := filepath.Join(root, "big.bin")
	require.NoError(t, os.WriteFile(big, nil, 0o644))
	require.NoError(t, os.Truncate(big, MaxDeleteReadBytes+1))

	events, err := extractForIn(t, "rm "+big, root)
	require.NoError(t, err)
	got := deleteEvents(t, events)
	require.Contains(t, got, "big.bin")
	assert.False(t, got["big.bin"].OldContentKnown)
	assert.Empty(t, got["big.bin"].OldContent)
	assert.Equal(t, 1, *reads, "one read attempt, refused on the size before any byte")
}

// TestExtractCommand_ALinkToAFIFOOrADeviceIsNotRead: a removal of a link to a
// FIFO used to block the hook forever (the read waits for a writer), and one to
// /dev/zero read without end. Both are predicted as deletes of the link, with
// no bytes — and both must return promptly.
// sr:proves events/unknown-bytes-are-flagged
func TestExtractCommand_ALinkToAFIFOOrADeviceIsNotRead(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "d")
	require.NoError(t, os.Mkdir(dir, 0o755))
	fifo := filepath.Join(root, "fifo")
	require.NoError(t, syscall.Mkfifo(fifo, 0o644))
	require.NoError(t, os.Symlink(fifo, filepath.Join(dir, "to-fifo")))
	require.NoError(t, os.Symlink("/dev/zero", filepath.Join(dir, "to-zero")))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "plain.md"), []byte("p\n"), 0o644))

	for _, command := range []string{
		"rm -rf " + dir,
		"rm " + filepath.Join(dir, "to-fifo") + " " + filepath.Join(dir, "to-zero"),
	} {
		done := make(chan map[string]FileEvent, 1)
		go func() {
			events, _ := extractForIn(t, command, root)
			done <- deleteEvents(t, events)
		}()
		select {
		case got := <-done:
			for _, p := range []string{"d/to-fifo", "d/to-zero"} {
				require.Contains(t, got, p, "command %q: the link's removal is predicted", command)
				assert.False(t, got[p].OldContentKnown, "command %q: %s is not read", command, p)
				assert.Empty(t, got[p].OldContent)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("command %q: extraction blocked reading a FIFO or a device", command)
		}
	}
}

// headPending is a fakePending that can also read files at HEAD — the
// optional HeadReader a producer with git offers.
type headPending struct {
	fakePending
	head  map[string]string
	asked []string
}

func (h *headPending) HeadContent(path string, limit int64) (string, bool) {
	h.asked = append(h.asked, path)
	c, ok := h.head[path]
	if !ok || int64(len(c)) > limit {
		return "", false
	}
	return c, true
}

// TestExtractCommand_AnUnreadDeleteCarriesHeadsMarkers: a delete whose bytes
// could not be read on disk (here, a marked spec that does not fit what is left
// of the read budget) still carries the markers of HEAD's copy of the file, so a
// rule selecting by marker sees it before the delete lands. oldContent stays ""
// and oldContentKnown false: HEAD's bytes are not the file's now. A read file is
// never looked up at HEAD; a file HEAD cannot supply keeps no markers.
func TestExtractCommand_AnUnreadDeleteCarriesHeadsMarkers(t *testing.T) {
	old := maxRemovedDirectoryBytes
	maxRemovedDirectoryBytes = 64
	t.Cleanup(func() { maxRemovedDirectoryBytes = old })

	root := t.TempDir()
	dir := filepath.Join(root, "specs")
	require.NoError(t, os.Mkdir(dir, 0o755))
	marked := "// sr:invariant \"refunds-capped\"\n" + strings.Repeat("x", 100) + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.md"), []byte("small\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.md"), []byte(marked), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "c.md"), make([]byte, 100), 0o644))

	p := &headPending{fakePending: bashPendingIn("rm -rf "+dir, root), head: map[string]string{"specs/b.md": marked}}
	events, err := New().Extract(module.Input{module.InputPhase: module.PhasePre, module.InputPayload: p})
	require.NoError(t, err)
	got := deleteEvents(t, events)

	b := got["specs/b.md"]
	assert.False(t, b.OldContentKnown)
	assert.Empty(t, b.OldContent)
	require.Len(t, b.OldMarkers, 1, "HEAD's markers are carried: %+v", b)
	assert.Equal(t, "invariant", b.OldMarkers[0].Kind)

	assert.Empty(t, got["specs/c.md"].OldMarkers, "HEAD has no copy: no markers")
	assert.NotContains(t, p.asked, "specs/a.md", "a file read on disk is never looked up at HEAD")
	assert.ElementsMatch(t, []string{"specs/b.md", "specs/c.md"}, p.asked)
}
