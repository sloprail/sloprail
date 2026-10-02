package checkcache

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
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
const SchemaDir = "v2026-10-02"

// ErrFutureSchema means the ref holds a schema directory newer than this binary.
var ErrFutureSchema = errors.New("checkcache: ref uses a newer schema; upgrade sloprail")

// ErrCorrupt means stored bytes do not match their content address or cannot
// be decoded. It is never reported as a miss.
var ErrCorrupt = errors.New("checkcache: corrupt segment")

//go:embed default.zdict
var defaultDictBytes []byte

const (
	defaultRef    = "refs/sloprail/checks"
	defaultBranch = "refs/heads/sloprail/checks"
	maxAttempts   = 30
	// TrainMin is how many records Gc needs before it trains a dictionary
	// from them instead of keeping the embedded default.
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
}

// Store is the git-ref Cache. It implements Cache.
type Store struct {
	opt Options
	g   gitRunner
	mu  sync.Mutex

	dicts map[string]*zdict
	snap  *snapshot // in-process copy keyed by dir tree
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

// snapshot is the read index of one version of the schema directory.
type snapshot struct {
	DirTree      string
	Segs         []*segIdx
	Dicts        map[string]string // dict sha -> blob oid
	ManifestDict string
}

type manifest struct {
	Schema string `json:"schema"`
	Dict   string `json:"dict"`
}

// tip returns the commit the local ref points at, "" if none.
func (s *Store) tip() string {
	out, err := s.g.str("rev-parse", "--verify", "-q", s.opt.Ref+"^{commit}")
	if err != nil {
		return ""
	}
	return out
}

// Sync fetches the remote branch into the local ref. A remote without the
// branch yet is not an error. It is a no-op for a local-only store.
func (s *Store) Sync() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sync()
}

func (s *Store) sync() error {
	if s.opt.Remote == "" {
		return nil
	}
	_, err := s.g.run(nil, nil, "fetch", "--quiet", "--no-tags", s.opt.Remote, "+"+s.opt.Branch+":"+s.opt.Ref)
	if err != nil && !strings.Contains(err.Error(), "couldn't find remote ref") {
		return err
	}
	return nil
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
	}
	s.snap = sn
	s.saveCache(sn)
	return sn, nil
}

func (s *Store) dictByOid(sha, oid string) (*zdict, error) {
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

func (s *Store) defaultDict() (*zdict, error) {
	sum := sha256.Sum256(defaultDictBytes)
	sha := hex.EncodeToString(sum[:])
	if d, ok := s.dicts[sha]; ok {
		return d, nil
	}
	if len(defaultDictBytes) == 0 {
		return nil, errors.New("checkcache: embedded default dictionary is empty")
	}
	d, err := newZdict(defaultDictBytes)
	if err != nil {
		return nil, err
	}
	s.dicts[sha] = d
	return d, nil
}

// writeDict returns the dictionary new segments should use for a snapshot.
func (s *Store) writeDict(sn *snapshot) (*zdict, error) {
	if sn.ManifestDict != "" {
		if oid, ok := sn.Dicts[sn.ManifestDict]; ok {
			return s.dictByOid(sn.ManifestDict, oid)
		}
	}
	return s.defaultDict()
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

// Put stores results as one new segment and publishes it. Concurrent writers
// never conflict: the segment is content-addressed, so on a rejected push the
// store re-bases the same files onto the new tip and pushes again.
func (s *Store) Put(runs []Run) error {
	if len(runs) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.sync(); err != nil {
		return err
	}
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
		"seg/" + name + ".zst":     zst,
		"seg/" + name + ".idx":     idx,
		"dict/" + d.sha + ".zdict": d.bytes,
	}
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(10+rand.Intn(40*attempt)) * time.Millisecond)
			if err := s.sync(); err != nil {
				return err
			}
			tip = s.tip()
			if sn, err = s.snapshotAt(tip); err != nil {
				return err
			}
		}
		if _, have := s.segmentPresent(sn, name); have {
			return nil
		}
		f := map[string][]byte{}
		for k, v := range files {
			f[k] = v
		}
		if sn.ManifestDict == "" {
			m, _ := json.Marshal(manifest{Schema: SchemaDir, Dict: d.sha})
			f["MANIFEST.json"] = m
		}
		commit, err := s.commit(tip, false, f, fmt.Sprintf("checks: +%d runs", len(runs)))
		if err != nil {
			return err
		}
		if err := s.publish(commit, tip, false); err != nil {
			lastErr = err
			continue
		}
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

// publish moves the ref (and the remote branch) from tip to commit; with force
// the remote is replaced under a lease on tip.
func (s *Store) publish(commit, tip string, force bool) error {
	if s.opt.Remote != "" {
		args := []string{"push", "--quiet", s.opt.Remote, commit + ":" + s.opt.Branch}
		if force {
			args = []string{"push", "--quiet", "--force-with-lease=" + s.opt.Branch + ":" + tip, s.opt.Remote, commit + ":" + s.opt.Branch}
		}
		if _, err := s.g.run(nil, nil, args...); err != nil {
			return err
		}
		_, err := s.g.run(nil, nil, "update-ref", s.opt.Ref, commit)
		return err
	}
	_, err := s.g.run(nil, nil, "update-ref", s.opt.Ref, commit, tip)
	return err
}

// commit writes files on top of parent ("" = root commit, empty tree) with
// plumbing only: hash-object, a private index, write-tree, commit-tree.
func (s *Store) commit(parent string, fresh bool, files map[string][]byte, msg string) (string, error) {
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
	if parent != "" {
		if _, err := s.g.run(nil, env, "read-tree", parent); err != nil {
			return "", err
		}
	}
	var info bytes.Buffer
	for path, content := range files {
		oid, err := s.g.run(content, nil, "hash-object", "-w", "--stdin")
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&info, "100644 %s\t%s/%s\n", strings.TrimSpace(string(oid)), SchemaDir, path)
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
