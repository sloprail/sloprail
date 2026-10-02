package checkcache

import (
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/sessionpath"
)

func lookupAll(t *testing.T, s *Store, rs []Run) int {
	t.Helper()
	got, err := s.Lookup(keysOf(rs))
	if err != nil {
		t.Fatal(err)
	}
	return len(got)
}

// A push that fails (the remote is unreachable) never loses results: they are in the local ref,
// found by Lookup, and go out on the next Sync once the remote is back.
func TestOfflinePutKeepsResultsAndPushesLater(t *testing.T) {
	remote := filepath.Join(t.TempDir(), "remote.git") // does not exist yet: offline
	s := newRepoOpt(t, Options{Remote: remote, NoAutoGc: true})
	first, second := genRuns(1, 4), genRuns(2, 3)
	if err := s.Put(first); err != nil {
		t.Fatalf("an offline put must still succeed: %v", err)
	}
	if err := s.Put(second); err != nil {
		t.Fatal(err)
	}
	if s.PendingPush() == nil {
		t.Fatal("the failed push must be reported for the next attempt")
	}
	if n := lookupAll(t, s, append(append([]Run{}, first...), second...)); n != 7 {
		t.Fatalf("local results found = %d, want 7", n)
	}
	if err := s.Sync(); err == nil {
		t.Fatal("a failed fetch is still reported by Sync")
	}
	if n := lookupAll(t, s, first); n != 4 {
		t.Fatalf("a failed sync dropped local results: %d", n)
	}

	git(t, filepath.Dir(remote), "init", "-q", "--bare", remote) // back online
	if err := s.Sync(); err != nil {
		t.Fatal(err)
	}
	if s.PendingPush() != nil {
		t.Fatalf("pending push after sync: %v", s.PendingPush())
	}
	reader := newRepoOpt(t, Options{Remote: remote})
	if err := reader.Sync(); err != nil {
		t.Fatal(err)
	}
	if n := lookupAll(t, reader, append(append([]Run{}, first...), second...)); n != 7 {
		t.Fatalf("the remote holds %d of 7 results", n)
	}
}

// Offline results and results another machine pushed meanwhile are both kept: the next sync
// merges them (verify reads local union fetched), linear, and pushes the union.
func TestOfflineResultsMergeWithWhatTheRemoteGainedMeanwhile(t *testing.T) {
	remote := bareRemote(t)
	a := newRepoOpt(t, Options{Remote: remote, NoAutoGc: true})
	b := newRepoOpt(t, Options{Remote: remote, NoAutoGc: true})
	base := genRuns(10, 3)
	if err := a.Put(base); err != nil {
		t.Fatal(err)
	}
	if err := b.Sync(); err != nil {
		t.Fatal(err)
	}
	// a goes offline and keeps working
	online := a.opt.Remote
	a.opt.Remote = filepath.Join(t.TempDir(), "gone.git")
	offline := genRuns(11, 3)
	if err := a.Put(offline); err != nil {
		t.Fatal(err)
	}
	// b pushes meanwhile
	other := genRuns(12, 3)
	if err := b.Put(other); err != nil {
		t.Fatal(err)
	}
	a.opt.Remote = online
	if err := a.Sync(); err != nil {
		t.Fatal(err)
	}
	all := append(append(append([]Run{}, base...), offline...), other...)
	if n := lookupAll(t, a, all); n != 9 {
		t.Fatalf("after sync a holds %d of 9", n)
	}
	if a.PendingPush() != nil {
		t.Fatal(a.PendingPush())
	}
	if merges := git(t, a.opt.Dir, "rev-list", "--merges", a.opt.Ref); merges != "" {
		t.Fatal("history must stay linear")
	}
	reader := newRepoOpt(t, Options{Remote: remote})
	_ = reader.Sync()
	if n := lookupAll(t, reader, all); n != 9 {
		t.Fatalf("the remote holds %d of 9", n)
	}
}

// Gc keeps the run history: a run with nothing findable and a run all of whose results were
// superseded are still listed by Runs after it.
func TestGcKeepsRunOnlyAndSupersededRuns(t *testing.T) {
	s := newRepoOpt(t, Options{NoAutoGc: true})
	rs := genRuns(20, 5)
	empty := Run{ID: "run_empty", RunAt: "2026-01-01T00:00:00.000000000Z", Rule: "plug/file-guard/x", RuleHash: "h", Error: "git: bad", ExitCode: 1, Checks: []Check{}}
	older := withCheck(rs[0], StatusFail, "2000-01-01T00:00:00Z")
	older.ID = "run_superseded"
	if err := s.Put(append([]Run{empty, older}, rs...)); err != nil {
		t.Fatal(err)
	}
	before, err := s.Runs()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Gc(); err != nil {
		t.Fatal(err)
	}
	after, err := s.Runs()
	if err != nil {
		t.Fatal(err)
	}
	ids := func(rs []Run) string {
		var out []string
		for _, r := range rs {
			out = append(out, r.ID)
		}
		return strings.Join(out, ",")
	}
	if len(before) != 7 || ids(before) != ids(after) {
		t.Fatalf("runs changed across gc:\n before %s\n after  %s", ids(before), ids(after))
	}
}

// A Put that leaves enough runs over several segments and no dictionary compacts and trains
// one by itself; a young store is left alone.
func TestPutTriggersGcWhenADictionaryIsDue(t *testing.T) {
	s := newRepo(t, "")
	if err := s.Put(genRuns(30, 20)); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(genRuns(31, 20)); err != nil {
		t.Fatal(err)
	}
	if sn, _ := s.snapshotAt(s.tip()); len(sn.Segs) != 2 || sn.ManifestDict != "" {
		t.Fatalf("a young store is not compacted: %+v", sn)
	}
	if err := s.Put(genRuns(32, TrainMin)); err != nil {
		t.Fatal(err)
	}
	sn, _ := s.snapshotAt(s.tip())
	if len(sn.Segs) != 1 || sn.ManifestDict == "" || len(sn.Dicts) != 1 {
		t.Fatalf("enough runs: gc must have squashed and trained: segs=%d dict=%q", len(sn.Segs), sn.ManifestDict)
	}
	if rs, _ := s.Runs(); len(rs) != 40+TrainMin {
		t.Fatalf("runs = %d", len(rs))
	}
}

func TestPutTriggersGcOnManySegments(t *testing.T) {
	s := newRepo(t, "")
	for i := 0; i < GcSegments; i++ {
		if err := s.Put(genRuns(int64(40+i), 2)); err != nil {
			t.Fatal(err)
		}
	}
	if sn, _ := s.snapshotAt(s.tip()); len(sn.Segs) >= GcSegments {
		t.Fatalf("segments were never compacted: %d", len(sn.Segs))
	}
}

const legacyDDL = `CREATE TABLE check_runs (id TEXT PRIMARY KEY, run_batch_id TEXT NOT NULL, run_at TEXT NOT NULL, check_id TEXT NOT NULL,
	repo_id TEXT NOT NULL, branch TEXT NOT NULL, session_id TEXT NOT NULL, agent_id TEXT NOT NULL DEFAULT '', base_ref TEXT NOT NULL DEFAULT '',
	head_ref TEXT NOT NULL DEFAULT '', exit_code INTEGER NOT NULL DEFAULT 0, error TEXT, metadata TEXT NOT NULL DEFAULT '{}');
	CREATE TABLE checks (id TEXT PRIMARY KEY, run_id TEXT NOT NULL, subject TEXT NOT NULL, kind TEXT NOT NULL, status TEXT NOT NULL,
	fingerprint TEXT, metadata TEXT NOT NULL DEFAULT '{}');
	CREATE TABLE check_items (id TEXT PRIMARY KEY, check_id TEXT NOT NULL, key TEXT, passed INTEGER NOT NULL DEFAULT 0, metadata TEXT NOT NULL DEFAULT '{}');
	INSERT INTO check_runs (id, run_batch_id, run_at, check_id, repo_id, branch, session_id, base_ref, head_ref, metadata)
		VALUES ('run_old', 'b', '2026-01-01T00:00:00.000000000Z', 'plug/file-guard/size', 'r', 'main', 's1', 'base0', 'h0', '{"ruleHash":"h1","state":"complete"}');
	INSERT INTO checks (id, run_id, subject, kind, status, fingerprint, metadata)
		VALUES ('chk_old', 'run_old', 'changeset', 'check[1]:judge:./rubric.md.j2', 'fail', 'fp-old', '{"reasoning":"nope"}');
	INSERT INTO check_items (id, check_id, key, passed) VALUES ('itm_old', 'chk_old', 'a.go', 0)`

// The first Open on a repository migrates an older engine's sqlite checks.db into the ref,
// once: reopening writes nothing, and the old file is left as it was.
func TestOpenMigratesAnOldSqliteChecksDBOnce(t *testing.T) {
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	top := git(t, dir, "rev-parse", "--show-toplevel")
	old := filepath.Join(data, sessionpath.AppName, "sessions", sessionpath.EncodeWorkspace(top), "s1", "checks.db")
	if err := os.MkdirAll(filepath.Dir(old), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", old)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(legacyDDL); err != nil {
		t.Fatal(err)
	}
	db.Close()
	before, _ := os.ReadFile(old)

	s, err := Open(Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if s.tip() != "" {
		t.Fatal("Open must not migrate")
	}
	s.MigrateLegacy()
	runs, err := s.Runs()
	if err != nil || len(runs) != 1 || runs[0].ID != "run_old" || !runs[0].Complete || runs[0].RuleHash != "h1" {
		t.Fatalf("migrated runs = %+v (%v)", runs, err)
	}
	if got := runs[0].Checks; len(got) != 1 || got[0].Status != StatusFail || len(got[0].Items) != 1 {
		t.Fatalf("migrated checks = %+v", got)
	}
	tip := s.tip()

	// a second Open (another process) and a second migration attempt change nothing
	s2, err := Open(Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	s2.MigrateLegacy()
	if s2.tip() != tip {
		t.Fatal("the migration must be idempotent")
	}
	// even without its marker, a run the ref holds is not written again
	_ = os.Remove(s2.migrationMarker(old))
	s3, err := Open(Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	s3.MigrateLegacy()
	if s3.tip() != tip {
		t.Fatal("a run already in the ref must not be put again")
	}
	if after, _ := os.ReadFile(old); string(after) != string(before) {
		t.Fatal("the old database must be left untouched")
	}
}

func TestOpenWithNoOldDatabaseWritesNothing(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	s := newRepo(t, "")
	if s.tip() != "" {
		t.Fatal("nothing to migrate: no ref")
	}
	if out, _ := exec.Command("git", "-C", s.opt.Dir, "for-each-ref", "refs/sloprail").Output(); len(out) != 0 {
		t.Fatalf("refs written: %s", out)
	}
}

// Pull is what verify/show use: it fetches, and never pushes. Results a reader holds only
// locally stay local, and the remote branch is exactly as another machine left it.
func TestPullFetchesButNeverPushes(t *testing.T) {
	remote := bareRemote(t)
	writer := newRepoOpt(t, Options{Remote: remote, NoAutoGc: true})
	require.NoError(t, writer.Put(genRuns(1, 2)))
	remoteTip := git(t, remote, "rev-parse", "refs/heads/sloprail/checks")

	reader := newRepoOpt(t, Options{Remote: remote, NoAutoGc: true})
	require.NoError(t, reader.Pull())
	got, err := reader.Runs()
	require.NoError(t, err)
	assert.Len(t, got, 2, "what another machine stored is found")
	assert.Equal(t, remoteTip, git(t, remote, "rev-parse", "refs/heads/sloprail/checks"))

	// a result stored while the remote was gone stays pending through a Pull
	online := reader.opt.Remote
	reader.opt.Remote = filepath.Join(t.TempDir(), "gone.git")
	require.NoError(t, reader.Put(genRuns(2, 1)))
	reader.opt.Remote = online
	require.NoError(t, reader.Pull())
	assert.Equal(t, remoteTip, git(t, remote, "rev-parse", "refs/heads/sloprail/checks"), "a Pull never pushes")
}

// Every old session store of the repository is imported: each worktree's, and a store of a
// removed worktree that records the repository's root commit. Another repository's store is
// not. A store is marked once imported, and the import is idempotent.
func TestMigrateLegacyImportsEveryStoreOfTheRepository(t *testing.T) {
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	git(t, dir, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "root")
	root := git(t, dir, "rev-parse", "HEAD")
	top := git(t, dir, "rev-parse", "--show-toplevel")
	other := filepath.Join(t.TempDir(), "wt")
	git(t, dir, "worktree", "add", "-q", "--detach", other)
	otherTop := git(t, other, "rev-parse", "--show-toplevel")

	mk := func(workspace, session, runID, repoID string) string {
		p := filepath.Join(data, sessionpath.AppName, "sessions", workspace, session, "checks.db")
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		db, err := sql.Open("sqlite", p)
		require.NoError(t, err)
		_, err = db.Exec(strings.NewReplacer("run_old", runID, "chk_old", "chk_"+runID, "itm_old", "itm_"+runID, "'r'", "'"+repoID+"'").Replace(legacyDDL))
		require.NoError(t, err)
		db.Close()
		return p
	}
	mk(sessionpath.EncodeWorkspace(top), "s1", "run_a", "x")
	mk(sessionpath.EncodeWorkspace(otherTop), "s2", "run_b", "x")
	mk("removed-worktree", "s3", "run_c", root)
	mk("someone-elses-repo", "s4", "run_d", "unrelated")

	s, err := Open(Options{Dir: dir})
	require.NoError(t, err)
	s.MigrateLegacy()
	runs, err := s.Runs()
	require.NoError(t, err)
	ids := []string{}
	for _, r := range runs {
		ids = append(ids, r.ID)
	}
	assert.ElementsMatch(t, []string{"run_a", "run_b", "run_c"}, ids)

	tip := s.tip()
	s.MigrateLegacy()
	assert.Equal(t, tip, s.tip(), "idempotent")
}

// "A remote without the branch yet" is recognised from git's stderr, so the runner must not
// let the user's locale translate it.
func TestPullOnAFreshRemoteUnderANonEnglishLocale(t *testing.T) {
	t.Setenv("LC_ALL", "de_DE.UTF-8")
	t.Setenv("LANG", "de_DE.UTF-8")
	t.Setenv("LANGUAGE", "de")
	s := newRepo(t, bareRemote(t))
	require.NoError(t, s.Pull())
}
