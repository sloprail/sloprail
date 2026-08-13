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
// update event a pending Edit would produce for it.
func write(t *testing.T, cwd, path, content string) event.Event {
	t.Helper()
	full := filepath.Join(cwd, path)
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
	return filemod.FileEvent{Path: path}.Event(filemod.KindPreUpdate)
}

func TestRevalidation_SkipsMatchingPass(t *testing.T) {
	// The exemption itself: same guardrail, same content, and the verdict it
	// reached was a pass.
	rev, cwd := openTestRevalidation(t)
	e := write(t, cwd, "a.go", "v1\n")

	subj, ok := rev.Subject(e, cwd)
	require.True(t, ok)
	require.False(t, rev.Skip("no-slop", subj), "nothing judged yet, so nothing to skip")

	require.NoError(t, rev.Record("no-slop", subj, true))

	// A second cycle asks again from scratch — the content is re-read, not
	// carried over from the subject above.
	again, ok := rev.Subject(e, cwd)
	require.True(t, ok)
	require.Equal(t, subj.Fingerprint, again.Fingerprint)
	assert.True(t, rev.Skip("no-slop", again))
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
	require.True(t, rev.Skip("no-slop", before))

	write(t, cwd, "a.go", "v2 — edited after the pass\n")
	after, ok := rev.Subject(e, cwd)
	require.True(t, ok)
	require.NotEqual(t, before.Fingerprint, after.Fingerprint, "the edit must actually change the fingerprint, or this test proves nothing")

	assert.False(t, rev.Skip("no-slop", after))
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

	assert.False(t, rev.Skip("no-slop", again))
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

	assert.True(t, rev.Skip("no-slop", subj), "the rule that passed it may skip")
	assert.False(t, rev.Skip("added-later", subj), "a rule that has never seen this file may not")
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
		require.Falsef(t, rev.Skip("no-slop", subj), "cycle %d: the unfixed violation must reach the hook again", cycle)
		require.NoError(t, rev.Record("no-slop", subj, false))
	}

	// The agent fixes it and the hook permits it.
	write(t, cwd, "a.go", "fixed\n")
	fixed, ok := rev.Subject(e, cwd)
	require.True(t, ok)
	require.False(t, rev.Skip("no-slop", fixed), "new content, so the hook runs")
	require.NoError(t, rev.Record("no-slop", fixed, true))

	settled, ok := rev.Subject(e, cwd)
	require.True(t, ok)
	assert.True(t, rev.Skip("no-slop", settled), "once passed, the fixed content is left alone")
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

	assert.False(t, rev.Skip("no-slop", badSubj), "the refusal is still there")
	assert.True(t, rev.Skip("no-slop", goodSubj))
	assert.True(t, rev.Skip("no-comments", badSubj), "the other rule's pass is its own")
}

func TestRevalidation_SubjectOfACreationFingerprintsThePendingContent(t *testing.T) {
	// On a creation the file is not on disk, and what a hook is shown is the
	// content on the event. Fingerprinting the disk would fail here; the
	// assertion is that the answer is the pending bytes.
	rev, cwd := openTestRevalidation(t)
	e := filemod.FileEvent{Path: "new.go", Content: "about to be written\n"}.Event(filemod.KindPreCreate)

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

	create := filemod.FileEvent{Path: "a.go", Content: "v1\n"}.Event(filemod.KindPreCreate)
	created, ok := rev.Subject(create, cwd)
	require.True(t, ok)
	require.NoError(t, rev.Record("no-slop", created, true))

	// The write lands: what was pending is now on disk.
	update := write(t, cwd, "a.go", "v1\n")
	settled, ok := rev.Subject(update, cwd)
	require.True(t, ok)
	require.Equal(t, created.Fingerprint, settled.Fingerprint,
		"the same bytes must fingerprint the same whether they are pending or on disk")
	assert.True(t, rev.Skip("no-slop", settled))
}

func TestRevalidation_CreationThenChangedFileIsJudgedAgain(t *testing.T) {
	// The same sequence with the content actually different, which is what
	// separates a working exemption from one keyed on the path.
	rev, cwd := openTestRevalidation(t)

	create := filemod.FileEvent{Path: "a.go", Content: "v1\n"}.Event(filemod.KindPreCreate)
	created, ok := rev.Subject(create, cwd)
	require.True(t, ok)
	require.NoError(t, rev.Record("no-slop", created, true))

	update := write(t, cwd, "a.go", "v2\n")
	changed, ok := rev.Subject(update, cwd)
	require.True(t, ok)
	require.NotEqual(t, created.Fingerprint, changed.Fingerprint)
	assert.False(t, rev.Skip("no-slop", changed))
}

func TestRevalidation_SubjectOfADifferentPendingCreationDiffers(t *testing.T) {
	// Two creations at the same path with different bodies are different
	// content, so a pass on one is not a licence for the other.
	rev, cwd := openTestRevalidation(t)
	first := filemod.FileEvent{Path: "new.go", Content: "one\n"}.Event(filemod.KindPreCreate)
	second := filemod.FileEvent{Path: "new.go", Content: "two\n"}.Event(filemod.KindPreCreate)

	a, ok := rev.Subject(first, cwd)
	require.True(t, ok)
	b, ok := rev.Subject(second, cwd)
	require.True(t, ok)
	require.NotEqual(t, a.Fingerprint, b.Fingerprint)

	require.NoError(t, rev.Record("no-slop", a, true))
	assert.False(t, rev.Skip("no-slop", b))
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
	// An update event about a file that is not there has no content to compare.
	// Answering false is what keeps an unreadable file from being handed the
	// same empty fingerprint as every other unreadable file.
	rev, cwd := openTestRevalidation(t)

	_, ok := rev.Subject(filemod.FileEvent{Path: "gone.go"}.Event(filemod.KindPreUpdate), cwd)
	assert.False(t, ok)
}

func TestRevalidation_WithoutAStoreNothingIsSkipped(t *testing.T) {
	// The engine leaves this nil when it cannot identify the session. Losing
	// the record must cost re-judging, never a rule that quietly stops firing.
	var rev *revalidation

	subj := subject{Path: "a.go", Fingerprint: "f1"}
	assert.False(t, rev.Skip("no-slop", subj))
	assert.NoError(t, rev.Record("no-slop", subj, true))
	_, ok := rev.Subject(filemod.FileEvent{Path: "a.go", Content: "x"}.Event(filemod.KindPreCreate), "")
	assert.False(t, ok)
	rev.Close()

	empty := &revalidation{}
	assert.False(t, empty.Skip("no-slop", subj))
	assert.NoError(t, empty.Record("no-slop", subj, true))
	empty.Close()
}
