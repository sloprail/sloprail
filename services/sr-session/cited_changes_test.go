package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dispatchcore "github.com/sloprail/sloprail/internal/dispatch"
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/filemod"
	"github.com/sloprail/sloprail/internal/grounding"
	"github.com/sloprail/sloprail/internal/sessionstate"
	"github.com/sloprail/sloprail/internal/transcript"
)

func openTestStore(t *testing.T) sessionstate.Store {
	t.Helper()
	store, err := sessionstate.Open(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	return store
}

var userCite = []transcript.Citation{{Quote: "q", SourceTypes: []transcript.SourceType{transcript.SourceUser}, Path: "/r.jsonl", Line: 1}}

// A permitted call's cited change is kept only when the file holds what it
// produces by the next hook: a call that failed, was denied, or never ran
// leaves the file as it was, and its citation grounds nothing.
func TestSettleKeepsOnlyChangesThatLanded(t *testing.T) {
	root := t.TempDir()
	store := openTestStore(t)
	landed := filepath.Join(root, "landed.md")
	require.NoError(t, os.WriteFile(landed, []byte("new"), 0o644))
	failed := filepath.Join(root, "failed.md")
	require.NoError(t, os.WriteFile(failed, []byte("old"), 0o644))
	deleted := filepath.Join(root, "deleted.md")

	require.NoError(t, recordPending(store, []pendingChange{
		{Path: "landed.md", Abs: landed, Change: citedChange{Cites: userCite, Before: fileState{Exists: true, Content: "old"}, After: fileState{Exists: true, Content: "new"}, At: 1}},
		{Path: "failed.md", Abs: failed, Change: citedChange{Cites: userCite, Before: fileState{Exists: true, Content: "old"}, After: fileState{Exists: true, Content: "new"}, At: 2}},
		{Path: "deleted.md", Abs: deleted, Change: citedChange{Cites: userCite, Before: fileState{Exists: true, Content: "x"}, At: 3}},
	}))
	require.NoError(t, settleCitedChanges(store))

	got := citedChangesIn(store, true)
	assert.Contains(t, got, "landed.md")
	assert.Contains(t, got, "deleted.md", "a delete that happened landed")
	assert.NotContains(t, got, "failed.md", "a change that never landed was kept")

	// Settled means gone from the pending list: a later write of the same
	// bytes, uncited, does not revive it.
	require.NoError(t, os.WriteFile(failed, []byte("new"), 0o644))
	require.NoError(t, settleCitedChanges(store))
	assert.NotContains(t, citedChangesIn(store, true), "failed.md")
}

func citedPostEvent(kind, path, oldContent, newContent string) event.Event {
	fe := filemod.FileEvent{Path: path, OldContent: oldContent, NewContent: newContent}
	return fe.Event(kind)
}

// A Post event carries the citations of every cited change that landed on its
// path, and hands the rule each stretch of its history no cited change made.
func TestAttachCitedChangesNamesTheUncitedParts(t *testing.T) {
	store := openTestStore(t)
	cited := citedChange{Cites: userCite, Before: fileState{Exists: true, Content: "base"}, After: fileState{Exists: true, Content: "cited"}, At: 1}
	require.NoError(t, swapJSON(store, sessionstate.MetaCitations, func(all *map[string][]citedChange) {
		*all = map[string][]citedChange{"a.md": {cited}, "b.md": {cited}, "c.md": {cited}}
	}))

	events := []event.Event{
		citedPostEvent(filemod.KindPostUpdate, "a.md", "base", "cited"),          // exactly the cited change
		citedPostEvent(filemod.KindPostUpdate, "b.md", "base", "cited and more"), // an uncited change after it
		citedPostEvent(filemod.KindPostUpdate, "c.md", "other base", "cited"),    // an uncited change before it
		citedPostEvent(filemod.KindPostUpdate, "d.md", "base", "never cited"),    // no cited change at all
	}
	uncited := attachCitedChanges(store, events, nil)

	for i, path := range []string{"a.md", "b.md", "c.md"} {
		cs := grounding.FromWire(events[i].Fields[grounding.FieldCitations])
		assert.Len(t, cs, 1, "%s carries its cited change's citation", path)
	}
	assert.Empty(t, grounding.FromWire(events[3].Fields[grounding.FieldCitations]))

	assert.Empty(t, uncited["a.md"], "a file exactly as its cited change left it has no uncited part")
	assert.Equal(t, []dispatchcore.UncitedChange{{FromExists: true, From: "cited", ToExists: true, To: "cited and more"}}, uncited["b.md"])
	assert.Equal(t, []dispatchcore.UncitedChange{{FromExists: true, From: "other base", ToExists: true, To: "base"}}, uncited["c.md"])
	assert.NotContains(t, uncited, "d.md", "no citation, nothing to narrow: the rule sees no citation at all")
}

// Cited changes from the session and its sub-agents are laid over the file's
// history in the order they were made, so two cited changes in sequence leave
// no gap between them whichever store each is in.
func TestUncitedPartsOrdersChangesAcrossStores(t *testing.T) {
	first := citedChange{Cites: userCite, Before: fileState{}, After: fileState{Exists: true, Content: "one"}, At: 1}
	second := citedChange{Cites: userCite, Before: fileState{Exists: true, Content: "one"}, After: fileState{Exists: true, Content: "two"}, At: 2}
	e := citedPostEvent(filemod.KindPostCreate, "a.md", "", "two")
	assert.Empty(t, uncitedParts(e, []citedChange{second, first}))

	gone := citedChange{Cites: userCite, Before: fileState{Exists: true, Content: "two"}, After: fileState{}, At: 3}
	del := citedPostEvent(filemod.KindPostDelete, "a.md", "base", "")
	assert.Equal(t, []dispatchcore.UncitedChange{{FromExists: true, From: "base", ToExists: false}, {FromExists: true, From: "one", ToExists: true, To: "two"}},
		uncitedParts(del, []citedChange{first, gone}), "an uncited deletion of the baseline, and an uncited edit before the cited delete")
}

// A dry-run failure is quoted beside its own file: the refusal of one target
// never carries what sr-file said about another.
func TestResolveNotesAreKeyedByFile(t *testing.T) {
	n := resolveNotes{byPath: map[string]string{"a.md": "A-ERROR", "b.md": "B-ERROR"}, line: "A-ERROR\nB-ERROR"}
	assert.Equal(t, "A-ERROR", n.For("a.md"))
	assert.NotContains(t, n.For("a.md"), "B-ERROR")
	assert.Equal(t, "B-ERROR", n.For("b.md"))

	// A target the dry run never reached is told which call stopped the line.
	stopped := resolveNotes{byPath: map[string]string{"b.md": "B-ERROR"}, line: "B-ERROR"}
	assert.Contains(t, stopped.For("c.md"), "b.md")

	// A failure sr-file tied to no file is the line's.
	assert.Equal(t, "unparseable", resolveNotes{line: "unparseable"}.For("a.md"))
}
