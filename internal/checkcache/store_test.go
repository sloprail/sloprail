package checkcache

import (
	"encoding/hex"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var words = strings.Fields(`the change adds a new handler for requests and the rubric requires that every exported
function carries a doc comment explaining behaviour not implementation. The diff removes dead code, renames a variable,
keeps the public interface stable, and the tests cover the failure path. Reviewer notes: layering is respected, error
values are wrapped with context, no global state is introduced, the migration is idempotent, and logging uses the
structured logger. However the helper duplicates existing logic in the package so the claim of reuse is not met.
configuration schema validation timeout retry backoff cache fingerprint subject citation transcript session guardrail`)

func sentence(r *rand.Rand, n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(words[r.Intn(len(words))])
	}
	return b.String()
}

func hex32(r *rand.Rand) string {
	var b [32]byte
	r.Read(b[:])
	return hex.EncodeToString(b[:])
}

// genRuns makes n synthetic runs shaped like real ones: one rule evaluated once, holding a
// judge check (findable) and a script check (cached like any other).
func genRuns(seed int64, n int) []Run {
	r := rand.New(rand.NewSource(seed))
	out := make([]Run, n)
	for i := range out {
		st := StatusPass
		if r.Intn(5) == 0 {
			st = StatusFail
		}
		out[i] = Run{
			ID:       "run_" + hex32(r)[:16],
			RunAt:    time.Unix(1_790_000_000+int64(i), 0).UTC().Format("2006-01-02T15:04:05.000000000Z"),
			Rule:     fmt.Sprintf("plug/file-guard/rule-%d", r.Intn(30)),
			RuleHash: hex32(r)[:16],
			BaseRef:  hex32(r)[:40], HeadRef: hex32(r)[:40],
			Complete: true, SessionID: hex32(r)[:16],
			Checks: []Check{
				{
					Subject:     fmt.Sprintf("internal/%s/%s.go", words[r.Intn(len(words))], words[r.Intn(len(words))]),
					Kind:        fmt.Sprintf("check[%d]:judge:./rubric.md.j2", r.Intn(3)),
					Status:      st,
					Fingerprint: hex32(r),
					Metadata:    map[string]any{"reasoning": sentence(r, 120), "model": "claude-x"},
					Items:       []Item{{Key: sentence(r, 12), Passed: st == StatusPass, Metadata: map[string]any{"line": float64(r.Intn(900))}}},
				},
				{Subject: "changeset", Kind: "check[1]:script:./x.sh", Status: StatusPass},
			},
		}
	}
	return out
}

func git(t testing.TB, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func newRepo(t testing.TB, remote string) *Store { return newRepoOpt(t, Options{Remote: remote}) }

func newRepoOpt(t testing.TB, opt Options) *Store {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	opt.Dir = dir
	s, err := Open(opt)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func bareRemote(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "--bare")
	return dir
}

// keyOf is the key of a run's judge check.
func keyOf(r Run) Key { return r.CheckKey(r.Checks[0]) }

func keysOf(rs []Run) []Key {
	ks := make([]Key, len(rs))
	for i, r := range rs {
		ks[i] = keyOf(r)
	}
	return ks
}

// withCheck is a copy of r whose judge check has the status, at the run time.
func withCheck(r Run, status, at string) Run {
	r.Checks = append([]Check(nil), r.Checks...)
	r.Checks[0].Status = status
	r.RunAt = at
	return r
}

func TestRoundTrip(t *testing.T) {
	s := newRepo(t, "")
	rs := genRuns(1, 50)
	if err := s.Put(rs); err != nil {
		t.Fatal(err)
	}
	got, err := s.Lookup(keysOf(rs))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 50 {
		t.Fatalf("got %d of 50", len(got))
	}
	for _, r := range rs {
		g := got[keyOf(r).ID()]
		if g.Check.Metadata["reasoning"] != r.Checks[0].Metadata["reasoning"] || g.Check.Status != r.Checks[0].Status ||
			g.Run.ID != r.ID || g.Run.HeadRef != r.HeadRef || g.Check.Items[0].Key != r.Checks[0].Items[0].Key || len(g.Run.Checks) != 2 {
			t.Fatalf("round trip differs for %s", r.Checks[0].Subject)
		}
	}
	miss := Key{Rule: "nope", Subject: "x"}
	got, _ = s.Lookup([]Key{miss})
	if len(got) != 0 {
		t.Fatal("a missing key must be absent")
	}
	// the branch holds only the schema dir, never checked out
	if root := git(t, s.opt.Dir, "ls-tree", "--name-only", s.opt.Ref); root != SchemaDir {
		t.Fatalf("root = %q", root)
	}
}

func TestLookupAcrossManySegmentsAndFreshProcess(t *testing.T) {
	s := newRepoOpt(t, Options{NoAutoGc: true})
	var all []Run
	for i := 0; i < 25; i++ {
		b := genRuns(int64(100+i), 20)
		all = append(all, b...)
		if err := s.Put(b); err != nil {
			t.Fatal(err)
		}
	}
	// a second Store on the same repo reads from the persisted local index
	s2, _ := Open(s.opt)
	for _, st := range []*Store{s, s2} {
		got, err := st.Lookup(keysOf(all))
		if err != nil || len(got) != 500 {
			t.Fatalf("got %d, err %v", len(got), err)
		}
	}
	if len(s.snap.Segs) != 25 {
		t.Fatalf("segments = %d", len(s.snap.Segs))
	}
}

func TestDuplicateKeysResolveLatestWins(t *testing.T) {
	s := newRepo(t, "")
	a := genRuns(7, 1)[0]
	older := withCheck(a, StatusFail, "2026-01-01T00:00:00Z")
	newer := withCheck(a, StatusPass, "2026-02-01T00:00:00Z")
	if err := s.Put([]Run{newer}); err != nil {
		t.Fatal(err)
	}
	if err := s.Put([]Run{older}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Lookup([]Key{keyOf(a)})
	if got[keyOf(a).ID()].Check.Status != StatusPass {
		t.Fatal("the later At must win regardless of write order")
	}
}

func TestTwoWritersConcurrentlyToBareRemote(t *testing.T) {
	remote := bareRemote(t)
	const writers, per = 4, 8
	stores := make([]*Store, writers)
	for i := range stores {
		stores[i] = newRepoOpt(t, Options{Remote: remote, NoAutoGc: true})
	}
	var wg sync.WaitGroup
	errs := make([]error, writers)
	var all [][]Run
	for i := range stores {
		var mine []Run
		for j := 0; j < per; j++ {
			mine = append(mine, genRuns(int64(1000*i+j), 5)...)
		}
		all = append(all, mine)
	}
	for i := range stores {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < per; j++ {
				if err := stores[i].Put(all[i][j*5 : j*5+5]); err != nil {
					errs[i] = err
					return
				}
			}
		}(i)
	}
	wg.Wait()
	for _, e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	// a fresh reader sees every writer's results, no merge conflicts, one linear history
	reader := newRepo(t, remote)
	if err := reader.Sync(); err != nil {
		t.Fatal(err)
	}
	for i := range all {
		got, err := reader.Lookup(keysOf(all[i]))
		if err != nil || len(got) != len(all[i]) {
			t.Fatalf("writer %d: got %d of %d (%v)", i, len(got), len(all[i]), err)
		}
	}
	if n := git(t, reader.opt.Dir, "rev-list", "--count", reader.opt.Ref); n != fmt.Sprint(writers*per) {
		t.Fatalf("commits = %s, want %d", n, writers*per)
	}
	if merges := git(t, reader.opt.Dir, "rev-list", "--merges", reader.opt.Ref); merges != "" {
		t.Fatal("history must be linear")
	}
}

func TestPutIsIdempotent(t *testing.T) {
	s := newRepo(t, "")
	rs := genRuns(3, 10)
	_ = s.Put(rs)
	tip := s.tip()
	if err := s.Put(rs); err != nil {
		t.Fatal(err)
	}
	if s.tip() != tip {
		t.Fatal("putting the same segment again must not add a commit")
	}
}

func TestGcPreservesLatestResults(t *testing.T) {
	remote := bareRemote(t)
	s := newRepo(t, remote)
	var all []Run
	for i := 0; i < 12; i++ {
		b := genRuns(int64(50+i), 100)
		all = append(all, b...)
		if err := s.Put(b); err != nil {
			t.Fatal(err)
		}
	}
	// a superseding duplicate
	dup := withCheck(all[0], StatusFail, "2999-01-01T00:00:00Z")
	dup.ID = "run_superseding"
	if err := s.Put([]Run{dup}); err != nil {
		t.Fatal(err)
	}
	all[0] = dup
	preGc := s.tip()
	st, err := s.Gc()
	if err != nil {
		t.Fatal(err)
	}
	if st.Records != 1200 || st.Duplicates != 1 || st.SegsAfter != 2 || !st.Retrained {
		t.Fatalf("stats %+v", st)
	}
	if git(t, s.opt.Dir, "rev-parse", s.opt.Ref+"^") != preGc {
		t.Fatal("gc must be one new commit on top of the previous tip (history kept)")
	}
	if git(t, s.opt.Dir, "rev-parse", s.opt.Ref) != git(t, remote, "rev-parse", defaultBranch) {
		t.Fatal("the compaction must reach the remote as a fast-forward")
	}
	fresh := newRepo(t, remote)
	_ = fresh.Sync()
	got, err := fresh.Lookup(keysOf(all))
	if err != nil || len(got) != 1200 {
		t.Fatalf("got %d (%v)", len(got), err)
	}
	for _, r := range all {
		if got[keyOf(r).ID()].Check.Status != r.Checks[0].Status {
			t.Fatalf("latest result lost for %s", r.Checks[0].Subject)
		}
	}
	// writes still work on top of the squashed branch
	if err := fresh.Put(genRuns(999, 3)); err != nil {
		t.Fatal(err)
	}
}

func TestCorruptSegmentIsAnErrorNotAMiss(t *testing.T) {
	s := newRepo(t, "")
	rs := genRuns(5, 10)
	if err := s.Put(rs); err != nil {
		t.Fatal(err)
	}
	sn, _ := s.snapshotAt(s.tip())
	sg := sn.Segs[0]
	// replace the segment blob with same-length garbage under the same name
	bad := make([]byte, 200)
	for i := range bad {
		bad[i] = byte(i)
	}
	files := map[string][]byte{"seg/" + sg.Name + ".zst": bad}
	commit, err := s.commit(s.tip(), false, files, "corrupt")
	if err != nil {
		t.Fatal(err)
	}
	git(t, s.opt.Dir, "update-ref", s.opt.Ref, commit)
	s2, _ := Open(s.opt)
	if _, err := s2.Lookup(keysOf(rs)); err == nil || !strings.Contains(err.Error(), "corrupt") {
		t.Fatalf("want ErrCorrupt, got %v", err)
	}
	// a frame that is damaged but hashes correctly under a forged name is also caught
	if _, err := s2.Gc(); err == nil {
		t.Fatal("gc must not silently drop a corrupt segment")
	}
}

func TestCorruptFrameDetected(t *testing.T) {
	s := newRepo(t, "")
	rs := genRuns(6, 5)
	_ = s.Put(rs)
	sn, _ := s.snapshotAt(s.tip())
	sg := sn.Segs[0]
	blob, _ := s.g.run(nil, nil, "cat-file", "blob", sg.ZstOid)
	blob = append([]byte(nil), blob...)
	blob[len(blob)/2] ^= 0xff
	if err := verifySegment(sg.Name, blob); err == nil {
		t.Fatal("a flipped byte must fail the content address")
	}
	d, _ := s.dictByOid(sg.Dict, sn.Dicts[sg.Dict])
	var failed bool
	for i := range sg.Keys {
		if _, err := sg.decodeAt(blob, i, d); err != nil {
			failed = true
		}
	}
	if !failed {
		t.Fatal("a flipped byte must fail decoding or the key check")
	}
}

func TestFutureSchemaRefused(t *testing.T) {
	s := newRepo(t, "")
	if err := s.Put(genRuns(8, 3)); err != nil {
		t.Fatal(err)
	}
	// someone running a newer sloprail adds v2099-01-01/
	oid := hashObject(t, s.opt.Dir, "{}")
	idx := filepath.Join(t.TempDir(), "idx")
	env := []string{"GIT_INDEX_FILE=" + idx}
	run := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = s.opt.Dir
		cmd.Env = append(os.Environ(), env...)
		cmd.Env = append(cmd.Env, "GIT_AUTHOR_NAME=a", "GIT_AUTHOR_EMAIL=a@a", "GIT_COMMITTER_NAME=a", "GIT_COMMITTER_EMAIL=a@a")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("read-tree", s.tip())
	run("update-index", "--add", "--cacheinfo", "100644,"+oid+",v2099-01-01/MANIFEST.json")
	tree := run("write-tree")
	c := run("commit-tree", tree, "-p", s.tip(), "-m", "future")
	git(t, s.opt.Dir, "update-ref", s.opt.Ref, c)
	s2, _ := Open(s.opt)
	if _, err := s2.Lookup([]Key{{Rule: "x"}}); err == nil || !strings.Contains(err.Error(), "newer schema") {
		t.Fatalf("lookup: %v", err)
	}
	if err := s2.Put(genRuns(9, 1)); err == nil || !strings.Contains(err.Error(), "newer schema") {
		t.Fatalf("put: %v", err)
	}
}

func hashObject(t *testing.T, dir, content string) string {
	cmd := exec.Command("git", "hash-object", "-w", "--stdin")
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(content)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func TestShow(t *testing.T) {
	s := newRepo(t, "")
	rs := genRuns(11, 4)
	for i := range rs {
		rs[i].Checks[0].Subject = "internal/foo/bar.go"
	}
	_ = s.Put(rs)
	out, err := s.Show("internal/foo/bar.go")
	if err != nil || !strings.Contains(out, "4 stored result(s)") || !strings.Contains(out, rs[0].Rule) {
		t.Fatalf("%v\n%s", err, out)
	}
	out, _ = s.Show("nothing.go")
	if !strings.Contains(out, "no stored results") {
		t.Fatal(out)
	}
}

// Bound: looking up the 40 keys of one PR among 10k stored records.
func TestLookupBound40In10k(t *testing.T) {
	s := newRepo(t, "")
	all := make([]Run, 0, 10000)
	for i := 0; i < 10; i++ {
		b := genRuns(int64(9000+i), 1000)
		all = append(all, b...)
		if err := s.Put(b); err != nil {
			t.Fatal(err)
		}
	}
	r := rand.New(rand.NewSource(1))
	var ks []Key
	for i := 0; i < 40; i++ {
		ks = append(ks, keyOf(all[r.Intn(len(all))]))
	}
	cold, _ := Open(s.opt)
	_ = os.RemoveAll(filepath.Dir(cold.cachePath()))
	t0 := time.Now()
	got, err := cold.Lookup(ks)
	coldD := time.Since(t0)
	if err != nil || len(got) != 40 {
		t.Fatalf("got %d (%v)", len(got), err)
	}
	warm, _ := Open(s.opt) // fresh process: persisted local index, no memory
	t0 = time.Now()
	if _, err := warm.Lookup(ks); err != nil {
		t.Fatal(err)
	}
	warmD := time.Since(t0)
	t.Logf("40 keys in 10k records: cold index build %v, warm %v", coldD, warmD)
	if coldD > 3*time.Second || warmD > time.Second {
		t.Fatalf("lookup too slow: cold %v warm %v", coldD, warmD)
	}
}

func BenchmarkLookup40In10k(b *testing.B) {
	s := newRepo(b, "")
	var all []Run
	for i := 0; i < 10; i++ {
		bt := genRuns(int64(9000+i), 1000)
		all = append(all, bt...)
		if err := s.Put(bt); err != nil {
			b.Fatal(err)
		}
	}
	var ks []Key
	for i := 0; i < 40; i++ {
		ks = append(ks, keyOf(all[i*250]))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w, _ := Open(s.opt)
		if _, err := w.Lookup(ks); err != nil {
			b.Fatal(err)
		}
	}
}

func TestNoDictionaryWriteRead(t *testing.T) {
	s := newRepo(t, "")
	rs := genRuns(77, 6)
	if err := s.Put(rs); err != nil {
		t.Fatal(err)
	}
	sn, _ := s.snapshotAt(s.tip())
	if len(sn.Dicts) != 0 || sn.Segs[0].Dict != "" || sn.ManifestDict != "" || !sn.HasManifest {
		t.Fatalf("a young store keeps no dictionary: %+v", sn)
	}
	cold, _ := Open(s.opt)
	got, err := cold.Lookup([]Key{keyOf(rs[0]), keyOf(rs[5])})
	if err != nil || len(got) != 2 {
		t.Fatalf("%v %d", err, len(got))
	}
}

func TestGcBelowTrainMinKeepsNoDictionary(t *testing.T) {
	s := newRepo(t, "")
	_ = s.Put(genRuns(78, 20))
	st, err := s.Gc()
	if err != nil || st.Retrained {
		t.Fatalf("%v %+v", err, st)
	}
	sn, _ := s.snapshotAt(s.tip())
	if len(sn.Dicts) != 0 || sn.ManifestDict != "" || sn.Segs[0].Dict != "" {
		t.Fatalf("no dictionary below TrainMin: %+v", sn)
	}
}

func TestGcAtTrainMinTrainsAndOldSegmentsStillRead(t *testing.T) {
	s := newRepo(t, "")
	old := genRuns(79, 20)
	_ = s.Put(old)
	_ = s.Put(genRuns(80, TrainMin+50))
	st, err := s.Gc()
	if err != nil || !st.Retrained {
		t.Fatalf("%v %+v", err, st)
	}
	sn, _ := s.snapshotAt(s.tip())
	if len(sn.Dicts) != 1 || sn.ManifestDict == "" || sn.Segs[0].Dict != sn.ManifestDict {
		t.Fatalf("gc at TrainMin trains one: %+v", sn)
	}
	cold, _ := Open(s.opt)
	if got, err := cold.Lookup([]Key{keyOf(old[0])}); err != nil || len(got) != 1 {
		t.Fatalf("%v %d", err, len(got))
	}
	// a later Put uses the trained dictionary; a dict-less segment from before still reads
	_ = cold.Put(genRuns(81, 3))
	sn, _ = cold.snapshotAt(cold.tip())
	for _, sg := range sn.Segs {
		if sg.Dict != sn.ManifestDict {
			t.Fatalf("new segments use the manifest dictionary: %q", sg.Dict)
		}
	}
}

// Gc never rewrites the shared history: a run another machine pushes between Gc's read and
// its push survives, and the remote only ever moves forward.
func TestGcKeepsAConcurrentWritersRun(t *testing.T) {
	remote := bareRemote(t)
	a := newRepoOpt(t, Options{Remote: remote, NoAutoGc: true})
	b := newRepoOpt(t, Options{Remote: remote, NoAutoGc: true})
	mine := genRuns(1, 5)
	for i := 0; i < 3; i++ {
		if err := a.Put(genRuns(int64(10+i), 4)); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Put(mine); err != nil {
		t.Fatal(err)
	}
	before := git(t, remote, "rev-parse", defaultBranch)
	theirs := genRuns(2, 3)
	a.beforeGcPush = func() {
		if err := b.Put(theirs); err != nil {
			t.Error(err)
		}
	}
	if _, err := a.Gc(); err != nil {
		t.Fatal(err)
	}
	if a.PendingPush() != nil {
		t.Fatalf("gc push left pending: %v", a.PendingPush())
	}
	if err := exec.Command("git", "-C", remote, "merge-base", "--is-ancestor", before, defaultBranch).Run(); err != nil {
		t.Fatal("the remote history was rewritten: the old tip is no longer an ancestor")
	}
	reader := newRepo(t, remote)
	if err := reader.Sync(); err != nil {
		t.Fatal(err)
	}
	if n := lookupAll(t, reader, theirs); n != 3 {
		t.Fatalf("the concurrent writer's run was lost: %d of 3", n)
	}
	if n := lookupAll(t, reader, mine); n != 5 {
		t.Fatalf("Gc's own results lost: %d of 5", n)
	}
}
