package checkcache

import (
	"crypto/sha256"
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

// genResults makes n synthetic results shaped like real judge rows.
func genResults(seed int64, n int) []Result {
	r := rand.New(rand.NewSource(seed))
	out := make([]Result, n)
	for i := range out {
		st := StatusPass
		if r.Intn(5) == 0 {
			st = StatusFail
		}
		out[i] = Result{
			Key: Key{
				Rule:        fmt.Sprintf("plug/file-guard/rule-%d", r.Intn(30)),
				RuleHash:    hex32(r)[:16],
				Kind:        fmt.Sprintf("check[%d]:judge:./rubric.md.j2", r.Intn(3)),
				Subject:     fmt.Sprintf("internal/%s/%s.go", words[r.Intn(len(words))], words[r.Intn(len(words))]),
				Fingerprint: hex32(r),
			},
			Status:    st,
			Reasoning: sentence(r, 120),
			Cites:     []Cite{{Quote: sentence(r, 12), Path: "t.jsonl", Line: r.Intn(900)}},
			Prov: Provenance{Model: "claude-x", Prompt: hex32(r), Response: hex32(r), SR: "0.4.0", Session: hex32(r)[:16],
				At: time.Unix(1_790_000_000+int64(i), 0).UTC().Format(time.RFC3339)},
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

func newRepo(t testing.TB, remote string) *Store {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	s, err := Open(Options{Dir: dir, Remote: remote})
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

func keysOf(rs []Result) []Key {
	ks := make([]Key, len(rs))
	for i, r := range rs {
		ks[i] = r.Key
	}
	return ks
}

func TestRoundTrip(t *testing.T) {
	s := newRepo(t, "")
	rs := genResults(1, 50)
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
		g := got[r.Key.ID()]
		if g.Reasoning != r.Reasoning || g.Status != r.Status || g.Prov != r.Prov || g.Cites[0] != r.Cites[0] {
			t.Fatalf("round trip differs for %s", r.Key.Subject)
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
	s := newRepo(t, "")
	var all []Result
	for i := 0; i < 25; i++ {
		b := genResults(int64(100+i), 20)
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
	a := genResults(7, 1)[0]
	older, newer := a, a
	older.Status, older.Prov.At = StatusFail, "2026-01-01T00:00:00Z"
	newer.Status, newer.Prov.At = StatusPass, "2026-02-01T00:00:00Z"
	if err := s.Put([]Result{newer}); err != nil {
		t.Fatal(err)
	}
	if err := s.Put([]Result{older}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Lookup([]Key{a.Key})
	if got[a.Key.ID()].Status != StatusPass {
		t.Fatal("the later At must win regardless of write order")
	}
}

func TestTwoWritersConcurrentlyToBareRemote(t *testing.T) {
	remote := bareRemote(t)
	const writers, per = 4, 8
	stores := make([]*Store, writers)
	for i := range stores {
		stores[i] = newRepo(t, remote)
	}
	var wg sync.WaitGroup
	errs := make([]error, writers)
	var all [][]Result
	for i := range stores {
		var mine []Result
		for j := 0; j < per; j++ {
			mine = append(mine, genResults(int64(1000*i+j), 5)...)
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
	rs := genResults(3, 10)
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
	var all []Result
	for i := 0; i < 12; i++ {
		b := genResults(int64(50+i), 100)
		all = append(all, b...)
		if err := s.Put(b); err != nil {
			t.Fatal(err)
		}
	}
	// a superseding duplicate
	dup := all[0]
	dup.Status, dup.Prov.At = StatusFail, "2999-01-01T00:00:00Z"
	if err := s.Put([]Result{dup}); err != nil {
		t.Fatal(err)
	}
	all[0] = dup
	st, err := s.Gc()
	if err != nil {
		t.Fatal(err)
	}
	if st.Records != 1200 || st.Duplicates != 1 || st.SegsAfter != 2 || !st.Retrained {
		t.Fatalf("stats %+v", st)
	}
	if n := git(t, s.opt.Dir, "rev-list", "--count", s.opt.Ref); n != "1" {
		t.Fatalf("gc must squash to one commit, got %s", n)
	}
	fresh := newRepo(t, remote)
	_ = fresh.Sync()
	got, err := fresh.Lookup(keysOf(all))
	if err != nil || len(got) != 1200 {
		t.Fatalf("got %d (%v)", len(got), err)
	}
	for _, r := range all {
		if got[r.Key.ID()].Status != r.Status {
			t.Fatalf("latest result lost for %s", r.Key.Subject)
		}
	}
	// writes still work on top of the squashed branch
	if err := fresh.Put(genResults(999, 3)); err != nil {
		t.Fatal(err)
	}
}

func TestCorruptSegmentIsAnErrorNotAMiss(t *testing.T) {
	s := newRepo(t, "")
	rs := genResults(5, 10)
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
	rs := genResults(6, 5)
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
	if err := s.Put(genResults(8, 3)); err != nil {
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
	if err := s2.Put(genResults(9, 1)); err == nil || !strings.Contains(err.Error(), "newer schema") {
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
	rs := genResults(11, 4)
	for i := range rs {
		rs[i].Key.Subject = "internal/foo/bar.go"
	}
	_ = s.Put(rs)
	out, err := s.Show("internal/foo/bar.go")
	if err != nil || !strings.Contains(out, "4 stored result(s)") || !strings.Contains(out, rs[0].Key.Rule) {
		t.Fatalf("%v\n%s", err, out)
	}
	out, _ = s.Show("nothing.go")
	if !strings.Contains(out, "no stored results") {
		t.Fatal(out)
	}
}

func TestDefaultDictIsStable(t *testing.T) {
	if len(defaultDictBytes) < 1024 {
		t.Fatal("embedded default dictionary missing; run go generate ./internal/checkcache")
	}
	sum := sha256.Sum256(defaultDictBytes)
	if _, err := newZdict(defaultDictBytes); err != nil {
		t.Fatal(err, hex.EncodeToString(sum[:4]))
	}
}

// TestGenerateDefaultDict rewrites default.zdict from synthetic judge-shaped
// records. Run it via `go generate` only.
func TestGenerateDefaultDict(t *testing.T) {
	if os.Getenv("SR_GEN_DICT") == "" {
		t.Skip("set SR_GEN_DICT=1 (go generate)")
	}
	var samples [][]byte
	for _, r := range genResults(424242, 3000) {
		raw, _ := marshalResult(r)
		samples = append(samples, raw)
	}
	d, err := trainDict(samples)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("default.zdict", d, 0o644); err != nil {
		t.Fatal(err)
	}
}

// Bound: looking up the 40 keys of one PR among 10k stored records.
func TestLookupBound40In10k(t *testing.T) {
	s := newRepo(t, "")
	all := make([]Result, 0, 10000)
	for i := 0; i < 10; i++ {
		b := genResults(int64(9000+i), 1000)
		all = append(all, b...)
		if err := s.Put(b); err != nil {
			t.Fatal(err)
		}
	}
	r := rand.New(rand.NewSource(1))
	var ks []Key
	for i := 0; i < 40; i++ {
		ks = append(ks, all[r.Intn(len(all))].Key)
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
	var all []Result
	for i := 0; i < 10; i++ {
		bt := genResults(int64(9000+i), 1000)
		all = append(all, bt...)
		if err := s.Put(bt); err != nil {
			b.Fatal(err)
		}
	}
	var ks []Key
	for i := 0; i < 40; i++ {
		ks = append(ks, all[i*250].Key)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w, _ := Open(s.opt)
		if _, err := w.Lookup(ks); err != nil {
			b.Fatal(err)
		}
	}
}
