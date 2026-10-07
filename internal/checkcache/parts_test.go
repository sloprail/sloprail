package checkcache

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The test's key derivation: whatever the engine's is, it is a function of the two parts.
func partsKey(files []byte, subjectFingerprint string) string {
	sum := sha256.Sum256(append(append([]byte("v2:"), files...), subjectFingerprint...))
	return hex.EncodeToString(sum[:])
}

func guardWithParts(subject string, i int) Check {
	return Check{Subject: subject, Kind: "guard", Status: StatusPass, Fingerprint: fmt.Sprintf("old-%s-%d", subject, i),
		FilesPart: []byte(fmt.Sprintf("path/%d\x00blob:%040d", i, i)), SubjectFingerprint: fmt.Sprintf("fp-%d", i%7)}
}

func declaredAll(string) bool { return true }

// A record that carries its parts is re-keyed by deriving the fingerprint again from them: no
// repository read, no script, no judge. Checks without parts, failures and other kinds are left
// as they were, a rule that is gone is counted as skipped, and the caller's runs are never edited.
func TestMigrateFromParts_RewritesTheRecordsFromTheirParts(t *testing.T) {
	gone := Run{ID: "r-gone", Rule: "file-guard/gone", Checks: []Check{guardWithParts("a", 1)}}
	kept := Run{ID: "r1", Rule: "file-guard/x", Checks: []Check{
		guardWithParts("a", 2),
		{Subject: "b", Kind: "guard", Status: StatusPass, Fingerprint: "no-parts"},                     // an older build's record
		{Subject: "c", Kind: "guard", Status: StatusFail, Fingerprint: "fail", FilesPart: []byte("x")}, // only a pass is carried
		{Subject: "a", Kind: "check[0]:judge:./r.md.j2", Status: StatusPass, Fingerprint: "step", FilesPart: []byte("x")},
	}}
	old := []Run{kept, gone}
	before := fmt.Sprint(old)

	got, st := MigrateFromParts(old, func(rule string) bool { return rule == "file-guard/x" }, partsKey, StatusPass, "guard")

	assert.Equal(t, before, fmt.Sprint(old), "the runs given are never edited")
	assert.Equal(t, 1, st.Migrated)
	assert.Equal(t, 1, st.Skipped)
	assert.Equal(t, map[string]int{ReasonRuleGone: 1}, st.Reasons)
	assert.Equal(t, partsKey(kept.Checks[0].FilesPart, kept.Checks[0].SubjectFingerprint), got[0].Checks[0].Fingerprint)
	assert.Equal(t, kept.Checks[0].FilesPart, got[0].Checks[0].FilesPart, "the parts stay with the record, for the next change")
	for i := 1; i < 4; i++ {
		assert.Equal(t, kept.Checks[i], got[0].Checks[i], "check %d is left as it was", i)
	}
	assert.Equal(t, gone.Checks[0].Fingerprint, got[1].Checks[0].Fingerprint, "a rule that is gone keeps what it had")
}

// The whole store is rewritten from the parts, fast, with the repository's only job being to
// hold the records: many thousands of verdicts are found under the new key afterwards.
func TestMigrateKeys_FromPartsIsAFastRewriteOfTheSegments(t *testing.T) {
	const perRun, nRuns = 50, 60
	n := perRun * nRuns
	runs := make([]Run, 0, nRuns)
	for r := 0; r < nRuns; r++ {
		checks := make([]Check, 0, perRun)
		for j := 0; j < perRun; j++ {
			i := r*perRun + j
			checks = append(checks, guardWithParts(fmt.Sprintf("s%d", i), i))
		}
		runs = append(runs, Run{ID: fmt.Sprintf("r%05d", r), RunAt: fmt.Sprintf("2026-10-01T10:00:%02d.000Z", r),
			Rule: "file-guard/x", BaseRef: "b", HeadRef: "h", Complete: true, Checks: checks})
	}
	s := olderStore(t, "v2026-10-07", "sr2", runs...)
	var calls atomic.Int32
	s.SetRebuild(func(old []Run) ([]Run, MigrationStats, error) {
		calls.Add(1)
		out, st := MigrateFromParts(old, declaredAll, partsKey, StatusPass, "guard")
		return out, st, nil
	})

	start := time.Now()
	st, done, err := s.MigrateKeys()
	took := time.Since(start)
	require.NoError(t, err)
	require.True(t, done)
	assert.Equal(t, n, st.Migrated)
	assert.Zero(t, st.Skipped)
	assert.Less(t, took, 10*time.Second, "a rewrite from stored parts does no git work per verdict")
	t.Logf("%d verdicts rewritten in %s", n, took)

	var keys []Key
	for _, i := range []int{0, 1, n / 2, n - 1} {
		c := runs[i/perRun].Checks[i%perRun]
		keys = append(keys, Key{Rule: "file-guard/x", Kind: "guard", Subject: c.Subject, Fingerprint: partsKey(c.FilesPart, c.SubjectFingerprint)})
	}
	got, err := s.Lookup(keys)
	require.NoError(t, err)
	assert.Len(t, got, len(keys), "every verdict is found under the new key")
	assert.Equal(t, int32(1), calls.Load())
}

// Several sessions open the same older store at once: one of them migrates, the others wait for
// it and then find the current directory there. The records are rebuilt once, the ref holds one
// migration commit, and every session carries on (none fails, none rebuilds a second time).
func TestMigrateKeys_ConcurrentOpensMigrateOnce(t *testing.T) {
	s := olderStore(t, "v2026-10-07", "sr2", twoRuleHashes()...)
	var rebuilds atomic.Int32
	rebuild := func(old []Run) ([]Run, MigrationStats, error) {
		rebuilds.Add(1)
		time.Sleep(300 * time.Millisecond) // long enough for every other opener to arrive meanwhile
		return rekey(nil)(old)
	}
	const sessions = 4
	var wg sync.WaitGroup
	var migrated atomic.Int32
	errs := make([]error, sessions)
	for i := 0; i < sessions; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Each session is a process of its own: its own store over the same repository.
			p, err := Open(Options{Dir: s.opt.Dir})
			if err != nil {
				errs[i] = err
				return
			}
			p.SetRebuild(rebuild)
			_, done, err := p.MigrateKeys()
			errs[i] = err
			if done {
				migrated.Add(1)
			}
		}()
	}
	wg.Wait()
	for i, err := range errs {
		require.NoError(t, err, "session %d", i)
	}
	assert.Equal(t, int32(1), migrated.Load(), "one session migrated")
	assert.Equal(t, int32(1), rebuilds.Load(), "the records were rebuilt once")
	assert.Equal(t, "1", git(t, s.opt.Dir, "rev-list", "--count", "--grep=re-key", s.opt.Ref), "the ref holds one migration commit")
	got, err := s.Lookup([]Key{key("new-fp"), key("new-other")})
	require.NoError(t, err)
	assert.Len(t, got, 2)
}
