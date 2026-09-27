package filemod

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

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
func TestExtractCommand_RemovingADirectoryRecursivelyDeletesEveryFileInIt(t *testing.T) {
	root := scannerTree(t)
	dir := filepath.Join(root, "scanners", "x")

	for _, command := range []string{
		"rm -rf " + dir,
		"rm -r " + dir,
		"rm -R " + dir,
		"rm -fR " + dir,
		"rm --recursive --force " + dir,
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
