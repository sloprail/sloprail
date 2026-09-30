package sessionstate

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func key(fp string) VerdictKey {
	return VerdictKey{Rule: "size", RuleHash: "h1", Check: "0", Subject: "changeset", Model: "", Fingerprint: fp}
}

func TestChangesetVerdict_AbsentIsNotAnError(t *testing.T) {
	s := openTestStore(t)
	_, found, err := s.ChangesetVerdict(key("f1"))
	require.NoError(t, err)
	assert.False(t, found)
}

func TestChangesetVerdict_RoundTripsAFailure(t *testing.T) {
	s := openTestStore(t)
	require.NoError(t, s.RecordChangesetVerdict(key("f1"), ChangesetVerdict{Reasoning: "too long", Files: []string{"a.go", "b.go"}}))

	v, found, err := s.ChangesetVerdict(key("f1"))
	require.NoError(t, err)
	require.True(t, found)
	assert.False(t, v.Passed)
	assert.Equal(t, "too long", v.Reasoning)
	assert.Equal(t, []string{"a.go", "b.go"}, v.Files)
	assert.False(t, v.Stale)
}

func TestChangesetVerdict_PassWithNoFilesReadsBackEmptyNotNil(t *testing.T) {
	s := openTestStore(t)
	require.NoError(t, s.RecordChangesetVerdict(key("f1"), ChangesetVerdict{Passed: true}))
	v, _, err := s.ChangesetVerdict(key("f1"))
	require.NoError(t, err)
	assert.True(t, v.Passed)
	assert.Empty(t, v.Files)
}

// Every part of the key must matter: each of these, changed alone, misses.
func TestChangesetVerdict_EveryPartOfTheKeyIsPartOfTheKey(t *testing.T) {
	s := openTestStore(t)
	base := key("f1")
	require.NoError(t, s.RecordChangesetVerdict(base, ChangesetVerdict{Passed: true}))

	for name, k := range map[string]VerdictKey{
		"rule":        {Rule: "other", RuleHash: base.RuleHash, Check: base.Check, Subject: base.Subject, Model: base.Model, Fingerprint: base.Fingerprint},
		"rule hash":   {Rule: base.Rule, RuleHash: "h2", Check: base.Check, Subject: base.Subject, Model: base.Model, Fingerprint: base.Fingerprint},
		"check":       {Rule: base.Rule, RuleHash: base.RuleHash, Check: "1", Subject: base.Subject, Model: base.Model, Fingerprint: base.Fingerprint},
		"subject":     {Rule: base.Rule, RuleHash: base.RuleHash, Check: base.Check, Subject: "pkg/a", Model: base.Model, Fingerprint: base.Fingerprint},
		"model":       {Rule: base.Rule, RuleHash: base.RuleHash, Check: base.Check, Subject: base.Subject, Model: "opus", Fingerprint: base.Fingerprint},
		"fingerprint": {Rule: base.Rule, RuleHash: base.RuleHash, Check: base.Check, Subject: base.Subject, Model: base.Model, Fingerprint: "f2"},
	} {
		_, found, err := s.ChangesetVerdict(k)
		require.NoError(t, err)
		assert.False(t, found, "changing only the %s must miss", name)
	}
}

func TestChangesetVerdict_ASameInputVerdictSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	first, err := Open(path)
	require.NoError(t, err)
	require.NoError(t, first.RecordChangesetVerdict(key("f1"), ChangesetVerdict{Reasoning: "no"}))
	require.NoError(t, first.SetWatermark("size", "h1", "abc"))
	require.NoError(t, first.Close())

	second, err := Open(path)
	require.NoError(t, err)
	defer second.Close()
	v, found, err := second.ChangesetVerdict(key("f1"))
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "no", v.Reasoning)
	head, found, err := second.Watermark("size", "h1")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "abc", head)
}

func TestChangesetStale_AFailureWhoseInputLeftIsStaleAndNotOutstanding(t *testing.T) {
	s := openTestStore(t)
	require.NoError(t, s.RecordChangesetVerdict(key("old"), ChangesetVerdict{Reasoning: "bad"}))

	out, err := s.OutstandingChangesetFailures("size", "h1")
	require.NoError(t, err)
	require.Len(t, out, 1)

	// The agent fixed it: the live input is a different one.
	require.NoError(t, s.MarkChangesetStale("size", "h1", []string{"new"}))
	out, err = s.OutstandingChangesetFailures("size", "h1")
	require.NoError(t, err)
	assert.Empty(t, out)

	v, found, err := s.ChangesetVerdict(key("old"))
	require.NoError(t, err)
	assert.True(t, found, "a stale failure is kept, not deleted")
	assert.True(t, v.Stale)
}

func TestChangesetStale_ARevertedInputIsLiveAgain(t *testing.T) {
	s := openTestStore(t)
	require.NoError(t, s.RecordChangesetVerdict(key("bad"), ChangesetVerdict{Reasoning: "bad"}))
	require.NoError(t, s.MarkChangesetStale("size", "h1", []string{"good"}))
	require.NoError(t, s.MarkChangesetStale("size", "h1", []string{"bad"}))

	out, err := s.OutstandingChangesetFailures("size", "h1")
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, "bad", out[0].Reasoning)
}

func TestChangesetStale_NoLiveInputsStalesEveryFailure(t *testing.T) {
	// The files left the range entirely: nothing is live.
	s := openTestStore(t)
	require.NoError(t, s.RecordChangesetVerdict(key("a"), ChangesetVerdict{Reasoning: "x"}))
	require.NoError(t, s.RecordChangesetVerdict(key("b"), ChangesetVerdict{Reasoning: "y"}))
	require.NoError(t, s.MarkChangesetStale("size", "h1", nil))
	out, err := s.OutstandingChangesetFailures("size", "h1")
	require.NoError(t, err)
	assert.Empty(t, out)
}

func TestChangesetStale_OnlyTouchesItsOwnRuleAndHash(t *testing.T) {
	s := openTestStore(t)
	other := VerdictKey{Rule: "other", RuleHash: "h1", Check: "0", Subject: "changeset", Fingerprint: "a"}
	require.NoError(t, s.RecordChangesetVerdict(other, ChangesetVerdict{Reasoning: "x"}))
	require.NoError(t, s.MarkChangesetStale("size", "h1", nil))
	out, err := s.OutstandingChangesetFailures("other", "h1")
	require.NoError(t, err)
	assert.Len(t, out, 1)
}

func TestChangesetStale_PassesAreNeverStale(t *testing.T) {
	s := openTestStore(t)
	require.NoError(t, s.RecordChangesetVerdict(key("p"), ChangesetVerdict{Passed: true}))
	require.NoError(t, s.MarkChangesetStale("size", "h1", nil))
	v, _, err := s.ChangesetVerdict(key("p"))
	require.NoError(t, err)
	assert.False(t, v.Stale)
}

func TestChangesetVerdict_OutstandingIsOrdered(t *testing.T) {
	s := openTestStore(t)
	for _, fp := range []string{"c", "a", "b"} {
		require.NoError(t, s.RecordChangesetVerdict(key(fp), ChangesetVerdict{Reasoning: fp}))
	}
	out, err := s.OutstandingChangesetFailures("size", "h1")
	require.NoError(t, err)
	require.Len(t, out, 3)
	assert.Equal(t, []string{"a", "b", "c"}, []string{out[0].Key.Fingerprint, out[1].Key.Fingerprint, out[2].Key.Fingerprint})
}

func TestWatermark_PerRuleAndHash(t *testing.T) {
	s := openTestStore(t)
	require.NoError(t, s.SetWatermark("size", "h1", "aaa"))
	require.NoError(t, s.SetWatermark("size", "h1", "bbb"))

	head, found, err := s.Watermark("size", "h1")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "bbb", head)

	_, found, err = s.Watermark("size", "h2")
	require.NoError(t, err)
	assert.False(t, found, "a changed definition has no watermark")
	_, found, err = s.Watermark("other", "h1")
	require.NoError(t, err)
	assert.False(t, found)
}

func TestChangesetVerdicts_ClosedStoreReportsErrClosed(t *testing.T) {
	s, err := open(":memory:")
	require.NoError(t, err)
	require.NoError(t, s.Close())
	_, _, err = s.ChangesetVerdict(key("x"))
	assert.ErrorIs(t, err, ErrClosed)
	assert.ErrorIs(t, s.SetWatermark("a", "b", "c"), ErrClosed)
}
