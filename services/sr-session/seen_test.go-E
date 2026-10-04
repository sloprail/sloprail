package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/filemod"
	"github.com/sloprail/sloprail/internal/sessionstate"
)

// say appends an assistant message with the given prose, returning its uuid.
func (s *session) say(text string) string {
	s.t.Helper()
	id := s.uuid(s.entries)
	s.append(fmt.Sprintf(
		`{"type":"assistant","uuid":%q,"parentUuid":%q,"message":{"role":"assistant","content":[{"type":"text","text":%q}]}}`,
		id, s.uuid(s.entries-1), text))
	return id
}

// feedback appends the Stop-hook feedback entry the harness writes on a refusal.
func (s *session) feedback() {
	s.t.Helper()
	s.append(fmt.Sprintf(
		`{"type":"user","uuid":%q,"parentUuid":%q,"isMeta":true,"message":{"role":"user","content":"Stop hook feedback: no"}}`,
		s.uuid(s.entries), s.uuid(s.entries-1)))
}

func (s *session) store() sessionstate.Store {
	s.t.Helper()
	store, err := openEngineState(HookPayload{TranscriptPath: s.transcriptPath, Cwd: s.dir})
	require.NoError(s.t, err)
	s.t.Cleanup(func() { store.Close() })
	return store
}

func (s *session) payloadStruct() HookPayload {
	return HookPayload{TranscriptPath: s.transcriptPath, Cwd: s.dir}
}

// The refused reply's text is re-read with the retry; the split puts it in seen
// and only the retry's own text in fresh.
func TestCycleAgentMessages_SplitsAtThePreviousStop(t *testing.T) {
	s := newSession(t)
	store := s.store()

	s.say("Done. #skip")
	seen, fresh, end := cycleAgentMessages(discard(), store, s.payloadStruct())
	assert.Empty(t, seen, "nothing was read by an earlier Stop yet")
	assert.Equal(t, []string{"Done. #skip"}, fresh)
	recordStopSeen(discard(), store, nil, end) // the Stop that refused it

	s.feedback()
	s.say("Saved. #preference")
	seen, fresh, _ = cycleAgentMessages(discard(), store, s.payloadStruct())
	assert.Equal(t, []string{"Done. #skip"}, seen, "the refused reply is re-sent, and marked as such")
	assert.Equal(t, []string{"Saved. #preference"}, fresh)
}

// A harness can re-emit an entry on a retry with the SAME uuid (the e2e mock
// does, for a prose-only turn). The re-emitted text is new text the retry wrote,
// so it must be fresh — which a position kept as a uuid alone gets wrong, since
// the first match of a repeated uuid is the old entry. The count is what holds.
func TestCycleAgentMessages_ARepeatedUUIDIsNotSeenTwice(t *testing.T) {
	s := newSession(t)
	store := s.store()

	id := s.say("Starting. #alpha")
	_, _, end := cycleAgentMessages(discard(), store, s.payloadStruct())
	recordStopSeen(discard(), store, nil, end)

	s.append(fmt.Sprintf(
		`{"type":"assistant","uuid":%q,"parentUuid":%q,"message":{"role":"assistant","content":[{"type":"text","text":"Starting. #alpha"}]}}`,
		id, id))
	seen, fresh, _ := cycleAgentMessages(discard(), store, s.payloadStruct())
	assert.Equal(t, []string{"Starting. #alpha"}, seen, "the first occurrence was read by the refused Stop")
	assert.Equal(t, []string{"Starting. #alpha"}, fresh, "the re-emitted one is the retry's own text")
}

// A position that is not in the window — the cycle completed and the window now
// starts after it, or it was never recorded — marks nothing seen.
func TestCycleAgentMessages_AnUnknownPositionMarksNothingSeen(t *testing.T) {
	s := newSession(t)
	store := s.store()
	require.NoError(t, store.SetMeta(sessionstate.MetaStopSeenRecord, "no-such-entry"))
	s.say("#skip")
	seen, fresh, _ := cycleAgentMessages(discard(), store, s.payloadStruct())
	assert.Empty(t, seen)
	assert.Equal(t, []string{"#skip"}, fresh)
}

func postEvent(kind, path string) event.Event {
	return filemod.FileEvent{Path: path}.Event(kind)
}

func seenOf(e event.Event) bool { v, _ := e.Fields[filemod.FieldSeen].(bool); return v }

// A file re-sent with the content an earlier Stop was handed is seen; one
// changed since is not; a delete is its own fingerprint.
func TestMarkSeenFiles_AcrossStops(t *testing.T) {
	s := newSession(t)
	store := s.store()
	root := t.TempDir()
	write := func(name, body string) {
		require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(body), 0o644))
	}

	write("a.md", "one")
	write("b.md", "one")
	first := []event.Event{postEvent(filemod.KindPostCreate, "a.md"), postEvent(filemod.KindPostCreate, "b.md")}
	snap := markSeenFiles(discard(), store, first, root)
	assert.False(t, seenOf(first[0]), "no earlier Stop")
	assert.False(t, seenOf(first[1]))
	recordStopSeen(discard(), store, snap, "")

	write("b.md", "two") // b changes between the Stops; a does not
	second := []event.Event{postEvent(filemod.KindPostCreate, "a.md"), postEvent(filemod.KindPostCreate, "b.md")}
	snap = markSeenFiles(discard(), store, second, root)
	assert.True(t, seenOf(second[0]), "a.md is re-sent unchanged")
	assert.False(t, seenOf(second[1]), "b.md changed since the previous Stop")
	recordStopSeen(discard(), store, snap, "")

	require.NoError(t, os.Remove(filepath.Join(root, "a.md")))
	third := []event.Event{postEvent(filemod.KindPostDelete, "a.md")}
	snap = markSeenFiles(discard(), store, third, root)
	assert.False(t, seenOf(third[0]), "deleting it is a change")
	recordStopSeen(discard(), store, snap, "")

	fourth := []event.Event{postEvent(filemod.KindPostDelete, "a.md")}
	markSeenFiles(discard(), store, fourth, root)
	assert.True(t, seenOf(fourth[0]), "the same delete, re-sent")
}

// A file that cannot be read is left unseen and out of the snapshot.
func TestMarkSeenFiles_AnUnreadableFileIsNeverSeen(t *testing.T) {
	s := newSession(t)
	store := s.store()
	ev := []event.Event{postEvent(filemod.KindPostCreate, "missing.md")}
	snap := markSeenFiles(discard(), store, ev, t.TempDir())
	assert.False(t, seenOf(ev[0]))
	assert.NotContains(t, snap, "missing.md")
}
