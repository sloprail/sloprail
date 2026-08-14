package sessionstate

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileCheck_AbsentIsNotAnError(t *testing.T) {
	s := openTestStore(t)

	v, found, err := s.FileCheck("a.go", "no-slop")
	require.NoError(t, err)
	assert.False(t, found)
	assert.Equal(t, Verdict{}, v)
}

func TestRecordFileCheck_RoundTrips(t *testing.T) {
	s := openTestStore(t)
	require.NoError(t, s.RecordFileCheck("a.go", "no-slop", Verdict{Fingerprint: "f1", Passed: true}))

	v, found, err := s.FileCheck("a.go", "no-slop")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, Verdict{Fingerprint: "f1", Passed: true}, v)
}

func TestRecordFileCheck_GrainIsCheckNotFile(t *testing.T) {
	// A file governed by three guardrails has three rows. Keying by file alone
	// would leave nowhere to record that a file satisfies one rule while
	// violating another — which is exactly what this asserts.
	s := openTestStore(t)
	require.NoError(t, s.RecordFileCheck("a.go", "no-slop", Verdict{Fingerprint: "f1", Passed: true}))
	require.NoError(t, s.RecordFileCheck("a.go", "no-comments", Verdict{Fingerprint: "f1", Passed: false}))

	slop, found, err := s.FileCheck("a.go", "no-slop")
	require.NoError(t, err)
	require.True(t, found)
	comments, found, err := s.FileCheck("a.go", "no-comments")
	require.NoError(t, err)
	require.True(t, found)

	assert.True(t, slop.Passed)
	assert.False(t, comments.Passed)
}

func TestRecordFileCheck_NewGuardrailSeesNothingJudged(t *testing.T) {
	// The other half of the same reason: a guardrail added after a file was
	// judged must not inherit the verdict another rule reached.
	s := openTestStore(t)
	require.NoError(t, s.RecordFileCheck("a.go", "no-slop", Verdict{Fingerprint: "f1", Passed: true}))

	_, found, err := s.FileCheck("a.go", "added-later")
	require.NoError(t, err)
	assert.False(t, found)
}

func TestRecordFileCheck_ReplacesSameFileAndGuardrail(t *testing.T) {
	s := openTestStore(t)
	require.NoError(t, s.RecordFileCheck("a.go", "no-slop", Verdict{Fingerprint: "f1", Passed: false}))
	require.NoError(t, s.RecordFileCheck("a.go", "no-slop", Verdict{Fingerprint: "f2", Passed: true}))

	v, _, err := s.FileCheck("a.go", "no-slop")
	require.NoError(t, err)
	assert.Equal(t, Verdict{Fingerprint: "f2", Passed: true}, v)
}

func TestRecordFileCheck_PathsAreIndependent(t *testing.T) {
	s := openTestStore(t)
	require.NoError(t, s.RecordFileCheck("a.go", "no-slop", Verdict{Fingerprint: "f1", Passed: true}))
	require.NoError(t, s.RecordFileCheck("b.go", "no-slop", Verdict{Fingerprint: "f2", Passed: false}))

	a, _, err := s.FileCheck("a.go", "no-slop")
	require.NoError(t, err)
	b, _, err := s.FileCheck("b.go", "no-slop")
	require.NoError(t, err)
	assert.True(t, a.Passed)
	assert.False(t, b.Passed)
}

func TestSkippable(t *testing.T) {
	// A row is a licence to skip only when the content still matches AND the
	// verdict was a pass. Every other combination runs the hook — erring the
	// other way is a rule that silently stops firing.
	cases := []struct {
		name    string
		record  *Verdict
		current string
		want    bool
	}{
		{"never judged", nil, "f1", false},
		{"passed, same content", &Verdict{Fingerprint: "f1", Passed: true}, "f1", true},
		{"passed, content changed", &Verdict{Fingerprint: "f1", Passed: true}, "f2", false},
		{"failed, same content", &Verdict{Fingerprint: "f1", Passed: false}, "f1", false},
		{"failed, content changed", &Verdict{Fingerprint: "f1", Passed: false}, "f2", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := openTestStore(t)
			if tc.record != nil {
				require.NoError(t, s.RecordFileCheck("a.go", "no-slop", *tc.record))
			}
			got, err := s.Skippable("a.go", "no-slop", tc.current)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestFileChecks_RevertedContentIsStillSkippable.
//
// A verdict is remembered per (path, guardrail, CONTENT), not one row per
// (path, guardrail) overwritten by whatever was written last.
//
// The overwriting shape rests on an assumption that does not hold: that
// superseded content cannot come back under the same path. It can — an agent
// that edits a file and then reverts it restores exactly the bytes an earlier
// cycle judged, and with a single row the revert finds a fingerprint that no
// longer matches and a settled question is asked again. identity_is_content
// forbids that: content reverted to something already judged has not become new.
func TestFileChecks_RevertedContentIsStillSkippable(t *testing.T) {
	s := openTestStore(t)

	require.NoError(t, s.RecordFileCheck("a.go", "no-slop", Verdict{Fingerprint: "fp-original", Passed: true}))
	require.NoError(t, s.RecordFileCheck("a.go", "no-slop", Verdict{Fingerprint: "fp-edited", Passed: true}))

	// Back to the content the first check passed.
	skippable, err := s.Skippable("a.go", "no-slop", "fp-original")
	require.NoError(t, err)
	assert.True(t, skippable,
		"content this guardrail already judged and passed must not be re-judged because something else was written in between")

	// The intervening content is still settled too.
	skippable, err = s.Skippable("a.go", "no-slop", "fp-edited")
	require.NoError(t, err)
	assert.True(t, skippable)

	// Content never judged is still judged, whatever else this path has held.
	skippable, err = s.Skippable("a.go", "no-slop", "fp-never-seen")
	require.NoError(t, err)
	assert.False(t, skippable, "content with no verdict must be judged")

	// The same content at another path is a question that has not been asked.
	skippable, err = s.Skippable("b.go", "no-slop", "fp-original")
	require.NoError(t, err)
	assert.False(t, skippable, "a verdict is recorded for a path, so the same content elsewhere is new")
}

// TestFileChecks_ARefusalIsNotErasedByALaterPass: the retained refusal survives
// the content moving away and back, which is the half that keeps a violation
// resurfacing.
func TestFileChecks_ARefusalIsNotErasedByALaterPass(t *testing.T) {
	s := openTestStore(t)

	require.NoError(t, s.RecordFileCheck("a.go", "no-slop", Verdict{Fingerprint: "fp-bad", Passed: false}))
	require.NoError(t, s.RecordFileCheck("a.go", "no-slop", Verdict{Fingerprint: "fp-fixed", Passed: true}))

	// Reverting to the refused content must NOT be skippable: the violation is
	// back, and so is the obligation to report it.
	skippable, err := s.Skippable("a.go", "no-slop", "fp-bad")
	require.NoError(t, err)
	assert.False(t, skippable, "content that was refused must be refused again when it comes back")

	// The two positive controls that stop the assertion above from passing for
	// the wrong reason, and they are what this test was missing.
	//
	// Skippable answers false for a RETAINED refusal (the row is there and
	// says it failed) and false for NO ROW AT ALL, and those are the two
	// readings the name of this test is meant to distinguish. Measured: making
	// RecordFileCheck discard failing verdicts left the assertion above green,
	// so the claim "not erased" was not pinned by anything.
	//
	// First that the writer works at all in this setup — otherwise a store
	// recording nothing whatsoever would satisfy every negative here.
	fixedSkippable, err := s.Skippable("a.go", "no-slop", "fp-fixed")
	require.NoError(t, err)
	require.True(t, fixedSkippable,
		"the passing verdict was not stored either, so this store records nothing and the "+
			"refusal assertion above says nothing about refusals")

	// And then that the refusal is PRESENT rather than absent, which is the
	// half Skippable cannot express: it answers false for a retained refusal
	// and false for a missing row alike, so the assertion above is satisfied by
	// a store that discarded the refusal entirely.
	//
	// FileCheck is the reader that can tell them apart, and it reports the
	// LATEST verdict for the pair. So the refusal is put back last: with the
	// row retained, the latest verdict for a.go/no-slop is a refusal on
	// fp-bad; with failing verdicts discarded, the write is a no-op and the
	// latest verdict is still the pass on fp-fixed. That difference is the
	// whole of what "not erased" means here.
	require.NoError(t, s.RecordFileCheck("a.go", "no-slop", Verdict{Fingerprint: "fp-bad", Passed: false}))

	v, found, err := s.FileCheck("a.go", "no-slop")
	require.NoError(t, err)
	require.True(t, found, "no verdict at all was recorded for this file")
	assert.Equal(t, Verdict{Fingerprint: "fp-bad", Passed: false}, v,
		"the refusal was not retained — a discarded refusal reads as never-judged rather than "+
			"as refused, and the first thing to record a pass for that content is then believed")
}

// TestOutstandingRefusals_ReportsWhatIsStillUnfixed: the reader that tells a
// retained refusal from a discarded one.
//
// Skippable answers false for both, which is why the retention was
// unobservable from outside until this existed — see the doc on
// OutstandingRefusals. What is asserted here is the whole of the distinction:
// a refusal that stands is reported, and a refusal that has since been passed
// is not.
func TestOutstandingRefusals_ReportsWhatIsStillUnfixed(t *testing.T) {
	s := openTestStore(t)

	// Refused and left alone: outstanding.
	require.NoError(t, s.RecordFileCheck("broken.go", "no-slop", Verdict{Fingerprint: "fp-bad", Passed: false}))
	// Refused, then passed at new content: fixed, so no longer outstanding.
	require.NoError(t, s.RecordFileCheck("fixed.go", "no-slop", Verdict{Fingerprint: "fp-was-bad", Passed: false}))
	require.NoError(t, s.RecordFileCheck("fixed.go", "no-slop", Verdict{Fingerprint: "fp-good", Passed: true}))
	// Never refused at all.
	require.NoError(t, s.RecordFileCheck("clean.go", "no-slop", Verdict{Fingerprint: "fp-clean", Passed: true}))

	got, err := s.OutstandingRefusals()
	require.NoError(t, err)
	assert.Equal(t, []Refusal{{Path: "broken.go", Guardrail: "no-slop", Fingerprint: "fp-bad"}}, got,
		"only the refusal nothing has since passed is still outstanding")
}

// TestOutstandingRefusals_AFixIsNotUndoneByTheOldRefusalStillSitingThere: the
// direction that matters most, stated on its own.
//
// The refusal row is KEPT after the fix — that is what makes a revert
// re-refuse. So a reader that filtered on passed = 0 alone would go on
// reporting a file the agent has already corrected, forever.
func TestOutstandingRefusals_AFixEndsTheReporting(t *testing.T) {
	s := openTestStore(t)

	require.NoError(t, s.RecordFileCheck("a.go", "no-slop", Verdict{Fingerprint: "fp-bad", Passed: false}))
	outstanding, err := s.OutstandingRefusals()
	require.NoError(t, err)
	require.Len(t, outstanding, 1, "the refusal must be outstanding before the fix, or the assertion below cannot fail")

	require.NoError(t, s.RecordFileCheck("a.go", "no-slop", Verdict{Fingerprint: "fp-good", Passed: true}))

	outstanding, err = s.OutstandingRefusals()
	require.NoError(t, err)
	assert.Empty(t, outstanding,
		"a file a hook has passed must stop being reported, or every file ever refused accumulates forever")
	// And the refusal row still exists, so reverting re-refuses.
	skippable, err := s.Skippable("a.go", "no-slop", "fp-bad")
	require.NoError(t, err)
	assert.False(t, skippable, "the kept refusal must still deny the old content")
}

// TestOutstandingRefusals_PerGuardrailNotPerFile: a file one rule refused is
// not outstanding for a rule that passed it.
func TestOutstandingRefusals_PerGuardrailNotPerFile(t *testing.T) {
	s := openTestStore(t)

	require.NoError(t, s.RecordFileCheck("a.go", "no-slop", Verdict{Fingerprint: "fp1", Passed: false}))
	require.NoError(t, s.RecordFileCheck("a.go", "no-comments", Verdict{Fingerprint: "fp1", Passed: true}))

	got, err := s.OutstandingRefusals()
	require.NoError(t, err)
	assert.Equal(t, []Refusal{{Path: "a.go", Guardrail: "no-slop", Fingerprint: "fp1"}}, got,
		"the refusal belongs to the rule that reached it, not to the file")
}
