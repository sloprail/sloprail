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
		Metadata: map[string]any{"ruleHash": "h1", "eventKind": "Changeset", "baseOrigin": "floor"},
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

// a fail is terminal: replayed exactly like a pass (a10n's CacheHit reads only passes).
func TestCachedCheck_AFailIsReplayedToo(t *testing.T) {
	s := openTestStore(t)
	record(t, s, run("h0"), judge("fail", "fp1"))

	c, found, err := s.cached("changeset", "check[1]:judge:./rubric.md.j2", "fp1")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, StatusFail, c.Status)
	assert.Equal(t, "because fail", c.Metadata["reasoning"])
}

func TestCachedCheck_OnlyPassAndFailAreCached(t *testing.T) {
	s := openTestStore(t)
	for i, st := range []string{StatusSkip, StatusError, StatusInterrupted} {
		record(t, s, run("h"+st), judge(st, "fp-"+st))
		_, found, err := s.cached("changeset", "check[1]:judge:./rubric.md.j2", "fp-"+st)
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

	rows, err := s.Query(`SELECT fingerprint FROM checks WHERE run_id = '` + id + `'`)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Nil(t, rows[0]["fingerprint"], "a script's fingerprint is NULL")

	_, found, err := s.cached("changeset", "check[0]:script:./size.sh", "")
	require.NoError(t, err)
	assert.False(t, found)
}

func TestCachedCheck_TheMostRecentWins(t *testing.T) {
	s := openTestStore(t)
	record(t, s, run("h0"), judge("pass", "fp1"))
	record(t, s, run("h1"), judge("fail", "fp1"))
	c, _, err := s.cached("changeset", "check[1]:judge:./rubric.md.j2", "fp1")
	require.NoError(t, err)
	assert.Equal(t, StatusFail, c.Status)
}

func TestRecordCheck_ReplacesTheSameSubjectAndKindWithinARun(t *testing.T) {
	s := openTestStore(t)
	id := record(t, s, run("h0"), judge("fail", "fp1"))
	_, err := s.RecordCheck(id, judge("pass", "fp1"))
	require.NoError(t, err)

	rows, err := s.Query(`SELECT status FROM checks WHERE run_id = '` + id + `'`)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "pass", rows[0]["status"])
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

	rows, err := s.Query(`SELECT key, passed FROM check_items ORDER BY key`)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "a.go", rows[0]["key"])
}

func TestRecordCheck_RefusesAStatusThatIsNotOne(t *testing.T) {
	s := openTestStore(t)
	id := record(t, s, run("h0"))
	_, err := s.RecordCheck(id, CheckRecord{Subject: "changeset", Kind: "k", Status: "maybe"})
	assert.Error(t, err)
}

func TestRecordRun_IdentityColumnsAreStored(t *testing.T) {
	s := openTestStore(t)
	record(t, s, run("h0"))
	rows, err := s.Query(`SELECT repo_id, branch, session_id, check_id, run_batch_id, base_ref, head_ref FROM check_runs`)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "root0", rows[0]["repo_id"])
	assert.Equal(t, "main", rows[0]["branch"])
	assert.Equal(t, "s1", rows[0]["session_id"])
	assert.Equal(t, rule, rows[0]["check_id"])
	assert.Equal(t, "h0", rows[0]["head_ref"])
}

// The watermark is derived from rows.
func TestPassedHeads_NewestAllPassRunFirst(t *testing.T) {
	s := openTestStore(t)
	record(t, s, run("h0"), script("pass"), judge("pass", "a"))
	record(t, s, run("h1"), script("pass"), judge("fail", "b"))
	record(t, s, run("h2"), script("pass"), judge("pass", "c"))

	heads, err := s.PassedHeads(rule)
	require.NoError(t, err)
	assert.Equal(t, []string{"h2", "h0"}, heads, "a run with a failing check passed nothing")
}

func TestPassedHeads_ARunWithNoChecksPassed(t *testing.T) {
	// `match` selected nothing: a pass, and the watermark advances.
	s := openTestStore(t)
	record(t, s, run("h0"))
	heads, err := s.PassedHeads(rule)
	require.NoError(t, err)
	assert.Equal(t, []string{"h0"}, heads)
}

// A run that failed as an engine moves nothing, and is never an empty range.
func TestPassedHeads_AnEngineFailureIsNotAPass(t *testing.T) {
	s := openTestStore(t)
	bad := run("h0")
	bad.ExitCode, bad.Error = 1, "git: bad object"
	record(t, s, bad)
	heads, err := s.PassedHeads(rule)
	require.NoError(t, err)
	assert.Empty(t, heads)
}

func TestPassedHeads_ErrorAndInterruptedAreNotPasses(t *testing.T) {
	s := openTestStore(t)
	record(t, s, run("h0"), judge("error", "a"))
	record(t, s, run("h1"), judge("interrupted", "b"))
	heads, err := s.PassedHeads(rule)
	require.NoError(t, err)
	assert.Empty(t, heads)
}

func TestPassedHeads_SkipDoesNotSpoilAPass(t *testing.T) {
	s := openTestStore(t)
	record(t, s, run("h0"), judge("skip", "a"), script("pass"))
	heads, err := s.PassedHeads(rule)
	require.NoError(t, err)
	assert.Equal(t, []string{"h0"}, heads)
}

func TestPassedHeads_ScopedToTheRuleNotItsHash(t *testing.T) {
	s := openTestStore(t)
	record(t, s, run("h0"))
	edited := run("h9")
	edited.Metadata = map[string]any{"ruleHash": "h2"}
	record(t, s, edited)
	other := run("h5")
	other.CheckID = "file-guard/other"
	record(t, s, other)

	heads, err := s.PassedHeads(rule)
	require.NoError(t, err)
	assert.Equal(t, []string{"h9", "h0"}, heads, "a pass under an older definition still approved its work; another rule's runs are not this one's")
}

// ResolveStale is a sanctioned no-op (a skip recorded over a content-keyed fail would hide the
// refusal from every other branch): whatever the fail's relation to the live run (its input
// left the range, the live run holds it too, another run is still in flight), it stays stored
// and replayed, and nothing is written.
func TestResolveStale_IsANoOpAStoredFailStaysVisible(t *testing.T) {
	s := openTestStore(t)
	record(t, s, run("h0"), judge("fail", "old"))
	record(t, s, run("h0"), judge("fail", "same"))
	inflight, err := s.RecordRun(run("h0")) // another Stop's run, still judging
	require.NoError(t, err)
	_, err = s.RecordCheck(inflight, judge("fail", "theirs"))
	require.NoError(t, err)
	live := record(t, s, run("h1"), judge("fail", "same"), judge("pass", "new"))

	n, err := s.ResolveStale(rule, live)
	require.NoError(t, err)
	assert.Equal(t, 0, n)
	rows, err := s.Query(`SELECT status FROM checks WHERE fingerprint = 'old'`)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "fail", rows[0]["status"])

	c, found, err := s.cached("changeset", "check[1]:judge:./rubric.md.j2", "old")
	require.NoError(t, err)
	require.True(t, found, "the other branch's fail is still replayed")
	assert.Equal(t, StatusFail, c.Status)
}

func TestCheckStatus_LatestRunPerRuleAndFailingFilter(t *testing.T) {
	s := openTestStore(t)
	record(t, s, run("h0"), judge("fail", "a"))
	record(t, s, run("h1"), script("pass"), judge("fail", "b"))
	clean := run("h1")
	clean.CheckID = "file-guard/other"
	record(t, s, clean, script("pass"))
	empty := run("h1")
	empty.CheckID = "file-guard/empty"
	record(t, s, empty)
	bad := run("h1")
	bad.CheckID = "file-guard/broken"
	bad.ExitCode, bad.Error = 1, "git: bad"
	record(t, s, bad)

	all, err := s.CheckStatus(false, "")
	require.NoError(t, err)
	byRule := map[string][]string{}
	for _, r := range all {
		byRule[r.Rule] = append(byRule[r.Rule], r.Status)
	}
	assert.Equal(t, []string{"pass", "fail"}, byRule[rule], "only the latest run's checks")
	assert.Equal(t, []string{"pass"}, byRule["file-guard/other"])
	assert.Equal(t, []string{"pass"}, byRule["file-guard/empty"], "a run with no checks reads as a pass")
	assert.Equal(t, []string{"error"}, byRule["file-guard/broken"], "an engine failure reads as an error")

	failing, err := s.CheckStatus(true, "")
	require.NoError(t, err)
	got := map[string]string{}
	for _, r := range failing {
		got[r.Rule] = r.Status
	}
	assert.Equal(t, map[string]string{rule: "fail", "file-guard/broken": "error"}, got)
	for _, r := range failing {
		if r.Rule == "file-guard/broken" {
			assert.Equal(t, "git: bad", r.Error)
		}
	}
}

func TestQuery_ReadsAndRefusesToWrite(t *testing.T) {
	s := openTestStore(t)
	record(t, s, run("h0"), script("pass"))

	rows, err := s.Query(`select count(*) as n from checks`)
	require.NoError(t, err)
	assert.EqualValues(t, 1, rows[0]["n"])

	_, err = s.Query(`delete from checks`)
	assert.Error(t, err)
	_, err = s.Query(`select 1; delete from checks`)
	// Whatever the driver does with a second statement, nothing was written.
	after, qerr := s.Query(`select count(*) as n from checks`)
	require.NoError(t, qerr)
	assert.EqualValues(t, 1, after[0]["n"], "err=%v", err)
	_, err = s.Query(`with x as (select 1) delete from checks`)
	assert.Error(t, err, "query_only stops a write that starts with WITH")
	after, _ = s.Query(`select count(*) as n from checks`)
	assert.EqualValues(t, 1, after[0]["n"])
}

func TestQuery_LeavesTheStoreWritableAfterwards(t *testing.T) {
	s := openTestStore(t)
	_, err := s.Query(`select 1 as one`)
	require.NoError(t, err)
	record(t, s, run("h0"), script("pass"))
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
	c, found, err := second.cached("changeset", "check[1]:judge:./rubric.md.j2", "fp")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, StatusFail, c.Status)
}

func TestCheckResults_ClosedStoreReportsErrClosed(t *testing.T) {
	s := Open(checkcache.NewMemory(), false).(*store)
	require.NoError(t, s.Close())
	_, err := s.RecordRun(run("h0"))
	assert.ErrorIs(t, err, ErrClosed)
	_, _, err = s.cached("a", "b", "c")
	assert.ErrorIs(t, err, ErrClosed)
}

// A run that failed is never a watermark, whatever other runs did after it: only the run that
// passed is a head.
func TestPassedHeads_AFailedRunIsNotAHeadOnlyTheLaterPassIs(t *testing.T) {
	s := openTestStore(t)
	record(t, s, run("h0"), judge("fail", "old"))
	record(t, s, run("h1"), judge("pass", "new"))

	heads, err := s.PassedHeads(rule)
	require.NoError(t, err)
	assert.Equal(t, []string{"h1"}, heads, "h0 failed; only h1 passed")
}

// a10n #7: a run that died mid-judge must not read as a pass. Its row is written
// before its checks, and a run with no checks looks exactly like one with
// nothing to check.
func TestPassedHeads_AnUnfinishedRunIsNeverAWatermark(t *testing.T) {
	s := openTestStore(t)
	dead, err := s.RecordRun(run("h0")) // recorded running, never finished
	require.NoError(t, err)

	heads, err := s.PassedHeads(rule)
	require.NoError(t, err)
	assert.Empty(t, heads, "a run that never finished passed nothing")

	rows, err := s.CheckStatus(true, "")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, StatusInterrupted, rows[0].Status, "and it shows as interrupted, not as a pass")

	_, err = s.RecordCheck(dead, script("pass"))
	require.NoError(t, err)
	heads, _ = s.PassedHeads(rule)
	assert.Empty(t, heads, "checks recorded but the run not finished: still not a pass")
	rows, err = s.CheckStatus(true, "")
	require.NoError(t, err)
	require.Len(t, rows, 1, "a running run with a passing check is still listed as failing-or-unfinished")
	assert.Equal(t, StatusInterrupted, rows[0].Status, "the run's state wins over its checks")

	require.NoError(t, s.FinishRun(dead))
	heads, err = s.PassedHeads(rule)
	require.NoError(t, err)
	assert.Equal(t, []string{"h0"}, heads)
}

func TestFinishRun_UnknownRunIsAnError(t *testing.T) {
	assert.Error(t, openTestStore(t).FinishRun("run_nope"))
}

func TestPassedHeads_ACompleteRunWithNothingToCheckIsAPass(t *testing.T) {
	s := openTestStore(t)
	r := run("h0")
	r.Complete = true
	_, err := s.RecordRun(r)
	require.NoError(t, err)
	heads, err := s.PassedHeads(rule)
	require.NoError(t, err)
	assert.Equal(t, []string{"h0"}, heads)
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

// Nothing ResolveStale does reaches the backend: a store opened afterwards still replays the fail.
func TestResolveStale_LeavesNothingInTheBackend(t *testing.T) {
	backend := checkcache.NewMemory()
	first := Open(backend, false).(*store)
	record(t, first, run("h0"), judge("fail", "old"))
	live := record(t, first, run("h1"), judge("pass", "new"))
	n, err := first.ResolveStale(rule, live)
	require.NoError(t, err)
	require.Equal(t, 0, n)
	require.NoError(t, first.Close())

	second := Open(backend, false).(*store)
	defer second.Close()
	c, found, err := second.cached("changeset", "check[1]:judge:./rubric.md.j2", "old")
	require.NoError(t, err)
	require.True(t, found, "a fail is replayed")
	assert.Equal(t, StatusFail, c.Status)
	runs, err := backend.Runs()
	require.NoError(t, err)
	assert.Len(t, runs, 2, "no resolving run was written")
}

func TestCheckResults_ClosedStoreRefusesEveryMethod(t *testing.T) {
	s := Open(checkcache.NewMemory(), false)
	id, err := s.RecordRun(run("h0"))
	require.NoError(t, err)
	require.NoError(t, s.Close())
	_, err = s.RecordCheck(id, script("pass"))
	assert.ErrorIs(t, err, ErrClosed)
	assert.ErrorIs(t, s.FinishRun(id), ErrClosed)
	_, err = s.PassedHeads(rule)
	assert.ErrorIs(t, err, ErrClosed)
	_, err = s.Query(`select 1`)
	assert.ErrorIs(t, err, ErrClosed)
}

// EffectiveRuns is the input of a10n's GetEffectiveBase over evaluations: the runs of one batch
// over one head are one evaluation (a run per subject), and it passed only when every one did.
func TestEffectiveRuns_AnEvaluationPassesOnlyWhenEverySubjectDid(t *testing.T) {
	s := openTestStore(t)
	a := func(head, batch string) CheckRun { r := run(head); r.BatchID = batch; r.RuleHash = "h1"; return r }
	record(t, s, a("h0", "b0"), script("pass"))
	record(t, s, a("h1", "b1"), script("pass"))
	record(t, s, a("h1", "b1"), script("fail")) // one subject of h1's evaluation failed
	record(t, s, a("h2", "b2"), script("pass"))
	other := a("h3", "b3")
	other.RuleHash = "h2" // another definition of the rule: its pass counts too
	record(t, s, other, script("pass"))
	unfinished, err := s.RecordRun(a("h4", "b4"))
	require.NoError(t, err)
	_ = unfinished

	runs, err := s.EffectiveRuns(rule)
	require.NoError(t, err)
	var heads []string
	for _, r := range runs {
		assert.Equal(t, "base0", r.Base, "each carries the range it covered")
		heads = append(heads, r.Head)
	}
	assert.Equal(t, []string{"h3", "h2", "h0"}, heads, "newest first, whatever the rule hash; a failed subject and an unfinished run are no base")
}

// A process waiting on another's in-flight key reads the verdict the holder flushed before it
// let go: a second store over the same backend finds it without the first having closed, and
// Close does not write the flushed run twice.
func TestFlushRun_AWaitingProcessReusesTheVerdict(t *testing.T) {
	backend := checkcache.NewMemory()
	first := Open(backend, false).(*store)
	second := Open(backend, false).(*store)
	t.Cleanup(func() { second.Close() })

	id := record(t, first, run("h0"), judge("pass", "fp1"))
	_, found, err := second.cached("changeset", "check[1]:judge:./rubric.md.j2", "fp1")
	require.NoError(t, err)
	assert.False(t, found, "not stored before the flush")

	require.NoError(t, first.FlushRun(id))
	c, found, err := second.cached("changeset", "check[1]:judge:./rubric.md.j2", "fp1")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, StatusPass, c.Status)

	require.NoError(t, first.Close())
	runs, err := backend.Runs()
	require.NoError(t, err)
	assert.Len(t, runs, 1)
}
