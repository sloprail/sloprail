package checkcache

import (
	"bytes"
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// SchemaDir is the schema version (a date) and the single directory the store
// reads and writes. A ref carrying a newer v<date>/ directory is refused.
const SchemaDir = "v2026-10-03"

// ErrFutureSchema means the ref holds a schema directory newer than this binary.
var ErrFutureSchema = errors.New("checkcache: ref uses a newer schema; upgrade sloprail")

// ErrCorrupt means stored bytes do not match their content address or cannot
// be decoded. It is never reported as a miss.
var ErrCorrupt = errors.New("checkcache: corrupt segment")

const (
	defaultRef    = "refs/sloprail/checks"
	defaultBranch = "refs/heads/sloprail/checks"
	maxAttempts   = 30
	// TrainMin is how many records Gc needs before it trains a dictionary
	// from them; below it segments are written with no dictionary (plain zstd).
	TrainMin = 300
	// SegmentTarget is the record count Gc compacts segments to.
	SegmentTarget = 1000
)

var schemaRe = regexp.MustCompile(`^v\d{4}-\d{2}-\d{2}$`)

// Options configures a Store.
type Options struct {
	// Dir is any directory inside the repository whose object store holds the ref.
	Dir string
	// Remote is a remote name or URL the ref is synced with. Empty = local only.
	Remote string
	// Ref is the local ref that mirrors the branch. Default refs/sloprail/checks.
	// CI points it at a ref it fetched (refs/remotes/origin/sloprail/checks).
	Ref string
	// Branch is the remote ref. Default refs/heads/sloprail/checks.
	Branch string
	// NoAutoGc stops a Put from compacting the branch when it grows (see maybeGc). For tests
	// that count segments or commits.
	NoAutoGc bool
}

// Store is the git-ref Cache. It implements Cache.
type Store struct {
	opt Options
	g   gitRunner
	mu  sync.Mutex

	dicts map[string]*zdict
	snap  *snapshot // in-process copy keyed by dir tree

	frozen    bool // read-only use: the ref is read once (FreezeTip)
	frozenTip string
	frozenSn  *snapshot

	beforeGcPush func() // test seam: runs after Gc committed locally, before it pushes

	pushErr error // why the last push of local results failed; they are retried on the next sync or put
}

var _ Cache = (*Store)(nil)

// Open prepares a store on the repository containing opt.Dir.
func Open(opt Options) (*Store, error) {
	if opt.Ref == "" {
		opt.Ref = defaultRef
	}
	if opt.Branch == "" {
		opt.Branch = defaultBranch
	}
	s := &Store{opt: opt, g: gitRunner{dir: opt.Dir}, dicts: map[string]*zdict{}}
	if _, err := s.g.str("rev-parse", "--git-dir"); err != nil {
		return nil, err
	}
	return s, nil
}

// PendingPush is why the last push of local results failed (nil when nothing is waiting).
// The results are stored locally regardless and pushed on a later Sync or Put.
func (s *Store) PendingPush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pushErr
}

// snapshot is the read index of one version of the schema directory.
type snapshot struct {
	DirTree      string
	Segs         []*segIdx
	Dicts        map[string]string // dict sha -> blob oid
	ManifestDict string            // "" = no dictionary
	HasManifest  bool
}

type manifest struct {
	Schema string `json:"schema"`
	Dict   string `json:"dict"` // sha of the dictionary new segments use; "" = none
}

// FreezeTip reads the local ref once and answers every later read from that commit and its
// index, with no git process: for a caller that only reads (`verify`, `show`) and asks hundreds
// of times. Do not write through a frozen store.
func (s *Store) FreezeTip() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.frozen = false
	s.frozenTip = s.tip()
	s.frozen = true
	s.frozenSn = nil
}

// tip returns the commit the local ref points at, "" if none.
func (s *Store) tip() string {
	if s.frozen {
		return s.frozenTip
	}
	out, err := s.g.str("rev-parse", "--verify", "-q", s.opt.Ref+"^{commit}")
	if err != nil {
		return ""
	}
	return out
}

// Pull brings the remote branch into the local ref, and nothing else: it never pushes and
// never writes a result. It is what a read-only command (`verify`, `show`) uses. A remote
// without the branch yet is not an error; only a failed fetch is.
func (s *Store) Pull() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sync()
}

// Sync brings the remote branch into the local ref and pushes local results the remote
// does not hold yet (a failed push is retried here). A remote without the branch yet is not
// an error. It is a no-op for a local-only store. Only a failed FETCH is an error: a push
// that fails leaves the results local and is reported by PendingPush.
func (s *Store) Sync() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.sync(); err != nil {
		return err
	}
	s.pushErr = s.push()
	return nil
}

// track is the ref that mirrors the remote branch as last fetched or pushed. The local ref
// is never overwritten by a fetch: results stored while the remote was unreachable live
// there until they are pushed, so the local ref always holds at least what the remote did.
func (s *Store) track() string { return s.opt.Ref + "-remote" }

func (s *Store) rev(ref string) string {
	out, err := s.g.str("rev-parse", "--verify", "-q", ref+"^{commit}")
	if err != nil {
		return ""
	}
	return out
}

func (s *Store) isAncestor(a, b string) bool {
	_, err := s.g.run(nil, nil, "merge-base", "--is-ancestor", a, b)
	return err == nil
}

// fetch updates the tracking ref from the remote. A remote without the branch is not an error.
func (s *Store) fetch() error {
	_, err := s.g.run(nil, nil, "fetch", "--quiet", "--no-tags", s.opt.Remote, "+"+s.opt.Branch+":"+s.track())
	if err != nil && !strings.Contains(err.Error(), "couldn't find remote ref") {
		return err
	}
	return nil
}

func (s *Store) sync() error {
	if s.opt.Remote == "" {
		return nil
	}
	old := s.rev(s.track())
	if err := s.fetch(); err != nil {
		return err
	}
	return s.reconcile(old)
}

// reconcile makes the local ref hold everything the tracking ref does, and keeps what only
// the local ref holds: local results the remote has not got. Fast-forward when the local ref
// is behind; nothing when it is ahead; when both moved, a new commit on top of the remote
// tip that adds the local-only segments (those added since oldTrack, the tracking ref before
// this fetch; every local segment when that is unknown) — segments are content-addressed, so
// a union is always sound, and a Gc by another machine is not undone by it.
func (s *Store) reconcile(oldTrack string) error {
	local, remote := s.tip(), s.rev(s.track())
	if remote == "" || local == remote {
		return nil
	}
	if local == "" {
		_, err := s.g.run(nil, nil, "update-ref", s.opt.Ref, remote)
		return err
	}
	if s.isAncestor(remote, local) {
		return nil
	}
	if s.isAncestor(local, remote) {
		_, err := s.g.run(nil, nil, "update-ref", s.opt.Ref, remote, local)
		return err
	}
	// Both moved. Replay the local-only commits (those since oldTrack, the tracking ref before
	// this fetch) one by one on top of the remote tip, so one Put stays one commit and the
	// history stays linear. Without a known common point every local segment goes in one commit.
	cur := remote
	hasManifest := func() bool {
		_, err := s.g.str("rev-parse", "--verify", "-q", cur+":"+SchemaDir+"/MANIFEST.json")
		return err == nil
	}
	dicts := map[string]string{}
	if out, err := s.g.run(nil, nil, "ls-tree", "-r", local); err == nil {
		for k, v := range rawEntries(string(out), 2) {
			if strings.HasPrefix(k, "dict/") {
				dicts[k] = v
			}
		}
	}
	replay := func(entries map[string]string, msg string) error {
		if hasManifest() {
			delete(entries, "MANIFEST.json") // the remote's manifest (and its dictionary) stands
		}
		if len(entries) == 0 {
			return nil
		}
		for k, v := range dicts { // the dictionaries local segments name travel with them
			entries[k] = v
		}
		c, err := s.commitEntries(cur, false, entries, msg)
		cur = c
		return err
	}
	if oldTrack != "" && s.isAncestor(oldTrack, local) {
		revs, err := s.g.str("rev-list", "--reverse", "--first-parent", oldTrack+".."+local)
		if err != nil {
			return err
		}
		for _, c := range strings.Fields(revs) {
			out, err := s.g.run(nil, nil, "diff-tree", "-r", "--root", "--no-commit-id", "--no-renames", "--diff-filter=AM", c)
			if err != nil {
				return err
			}
			msg, _ := s.g.str("log", "-1", "--format=%s", c)
			if err := replay(rawEntries(string(out), 3), msg); err != nil {
				return err
			}
		}
	} else {
		out, err := s.g.run(nil, nil, "ls-tree", "-r", local)
		if err != nil {
			return err
		}
		if err := replay(rawEntries(string(out), 2), "checks: merge local results"); err != nil {
			return err
		}
	}
	if cur == remote {
		return nil
	}
	_, err := s.g.run(nil, nil, "update-ref", s.opt.Ref, cur, local)
	return err
}

// rawEntries parses `ls-tree -r` (oid is field 2) or `diff-tree --raw` (new oid is field 3)
// lines into path-under-SchemaDir -> blob id, keeping only the current schema directory.
func rawEntries(listing string, oidField int) map[string]string {
	prefix := SchemaDir + "/"
	out := map[string]string{}
	for _, ln := range strings.Split(listing, "\n") {
		tab := strings.IndexByte(ln, '\t')
		if tab < 0 || !strings.HasPrefix(ln[tab+1:], prefix) {
			continue
		}
		if f := strings.Fields(ln[:tab]); len(f) > oidField {
			out[strings.TrimPrefix(ln[tab+1:], prefix)] = f[oidField]
		}
	}
	return out
}

// PushErrorFile is the file under the git common dir holding why the last push of local
// results failed (absent once a push succeeds). The pre-push gate, a separate process, reads it.
const PushErrorFile = "sloprail-checks-push-error"

// push is doPush that also records its failure for the pre-push gate (see PushErrorFile).
func (s *Store) push() error {
	err := s.doPush()
	if s.opt.Remote == "" {
		return err
	}
	dir, derr := s.g.str("rev-parse", "--path-format=absolute", "--git-common-dir")
	if derr != nil {
		return err
	}
	f := filepath.Join(dir, PushErrorFile)
	if err == nil {
		_ = os.Remove(f)
	} else {
		_ = os.WriteFile(f, []byte(strings.Join(strings.Fields(err.Error()), " ")+"\n"), 0o644)
	}
	return err
}

// doPush publishes the local ref to the remote branch (a fast-forward: reconcile has put the
// remote tip underneath it). A rejection because someone else pushed first is retried on top
// of their tip; any other failure (offline, auth, a hook) leaves the results local and is
// returned for the next attempt. It never moves the local ref.
func (s *Store) doPush() error {
	if s.opt.Remote == "" {
		return nil
	}
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		local, remote := s.tip(), s.rev(s.track())
		if local == "" || local == remote {
			return nil
		}
		_, err := s.g.run(nil, nil, "push", "--quiet", s.opt.Remote, local+":"+s.opt.Branch)
		if err == nil {
			_, err = s.g.run(nil, nil, "update-ref", s.track(), local)
			return err
		}
		lastErr = err
		if ferr := s.fetch(); ferr != nil {
			return lastErr // unreachable: not a race
		}
		if s.rev(s.track()) == remote {
			return lastErr // the remote did not move: refused for another reason
		}
		if err := s.reconcile(remote); err != nil {
			return err
		}
		time.Sleep(time.Duration(10+rand.Intn(40*(attempt+1))) * time.Millisecond)
	}
	return fmt.Errorf("checkcache: push gave up after %d attempts: %w", maxAttempts, lastErr)
}

func (s *Store) cachePath() string {
	// The COMMON git dir, so every worktree of a repository shares one index.
	gd, err := s.g.str("rev-parse", "--git-common-dir")
	if err != nil {
		return ""
	}
	if !filepath.IsAbs(gd) {
		gd = filepath.Join(s.opt.Dir, gd)
	}
	gd = filepath.Join(gd, "sloprail")
	sum := sha256.Sum256([]byte(s.opt.Ref))
	return filepath.Join(gd, "checks-index-"+hex.EncodeToString(sum[:4])+".gob")
}

func (s *Store) loadCache() *snapshot {
	p := s.cachePath()
	if p == "" {
		return nil
	}
	f, err := os.Open(p)
	if err != nil {
		return nil
	}
	defer f.Close()
	var sn snapshot
	if gob.NewDecoder(f).Decode(&sn) != nil {
		return nil
	}
	return &sn
}

func (s *Store) saveCache(sn *snapshot) {
	p := s.cachePath()
	if p == "" {
		return
	}
	var buf bytes.Buffer
	if gob.NewEncoder(&buf).Encode(sn) != nil || os.MkdirAll(filepath.Dir(p), 0o755) != nil {
		return
	}
	tmp := p + fmt.Sprintf(".%d.tmp", os.Getpid())
	if os.WriteFile(tmp, buf.Bytes(), 0o644) == nil {
		_ = os.Rename(tmp, p)
	}
}

// snapshotAt reads (or incrementally rebuilds) the index for a commit. Only
// segments the local cache has not seen cost an index read.
func (s *Store) snapshotAt(tip string) (*snapshot, error) {
	if s.frozen && s.frozenSn != nil && tip == s.frozenTip {
		return s.frozenSn, nil
	}
	sn, err := s.readSnapshotAt(tip)
	if s.frozen && err == nil && tip == s.frozenTip {
		s.frozenSn = sn
	}
	return sn, err
}

func (s *Store) readSnapshotAt(tip string) (*snapshot, error) {
	if tip == "" {
		return &snapshot{}, nil
	}
	root, err := s.g.str("ls-tree", tip)
	if err != nil {
		return nil, err
	}
	dirOid := ""
	for _, ln := range strings.Split(root, "\n") {
		tab := strings.IndexByte(ln, '\t')
		if tab < 0 {
			continue
		}
		name := ln[tab+1:]
		if schemaRe.MatchString(name) && name > SchemaDir {
			return nil, fmt.Errorf("%w (found %s, this build speaks %s)", ErrFutureSchema, name, SchemaDir)
		}
		if name == SchemaDir {
			f := strings.Fields(ln[:tab])
			if len(f) == 3 {
				dirOid = f[2]
			}
		}
	}
	if dirOid == "" {
		return &snapshot{}, nil
	}
	if s.snap != nil && s.snap.DirTree == dirOid {
		return s.snap, nil
	}
	prior := s.snap
	if prior == nil {
		prior = s.loadCache()
	}
	if prior != nil && prior.DirTree == dirOid {
		s.snap = prior
		return prior, nil
	}
	known := map[string]*segIdx{}
	if prior != nil {
		for _, sg := range prior.Segs {
			known[sg.Name] = sg
		}
	}
	listing, err := s.g.run(nil, nil, "ls-tree", "-r", dirOid)
	if err != nil {
		return nil, err
	}
	zst, idx := map[string]string{}, map[string]string{}
	sn := &snapshot{DirTree: dirOid, Dicts: map[string]string{}}
	manifestOid := ""
	for _, ln := range strings.Split(string(listing), "\n") {
		tab := strings.IndexByte(ln, '\t')
		if tab < 0 {
			continue
		}
		f := strings.Fields(ln[:tab])
		oid, path := f[2], ln[tab+1:]
		switch {
		case path == "MANIFEST.json":
			manifestOid = oid
		case strings.HasPrefix(path, "seg/") && strings.HasSuffix(path, ".zst"):
			zst[strings.TrimSuffix(path[4:], ".zst")] = oid
		case strings.HasPrefix(path, "seg/") && strings.HasSuffix(path, ".idx"):
			idx[strings.TrimSuffix(path[4:], ".idx")] = oid
		case strings.HasPrefix(path, "dict/") && strings.HasSuffix(path, ".zdict"):
			sn.Dicts[strings.TrimSuffix(path[5:], ".zdict")] = oid
		}
	}
	var b *batch
	defer func() {
		if b != nil {
			b.close()
		}
	}()
	for name, zo := range zst {
		if sg, ok := known[name]; ok && sg.ZstOid == zo {
			sn.Segs = append(sn.Segs, sg)
			continue
		}
		io, ok := idx[name]
		if !ok {
			return nil, fmt.Errorf("%w: seg/%s.zst has no index", ErrCorrupt, name)
		}
		if b == nil {
			if b, err = s.g.startBatch(); err != nil {
				return nil, err
			}
		}
		raw, err := b.read(io)
		if err != nil {
			return nil, err
		}
		sg, err := parseIdx(name, zo, raw)
		if err != nil {
			return nil, err
		}
		sn.Segs = append(sn.Segs, sg)
	}
	if manifestOid != "" {
		raw, err := s.g.run(nil, nil, "cat-file", "blob", manifestOid)
		if err != nil {
			return nil, err
		}
		var m manifest
		if json.Unmarshal(raw, &m) != nil {
			return nil, fmt.Errorf("%w: MANIFEST.json", ErrCorrupt)
		}
		sn.ManifestDict = m.Dict
		sn.HasManifest = true
	}
	s.snap = sn
	s.saveCache(sn)
	return sn, nil
}

func (s *Store) dictByOid(sha, oid string) (*zdict, error) {
	if sha == "" {
		return s.plainDict()
	}
	if d, ok := s.dicts[sha]; ok {
		return d, nil
	}
	if oid == "" {
		return nil, fmt.Errorf("%w: dictionary %s is not in the ref", ErrCorrupt, sha)
	}
	raw, err := s.g.run(nil, nil, "cat-file", "blob", oid)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != sha {
		return nil, fmt.Errorf("%w: dict/%s.zdict does not hash to its name", ErrCorrupt, sha)
	}
	d, err := newZdict(raw)
	if err != nil {
		return nil, err
	}
	s.dicts[sha] = d
	return d, nil
}

// plainDict is the no-dictionary codec, keyed by the empty sha.
func (s *Store) plainDict() (*zdict, error) {
	if d, ok := s.dicts[""]; ok {
		return d, nil
	}
	d, err := newPlainZdict()
	if err != nil {
		return nil, err
	}
	s.dicts[""] = d
	return d, nil
}

// writeDict returns the dictionary new segments should use for a snapshot: the one the
// manifest names, or none.
func (s *Store) writeDict(sn *snapshot) (*zdict, error) {
	if sn.ManifestDict != "" {
		if oid, ok := sn.Dicts[sn.ManifestDict]; ok {
			return s.dictByOid(sn.ManifestDict, oid)
		}
	}
	return s.plainDict()
}

type hit struct {
	seg *segIdx
	i   int
}

// Lookup returns the stored result of each key, by Key.ID(). It reads the local
// ref only (call Sync first for fresh data); a corrupt segment is an error.
func (s *Store) Lookup(keys []Key) (map[string]Found, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sn, err := s.snapshotAt(s.tip())
	if err != nil {
		return nil, err
	}
	out := make(map[string]Found, len(keys))
	if len(sn.Segs) == 0 || len(keys) == 0 {
		return out, nil
	}
	want := map[*segIdx][]int{}
	ids := map[*segIdx][]string{}
	for _, k := range keys {
		id := k.ID()
		k16, err := key16(id)
		if err != nil {
			return nil, err
		}
		for _, sg := range sn.Segs {
			if i := sg.find(k16); i >= 0 {
				want[sg] = append(want[sg], i)
				ids[sg] = append(ids[sg], id)
			}
		}
	}
	if len(want) == 0 {
		return out, nil
	}
	b, err := s.g.startBatch()
	if err != nil {
		return nil, err
	}
	defer b.close()
	for sg, idxs := range want {
		blob, err := b.read(sg.ZstOid)
		if err != nil {
			return nil, err
		}
		if err := verifySegment(sg.Name, blob); err != nil {
			return nil, err
		}
		d, err := s.dictByOid(sg.Dict, sn.Dicts[sg.Dict])
		if err != nil {
			return nil, err
		}
		for n, i := range idxs {
			r, err := sg.decodeAt(blob, i, d)
			if err != nil {
				return nil, err
			}
			id := ids[sg][n]
			if p, ok := out[id]; ok && !Newer(r, p) {
				continue
			}
			out[id] = r
		}
	}
	return out, nil
}

// Put stores results as one new segment. The local ref moves first, so a result is never
// lost to a push that fails (offline, denied): it is pushed best-effort afterwards and retried
// by the next Sync or Put (PendingPush says why it is waiting). Concurrent writers never
// conflict: the segment is content-addressed, so on a lost race the store re-bases the same
// files onto the new tip.
func (s *Store) Put(runs []Run) error {
	if len(runs) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.put(runs)
}

func (s *Store) put(runs []Run) error {
	_ = s.sync() // an unreachable remote is not a reason to lose results: they go in the local ref
	tip := s.tip()
	sn, err := s.snapshotAt(tip)
	if err != nil {
		return err
	}
	d, err := s.writeDict(sn)
	if err != nil {
		return err
	}
	name, zst, idx, err := encodeSegment(runs, d)
	if err != nil {
		return err
	}
	files := map[string][]byte{
		"seg/" + name + ".zst": zst,
		"seg/" + name + ".idx": idx,
	}
	if d.sha != "" {
		files["dict/"+d.sha+".zdict"] = d.bytes
	}
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(10+rand.Intn(40*attempt)) * time.Millisecond)
			_ = s.sync()
			tip = s.tip()
			if sn, err = s.snapshotAt(tip); err != nil {
				return err
			}
		}
		if _, have := s.segmentPresent(sn, name); have {
			s.pushErr = s.push()
			return nil
		}
		f := map[string][]byte{}
		for k, v := range files {
			f[k] = v
		}
		if !sn.HasManifest {
			m, _ := json.Marshal(manifest{Schema: SchemaDir, Dict: d.sha})
			f["MANIFEST.json"] = m
		}
		commit, err := s.commit(tip, false, f, fmt.Sprintf("checks: +%d runs", len(runs)))
		if err != nil {
			return err
		}
		if _, err := s.g.run(nil, nil, "update-ref", s.opt.Ref, commit, tip); err != nil {
			lastErr = err // another local writer moved the ref
			continue
		}
		s.pushErr = s.push()
		s.maybeGc()
		return nil
	}
	return fmt.Errorf("checkcache: put gave up after %d attempts: %w", maxAttempts, lastErr)
}

func (s *Store) segmentPresent(sn *snapshot, name string) (*segIdx, bool) {
	for _, sg := range sn.Segs {
		if sg.Name == name {
			return sg, true
		}
	}
	return nil, false
}

// commit writes files on top of parent ("" = root commit, empty tree) with
// plumbing only: hash-object, a private index, write-tree, commit-tree.
func (s *Store) commit(parent string, fresh bool, files map[string][]byte, msg string) (string, error) {
	entries := map[string]string{}
	for path, content := range files {
		oid, err := s.g.run(content, nil, "hash-object", "-w", "--stdin")
		if err != nil {
			return "", err
		}
		entries[path] = strings.TrimSpace(string(oid))
	}
	return s.commitEntries(parent, fresh, entries, msg)
}

// commitEntries is commit for blobs already in the object store: entries maps a path under
// SchemaDir to its blob id.
func (s *Store) commitEntries(parent string, fresh bool, entries map[string]string, msg string) (string, error) {
	return s.commitTree(parent, !fresh, fresh, entries, msg)
}

// commitReplacing writes a commit on top of parent whose tree is exactly files (the schema
// directory is replaced, not extended): a compaction that keeps the history.
func (s *Store) commitReplacing(parent string, files map[string][]byte, msg string) (string, error) {
	entries := map[string]string{}
	for path, content := range files {
		oid, err := s.g.run(content, nil, "hash-object", "-w", "--stdin")
		if err != nil {
			return "", err
		}
		entries[path] = strings.TrimSpace(string(oid))
	}
	return s.commitTree(parent, false, false, entries, msg)
}

// commitTree: readTree starts from parent's tree; fresh drops the parent too.
func (s *Store) commitTree(parent string, readTree, fresh bool, entries map[string]string, msg string) (string, error) {
	dir, err := os.MkdirTemp("", "sr-checks-index-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	env := []string{
		"GIT_INDEX_FILE=" + filepath.Join(dir, "index"),
		"GIT_AUTHOR_NAME=sloprail", "GIT_AUTHOR_EMAIL=sloprail@localhost",
		"GIT_COMMITTER_NAME=sloprail", "GIT_COMMITTER_EMAIL=sloprail@localhost",
	}
	if fresh {
		parent = ""
	}
	if parent != "" && readTree {
		if _, err := s.g.run(nil, env, "read-tree", parent); err != nil {
			return "", err
		}
	}
	var info bytes.Buffer
	for path, oid := range entries {
		fmt.Fprintf(&info, "100644 %s\t%s/%s\n", oid, SchemaDir, path)
	}
	if _, err := s.g.run(info.Bytes(), env, "update-index", "--add", "--index-info"); err != nil {
		return "", err
	}
	tree, err := s.g.run(nil, env, "write-tree")
	if err != nil {
		return "", err
	}
	args := []string{"commit-tree", strings.TrimSpace(string(tree)), "-m", msg}
	if parent != "" {
		args = append(args, "-p", parent)
	}
	out, err := s.g.run(nil, env, args...)
	return strings.TrimSpace(string(out)), err
}
