package checkstore

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/checkcache"
)

const rule = "file-guard/size"

func run(head string) CheckRun {
	return CheckRun{
		RunIdentity: RunIdentity{RepoID: "root0", Branch: "main", SessionID: "s1"},
		BatchID:     "batch-1", CheckID: rule, BaseRef: "base0", HeadRef: head,
		RuleHash: "h1", Metadata: map[string]any{"eventKind": "Changeset"},
	}
}

func record(t *testing.T, s *store, r CheckRun, checks ...CheckRecord) string {
	t.Helper()
	id, err := s.RecordRun(r)
	require.NoError(t, err)
	for _, c := range checks {
		_, err := s.RecordCheck(id, c)
		require.NoError(t, err)
	}
	require.NoError(t, s.FinishRun(id))
	return id
}

func judge(status, fp string) CheckRecord {
	return CheckRecord{Subject: "changeset", Kind: "check[1]:judge:./rubric.md.j2", Status: status, Fingerprint: fp,
		Metadata: map[string]any{"reasoning": "because " + status, "model": "size-md"}}
}

func script(status string) CheckRecord {
	return CheckRecord{Subject: "changeset", Kind: "check[0]:script:./size.sh", Status: status}
}

func TestCachedCheck_APassIsReplayedByFingerprint(t *testing.T) {
	s := openTestStore(t)
	record(t, s, run("h0"), judge("pass", "fp1"))

	c, found, err := s.cached("changeset", "check[1]:judge:./rubric.md.j2", "fp1")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, StatusPass, c.Status)
	assert.Equal(t, "because pass", c.Metadata["reasoning"])
}

// a fail is kept to be shown but is never a hit: a10n's CacheHit reads only passes.
func TestCachedCheck_AFailIsNotAHitButIsFound(t *testing.T) {
	s := openTestStore(t)
	record(t, s, run("h0"), judge("fail", "fp1"))

	_, hit, err := s.cached("changeset", "check[1]:judge:./rubric.md.j2", "fp1")
	require.NoError(t, err)
	assert.False(t, hit)
	c, found, err := s.latestOf("changeset", "check[1]:judge:./rubric.md.j2", "fp1")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, StatusFail, c.Status)
	assert.Equal(t, "because fail", c.Metadata["reasoning"])
}

func TestCachedCheck_OnlyPassAndFailAreCached(t *testing.T) {
	s := openTestStore(t)
	for i, st := range []string{StatusSkip, StatusError, StatusInterrupted} {
		record(t, s, run("h"+st), judge(st, "fp-"+st))
		_, found, err := s.latestOf("changeset", "check[1]:judge:./rubric.md.j2", "fp-"+st)
		require.NoError(t, err)
		assert.False(t, found, "%s (#%d) must not be replayed", st, i)
	}
}

func TestCachedCheck_EveryPartOfTheKeyMatters(t *testing.T) {
	s := openTestStore(t)
	record(t, s, run("h0"), judge("pass", "fp1"))
	for name, args := range map[string][3]string{
		"subject":     {"pkg/a", "check[1]:judge:./rubric.md.j2", "fp1"},
		"kind":        {"changeset", "check[1]:judge:./other.md.j2", "fp1"},
		"fingerprint": {"changeset", "check[1]:judge:./rubric.md.j2", "fp2"},
	} {
		_, found, err := s.cached(args[0], args[1], args[2])
		require.NoError(t, err)
		assert.False(t, found, "changing only the %s must miss", name)
	}
}

func TestCachedCheck_AScriptHasNoFingerprintAndIsNeverCached(t *testing.T) {
	s := openTestStore(t)
	id := record(t, s, run("h0"), script("pass"))
	assert.Empty(t, s.byID[id].Checks[0].Fingerprint, "a script has no fingerprint")

	_, found, err := s.cached("changeset", "check[0]:script:./size.sh", "")
	require.NoError(t, err)
	assert.False(t, found)
}

func TestCachedCheck_TheMostRecentWins(t *testing.T) {
	s := openTestStore(t)
	record(t, s, run("h0"), judge("pass", "fp1"))
	record(t, s, run("h1"), judge("fail", "fp1"))
	c, _, err := s.latestOf("changeset", "check[1]:judge:./rubric.md.j2", "fp1")
	require.NoError(t, err)
	assert.Equal(t, StatusFail, c.Status)
	_, hit, err := s.cached("changeset", "check[1]:judge:./rubric.md.j2", "fp1")
	require.NoError(t, err)
	assert.False(t, hit, "the newest result is a fail: not a hit")
}

func TestRecordCheck_ReplacesTheSameSubjectAndKindWithinARun(t *testing.T) {
	s := openTestStore(t)
	id := record(t, s, run("h0"), judge("fail", "fp1"))
	_, err := s.RecordCheck(id, judge("pass", "fp1"))
	require.NoError(t, err)

	require.Len(t, s.byID[id].Checks, 1)
	assert.Equal(t, "pass", s.byID[id].Checks[0].Status)
}

func TestRecordCheck_ItemsAreRowsAndReplacedWithTheCheck(t *testing.T) {
	s := openTestStore(t)
	id := record(t, s, run("h0"))
	c := judge("fail", "fp1")
	c.Items = []CheckItem{{Key: "a.go", Metadata: map[string]any{"message": "too long"}}, {Key: "b.go", Passed: true}}
	_, err := s.RecordCheck(id, c)
	require.NoError(t, err)
	c.Items = c.Items[:1]
	_, err = s.RecordCheck(id, c)
	require.NoError(t, err)

	items := s.byID[id].Checks[0].Items
	require.Len(t, items, 1)
	assert.Equal(t, "a.go", items[0].Key)
}

func TestRecordCheck_RefusesAStatusThatIsNotOne(t *testing.T) {
	s := openTestStore(t)
	id := record(t, s, run("h0"))
	_, err := s.RecordCheck(id, CheckRecord{Subject: "changeset", Kind: "k", Status: "maybe"})
	assert.Error(t, err)
}

func TestRecordRun_IdentityColumnsAreStored(t *testing.T) {
	s := openTestStore(t)
	id := record(t, s, run("h0"))
	got := s.byID[id]
	assert.Equal(t, "root0", got.RepoID)
	assert.Equal(t, "main", got.Branch)
	assert.Equal(t, "s1", got.SessionID)
	assert.Equal(t, rule, got.Rule)
	assert.Equal(t, "h0", got.HeadRef)
}

func TestCheckResults_SurviveReopen(t *testing.T) {
	backend := checkcache.OpenFile(filepath.Join(t.TempDir(), "results.jsonl"))
	first := Open(backend, false)
	id, err := first.RecordRun(run("h0"))
	require.NoError(t, err)
	_, err = first.RecordCheck(id, judge("fail", "fp"))
	require.NoError(t, err)
	require.NoError(t, first.Close())

	second := Open(backend, true).(*store)
	defer second.Close()
	c, found, err := second.latestOf("changeset", "check[1]:judge:./rubric.md.j2", "fp")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, StatusFail, c.Status)
}

func TestCheckResults_ClosedStoreReportsErrClosed(t *testing.T) {
	s := Open(checkcache.NewMemory(), false)
	require.NoError(t, s.Close())
	_, err := s.RecordRun(run("h0"))
	assert.ErrorIs(t, err, ErrClosed)
}

func TestFinishRun_UnknownRunIsAnError(t *testing.T) {
	assert.Error(t, openTestStore(t).FinishRun("run_nope"))
}

// A `sr-checks run` is one write however many rules it judged: one segment, one push.
func TestClose_WritesEveryRunAsOneWrite(t *testing.T) {
	counting := &countingCache{Cache: checkcache.NewMemory()}
	s := Open(counting, false)
	for _, name := range []string{"a", "b", "c"} {
		r := run("h0")
		r.CheckID = rule + name
		id, err := s.RecordRun(r)
		require.NoError(t, err)
		_, err = s.RecordCheck(id, judge("pass", "fp1"))
		require.NoError(t, err)
	}
	require.NoError(t, s.Close())
	assert.Equal(t, 1, counting.puts)
}

type countingCache struct {
	checkcache.Cache
	puts int
}

func (c *countingCache) Put(runs []checkcache.Run) error {
	c.puts++
	return c.Cache.Put(runs)
}
