package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/filemod"
	"github.com/sloprail/sloprail/internal/grounding"
)

// An sr-file target with no Pre file event yet gets one built here, reading the
// file's current bytes. A link to a FIFO or /dev/zero is a file that exists and
// must not be read the plain way — the read blocks the pre-tool hook forever,
// or never ends. It is predicted with oldContentKnown false instead; a plain
// file is read and says so.
func TestEnsureFileEvents_ALinkToAFIFOOrADeviceIsNotRead(t *testing.T) {
	root := t.TempDir()
	fifo := filepath.Join(t.TempDir(), "fifo")
	require.NoError(t, syscall.Mkfifo(fifo, 0o644))
	require.NoError(t, os.Symlink(fifo, filepath.Join(root, "to-fifo.md")))
	require.NoError(t, os.Symlink("/dev/zero", filepath.Join(root, "to-zero.md")))
	require.NoError(t, os.WriteFile(filepath.Join(root, "plain.md"), []byte("plain\n"), 0o644))

	targets := map[string]string{
		"to-fifo.md": grounding.VerbDelete,
		"to-zero.md": grounding.VerbDelete,
		"plain.md":   grounding.VerbDelete,
	}
	done := make(chan []event.Event, 1)
	go func() { done <- ensureFileEvents(nil, targets, root) }()
	var events []event.Event
	select {
	case events = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("building the delete events blocked reading a FIFO or a device")
	}

	got := map[string]filemod.FileEvent{}
	for _, e := range events {
		require.Equal(t, filemod.KindPreDelete, e.Kind)
		f, err := filemod.FromEvent(e)
		require.NoError(t, err)
		got[f.Path] = f
	}
	require.Len(t, got, 3, "every existing target is a delete: %v", got)
	for _, p := range []string{"to-fifo.md", "to-zero.md"} {
		assert.False(t, got[p].OldContentKnown, "%s is not read", p)
		assert.Empty(t, got[p].OldContent)
	}
	assert.True(t, got["plain.md"].OldContentKnown)
	assert.Equal(t, "plain\n", got["plain.md"].OldContent)
}
