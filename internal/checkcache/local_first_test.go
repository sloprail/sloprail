package checkcache

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// Opening a store on a fresh repository writes nothing.
func TestOpenWritesNothing(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	s := newRepo(t, "")
	if s.tip() != "" {
		t.Fatal("nothing written: no ref")
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

// "A remote without the branch yet" is recognised from git's stderr, so the runner must not
// let the user's locale translate it.
func TestPullOnAFreshRemoteUnderANonEnglishLocale(t *testing.T) {
	t.Setenv("LC_ALL", "de_DE.UTF-8")
	t.Setenv("LANG", "de_DE.UTF-8")
	t.Setenv("LANGUAGE", "de")
	s := newRepo(t, bareRemote(t))
	require.NoError(t, s.Pull())
}
