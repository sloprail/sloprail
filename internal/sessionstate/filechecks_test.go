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
