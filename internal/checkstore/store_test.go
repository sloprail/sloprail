package checkstore

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/checkcache"
)

const (
	rule   = "file-guard/docs"
	hash   = "h1"
	kindJ  = "check[0]:judge:./rubric.md.j2"
	kindS  = "check[1]:script:./size.sh"
	subj   = "changeset"
	fpA    = "fp-a"
	fpB    = "fp-b"
	headA  = "head-a"
	baseA  = "base-a"
	sessID = "sess-1"
)

func newRun() CheckRun {
	return CheckRun{CheckID: rule, RuleHash: hash, BaseRef: baseA, HeadRef: headA,
		RunIdentity: RunIdentity{RepoID: "repo", Branch: "main", SessionID: sessID, AgentID: "agent"}}
}

func judge(status, fp string) CheckRecord {
	return CheckRecord{Subject: subj, Kind: kindJ, Status: status, Fingerprint: fp, Metadata: map[string]any{"reasoning": "why " + status}}
}

// record writes one run with its checks and finishes it, through a store over backend, then
// closes (flushes) the store.
func record(t *testing.T, backend checkcache.Cache, r CheckRun, checks ...CheckRecord) {
	t.Helper()
	s := Open(backend, false)
	id, err := s.RecordRun(r)
	require.NoError(t, err)
	for _, c := range checks {
		_, err := s.RecordCheck(id, c)
		require.NoError(t, err)
	}
	require.NoError(t, s.FinishRun(id))
	require.NoError(t, s.Close())
}

func fileBackend(t *testing.T) checkcache.Cache {
	return checkcache.OpenFile(filepath.Join(t.TempDir(), "results.jsonl"))
}

func TestCachedCheck_APassIsReusedByItsKey(t *testing.T) {
	b := fileBackend(t)
	record(t, b, newRun(), judge(StatusPass, fpA))

	got, hit, err := Open(b, true).CachedCheck(rule, hash, subj, kindJ, fpA)
	require.NoError(t, err)
	require.True(t, hit)
	assert.Equal(t, StatusPass, got.Status)
	assert.Equal(t, "why pass", got.Metadata["reasoning"])
	assert.Equal(t, headA, got.Run.HeadRef, "the run it was recorded in comes with it")
	assert.Equal(t, sessID, got.Run.SessionID, "provenance is kept, and is not part of the key")
}

func TestCachedCheck_AFailIsNotAHitButIsFoundByLatestCheck(t *testing.T) {
	b := fileBackend(t)
	record(t, b, newRun(), judge(StatusFail, fpA))
	s := Open(b, true)

	_, hit, err := s.CachedCheck(rule, hash, subj, kindJ, fpA)
	require.NoError(t, err)
	assert.False(t, hit, "a fail is never reused: a judge is asked again")

	got, found, err := s.LatestCheck(rule, hash, subj, kindJ, fpA)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, StatusFail, got.Status)
}

func TestCachedCheck_OnlyAPassOrFailIsFindable(t *testing.T) {
	b := fileBackend(t)
	record(t, b, newRun(), judge(StatusSkip, "s"), judge(StatusError, "e"), judge(StatusInterrupted, "i"))
	s := Open(b, true)
	for _, fp := range []string{"s", "e", "i"} {
		_, found, err := s.LatestCheck(rule, hash, subj, kindJ, fp)
		require.NoError(t, err)
		assert.False(t, found, fp)
	}
}

func TestCachedCheck_EveryPartOfTheKeyMatters(t *testing.T) {
	b := fileBackend(t)
	record(t, b, newRun(), judge(StatusPass, fpA))
	s := Open(b, true)
	for name, probe := range map[string][5]string{
		"rule":        {"file-guard/other", hash, subj, kindJ, fpA},
		"rule hash":   {rule, "h2", subj, kindJ, fpA},
		"subject":     {rule, hash, "a.go", kindJ, fpA},
		"kind":        {rule, hash, subj, kindS, fpA},
		"fingerprint": {rule, hash, subj, kindJ, fpB},
	} {
		_, hit, err := s.CachedCheck(probe[0], probe[1], probe[2], probe[3], probe[4])
		require.NoError(t, err)
		assert.False(t, hit, name)
	}
}

func TestCachedCheck_AScriptHasNoFingerprintAndIsNeverCached(t *testing.T) {
	b := fileBackend(t)
	record(t, b, newRun(), CheckRecord{Subject: subj, Kind: kindS, Status: StatusPass})
	_, hit, err := Open(b, true).CachedCheck(rule, hash, subj, kindS, "")
	require.NoError(t, err)
	assert.False(t, hit)
}

func TestCachedCheck_TheMostRecentRunWins(t *testing.T) {
	b := fileBackend(t)
	record(t, b, newRun(), judge(StatusFail, fpA))
	record(t, b, newRun(), judge(StatusPass, fpA))
	got, found, err := Open(b, true).LatestCheck(rule, hash, subj, kindJ, fpA)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, StatusPass, got.Status)
}

func TestCachedCheck_FindsWhatThisStoreRecordedBeforeItIsClosed(t *testing.T) {
	s := Open(checkcache.NewMemory(), false)
	id, err := s.RecordRun(newRun())
	require.NoError(t, err)
	_, err = s.RecordCheck(id, judge(StatusPass, fpA))
	require.NoError(t, err)
	_, hit, err := s.CachedCheck(rule, hash, subj, kindJ, fpA)
	require.NoError(t, err)
	assert.True(t, hit)
}

func TestRecordCheck_ReplacesTheSameSubjectAndKindWithinARun(t *testing.T) {
	b := fileBackend(t)
	record(t, b, newRun(), judge(StatusFail, fpA), judge(StatusPass, fpA))
	got, _, err := Open(b, true).LatestCheck(rule, hash, subj, kindJ, fpA)
	require.NoError(t, err)
	assert.Equal(t, StatusPass, got.Status)
}

func TestRecordCheck_ItemsArePersistedWithTheCheck(t *testing.T) {
	b := fileBackend(t)
	c := judge(StatusPass, fpA)
	c.Items = []CheckItem{{Key: "a quote", Passed: true, Metadata: map[string]any{"line": float64(3)}}, {Key: "unresolved", Passed: false}}
	record(t, b, newRun(), c)

	found, err := b.Lookup([]checkcache.Key{{Rule: rule, RuleHash: hash, Kind: kindJ, Subject: subj, Fingerprint: fpA}})
	require.NoError(t, err)
	require.Len(t, found, 1)
	for _, f := range found {
		require.Len(t, f.Check.Items, 2)
		assert.Equal(t, "a quote", f.Check.Items[0].Key)
		assert.True(t, f.Check.Items[0].Passed)
		assert.False(t, f.Check.Items[1].Passed)
	}
}

func TestRecordCheck_RefusesAStatusThatIsNotOne(t *testing.T) {
	s := Open(checkcache.NewMemory(), false)
	id, err := s.RecordRun(newRun())
	require.NoError(t, err)
	_, err = s.RecordCheck(id, CheckRecord{Subject: subj, Kind: kindJ, Status: "maybe"})
	assert.Error(t, err)
	_, err = s.RecordCheck("run_nope", judge(StatusPass, fpA))
	assert.Error(t, err, "a check needs a run")
}

func TestRecordRun_IdentityAndRangeAreStoredAsProvenance(t *testing.T) {
	b := fileBackend(t)
	r := newRun()
	r.ExitCode, r.Error, r.Complete = 1, "git failed", true
	s := Open(b, false)
	_, err := s.RecordRun(r)
	require.NoError(t, err)
	require.NoError(t, s.Close())
	// An engine failure holds no checks, so nothing is findable: it is a record, not a verdict.
	_, hit, err := Open(b, true).CachedCheck(rule, hash, subj, kindJ, fpA)
	require.NoError(t, err)
	assert.False(t, hit)
}

func TestFinishRun_UnknownRunIsAnError(t *testing.T) {
	assert.Error(t, Open(checkcache.NewMemory(), false).FinishRun("run_nope"))
}

func TestReadOnly_RefusesToRecordAndWritesNothing(t *testing.T) {
	b := fileBackend(t)
	s := Open(b, true)
	_, err := s.RecordRun(newRun())
	assert.Error(t, err)
	require.NoError(t, s.Close())
	_, hit, err := Open(b, true).CachedCheck(rule, hash, subj, kindJ, fpA)
	require.NoError(t, err)
	assert.False(t, hit)
}

func TestClosedStoreReportsErrClosed(t *testing.T) {
	s := Open(checkcache.NewMemory(), false)
	require.NoError(t, s.Close())
	_, err := s.RecordRun(newRun())
	assert.ErrorIs(t, err, ErrClosed)
	assert.NoError(t, s.Close(), "closing twice is harmless")
}

func TestClose_WritesTheWholeRunAsOneWrite(t *testing.T) {
	counting := &countingCache{Cache: checkcache.NewMemory()}
	s := Open(counting, false)
	for i := 0; i < 3; i++ {
		r := newRun()
		r.CheckID = rule + string(rune('a'+i))
		id, err := s.RecordRun(r)
		require.NoError(t, err)
		_, err = s.RecordCheck(id, judge(StatusPass, fpA))
		require.NoError(t, err)
	}
	require.NoError(t, s.Close())
	assert.Equal(t, 1, counting.puts, "one segment, one push, however many rules were judged")
}

type countingCache struct {
	checkcache.Cache
	puts int
}

func (c *countingCache) Put(runs []checkcache.Run) error {
	c.puts++
	return c.Cache.Put(runs)
}
