package checkrun

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/checkcache"
	"github.com/sloprail/sloprail/internal/checkstore"
	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/gitrepo"
)

// rekeyFixture is a real repository with a tiny rule: `docs/**`, a `subjects:` script that
// names one subject "api" (fingerprint "schema-v1") and a JUDGE check. SR_CHECKS_JUDGE_MOCKS is
// empty, so any judge call is an error: a migration that judged would show.
type rekeyFixture struct {
	repo       string
	base, head string
	guard      declaration.FileGuard
}

func newRekeyFixture(t *testing.T) *rekeyFixture {
	t.Helper()
	t.Setenv("SR_CHECKS_JUDGE_MOCKS", "{}")
	repo := initRepo(t)
	gitT(t, repo, "config", "gc.auto", "0")
	base := commitFile(t, repo, "seed.txt", "seed")
	dir := filepath.Join(repo, ".sloprail", "file-guard", "docs")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "subjects.sh"),
		[]byte("#!/bin/sh\ncat >/dev/null\necho '[{\"id\":\"api\",\"files\":[\"docs/a.md\"],\"fingerprint\":\"schema-v1\"}]'\n"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "j.md.j2"), []byte("Judge {{ change }}\n"), 0o644))
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-m", "the rule")
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "docs"), 0o755))
	head := commitFile(t, repo, "docs/a.md", "clean")
	g := declaration.FileGuard{Name: "docs", Match: "docs/**", Dir: dir, Subjects: "./subjects.sh",
		Checks: []declaration.Check{{Judge: "j.md.j2", Model: "m"}}}
	return &rekeyFixture{repo: repo, base: base, head: head, guard: g}
}

// oldRun is a stored run of the previous schemas: a passing guard verdict for a subject over a
// recorded range, under a fingerprint that is nothing the new key would compute.
func (f *rekeyFixture) oldRun(id, subject, base, head, status string) checkcache.Run {
	return checkcache.Run{
		ID: id, RunAt: "2026-10-01T10:00:00.000Z", Rule: f.guard.Qualified(), RuleHash: "old-hash",
		BaseRef: base, HeadRef: head, Complete: true,
		Checks: []checkcache.Check{{Subject: subject, Kind: guardKind, Status: status, Fingerprint: "old-fingerprint-with-citations-" + id}},
	}
}

func (f *rekeyFixture) refTip(t *testing.T) string {
	t.Helper()
	return runGit(t, f.repo, "rev-parse", "refs/sloprail/checks")
}

// run is `sr-checks run` (or verify) over the fixture's range, from a freshly opened cache.
func (f *rekeyFixture) evaluate(t *testing.T, write bool) (string, []FileGuardResult, []CheckOutcome) {
	t.Helper()
	var w bytes.Buffer
	cache, err := OpenCache(&w, f.repo, write, []declaration.FileGuard{f.guard})
	require.NoError(t, err)
	results := checkstore.Open(cache, !write)
	rng, err := gitrepo.ResolveRange(f.repo, f.base, f.head)
	require.NoError(t, err)
	got, outcomes := Evaluate(Params{Guards: []declaration.FileGuard{f.guard}, Root: f.repo, Range: rng, Cwd: f.repo,
		SessionID: "s-rekey", Store: results, Verify: !write, Err: &w})
	require.NoError(t, results.Close())
	return w.String(), got, outcomes
}

// An older store (the released v2026-10-03 with sr1 keys, or the unreleased v2026-10-07 with
// sr2 keys) is carried across by rebuilding each stored pass's key at its recorded range, with
// the rule's subjects script as it is in the head tree: the first run then HITS under the new
// key and asks no judge (an unmocked judge is an error here). Records whose commits are
// unreachable, whose subject is gone or whose rule is no longer declared are skipped and
// counted, and a second run writes nothing.
func TestRekey_OlderStoresAreRebuiltWithoutJudging(t *testing.T) {
	for name, tc := range map[string]struct{ dir, version string }{
		"v2026-10-03 (sr1)": {"v2026-10-03", "sr1"},
		"v2026-10-07 (sr2)": {"v2026-10-07", "sr2"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newRekeyFixture(t)
			ghost := "0123456789abcdef0123456789abcdef01234567"
			otherRule := f.oldRun("r5", "api", f.base, f.head, checkstore.StatusPass)
			otherRule.Rule = "file-guard/removed"
			require.NoError(t, checkcache.PutAsOlder(checkcache.Options{Dir: f.repo}, tc.dir, tc.version,
				f.oldRun("r1", "api", f.base, f.head, checkstore.StatusPass),
				f.oldRun("r2", "api", f.base, ghost, checkstore.StatusPass),           // unreachable commits
				f.oldRun("r3", "gone-subject", f.base, f.head, checkstore.StatusPass), // the subject no longer appears
				f.oldRun("r4", "api", f.base, f.head, checkstore.StatusFail),          // only a pass is carried
				otherRule,
			))

			// Before: the first (write) open rebuilds, and says so in one line.
			out, got, outcomes := f.evaluate(t, true)
			assert.Contains(t, out, "migrated 1, skipped 3 (commits unreachable: 1, rule no longer declared: 1, subject no longer in the range: 1)")
			assert.Empty(t, got, "the rebuilt verdict is a hit: the judge was never asked (it would error)")
			for _, o := range outcomes {
				assert.Equal(t, checkstore.StatusPass, o.Status, o.Reason)
			}
			tip := f.refTip(t)

			// A read-only open never migrates, and the new key is found by verify.
			_, got, _ = f.evaluate(t, false)
			assert.Empty(t, got)

			// A second run is a no-op: the current directory is the marker.
			out, got, _ = f.evaluate(t, true)
			assert.NotContains(t, out, "migrated")
			assert.Empty(t, got)
			assert.NotEqual(t, tip, f.refTip(t), "the run itself recorded its replay")
			assert.Equal(t, 1, countCommitsMatching(t, f.repo, "re-key"), "the migration is one commit, once")
		})
	}
}

func countCommitsMatching(t *testing.T, repo, text string) int {
	t.Helper()
	out := runGit(t, repo, "log", "--format=%s", "--grep="+text, "refs/sloprail/checks")
	if out == "" {
		return 0
	}
	return len(bytes.Split([]byte(out), []byte("\n")))
}

// What the rebuild stores is a pure function of the record and the repository: the new
// fingerprint is the key `run` computes now, whatever the old one held.
func TestRekey_RebuiltKeysEqualTheKeysRunComputes(t *testing.T) {
	f := newRekeyFixture(t)
	fps, reason := rangeFingerprints(f.repo, map[string]declaration.FileGuard{f.guard.Qualified(): f.guard},
		rekeyGroup{rule: f.guard.Qualified(), base: f.base, head: f.head})
	require.Empty(t, reason)
	require.Contains(t, fps, "api")
	assert.NotEmpty(t, fps["api"].key)

	// The same rule, the subject's fingerprint moved: the key moves with it.
	require.NoError(t, os.WriteFile(filepath.Join(f.guard.Dir, "subjects.sh"),
		[]byte("#!/bin/sh\ncat >/dev/null\necho '[{\"id\":\"api\",\"files\":[\"docs/a.md\"],\"fingerprint\":\"schema-v2\"}]'\n"), 0o755))
	runGit(t, f.repo, "add", "-A")
	runGit(t, f.repo, "commit", "-m", "the subjects script now fingerprints v2")
	head2 := runGit(t, f.repo, "rev-parse", "HEAD")
	moved, reason := rangeFingerprints(f.repo, map[string]declaration.FileGuard{f.guard.Qualified(): f.guard},
		rekeyGroup{rule: f.guard.Qualified(), base: f.base, head: head2})
	require.Empty(t, reason)
	assert.NotEqual(t, fps["api"].key, moved["api"].key, "the script as it is in the head tree decides the fingerprint")
}

// The control: with no older verdict to carry across, the same evaluation asks the judge,
// which here is an error. So the green runs above did find their verdict under the rebuilt key.
func TestRekey_ControlAnUnmigratedStoreWouldAskTheJudge(t *testing.T) {
	f := newRekeyFixture(t)
	_, got, _ := f.evaluate(t, true)
	require.Len(t, got, 1)
	assert.Contains(t, got[0].Reason, "SR_CHECKS_JUDGE_MOCKS")
}
