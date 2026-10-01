package checkstore

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

	c, found, err := s.CachedCheck("changeset", "check[1]:judge:./rubric.md.j2", "fp1")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, StatusPass, c.Status)
	assert.Equal(t, "because pass", c.Metadata["reasoning"])
}

// a fail is terminal: replayed exactly like a pass (a10n's CacheHit reads only passes).
func TestCachedCheck_AFailIsReplayedToo(t *testing.T) {
	s := openTestStore(t)
	record(t, s, run("h0"), judge("fail", "fp1"))

	c, found, err := s.CachedCheck("changeset", "check[1]:judge:./rubric.md.j2", "fp1")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, StatusFail, c.Status)
	assert.Equal(t, "because fail", c.Metadata["reasoning"])
}

func TestCachedCheck_OnlyPassAndFailAreCached(t *testing.T) {
	s := openTestStore(t)
	for i, st := range []string{StatusSkip, StatusError, StatusInterrupted} {
		record(t, s, run("h"+st), judge(st, "fp-"+st))
		_, found, err := s.CachedCheck("changeset", "check[1]:judge:./rubric.md.j2", "fp-"+st)
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
		_, found, err := s.CachedCheck(args[0], args[1], args[2])
		require.NoError(t, err)
		assert.False(t, found, "changing only the %s must miss", name)
	}
}

func TestCachedCheck_AScriptHasNoFingerprintAndIsNeverCached(t *testing.T) {
	s := openTestStore(t)
	id := record(t, s, run("h0"), script("pass"))

	var fp any = "not null"
	require.NoError(t, s.db.QueryRow(`SELECT fingerprint FROM checks WHERE run_id = ?`, id).Scan(&fp))
	assert.Nil(t, fp, "a script's fingerprint is NULL")

	_, found, err := s.CachedCheck("changeset", "check[0]:script:./size.sh", "")
	require.NoError(t, err)
	assert.False(t, found)
}

func TestCachedCheck_TheMostRecentWins(t *testing.T) {
	s := openTestStore(t)
	record(t, s, run("h0"), judge("pass", "fp1"))
	record(t, s, run("h1"), judge("fail", "fp1"))
	c, _, err := s.CachedCheck("changeset", "check[1]:judge:./rubric.md.j2", "fp1")
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

func TestResolveStale_AFailWhoseInputLeftBecomesSkip(t *testing.T) {
	s := openTestStore(t)
	record(t, s, run("h0"), judge("fail", "old"))
	live := record(t, s, run("h1"), judge("pass", "new"))

	n, err := s.ResolveStale(rule, "h1", live)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	rows, err := s.Query(`SELECT status, json_extract(metadata, '$.reason') AS reason FROM checks WHERE fingerprint = 'old'`)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "skip", rows[0]["status"])
	assert.Contains(t, rows[0]["reason"], "stale")

	_, found, err := s.CachedCheck("changeset", "check[1]:judge:./rubric.md.j2", "old")
	require.NoError(t, err)
	assert.False(t, found, "a stale fail is no longer replayed")
}

func TestResolveStale_AFailTheLiveRunStillHoldsStaysAFail(t *testing.T) {
	s := openTestStore(t)
	record(t, s, run("h0"), judge("fail", "same"))
	live := record(t, s, run("h1"), judge("fail", "same")) // the replay

	n, err := s.ResolveStale(rule, "h1", live)
	require.NoError(t, err)
	assert.Equal(t, 0, n)
}

func TestResolveStale_OnlyTheRuleAtThisHashAndOnlyFails(t *testing.T) {
	s := openTestStore(t)
	other := run("h0")
	other.CheckID = "file-guard/other"
	record(t, s, other, judge("fail", "x"))
	oldDef := run("h0")
	oldDef.Metadata = map[string]any{"ruleHash": "h0"}
	record(t, s, oldDef, judge("fail", "y"))
	record(t, s, run("h0"), judge("pass", "z"))
	live := record(t, s, run("h1"))

	n, err := s.ResolveStale(rule, "h1", live)
	require.NoError(t, err)
	assert.Equal(t, 0, n)
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
	path := filepath.Join(t.TempDir(), "state.db")
	first, err := Open(path)
	require.NoError(t, err)
	id, err := first.RecordRun(run("h0"))
	require.NoError(t, err)
	_, err = first.RecordCheck(id, judge("fail", "fp"))
	require.NoError(t, err)
	require.NoError(t, first.Close())

	second, err := Open(path)
	require.NoError(t, err)
	defer second.Close()
	c, found, err := second.CachedCheck("changeset", "check[1]:judge:./rubric.md.j2", "fp")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, StatusFail, c.Status)
}

func TestCheckResults_ClosedStoreReportsErrClosed(t *testing.T) {
	s, err := open(":memory:")
	require.NoError(t, err)
	require.NoError(t, s.Close())
	_, err = s.RecordRun(run("h0"))
	assert.ErrorIs(t, err, ErrClosed)
	_, _, err = s.CachedCheck("a", "b", "c")
	assert.ErrorIs(t, err, ErrClosed)
}

// Clearing a stale fail must not turn the run that failed into a pass: a
// watermark there would sweep a refused range into the baseline.
func TestPassedHeads_ARunWhoseFailWentStaleStillDidNotPass(t *testing.T) {
	s := openTestStore(t)
	failedRun := record(t, s, run("h0"), judge("fail", "old"))
	live := record(t, s, run("h1"), judge("pass", "new"))

	n, err := s.ResolveStale(rule, "h1", live)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	_ = failedRun

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

func TestResolveStale_LeavesAnotherRunsInFlightFailsAlone(t *testing.T) {
	s := openTestStore(t)
	// A concurrent Stop's run for the same rule and hash, still running.
	inflight, err := s.RecordRun(run("h0"))
	require.NoError(t, err)
	_, err = s.RecordCheck(inflight, judge("fail", "theirs"))
	require.NoError(t, err)
	live := record(t, s, run("h1"), judge("pass", "mine"))

	n, err := s.ResolveStale(rule, "h1", live)
	require.NoError(t, err)
	assert.Equal(t, 0, n, "an unfinished run's failures are not stale: it has not finished judging")
}
