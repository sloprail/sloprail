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
	cache, err := OpenCache(&w, f.repo, write)
	require.NoError(t, err)
	results := checkstore.Open(cache, !write)
	rng, err := gitrepo.ResolveRange(f.repo, f.base, f.head)
	require.NoError(t, err)
	got, outcomes := Evaluate(Params{Guards: []declaration.FileGuard{f.guard}, Root: f.repo, Range: rng, Cwd: f.repo,
		SessionID: "s-rekey", Store: results, Verify: !write, Err: &w})
	require.NoError(t, results.Close())
	return w.String(), got, outcomes
}

// migrate is `sr-checks migrate-keys`.
func (f *rekeyFixture) migrate(t *testing.T) string {
	t.Helper()
	var w bytes.Buffer
	rekeyProgress = &w
	t.Cleanup(func() { rekeyProgress = os.Stderr })
	require.NoError(t, MigrateKeys(&w, f.repo, []declaration.FileGuard{f.guard}))
	return w.String()
}

// dirOid is the object id of a schema directory in the ref: the same while it is untouched.
func (f *rekeyFixture) dirOid(t *testing.T, dir string) string {
	t.Helper()
	return runGit(t, f.repo, "rev-parse", "refs/sloprail/checks:"+dir)
}

var olderLayouts = map[string]struct{ dir, version string }{
	"v2026-10-03 (sr1)": {"v2026-10-03", "sr1"},
	"v2026-10-07 (sr2)": {"v2026-10-07", "sr2"},
}

// Opening and running over an older store converts nothing: the new layout starts empty beside
// it, the older directory stays exactly as it was, and what was judged before is judged again
// (an unmocked judge is an error here, so a miss shows). Nobody waits for a rebuild.
func TestRekey_RunNeverMigratesAnOlderStore(t *testing.T) {
	for name, tc := range olderLayouts {
		t.Run(name, func(t *testing.T) {
			f := newRekeyFixture(t)
			require.NoError(t, checkcache.PutAsOlder(checkcache.Options{Dir: f.repo}, tc.dir, tc.version,
				f.oldRun("r1", "api", f.base, f.head, checkstore.StatusPass)))
			older := f.dirOid(t, tc.dir)

			out, got, _ := f.evaluate(t, true)
			assert.NotContains(t, out, "migrat", "no migration, no progress line")
			require.Len(t, got, 1, "the stored verdict is not found under the new key: the judge is asked")
			assert.Contains(t, got[0].Reason, "SR_CHECKS_JUDGE_MOCKS")
			assert.Equal(t, older, f.dirOid(t, tc.dir), "the older directory is untouched")
			assert.Equal(t, 0, countCommitsMatching(t, f.repo, "re-key"))

			out, got, _ = f.evaluate(t, false)
			assert.NotContains(t, out, "migrat")
			assert.NotEmpty(t, got, "verify finds nothing under the new key either")
		})
	}
}

// `migrate-keys` carries each subject's newest pass across by rebuilding its key at its recorded
// range, with the rule's subjects script as it is in the head tree: the next run then HITS under
// the new key and asks no judge. Older passes of the subject, records whose commits are
// unreachable, whose subject is gone or whose rule is no longer declared are skipped and counted,
// the older directory stays untouched, and a second call does nothing.
func TestRekey_MigrateKeysCarriesTheNewestPassesWithoutJudging(t *testing.T) {
	for name, tc := range olderLayouts {
		t.Run(name, func(t *testing.T) {
			f := newRekeyFixture(t)
			missing := "0123456789abcdef0123456789abcdef01234567"
			superseded := f.oldRun("r0", "api", f.base, f.head, checkstore.StatusPass)
			superseded.RunAt = "2026-09-01T10:00:00.000Z" // an older pass of the same subject
			otherRule := f.oldRun("r5", "api", f.base, f.head, checkstore.StatusPass)
			otherRule.Rule = "file-guard/removed"
			require.NoError(t, checkcache.PutAsOlder(checkcache.Options{Dir: f.repo}, tc.dir, tc.version,
				superseded,
				f.oldRun("r1", "api", f.base, f.head, checkstore.StatusPass),
				f.oldRun("r2", "elsewhere", f.base, missing, checkstore.StatusPass),   // unreachable commits
				f.oldRun("r3", "gone-subject", f.base, f.head, checkstore.StatusPass), // the subject no longer appears
				f.oldRun("r4", "api", f.base, f.head, checkstore.StatusFail),          // only a pass is carried
				otherRule,
			))
			older := f.dirOid(t, tc.dir)

			out := f.migrate(t)
			assert.Contains(t, out, "migrated 1, skipped 4 (commits unreachable: 1, rule no longer declared: 1, subject no longer in the range: 1, superseded by a newer pass: 1)")
			assert.Contains(t, out, "migrating cache keys: ", "progress is reported")
			assert.Equal(t, older, f.dirOid(t, tc.dir), "the older directory is untouched")
			assert.Equal(t, 1, countCommitsMatching(t, f.repo, "re-key"))

			// The next run HITS: the judge, which would be an error, is never asked.
			out, got, outcomes := f.evaluate(t, true)
			assert.NotContains(t, out, "migrated")
			assert.Empty(t, got)
			for _, o := range outcomes {
				assert.Equal(t, checkstore.StatusPass, o.Status, o.Reason)
			}
			_, got, _ = f.evaluate(t, false)
			assert.Empty(t, got, "verify finds the carried verdict")

			// A second call finds it done and writes nothing.
			tip := f.refTip(t)
			assert.Contains(t, f.migrate(t), "nothing to migrate")
			assert.Equal(t, tip, f.refTip(t))
			assert.Equal(t, 1, countCommitsMatching(t, f.repo, "re-key"), "the migration is one commit, once")
		})
	}
}

// After a run has already started the new layout, the migration still carries the older pass,
// next to what the run wrote.
func TestRekey_MigrateKeysAfterTheNewLayoutWasStarted(t *testing.T) {
	f := newRekeyFixture(t)
	require.NoError(t, checkcache.PutAsOlder(checkcache.Options{Dir: f.repo}, "v2026-10-07", "sr2",
		f.oldRun("r1", "api", f.base, f.head, checkstore.StatusPass)))
	_, got, _ := f.evaluate(t, true) // starts the new layout (the judge errors: nothing stored under the key)
	require.Len(t, got, 1)

	assert.Contains(t, f.migrate(t), "migrated 1, skipped 0")
	_, got, _ = f.evaluate(t, true)
	assert.Empty(t, got, "carried, and found")
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
	assert.NotEmpty(t, fps["api"])

	// The same rule, the subject's fingerprint moved: the key moves with it.
	require.NoError(t, os.WriteFile(filepath.Join(f.guard.Dir, "subjects.sh"),
		[]byte("#!/bin/sh\ncat >/dev/null\necho '[{\"id\":\"api\",\"files\":[\"docs/a.md\"],\"fingerprint\":\"schema-v2\"}]'\n"), 0o755))
	runGit(t, f.repo, "add", "-A")
	runGit(t, f.repo, "commit", "-m", "the subjects script now fingerprints v2")
	head2 := runGit(t, f.repo, "rev-parse", "HEAD")
	moved, reason := rangeFingerprints(f.repo, map[string]declaration.FileGuard{f.guard.Qualified(): f.guard},
		rekeyGroup{rule: f.guard.Qualified(), base: f.base, head: head2})
	require.Empty(t, reason)
	assert.NotEqual(t, fps["api"], moved["api"], "the script as it is in the head tree decides the fingerprint")
}

// The control: with no older verdict to carry across, the same evaluation asks the judge,
// which here is an error. So the green runs above did find their verdict under the rebuilt key.
func TestRekey_ControlAnUnmigratedStoreWouldAskTheJudge(t *testing.T) {
	f := newRekeyFixture(t)
	_, got, _ := f.evaluate(t, true)
	require.Len(t, got, 1)
	assert.Contains(t, got[0].Reason, "SR_CHECKS_JUDGE_MOCKS")
}
