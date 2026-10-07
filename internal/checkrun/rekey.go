package checkrun

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sloprail/sloprail/internal/changeset"
	"github.com/sloprail/sloprail/internal/checkcache"
	"github.com/sloprail/sloprail/internal/checkstore"
	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/guardrail"
)

// Why a stored verdict could not be carried across a key change (MigrationStats reasons).
const (
	skipRuleGone       = checkcache.ReasonRuleGone
	skipUnreachable    = "commits unreachable"
	skipSubjectGone    = "subject no longer in the range"
	skipNothingMatched = "nothing selected in the range"
	skipNotRebuildable = "range could not be rebuilt"
	skipSuperseded     = "superseded by a newer pass"
)

// RebuildKeys is the checkcache.Rebuild of this engine, run by `sr-checks migrate-keys` and by
// nothing else: it carries the verdicts of an older key schema into the current one WITHOUT
// judging anything.
//
// A record that stored its parts (Check.FilesPart, Check.SubjectFingerprint) is re-keyed from
// them, a hash each, with no repository read (checkcache.MigrateFromParts): that is what a future
// key change costs. The records of builds before the parts were kept are rebuilt, and that is
// the dear part: a guard's key is a pure function of (rule, subject, the range's file contents,
// the fingerprint the rule's `subjects:` script gives the subject at that range), and every stored
// run records its rule, base, head and per-subject checks. So of the PASSES of one subject of
// one rule the newest is rebuilt (an older one can be found again only if the content goes
// back to exactly what it was; it is skipped as superseded), at the run's recorded range exactly
// as `run` does (the changeset from git, then the rule's `subjects:` script as it is in the head
// tree), and keyed under the current schema's fingerprint. A pass whose commits are unreachable,
// whose rule is no longer declared or whose subject no longer appears is skipped and counted;
// the run itself is history either way. No judge is asked, no model is called, and no citation is
// resolved against a transcript.
//
// The rebuild is per (rule, range), so a rule's `subjects:` script runs once per range however
// many runs and subjects were stored for it; the ranges are rebuilt in parallel, a head's
// checkout is made once for every rule and range that ends at it, and a changeset is built once
// for every rule that selects the same files of the same range. A rule with no `subjects:`
// script whose match reads no markers needs nothing of its files but their paths and blob ids, and
// is built without reading any (changeset.Options.Lean), as `verify` builds it.
func RebuildKeys(root string, guards []declaration.FileGuard) checkcache.Rebuild {
	return func(old []checkcache.Run) ([]checkcache.Run, checkcache.MigrationStats, error) {
		return rekeyRuns(root, guards, old)
	}
}

type rekeyGroup struct{ rule, base, head string }

type rekeyRef struct{ run, check int }

// rekeyProgress is where a migration reports how far it has got (stderr: it runs inside the
// first `sr-checks run` after an upgrade, and takes a while on a big store).
var rekeyProgress io.Writer = os.Stderr

// rekeyProgressEvery is how often the progress line is repeated.
const rekeyProgressEvery = 2 * time.Second

// rekeyResult is what a range yields: the fingerprint of each of its subjects, or why it yields none.
type rekeyResult struct {
	keys   map[string]rekeyKey
	reason string
}

// rekeyKey is a subject's new fingerprint and the parts it is made of, which the migrated
// record keeps (checkcache.Check.FilesPart).
type rekeyKey struct {
	fingerprint, subjectFingerprint string
	files                           []byte
}

// rekeyConcurrency is how many ranges are rebuilt at once: SLOPRAIL_STOP_CONCURRENCY when set
// (the bound `run` puts on file-guards), else one per CPU, since a rebuild is git and shell
// processes, not a wait on a model.
func rekeyConcurrency() int {
	if os.Getenv(StopConcurrencyEnv) != "" {
		return stopConcurrency()
	}
	return max(runtime.NumCPU(), defaultStopConcurrency)
}

func rekeyRuns(root string, guards []declaration.FileGuard, old []checkcache.Run) ([]checkcache.Run, checkcache.MigrationStats, error) {
	byRule := map[string]declaration.FileGuard{}
	for _, g := range guards {
		byRule[g.Qualified()] = g
	}
	// What carries its parts is re-keyed from them, with no repository read; what does not (the
	// records of builds before the parts were kept) is rebuilt from its recorded range.
	fromParts, st := checkcache.MigrateFromParts(old, func(rule string) bool { _, ok := byRule[rule]; return ok },
		fingerprintOfParts, checkstore.StatusPass, guardKind)
	old = fromParts
	out := make([]checkcache.Run, len(old))
	copy(out, old)
	// Of the passes of one subject of one rule only the newest is carried: an older one can be
	// found again only if the content goes back to exactly what it was, and costs a rebuild of
	// its range like any other. The older ones stay in the older directory, counted as skipped.
	type subjectOf struct{ rule, subject string }
	newest := map[subjectOf]rekeyRef{}
	newer := func(a, b rekeyRef) bool { // a is newer than b
		ra, rb := old[a.run], old[b.run]
		if ra.RunAt != rb.RunAt {
			return ra.RunAt > rb.RunAt
		}
		if ra.ID != rb.ID {
			return ra.ID > rb.ID
		}
		return a.run > b.run || a.run == b.run && a.check > b.check
	}
	var passes []rekeyRef
	for i, r := range old {
		for j, c := range r.Checks {
			if c.Kind == guardKind && c.Status == checkstore.StatusPass && c.Fingerprint != "" && !c.HasParts() {
				ref := rekeyRef{i, j}
				passes = append(passes, ref)
				k := subjectOf{r.Rule, c.Subject}
				if cur, ok := newest[k]; !ok || newer(ref, cur) {
					newest[k] = ref
				}
			}
		}
	}
	groups := map[rekeyGroup][]rekeyRef{}
	for _, ref := range passes {
		r, c := old[ref.run], old[ref.run].Checks[ref.check]
		if newest[subjectOf{r.Rule, c.Subject}] != ref {
			st.Skip(skipSuperseded)
			continue
		}
		k := rekeyGroup{r.Rule, r.BaseRef, r.HeadRef}
		groups[k] = append(groups[k], ref)
	}
	keys := make([]rekeyGroup, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	// By head first: the ranges of one head share one checkout of it, taken once and removed
	// when the head's last range is done.
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.head != b.head {
			return a.head < b.head
		}
		if a.rule != b.rule {
			return a.rule < b.rule
		}
		return a.base < b.base
	})
	rc := newRekeyContext(root, keys)
	results := make([]rekeyResult, len(keys))
	started := time.Now()
	if len(passes) > 0 {
		fmt.Fprintf(rekeyProgress, "sloprail: migrating cache keys: %d passes stored, %d superseded by a newer pass of the same subject, %d ranges to rebuild\n",
			len(passes), st.Reasons[skipSuperseded], len(keys))
	}
	var done atomic.Int32
	stop := make(chan struct{})
	var reporter sync.WaitGroup
	if len(keys) > 0 {
		reporter.Add(1)
		go func() {
			defer reporter.Done()
			t := time.NewTicker(rekeyProgressEvery)
			defer t.Stop()
			for {
				select {
				case <-stop:
					return
				case <-t.C:
					fmt.Fprintf(rekeyProgress, "sloprail: migrating cache keys: %d/%d ranges\n", done.Load(), len(keys))
				}
			}
		}()
	}
	forEach(len(keys), rekeyConcurrency(), func(i int) {
		ks, reason := rc.keys(byRule, keys[i])
		results[i] = rekeyResult{ks, reason}
		done.Add(1)
	})
	close(stop)
	reporter.Wait()
	rc.close()

	// Apply serially, so the stats and the copy-on-write of a run need no locking.
	owned := map[int]bool{}
	for n, k := range keys {
		ks, reason := results[n].keys, results[n].reason
		for _, ref := range groups[k] {
			c := old[ref.run].Checks[ref.check]
			nk, ok := ks[c.Subject]
			switch {
			case reason != "":
				st.Skip(reason)
			case !ok:
				st.Skip(skipSubjectGone)
			default:
				if !owned[ref.run] { // never edit the caller's run
					out[ref.run].Checks = append([]checkcache.Check(nil), old[ref.run].Checks...)
					owned[ref.run] = true
				}
				chk := &out[ref.run].Checks[ref.check]
				chk.Fingerprint, chk.FilesPart, chk.SubjectFingerprint = nk.fingerprint, nk.files, nk.subjectFingerprint
				st.Migrated++
			}
		}
	}
	if len(keys) > 0 {
		fmt.Fprintf(rekeyProgress, "sloprail: migrating cache keys: %d/%d ranges, %s, in %s (%s)\n",
			len(keys), len(keys), st, time.Since(started).Round(100*time.Millisecond), rc.phases())
	}
	return out, st, nil
}

// rekeyContext is what the ranges of one migration share: per head commit, one checkout of
// it and the changesets built over it, kept until the head's last range is done.
type rekeyContext struct {
	root string
	ev   *changesetEvaluation // the subjects script runs through its runner, as in `run`

	mu    sync.Mutex
	heads map[string]*rekeyHead
	revs  map[string]*rekeyRev

	// Where the time went, summed over every worker (so it exceeds the elapsed time).
	buildNs, snapNs, scriptNs, builds, scripts, leanNs, leans atomic.Int64
}

type rekeyHead struct {
	mu        sync.Mutex
	remaining int // ranges of this head not yet done
	tree      *gitrepo.Snapshot
	treeErr   error
	treeMade  bool
	sets      map[rekeyCSKey]*rekeyCS
}

type rekeyCSKey struct {
	base, deletions, match string
	lean                   bool
}

type rekeyCS struct {
	once sync.Once
	cs   changeset.Changeset
	err  error
}

type rekeyRev struct {
	once sync.Once
	ok   bool
}

func newRekeyContext(root string, keys []rekeyGroup) *rekeyContext {
	rc := &rekeyContext{root: root, heads: map[string]*rekeyHead{}, revs: map[string]*rekeyRev{},
		ev: &changesetEvaluation{errw: io.Discard, root: root, params: Params{Root: root}}}
	for _, k := range keys {
		h := rc.heads[k.head]
		if h == nil {
			h = &rekeyHead{sets: map[rekeyCSKey]*rekeyCS{}}
			rc.heads[k.head] = h
		}
		h.remaining++
	}
	return rc
}

// close removes any checkout still held (none, once every range is done).
func (rc *rekeyContext) close() {
	for _, h := range rc.heads {
		rc.dropTree(h)
	}
}

func (rc *rekeyContext) dropTree(h *rekeyHead) {
	h.mu.Lock()
	tree := h.tree
	h.tree = nil
	h.mu.Unlock()
	if tree != nil {
		_ = tree.Remove() // the repository's worktree lock serializes the registry; the files go in parallel
	}
}

func (rc *rekeyContext) revisionExists(rev string) bool {
	rc.mu.Lock()
	r := rc.revs[rev]
	if r == nil {
		r = &rekeyRev{}
		rc.revs[rev] = r
	}
	rc.mu.Unlock()
	r.once.Do(func() { r.ok = revisionExists(rc.root, rev) })
	return r.ok
}

// snapshot is the head's one read-only checkout, made on first need.
func (rc *rekeyContext) snapshot(h *rekeyHead, head string) (*gitrepo.Snapshot, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.treeMade {
		h.treeMade = true
		defer rc.timed(&rc.snapNs, nil)()
		h.tree, h.treeErr = gitrepo.AddSnapshot(rc.root, "", head)
	}
	return h.tree, h.treeErr
}

// changeset is the range's changeset for a rule's selection, built once per (base, head,
// deletions mode, match) however many rules share them. Callers only read it. A rule with no
// `subjects:` script whose match reads no markers needs nothing of the files but their paths
// and git blob ids, which is all its key is over (what `verify` builds, see prepare): its
// changeset is lean, and no file is read.
func (rc *rekeyContext) changeset(h *rekeyHead, g declaration.FileGuard, match *guardrail.Matcher, r gitrepo.Range) (changeset.Changeset, error) {
	lean := g.Subjects == "" && !strings.Contains(g.Match, "arkers")
	key := rekeyCSKey{r.Base, string(g.Deletions), g.Match, lean}
	h.mu.Lock()
	e := h.sets[key]
	if e == nil {
		e = &rekeyCS{}
		h.sets[key] = e
	}
	h.mu.Unlock()
	e.once.Do(func() {
		if lean {
			defer rc.timed(&rc.leanNs, &rc.leans)()
		} else {
			defer rc.timed(&rc.buildNs, &rc.builds)()
		}
		e.cs, e.err = changeset.Build(rc.root, r, changeset.Options{
			Deletions:   changeset.DeletionMode(g.Deletions),
			Scan:        Markers,
			Select:      Selector(match),
			Lean:        lean,
			NoPatch:     lean,
			RawBlobs:    lean,
			SkipHistory: lean,
		})
	})
	return e.cs, e.err
}

// keys is rangeKeys over the shared checkout and changesets; when it is the head's last range,
// the head's checkout and changesets are let go.
func (rc *rekeyContext) keys(rules map[string]declaration.FileGuard, k rekeyGroup) (map[string]rekeyKey, string) {
	h := rc.heads[k.head]
	defer func() {
		h.mu.Lock()
		h.remaining--
		last := h.remaining == 0
		if last {
			h.sets = nil
		}
		h.mu.Unlock()
		if last {
			rc.dropTree(h)
		}
	}()
	return rc.rangeKeys(h, rules, k)
}

// rangeFingerprints is the guard fingerprint of every subject of a rule over a recorded range,
// as `run` computes them: by subject id. A non-empty reason says the range yields none.
func rangeFingerprints(root string, rules map[string]declaration.FileGuard, k rekeyGroup) (map[string]string, string) {
	rc := newRekeyContext(root, []rekeyGroup{k})
	defer rc.close()
	ks, reason := rc.rangeKeys(rc.heads[k.head], rules, k)
	fps := make(map[string]string, len(ks))
	for id, nk := range ks {
		fps[id] = nk.fingerprint
	}
	if reason != "" {
		return nil, reason
	}
	return fps, ""
}

// rangeKeys is the new key of every subject of a rule over a recorded range, as `run` computes
// them: by subject id. A non-empty reason says the range yields none.
func (rc *rekeyContext) rangeKeys(h *rekeyHead, rules map[string]declaration.FileGuard, k rekeyGroup) (map[string]rekeyKey, string) {
	g, ok := rules[k.rule]
	if !ok {
		return nil, skipRuleGone
	}
	if !rc.revisionExists(k.base) || !rc.revisionExists(k.head) {
		return nil, skipUnreachable
	}
	match, err := guardrail.CompileFileMatch(g.Match)
	if err != nil {
		return nil, skipNotRebuildable
	}
	r := gitrepo.Range{Base: k.base, Head: k.head}
	cs, err := rc.changeset(h, g, match, r)
	if err != nil {
		return nil, skipNotRebuildable
	}
	if len(cs.Files) == 0 {
		return nil, skipNothingMatched
	}
	subjects := []changeset.Subject{changeset.Whole(cs)}
	if g.Subjects != "" {
		subs, reason := rc.rangeSubjects(h, g, r, cs)
		if reason != "" {
			return nil, reason
		}
		subjects = subs
	}
	ks := make(map[string]rekeyKey, len(subjects))
	for _, sub := range subjects {
		payload := changeset.NewPayload(cs, sub, "")
		files, subFP := guardParts(payload)
		ks[sub.ID] = rekeyKey{fingerprint: guardKey(payload), files: files, subjectFingerprint: subFP}
	}
	return ks, ""
}

// rangeSubjects runs the rule's `subjects:` script over the range, from the rule's folder as it
// is in the head tree (a read-only checkout of head, like `run`'s), and returns its subjects.
func (rc *rekeyContext) rangeSubjects(h *rekeyHead, g declaration.FileGuard, r gitrepo.Range, cs changeset.Changeset) ([]changeset.Subject, string) {
	tree, err := rc.snapshot(h, r.Head)
	if err != nil {
		return nil, skipNotRebuildable
	}
	root := rc.root
	// The script as the head tree has it, when the rule lives in the repository.
	if rel, err := filepath.Rel(root, g.Dir); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		g.Dir = filepath.Join(tree.Path, rel)
		if _, err := os.Stat(filepath.Join(g.Dir, g.Subjects)); err != nil {
			return nil, skipRuleGone
		}
	}
	defer rc.timed(&rc.scriptNs, &rc.scripts)()
	subs, err := rc.ev.guardSubjects(g, r, cs, tree.Path)
	if err != nil {
		return nil, skipNotRebuildable
	}
	return subs, ""
}

// timed adds the time until the returned func is called to total (and one to n, when given).
func (rc *rekeyContext) timed(total, n *atomic.Int64) func() {
	start := time.Now()
	return func() {
		total.Add(int64(time.Since(start)))
		if n != nil {
			n.Add(1)
		}
	}
}

// phases says, in one line, where the migration's time went.
func (rc *rekeyContext) phases() string {
	d := func(ns *atomic.Int64) time.Duration { return time.Duration(ns.Load()).Round(time.Second) }
	return fmt.Sprintf("lean changesets %s (%d), full changesets %s (%d), checkouts %s (%d), subjects scripts %s (%d), summed over workers",
		d(&rc.leanNs), rc.leans.Load(), d(&rc.buildNs), rc.builds.Load(), d(&rc.snapNs), len(rc.heads), d(&rc.scriptNs), rc.scripts.Load())
}

// revisionExists says rev names a commit of the repository (the empty tree, a range's base
// before the first commit, always exists).
func revisionExists(root, rev string) bool {
	if rev == gitrepo.EmptyTree {
		return true
	}
	if rev == "" {
		return false
	}
	return exec.Command("git", "-C", root, "cat-file", "-e", rev+"^{commit}").Run() == nil
}

// migrationLine is the one line a migration reports.
func migrationLine(st checkcache.MigrationStats) string {
	return fmt.Sprintf("sloprail: the stored verdicts were re-keyed without judging again: %s\n", st)
}
