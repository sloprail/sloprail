package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/filemod"
	"github.com/sloprail/sloprail/internal/fingerprint"
	"github.com/sloprail/sloprail/internal/sessionstate"
)

// openTestRevalidation returns a revalidation over a real store in a temp
// directory, and the workspace whose files it will fingerprint.
func openTestRevalidation(t *testing.T) (*revalidation, string) {
	t.Helper()
	root := t.TempDir()
	store, err := sessionstate.Open(filepath.Join(root, "state", "state.db"))
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })

	cwd := filepath.Join(root, "work")
	require.NoError(t, os.MkdirAll(cwd, 0o755))
	return &revalidation{store: store}, cwd
}

// write puts content at a repository-relative path under cwd and returns the
// observed-change event a settled cycle would produce for it. Post kinds are
// observations, so the file on disk IS what they are about.
func write(t *testing.T, cwd, path, content string) event.Event {
	t.Helper()
	full := filepath.Join(cwd, path)
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
	return filemod.FileEvent{Path: path}.Event(filemod.KindPostUpdate)
}

// creation is the pending-write event a Write of new content produces.
func creation(path, content string) event.Event {
	return filemod.FileEvent{Path: path, NewContent: content}.Event(filemod.KindPreCreate)
}

// skips asks the exemption and fails the test on any error, which is what the
// tests below want everywhere the store is healthy. The error path has its own
// case.
func skips(t *testing.T, rev *revalidation, guardrail string, s subject) bool {
	t.Helper()
	ok, err := rev.Skip(guardrail, s)
	require.NoError(t, err)
	return ok
}

func TestRevalidation_SkipsMatchingPass(t *testing.T) {
	// The exemption itself: same guardrail, same content, and the verdict it
	// reached was a pass. A creation, because that is the kind whose subject is
	// the content the write would leave.
	rev, cwd := openTestRevalidation(t)
	e := creation("a.go", "v1\n")

	subj, ok := rev.Subject(e, cwd)
	require.True(t, ok)
	require.False(t, skips(t, rev, "no-slop", subj), "nothing judged yet, so nothing to skip")

	require.NoError(t, rev.Record("no-slop", subj, true))

	// A second cycle asks again from scratch — the subject is resolved anew, not
	// carried over from above.
	again, ok := rev.Subject(e, cwd)
	require.True(t, ok)
	require.Equal(t, subj.Fingerprint, again.Fingerprint)
	assert.True(t, skips(t, rev, "no-slop", again))
}

func TestRevalidation_NoSkipAfterContentChanges(t *testing.T) {
	// Without the content half, an edited file inherits its predecessor's
	// verdict. The file is genuinely rewritten here, and the fingerprints are
	// asserted different so the branch under test is the one the name claims.
	rev, cwd := openTestRevalidation(t)
	e := write(t, cwd, "a.go", "v1\n")

	before, ok := rev.Subject(e, cwd)
	require.True(t, ok)
	require.NoError(t, rev.Record("no-slop", before, true))
	require.True(t, skips(t, rev, "no-slop", before))

	write(t, cwd, "a.go", "v2 — edited after the pass\n")
	after, ok := rev.Subject(e, cwd)
	require.True(t, ok)
	require.NotEqual(t, before.Fingerprint, after.Fingerprint, "the edit must actually change the fingerprint, or this test proves nothing")

	assert.False(t, skips(t, rev, "no-slop", after))
}

func TestRevalidation_NoSkipOnStoredRefusal(t *testing.T) {
	// Without the passing half, a refusal is mistaken for settled work and the
	// violation goes quiet. Same content as the recorded row, so the ONLY thing
	// denying the exemption is the verdict.
	rev, cwd := openTestRevalidation(t)
	e := write(t, cwd, "a.go", "v1\n")

	subj, ok := rev.Subject(e, cwd)
	require.True(t, ok)
	require.NoError(t, rev.Record("no-slop", subj, false))

	again, ok := rev.Subject(e, cwd)
	require.True(t, ok)
	require.Equal(t, subj.Fingerprint, again.Fingerprint, "content is unchanged; only the verdict differs")

	assert.False(t, skips(t, rev, "no-slop", again))
}

func TestRevalidation_NoSkipForGuardrailThatNeverJudged(t *testing.T) {
	// verdict_per_guardrail. A pooled per-file verdict would exempt every
	// already-judged file the moment someone adds a rule — so the same content
	// that one guardrail may skip, another must not.
	rev, cwd := openTestRevalidation(t)
	e := write(t, cwd, "a.go", "v1\n")

	subj, ok := rev.Subject(e, cwd)
	require.True(t, ok)
	require.NoError(t, rev.Record("no-slop", subj, true))

	assert.True(t, skips(t, rev, "no-slop", subj), "the rule that passed it may skip")
	assert.False(t, skips(t, rev, "added-later", subj), "a rule that has never seen this file may not")
}

func TestRevalidation_RefusalReFiresUntilTheContentChanges(t *testing.T) {
	// refusal_is_retained, over cycles rather than in one call. The refusal is
	// re-recorded each cycle exactly as the engine does it, and the file is
	// still not exempt on the third — then the fix lands and it becomes exempt.
	rev, cwd := openTestRevalidation(t)
	e := write(t, cwd, "a.go", "broken\n")

	for cycle := range 3 {
		subj, ok := rev.Subject(e, cwd)
		require.True(t, ok)
		require.Falsef(t, skips(t, rev, "no-slop", subj), "cycle %d: the unfixed violation must reach the hook again", cycle)
		require.NoError(t, rev.Record("no-slop", subj, false))
	}

	// The agent fixes it and the hook permits it.
	write(t, cwd, "a.go", "fixed\n")
	fixed, ok := rev.Subject(e, cwd)
	require.True(t, ok)
	require.False(t, skips(t, rev, "no-slop", fixed), "new content, so the hook runs")
	require.NoError(t, rev.Record("no-slop", fixed, true))

	settled, ok := rev.Subject(e, cwd)
	require.True(t, ok)
	assert.True(t, skips(t, rev, "no-slop", settled), "once passed, the fixed content is left alone")
}

func TestRevalidation_RefusalSurvivesAnUnrelatedPass(t *testing.T) {
	// The refusal is retained, not overwritten by whatever happened next. A
	// pass recorded for a different file, and for a different guardrail on the
	// same file, must both leave it standing.
	rev, cwd := openTestRevalidation(t)
	bad := write(t, cwd, "bad.go", "broken\n")
	good := write(t, cwd, "good.go", "fine\n")

	badSubj, ok := rev.Subject(bad, cwd)
	require.True(t, ok)
	goodSubj, ok := rev.Subject(good, cwd)
	require.True(t, ok)

	require.NoError(t, rev.Record("no-slop", badSubj, false))
	require.NoError(t, rev.Record("no-slop", goodSubj, true))
	require.NoError(t, rev.Record("no-comments", badSubj, true))

	assert.False(t, skips(t, rev, "no-slop", badSubj), "the refusal is still there")
	assert.True(t, skips(t, rev, "no-slop", goodSubj))
	assert.True(t, skips(t, rev, "no-comments", badSubj), "the other rule's pass is its own")
}

func TestRevalidation_SubjectOfACreationFingerprintsThePendingContent(t *testing.T) {
	// On a creation the file is not on disk, and what a hook is shown is the
	// content on the event. Fingerprinting the disk would fail here; the
	// assertion is that the answer is the pending bytes.
	rev, cwd := openTestRevalidation(t)
	e := filemod.FileEvent{Path: "new.go", NewContent: "about to be written\n"}.Event(filemod.KindPreCreate)

	subj, ok := rev.Subject(e, cwd)
	require.True(t, ok)
	assert.Equal(t, "new.go", subj.Path)
	assert.Equal(t, fingerprint.Of([]byte("about to be written\n")), subj.Fingerprint)
	assert.NoFileExists(t, filepath.Join(cwd, "new.go"), "the point of this case is that nothing is on disk")
}

func TestRevalidation_CreationThenUnchangedUpdateIsExempt(t *testing.T) {
	// The sequence a real session produces: a file is created, the write lands,
	// and the agent offers the same bytes again — which is now an update,
	// because the file exists. The two kinds read their content from different
	// places, and this asserts they agree when the bytes are the same.
	rev, cwd := openTestRevalidation(t)

	create := filemod.FileEvent{Path: "a.go", NewContent: "v1\n"}.Event(filemod.KindPreCreate)
	created, ok := rev.Subject(create, cwd)
	require.True(t, ok)
	require.NoError(t, rev.Record("no-slop", created, true))

	// The write lands: what was pending is now on disk.
	update := write(t, cwd, "a.go", "v1\n")
	settled, ok := rev.Subject(update, cwd)
	require.True(t, ok)
	require.Equal(t, created.Fingerprint, settled.Fingerprint,
		"the same bytes must fingerprint the same whether they are pending or on disk")
	assert.True(t, skips(t, rev, "no-slop", settled))
}

func TestRevalidation_CreationThenChangedFileIsJudgedAgain(t *testing.T) {
	// The same sequence with the content actually different, which is what
	// separates a working exemption from one keyed on the path.
	rev, cwd := openTestRevalidation(t)

	create := filemod.FileEvent{Path: "a.go", NewContent: "v1\n"}.Event(filemod.KindPreCreate)
	created, ok := rev.Subject(create, cwd)
	require.True(t, ok)
	require.NoError(t, rev.Record("no-slop", created, true))

	update := write(t, cwd, "a.go", "v2\n")
	changed, ok := rev.Subject(update, cwd)
	require.True(t, ok)
	require.NotEqual(t, created.Fingerprint, changed.Fingerprint)
	assert.False(t, skips(t, rev, "no-slop", changed))
}

func TestRevalidation_SubjectOfADifferentPendingCreationDiffers(t *testing.T) {
	// Two creations at the same path with different bodies are different
	// content, so a pass on one is not a licence for the other.
	rev, cwd := openTestRevalidation(t)
	first := filemod.FileEvent{Path: "new.go", NewContent: "one\n"}.Event(filemod.KindPreCreate)
	second := filemod.FileEvent{Path: "new.go", NewContent: "two\n"}.Event(filemod.KindPreCreate)

	a, ok := rev.Subject(first, cwd)
	require.True(t, ok)
	b, ok := rev.Subject(second, cwd)
	require.True(t, ok)
	require.NotEqual(t, a.Fingerprint, b.Fingerprint)

	require.NoError(t, rev.Record("no-slop", a, true))
	assert.False(t, skips(t, rev, "no-slop", b))
}

func TestRevalidation_SubjectResolvesRelativeToTheWorkspace(t *testing.T) {
	// The path on an event is the project's own. Resolved against the process
	// working directory instead, this would fingerprint whatever happens to sit
	// at the same relative path from wherever the hook ran.
	rev, cwd := openTestRevalidation(t)
	e := write(t, cwd, "nested/a.go", "v1\n")

	subj, ok := rev.Subject(e, cwd)
	require.True(t, ok)
	assert.Equal(t, "nested/a.go", subj.Path, "the verdict is keyed on the path the event named, not the resolved one")
	assert.Equal(t, fingerprint.Of([]byte("v1\n")), subj.Fingerprint)
}

func TestRevalidation_SubjectOfAnEventWithNoFile(t *testing.T) {
	// A command event names no file. There is nothing to fingerprint and
	// nothing to key a verdict on, so it is never exempt and never recorded.
	rev, cwd := openTestRevalidation(t)

	_, ok := rev.Subject(event.Event{Kind: "PreCommandRun", Fields: map[string]any{"command": "rm -rf /"}}, cwd)
	assert.False(t, ok)
}

func TestRevalidation_SubjectOfAMissingFile(t *testing.T) {
	// An observed change to a file that is not there has no content to compare.
	// Answering false is what keeps an unreadable file from being handed the
	// same empty fingerprint as every other unreadable file.
	rev, cwd := openTestRevalidation(t)

	_, ok := rev.Subject(filemod.FileEvent{Path: "gone.go"}.Event(filemod.KindPostUpdate), cwd)
	assert.False(t, ok)
}

func TestRevalidation_PendingUpdateHasNoSubject(t *testing.T) {
	// FINDING 1, at its root. A PreFileUpdate is declared to carry a path and no
	// content, so what the write would leave is not merely unread but
	// unavailable. Fingerprinting the disk instead identifies the content the
	// write would REPLACE — which does not move between two offers, so the
	// second inherits the first's verdict.
	//
	// Two DIFFERENT pending payloads against an UNCHANGED file. Before the fix
	// these produced an identical subject; now neither produces one at all.
	rev, cwd := openTestRevalidation(t)
	write(t, cwd, "notes.md", "benign") // disk content, unchanged throughout

	a := filemod.FileEvent{Path: "notes.md", NewContent: "PAYLOAD-A"}.Event(filemod.KindPreUpdate)
	b := filemod.FileEvent{Path: "notes.md", NewContent: "SECRET=hunter2"}.Event(filemod.KindPreUpdate)

	_, aOK := rev.Subject(a, cwd)
	_, bOK := rev.Subject(b, cwd)
	assert.False(t, aOK, "a pending update cannot say what it would leave, so it may not license a skip")
	assert.False(t, bOK)
}

func TestRevalidation_APassOnOneUpdateCannotExemptAnother(t *testing.T) {
	// FINDING 1 as the bypass it was. The exemption is driven end to end at this
	// layer: judge and pass one pending payload, then offer a different one
	// against the same unchanged file. The second must not be skippable.
	//
	// Written against whatever Subject returns rather than around it, so it
	// still holds if a later change gives PreFileUpdate a real subject — what is
	// pinned is that a pass on one payload never exempts a different one.
	rev, cwd := openTestRevalidation(t)
	write(t, cwd, "notes.md", "benign")

	benign := filemod.FileEvent{Path: "notes.md", NewContent: "PAYLOAD-A"}.Event(filemod.KindPreUpdate)
	if subj, ok := rev.Subject(benign, cwd); ok {
		require.NoError(t, rev.Record("no-slop", subj, true))
	}

	malicious := filemod.FileEvent{Path: "notes.md", NewContent: "SECRET=hunter2"}.Event(filemod.KindPreUpdate)
	subj, ok := rev.Subject(malicious, cwd)
	if !ok {
		// No subject, so the dispatcher runs the hook and there is nothing to
		// bypass. That is the CURRENT engine — PreFileUpdate has no subject —
		// which means the assertion below is unreachable today and this test
		// asserts nothing.
		//
		// Said out loud rather than returned silently. A test that quietly
		// asserts nothing is indistinguishable from one that passed, and this
		// one is written to outlive the condition that makes it dormant: it
		// starts biting the moment PreFileUpdate gains a real subject. Naming
		// the state in the log is what keeps "dormant by design" from decaying
		// into "green and empty".
		t.Log("dormant: PreFileUpdate has no subject on this engine, so the exemption this " +
			"test guards against cannot arise. It begins asserting when the kind carries content.")
		return
	}
	assert.False(t, skips(t, rev, "no-slop", subj),
		"a pass recorded for one pending payload must never exempt a different one")
}

func TestRevalidation_PreSubjectDoesNotMoveWhenTheDiskDoes(t *testing.T) {
	// The property Finding 1's fix actually established, stated as itself: a
	// PENDING action's subject is a function of the EVENT and of nothing else.
	//
	// It is what makes the answer safe to resolve once and reuse for every
	// guardrail bound to the event, even though a guardrail's hook is an
	// arbitrary script that may rewrite the very file in question — nothing it
	// does to the disk can move the subject. The old disk-reading code had no
	// such guarantee, which is what let two different payloads share one
	// fingerprint.
	rev, cwd := openTestRevalidation(t)
	e := creation("notes.md", "hello")

	before, ok := rev.Subject(e, cwd)
	require.True(t, ok)

	// Whatever a hook might do between one guardrail and the next.
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "notes.md"), []byte("REWRITTEN BY A HOOK"), 0o644))

	after, ok := rev.Subject(e, cwd)
	require.True(t, ok)
	assert.Equal(t, before, after, "a pending subject must not move when the disk does")
	assert.Equal(t, fingerprint.Of([]byte("hello")), after.Fingerprint,
		"and it must be the bytes the write would leave, not the ones now on disk")
}

func TestRevalidation_NeitherKindOfDeleteHasASubject(t *testing.T) {
	// A delete has no resulting bytes, so there is nothing a pass could be a
	// licence for — before or after the fact.
	//
	// Both are checked against a file that is STILL THERE, which is the case that
	// separates the rule from an accident. A delete event whose file is already
	// gone answers false because there is nothing to read; these answer false
	// because there is nothing a verdict about a deletion could be keyed on.
	// Fingerprinting what is on disk would key the exemption on exactly the
	// content the action exists to remove.
	rev, cwd := openTestRevalidation(t)
	write(t, cwd, "doomed.go", "still here\n")

	for _, kind := range []string{filemod.KindPreDelete, filemod.KindPostDelete} {
		_, ok := rev.Subject(filemod.FileEvent{Path: "doomed.go"}.Event(kind), cwd)
		assert.Falsef(t, ok, "%s: a deletion leaves no content to exempt", kind)
	}
}

func TestRevalidation_SkipReportsAStoreThatCannotAnswer(t *testing.T) {
	// M8. A store error must reach the caller AND answer false. Folding it into
	// the false alone leaves a session that has silently lost its record looking
	// like one that simply has nothing settled.
	rev, cwd := openTestRevalidation(t)
	subj, ok := rev.Subject(creation("a.go", "v1\n"), cwd)
	require.True(t, ok)
	_ = cwd

	// Close the store underneath it: the connection is gone, so the query fails.
	require.NoError(t, rev.store.Close())

	skip, err := rev.Skip("no-slop", subj)
	assert.False(t, skip, "an unreadable record has lost the right to exempt anything")
	assert.Error(t, err, "and the caller must be told why it is re-judging")
}

func TestRevalidation_WithoutAStoreNothingIsSkipped(t *testing.T) {
	// The engine leaves this nil when it cannot identify the session. Losing
	// the record must cost re-judging, never a rule that quietly stops firing.
	var rev *revalidation

	subj := subject{Path: "a.go", Fingerprint: "f1"}
	assert.False(t, skips(t, rev, "no-slop", subj))
	assert.NoError(t, rev.Record("no-slop", subj, true))
	_, ok := rev.Subject(filemod.FileEvent{Path: "a.go", NewContent: "x"}.Event(filemod.KindPreCreate), "")
	assert.False(t, ok)
	rev.Close()

	// The other shape of the same thing: a revalidation that exists but holds no
	// store. Every method must answer the same way, INCLUDING Subject — a
	// subject resolved against no store is one nothing can be recorded under,
	// and handing one back invites a caller to skip on a row that was never
	// written. The Post kinds are checked explicitly because they are the ones
	// that read the disk, so they can produce an answer without the store's help
	// and are the only place this guard has anything to do.
	empty := &revalidation{}
	assert.False(t, skips(t, empty, "no-slop", subj))
	assert.NoError(t, empty.Record("no-slop", subj, true))

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.go"), []byte("v1\n"), 0o644))
	for _, kind := range []string{
		filemod.KindPreCreate,
		filemod.KindPreUpdate,
		filemod.KindPostCreate,
		filemod.KindPostUpdate,
		filemod.KindPostDelete,
	} {
		_, ok := empty.Subject(filemod.FileEvent{Path: "a.go", NewContent: "v1\n"}.Event(kind), root)
		assert.Falsef(t, ok, "%s: with no store there is nothing to key a verdict on", kind)
	}

	empty.Close()
}
