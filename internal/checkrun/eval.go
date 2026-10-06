package checkrun

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sloprail/sloprail/internal/changeset"
	"github.com/sloprail/sloprail/internal/checkstore"
	"github.com/sloprail/sloprail/internal/declaration"
	dispatchcore "github.com/sloprail/sloprail/internal/dispatch"
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/grounding"
	"github.com/sloprail/sloprail/internal/guardrail"
	"github.com/sloprail/sloprail/internal/judgelimit"
	"github.com/sloprail/sloprail/internal/transcript"
)

// Evaluating file-guards over an explicit range: one changeset per rule.
//
// A file-guard judges COMMITS. `sr-checks run|verify` evaluates each rule once, over the
// range merge-base(--base, --head)..--head, as the squashed net change: one payload,
// one run of its require and checks.
//
//   - EVERY check is cached by content, the same way: a script, a judge and a requirement
//     alike. What is kept is one verdict per guard x subject, under the key (rule hash, which
//     covers every script and template of the rule folder; subject id; the content of the
//     subject's files; for a rule that requires a citation, the commit messages and quotes;
//     the subject's own fingerprint from the `subjects:` script, when it gave one). A finished
//     pass or fail with the same key is a hit: `run` does not run the guard again, whatever
//     session, agent, branch or commit range made the verdict. A stored FAIL is replayed (terminal
//     until the input changes). The steps' own statuses and reasons are kept inside the verdict.
//   - A guard's checks read the subject they are handed. Anything else a verdict depends on (a
//     file a check opens with its own tools) must be declared through the `subjects:` script's
//     per-subject fingerprint, or a change to it is not seen. prepare only builds context.
//   - `verify` only reads: it never executes a script, a judge or a requirement. A key with no
//     stored verdict is red ("not judged yet"); a stored fail shows its reasons. It computes the
//     keys without a session (the `subjects:` script is given no transcript).
//   - An engine error is no verdict: nothing is stored, and the next `run` starts again. A real
//     refusal, also from a check that reads the session, is a complete FAIL and is stored; only
//     "no session available to judge" is not (verify shows it as "not judged yet").
//   - Anything that goes wrong in the engine — git, the rule's own folder, the
//     snapshot — fails CLOSED and refuses. A range that could not be read is never an
//     empty one; a range where `match` selects nothing is a pass.
//
// Checks run against a read-only snapshot of head (SR_TREE), never the working
// tree, with SR_BASE and SR_HEAD naming the range.

// Params is everything one evaluation of the file-guards is given. Nothing here knows about a
// session's store or a hook: the caller states the range and where the transcript is.
type Params struct {
	// On names where the check ran, for the rule-decision log: "Stop", "sr-checks run",
	// "sr-checks verify", "sr-checks show". Empty: "Stop", the in-session default.
	On string
	// Err receives the diagnostics (stderr); nil discards them.
	Err    io.Writer
	Guards []declaration.FileGuard
	// Root is the repository root; Range the commits judged.
	Root  string
	Range gitrepo.Range
	// Cwd, Transcript, Workspace, SessionID and AgentID name the session the run is made
	// from, when there is one; Subagent says a sub-agent's. All may be empty.
	Cwd, Transcript, Workspace, SessionID, AgentID string
	Subagent                                       bool
	// Store records the runs and finds earlier ones; nil records and looks up nothing.
	Store checkstore.Store
	// Verify: a judge is looked up, never asked; nothing is recorded.
	Verify bool
	// FailuresOnly: a Verify caller that drops every "not judged yet" (the Stop) does not
	// look up why a key has no verdict: that explanation is only for a reader who sees it.
	FailuresOnly bool
	// WholeRange: do not advance each rule's base to its effective base; list every subject
	// of the range asked about with its stored result (`sr-checks show`, a reader's view).
	WholeRange bool
	// Recorded is the citations the session already recorded per file (sr-file --cite),
	// oldest first; a citation refusal hands them back as the trailer to paste.
	Recorded map[string][]transcript.Citation
	// RecordedFn, when Recorded is nil, supplies it on first need: only a citation refusal reads
	// it, and building it can be expensive (it reads every sub-agent's store).
	RecordedFn func() map[string][]transcript.Citation
	// Locks coordinates judges with the other `run`s of this machine (slots, in-flight keys).
	// Nil: the machine's default under the user cache dir. Only a `run` (not Verify) uses it.
	Locks *judgelimit.Limiter
}

// FileGuardResult is one file-guard's outcome: the guard's name, how a refusal should
// attribute it, whether it refused, and the reason to relay.
//
// Attribution carries the plugin-aware name (bare for a project's guard, plus
// " from plugin X" for a shipped one).
type FileGuardResult struct {
	Name        string
	Attribution string
	Refused     bool
	Reason      string
	// Unavailable is the cause when the rule was refused for want of a judge (its substrate
	// died: usage limit, authentication, version skew). Such refusals are reported once, as one
	// outage, by Evaluate; see collapseUnavailable.
	Unavailable string
}

// changesetEvaluation is what one evaluation of every file-guard shares. Its rules are
// evaluated concurrently (see evaluateChangesets), so everything here is read-only or
// safe to use from several goroutines: stderr and git's worktree registry are guarded
// below.
type changesetEvaluation struct {
	errw      io.Writer
	diags     map[string]*bytes.Buffer // each rule's diagnostics, emitted in declaration order after the pool
	root      string
	params    Params
	store     checkstore.Store // nil: nothing is recorded or looked up
	identity  checkstore.RunIdentity
	batch     string
	verify    bool // judges are looked up, never asked; nothing is written
	rng       gitrepo.Range
	runner    dispatchcore.Runner
	snapshots sync.Mutex // `git worktree add/remove` race on the worktree names

	mu       sync.Mutex
	outcomes []CheckOutcome

	// Under verify the rules of one range share one checkout of its head: nothing writes to a
	// snapshot (it is read-only), and a checkout of a large tree is by far the dearest step.
	sharedMu   sync.Mutex
	sharedTree *gitrepo.Snapshot
	sharedHead string

	// Every rule's effectiveBase asks the same ancestry questions of the same commits: one
	// answer per pair for the whole evaluation, not one `git merge-base` per rule.
	ancMu sync.Mutex
	anc   map[[2]string]bool

	// Coordination with parallel runs, and live progress (a `run` only).
	locks             *judgelimit.Limiter
	errMu             sync.Mutex // errw is written by the pool's goroutines and by the heartbeat
	started           time.Time
	total, done, busy atomic.Int32 // rules to settle, rules settled, judges in flight

	statusMu   sync.Mutex
	statusMemo map[string][]checkstore.CheckStatusRow // verify only: nothing is written, so a rule's rows hold
}

// verifyTree is the one snapshot of head every rule of a verify shares, made on first need.
func (ev *changesetEvaluation) verifyTree(head string) (*gitrepo.Snapshot, error) {
	ev.sharedMu.Lock()
	defer ev.sharedMu.Unlock()
	if ev.sharedTree != nil && ev.sharedHead == head {
		return ev.sharedTree, nil
	}
	if ev.sharedTree != nil {
		return nil, nil // another head: the caller takes its own
	}
	ev.snapshots.Lock()
	defer ev.snapshots.Unlock()
	tree, err := gitrepo.AddSnapshot(ev.root, "", head)
	if err != nil {
		return nil, err
	}
	ev.sharedTree, ev.sharedHead = tree, head
	return tree, nil
}

// releaseShared removes the shared snapshot, once every rule is done with it.
func (ev *changesetEvaluation) releaseShared() {
	ev.sharedMu.Lock()
	tree := ev.sharedTree
	ev.sharedTree = nil
	ev.sharedMu.Unlock()
	if tree != nil {
		ev.snapshots.Lock()
		defer ev.snapshots.Unlock()
		_ = tree.Remove()
	}
}

// checkStatus is store.CheckStatus(false, rule), read once per rule under verify (the store is
// only read then, and each read rebuilds a view of every run).
func (ev *changesetEvaluation) checkStatus(rule string) ([]checkstore.CheckStatusRow, error) {
	if !ev.verify {
		return ev.store.CheckStatus(false, rule)
	}
	ev.statusMu.Lock()
	defer ev.statusMu.Unlock()
	if rows, ok := ev.statusMemo[rule]; ok {
		return rows, nil
	}
	rows, err := ev.store.CheckStatus(false, rule)
	if err != nil {
		return nil, err
	}
	if ev.statusMemo == nil {
		ev.statusMemo = map[string][]checkstore.CheckStatusRow{}
	}
	ev.statusMemo[rule] = rows
	return rows, nil
}

// CheckOutcome is one check's latest result as this evaluation saw it: what `verify`
// and `run` print per subject.
type CheckOutcome struct {
	Rule    string `json:"rule"`
	Subject string `json:"subject"`
	Kind    string `json:"kind"`
	Status  string `json:"status"` // pass | fail | missing
	Source  string `json:"source"` // ran | cached | stored
	Reason  string `json:"reason,omitempty"`
}

func (ev *changesetEvaluation) note(o CheckOutcome) {
	ev.mu.Lock()
	defer ev.mu.Unlock()
	ev.outcomes = append(ev.outcomes, o)
}

// StopConcurrencyEnv bounds how many file-guard rules are evaluated at once.
const StopConcurrencyEnv = "SLOPRAIL_STOP_CONCURRENCY"

// defaultStopConcurrency is 6, not GOMAXPROCS: a judge waits on a model, not a CPU. Judges are
// bounded machine-wide by the judge semaphore, so this is not shared with the run slots.
const defaultStopConcurrency = 6

func stopConcurrency() int {
	if v := os.Getenv(StopConcurrencyEnv); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return defaultStopConcurrency
}

// forEach runs fn(0..n-1) on at most limit goroutines and returns when all are done.
func forEach(n, limit int, fn func(i int)) {
	if limit < 1 {
		limit = 1
	}
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		sem <- struct{}{}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			fn(i)
		}(i)
	}
	wg.Wait()
}

// ruleRun is one rule mid-evaluation: prepared (changeset, snapshot) and carrying how
// far its checks have got.
type ruleRun struct {
	g          declaration.FileGuard
	hash       string
	req        dispatchcore.Request
	payload    changeset.Payload
	subject    changeset.Subject
	unresolved []changeset.Unresolved
	tree       *gitrepo.Snapshot
	head       string
	base       string // the range's base, and the trees of base and head ("" when unreadable)
	baseTree   string
	headTree   string
	runID      string
	next       int // index of the first check not yet run

	// key is the guard's verdict key over its subject ("" when it cannot be keyed).
	key string
	// volatile: the refusal is "no session available to judge", so it is not stored.
	volatile bool
	replayed bool // the verdict is a stored one, already recorded

	lockKey        string // the in-flight lock held while its judges run, and how to let it go
	unlockInflight func()

	cheap, slow time.Duration // time spent before / at the first judge
	result      FileGuardResult
	refused     bool
	settled     bool // result is final
	nothing     bool // the rule had nothing to run: it decided nothing unless it refused
}

// evaluateChangesets evaluates every file-guard over rng and returns each refusal, in
// the order the guards were declared, with every check's outcome. root is the
// repository root; store may be nil. In verify mode nothing is recorded.
//
// Evaluate evaluates every file-guard over the range and returns each refusal, in the order
// the guards were declared, with every check's outcome. In verify mode nothing is recorded.
// The caller closes the store, which writes everything recorded as one segment.
//
// Rules are independent, so they run concurrently (SLOPRAIL_STOP_CONCURRENCY, default
// 6) in three steps:
//
//  1. prepare every rule: its changeset and snapshot;
//  2. the CHEAP checks of every rule: its requirements and the script checks that
//     precede its first judge, in declared order. A refusal here settles that rule
//     alone: its own judges are skipped (with the reason);
//  3. the rest of every rule that did not refuse (its judges and what follows)
//     concurrently, so one rule's refusal never hides another rule's judges.
//
// Within a rule the declared order and first-refusal-ends are kept. The judged
// verdicts are put in the cache once, at the end (one write per run).
func Evaluate(p Params) ([]FileGuardResult, []CheckOutcome) {
	guards := p.Guards
	if len(guards) == 0 {
		return nil, nil
	}
	start := time.Now()
	errw := p.Err
	if errw == nil {
		errw = io.Discard
	}
	ev := &changesetEvaluation{
		errw: errw, diags: map[string]*bytes.Buffer{}, root: p.Root, params: p,
		store: p.Store, verify: p.Verify, rng: p.Range,
		batch: "check-" + strconv.FormatInt(time.Now().UnixNano(), 10),
	}
	ev.identity = ev.runIdentity()
	defer ev.releaseShared()
	ev.started = start
	if !p.Verify && p.Store != nil {
		l := p.Locks
		if l == nil {
			d := judgelimit.New(lockedWriter{&ev.errMu, errw})
			l = &d
		}
		ev.locks = l
		l.Prune()
		stop := make(chan struct{})
		defer close(stop)
		go ev.heartbeat(stop)
	}
	limit := stopConcurrency()
	for _, g := range guards {
		ev.diags[g.Qualified()] = &bytes.Buffer{} // filled before the pool: read-only map after
	}

	prepared := make([][]*ruleRun, len(guards)) // each rule's subjects, ready to run
	skipped := make([]*ruleRun, len(guards))    // a rule with nothing to run: its outcome
	forEach(len(guards), limit, func(i int) {
		g := guards[i]
		if IsLaunchedBy(os.Getenv, g.Name) {
			fmt.Fprintf(ev.log(g),
				"sloprail: file-guard %q not enforced here — this session was launched by its own check (%s)\n",
				g.Name, LaunchedByEnv)
			return
		}
		rrs, r, refused := ev.prepare(g)
		if len(rrs) == 0 {
			skipped[i] = &ruleRun{g: g, result: r, refused: refused, settled: true, nothing: true}
			return
		}
		prepared[i] = rrs
	})
	var runs, out []*ruleRun // every subject to run; every outcome, in declaration order
	for i := range guards {
		if skipped[i] != nil {
			out = append(out, skipped[i])
		}
		runs = append(runs, prepared[i]...)
		out = append(out, prepared[i]...)
	}

	ev.total.Store(int32(len(runs)))
	// The cheap checks of every rule, before any judge.
	forEach(len(runs), limit, func(i int) {
		if runs[i] != nil {
			ev.runCheap(runs[i])
		}
	})
	// A refusal in one rule defers only that rule's own judges; every other rule's
	// judges still run.
	forEach(len(runs), limit, func(i int) {
		if runs[i] != nil && !runs[i].settled {
			ev.runRest(runs[i])
		}
	})

	var refusals []FileGuardResult
	for _, o := range out {
		if o != nil && o.settled && o.refused {
			refusals = append(refusals, o.result)
		}
	}
	refusals = collapseUnavailable(refusals)
	emitFileGuardEvents(out, p.On, p.FailuresOnly)
	for _, o := range out {
		if o != nil && o.tree != nil {
			fmt.Fprintf(ev.log(o.g), "sloprail: file-guard %s: cheap checks %s, judges %s\n", o.g.Attribution(),
				o.cheap.Round(time.Millisecond), o.slow.Round(time.Millisecond))
		}
	}
	// Each rule's diagnostics, in declaration order: concurrent rules must not make
	// the log's order depend on who finished first.
	ev.errMu.Lock()
	for _, g := range guards {
		if b := ev.diags[g.Qualified()]; b != nil {
			errw.Write(b.Bytes())
		}
	}
	ev.errMu.Unlock()
	fmt.Fprintf(errw, "sloprail: file-guards evaluated in %s (%d rules, concurrency %d)\n",
		time.Since(start).Round(time.Millisecond), len(guards), limit)
	sort.SliceStable(ev.outcomes, func(i, j int) bool {
		a, b := ev.outcomes[i], ev.outcomes[j]
		if a.Rule != b.Rule {
			return a.Rule < b.Rule
		}
		if a.Subject != b.Subject {
			return a.Subject < b.Subject
		}
		return a.Kind < b.Kind
	})
	return refusals, ev.outcomes
}

// refusal is the result for a rule that could not be evaluated or that refused.
func refusal(g declaration.FileGuard, reason string) FileGuardResult {
	return FileGuardResult{Name: g.Name, Attribution: g.Attribution(), Refused: true, Reason: reason}
}

// runIdentity fills the provenance columns: the repository (gitrepo.RepoID), the branch and
// the session. Best effort — an unreadable one is left empty rather than costing the run.
func (ev *changesetEvaluation) runIdentity() checkstore.RunIdentity {
	id := checkstore.RunIdentity{SessionID: ev.params.SessionID, AgentID: ev.params.AgentID}
	if root, err := gitrepo.RepoID(ev.root); err == nil {
		id.RepoID = root
	}
	if pos, err := gitrepo.Head(ev.root); err == nil {
		id.Branch = pos.Branch
	}
	return id
}

// record stores a run, returning its id ("" when nothing is recorded: verify, or no store).
func (ev *changesetEvaluation) record(run checkstore.CheckRun) (string, error) {
	if ev.store == nil || ev.verify {
		return "", nil
	}
	run.RunIdentity, run.BatchID = ev.identity, ev.batch
	return ev.store.RecordRun(run)
}

// recordCheck stores one check of a run. A check that could not be stored is an ENGINE
// failure, not something to print and carry on past: the run would hold fewer checks than it
// ran, and read as more passed than it was.
func (ev *changesetEvaluation) recordCheck(runID string, c checkstore.CheckRecord) error {
	if ev.store == nil || ev.verify || runID == "" {
		return nil
	}
	if _, err := ev.store.RecordCheck(runID, c); err != nil {
		return fmt.Errorf("could not record the %s check: %w", c.Kind, err)
	}
	return nil
}

// engineFailure records a run that failed as an engine — it passes nothing — and returns the
// refusal that holds the caller.
func (ev *changesetEvaluation) engineFailure(g declaration.FileGuard, run checkstore.CheckRun, err error) (FileGuardResult, bool) {
	fmt.Fprintf(ev.log(g), "sloprail: file-guard %s: %v\n", g.Attribution(), err)
	run.ExitCode, run.Error, run.Complete = 1, err.Error(), true
	if _, recErr := ev.record(run); recErr != nil {
		fmt.Fprintln(ev.log(g), "sloprail:", recErr) // already refusing
	}
	return refusal(g, namingFiles(fmt.Sprintf(
		"the file-guard %q could not be evaluated (%v); refusing because a guard that could not decide must not be read as approval",
		g.Name, err), nil)), true
}

// prepare readies one rule over the range: the changeset, the snapshot, and the run recorded
// RUNNING. A nil ruleRun means there is nothing more to do — the result and whether it
// refused are the outcome.
func (ev *changesetEvaluation) prepare(g declaration.FileGuard) ([]*ruleRun, FileGuardResult, bool) {
	rule := g.Qualified()
	// RULE AGE: the range is the stated one, raised to the rule's floor (the parent of its last
	// change) when that is later, so work made before the rule existed is not its debt.
	r, err := ev.ruleRange(g)
	if err != nil {
		return ev.fail(g, checkstore.CheckRun{CheckID: rule, BaseRef: ev.rng.Base, HeadRef: ev.rng.Head, Metadata: map[string]any{"eventKind": changeset.Kind}}, fmt.Errorf("its range is not computable: %w", err))
	}
	run := checkstore.CheckRun{CheckID: rule, BaseRef: r.Base, HeadRef: r.Head, Metadata: map[string]any{"eventKind": changeset.Kind}}
	run.BaseTree, run.HeadTree = rangeTrees(ev.root, r)

	// The rule's hash is what a run is recorded under; verify records nothing, so it hashes only
	// a rule that selected something (hashing walks the rule's folder, and most rules select nothing).
	var hash string
	if !ev.verify {
		if hash, err = changeset.RuleHashAt(ev.root, g.Dir, g.Origin.FromPlugin()); err != nil {
			return ev.fail(g, run, err)
		}
		run.RuleHash = hash
	}
	match, err := guardrail.CompileFileMatch(g.Match)
	if err != nil {
		return ev.fail(g, run, fmt.Errorf("its match %q could not be compiled: %w", g.Match, err))
	}
	lean := ev.verify && g.Subjects == "" && !strings.Contains(g.Match, "arkers")
	build := func(r gitrepo.Range) (changeset.Changeset, error) {
		return changeset.Build(ev.root, r, changeset.Options{
			Deletions: changeset.DeletionMode(g.Deletions),
			Scan:      Markers,
			Select:    Selector(match),
			Lean:      lean,
			// A `subjects:` script is handed the whole payload in run and in verify alike.
			NoPatch:  ev.verify && g.Subjects == "",
			RawBlobs: ev.verify && g.Subjects == "",
			// Only a citation requirement reads which commits changed a file.
			SkipHistory: lean && !requiresCitation(g),
		})
	}
	cs, err := build(r)
	if err != nil {
		return ev.fail(g, run, err)
	}
	if len(cs.Files) == 0 {
		// `match` selected nothing in a range that WAS computed: a pass.
		run.Complete = true
		if _, err := ev.record(run); err != nil {
			return ev.fail(g, run, err)
		}
		return nil, FileGuardResult{}, false
	}
	if ev.verify {
		if hash, err = changeset.RuleHashAt(ev.root, g.Dir, g.Origin.FromPlugin()); err != nil {
			return ev.fail(g, run, err)
		}
		run.RuleHash = hash
	}
	// EFFECTIVE BASE (a10n's GetEffectiveBase): the head of the rule's latest complete passing
	// evaluation at this definition that is an ancestor of the head and a descendant of the
	// requested base. What passed once is not re-examined: only the change since it is. Computed
	// from the stored runs alone, so `verify` (CI too) finds the base `run` did; a FAIL never
	// advances it. Found only for a rule that selected something (verify hashes no other).
	if eff := ev.effectiveBase(g, hash, r); eff.Base != r.Base {
		r = eff
		run.BaseRef = r.Base
		run.BaseTree, run.HeadTree = rangeTrees(ev.root, r)
		if cs, err = build(r); err != nil {
			return ev.fail(g, run, err)
		}
		if len(cs.Files) == 0 {
			ev.note(CheckOutcome{Rule: rule, Subject: changeset.DefaultSubjectID, Kind: guardKind, Status: checkstore.StatusPass, Source: "stored",
				Reason: "nothing selected has changed since the last pass, at " + shortRev(r.Base)})
			run.Complete = true
			if _, err := ev.record(run); err != nil {
				return ev.fail(g, run, err)
			}
			return nil, FileGuardResult{}, false
		}
	}

	// Verify never consults the session: a citation counts when a commit trailer carries it
	// (what the author's `run`, where the transcript exists, resolved), and a verdict is keyed
	// by the quote, not by the record it was found in.
	var unresolved []changeset.Unresolved
	if ev.verify {
		TrustTrailers(&cs)
	} else {
		unresolved = ResolveCitations(&cs, ev.params.Transcript, ev.params.Cwd)
	}

	subjects := []changeset.Subject{changeset.Whole(cs)}
	trees := make([]*gitrepo.Snapshot, 0, 1)
	snapshot := func() (*gitrepo.Snapshot, error) {
		if ev.verify {
			if tree, err := ev.verifyTree(r.Head); tree != nil || err != nil {
				return tree, err
			}
		}
		ev.snapshots.Lock()
		defer ev.snapshots.Unlock()
		tree, err := gitrepo.AddSnapshot(ev.root, "", r.Head)
		if err == nil {
			trees = append(trees, tree)
		}
		return tree, err
	}
	dropAll := func() {
		for _, t := range trees {
			ev.dropTree(g, t, r.Head)
		}
	}
	var tree *gitrepo.Snapshot
	if !lean { // a lean (verify) rule runs nothing, so it needs no checkout
		if tree, err = snapshot(); err != nil {
			return ev.fail(g, run, err)
		}
	}
	if g.Subjects != "" {
		if subjects, err = ev.guardSubjects(g, r, cs, tree.Path); err != nil {
			dropAll()
			return ev.fail(g, run, err)
		}
	}

	rrs := make([]*ruleRun, 0, len(subjects))
	for i, sub := range subjects {
		if i > 0 && !lean {
			if tree, err = snapshot(); err != nil {
				dropAll()
				return ev.fail(g, run, err)
			}
		}
		treePath := ""
		if tree != nil {
			treePath = tree.Path
		}
		req := ev.requestFor(g, r, cs, sub, treePath, unresolved)
		// Recorded RUNNING, finished only once every check is stored: a run that dies half-way
		// never reads as a pass.
		subRun := run
		if g.Subjects != "" {
			subRun.Metadata = map[string]any{"eventKind": changeset.Kind, "subject": sub.ID}
		}
		runID, err := ev.record(subRun)
		if err != nil {
			dropAll()
			return ev.fail(g, run, err)
		}
		rrs = append(rrs, &ruleRun{g: g, hash: hash, req: req, payload: *req.Changeset, subject: sub, runID: runID, unresolved: unresolved, tree: tree, head: r.Head, base: r.Base, baseTree: run.BaseTree, headTree: run.HeadTree})
	}
	return rrs, FileGuardResult{}, false
}

// ruleRange is the stated range raised to the rule's floor (the parent of its last change)
// when that is later: the one range `run`, `verify` and `changeset` judge a rule over.
func (ev *changesetEvaluation) ruleRange(g declaration.FileGuard) (gitrepo.Range, error) {
	r := ev.rng
	if rel, err := filepath.Rel(ev.root, g.Dir); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return gitrepo.RaiseBaseToRuleFloor(ev.root, r, filepath.ToSlash(rel))
	}
	return r, nil
}

// maxEffectiveCandidates bounds how many stored passing evaluations are chained for the base.
const maxEffectiveCandidates = 200

// effectiveBase is r advanced to the rule's effective base: see prepare. It CHAINS the stored
// passing evaluations: one covers its own base..head, so it advances the base only when the
// base lies inside it (its base is an ancestor-or-equal of the base reached so far, the empty
// tree included) and its head is past that base and no later than the head being judged.
// Starting at the requested base, the furthest such head becomes the base, and so on until
// nothing advances: sequential passes B1..H1 then H1..H2 reach H2, while a pass over a narrow
// range B2..H with B2 after B1 leaves the span B1..B2 unjudged and so advances nothing.
func (ev *changesetEvaluation) effectiveBase(g declaration.FileGuard, hash string, r gitrepo.Range) gitrepo.Range {
	if ev.store == nil || hash == "" || g.Subjects != "" || ev.params.WholeRange {
		// A `subjects:` script names units whose verdicts depend on more than the diff (the
		// fingerprint it gives): a range narrowed to "what changed since" would never ask again.
		return r
	}
	runs, err := ev.store.EffectiveRuns(g.Qualified(), hash)
	if err != nil {
		return r // no history is read as none: the requested base
	}
	if len(runs) > maxEffectiveCandidates {
		runs = runs[:maxEffectiveCandidates]
	}
	anc := ev.isAncestor
	eb := r.Base
	for range runs {
		best := ""
		for _, c := range runs {
			if c.Head == eb || c.Head == best || !(c.Base == gitrepo.EmptyTree || anc(c.Base, eb)) {
				continue
			}
			if !anc(eb, c.Head) || !anc(c.Head, r.Head) {
				continue
			}
			if best == "" || anc(best, c.Head) {
				best = c.Head
			}
		}
		if best == "" {
			break
		}
		eb = best
	}
	if eb == r.Base {
		return r
	}
	return gitrepo.Range{Base: eb, Head: r.Head}
}

// isAncestor reports whether a is an ancestor-or-equal of b (the empty tree is everyone's),
// asking git once per pair for the whole evaluation.
func (ev *changesetEvaluation) isAncestor(a, b string) bool {
	if a == b || a == gitrepo.EmptyTree {
		return true
	}
	k := [2]string{a, b}
	ev.ancMu.Lock()
	ok, seen := ev.anc[k]
	ev.ancMu.Unlock()
	if seen {
		return ok
	}
	ok, err := gitrepo.IsAncestor(ev.root, a, b)
	ok = ok && err == nil
	ev.ancMu.Lock()
	if ev.anc == nil {
		ev.anc = map[[2]string]bool{}
	}
	ev.anc[k] = ok
	ev.ancMu.Unlock()
	return ok
}

// Shown is what `sr-checks changeset` prints for a rule: the range the engine would judge it
// over, and what each subject's checks would be handed.
type Shown struct {
	Range      gitrepo.Range
	RuleHash   string
	Unresolved []changeset.Unresolved
	// Subjects: one per `subjects:` entry, or the whole changeset when the rule has none.
	Subjects []ShownSubject
}

// ShownSubject is one subject and the payload its checks receive.
type ShownSubject struct {
	ID      string
	Payload changeset.Payload
}

// Show builds, for one rule and with no store, exactly what Evaluate would hand it: the
// same raised range, the same match and the same `subjects:` script (run here, as in `run`).
// Nothing is judged or recorded.
func Show(p Params, g declaration.FileGuard) (Shown, error) {
	ev := &changesetEvaluation{errw: io.Discard, diags: map[string]*bytes.Buffer{}, root: p.Root, params: p, store: p.Store, verify: p.Verify, rng: p.Range}
	r, err := ev.ruleRange(g)
	if err != nil {
		return Shown{}, fmt.Errorf("its range is not computable: %w", err)
	}
	out := Shown{Range: r}
	if out.RuleHash, err = changeset.RuleHashAt(p.Root, g.Dir, g.Origin.FromPlugin()); err != nil {
		return out, err
	}
	out.Range = ev.effectiveBase(g, out.RuleHash, r)
	r = out.Range
	match, err := guardrail.CompileFileMatch(g.Match)
	if err != nil {
		return out, fmt.Errorf("its match %q could not be compiled: %w", g.Match, err)
	}
	cs, err := changeset.Build(p.Root, r, changeset.Options{
		Deletions: changeset.DeletionMode(g.Deletions),
		Scan:      Markers,
		Select:    Selector(match),
	})
	if err != nil {
		return out, err
	}
	if p.Verify {
		TrustTrailers(&cs)
	} else {
		out.Unresolved = ResolveCitations(&cs, p.Transcript, p.Cwd)
	}
	subjects := []changeset.Subject{changeset.Whole(cs)}
	if g.Subjects != "" && len(cs.Files) > 0 {
		tree, err := gitrepo.AddSnapshot(p.Root, "", r.Head)
		if err != nil {
			return out, err
		}
		defer func() { _ = tree.Remove() }()
		if subjects, err = ev.guardSubjects(g, r, cs, tree.Path); err != nil {
			return out, err
		}
	}
	for _, sub := range subjects {
		out.Subjects = append(out.Subjects, ShownSubject{ID: sub.ID, Payload: changeset.NewPayload(cs, sub, p.Transcript)})
	}
	return out, nil
}

// requestFor is what a rule's checks are handed for one subject of the changeset.
func (ev *changesetEvaluation) requestFor(g declaration.FileGuard, r gitrepo.Range, cs changeset.Changeset, sub changeset.Subject, tree string, unresolved []changeset.Unresolved) dispatchcore.Request {
	payload := changeset.NewPayload(cs, sub, ev.params.Transcript)
	return dispatchcore.Request{
		Nature:         dispatchcore.NatureFileGuard,
		Event:          event.Event{Kind: changeset.Kind, Fields: map[string]any{grounding.FieldCitations: grounding.ToWire(changeset.Plain(cs.Citations))}},
		TranscriptPath: ev.params.Transcript,
		Subagent:       ev.params.Subagent,
		Dir:            g.Dir,
		GuardName:      g.Name,
		Qualified:      g.Qualified(),
		Workspace:      ev.params.Workspace,
		ProjectRoot:    tree,
		SessionID:      ev.params.SessionID,
		LaunchedBy:     AppendLaunchedBy(os.Getenv, g.Name),
		Changeset:      &payload,
		// SR_SESSION_START is a deprecated alias of SR_BASE, kept for rules written against it.
		Env: append(changeset.Env(tree, r.Base, r.Head), ev.agentEnv("SR_SESSION_START="+r.Base)...),
	}
}

// agentEnv appends SR_AGENT_ID, the sub-agent a run is made as, to env when there is one.
func (ev *changesetEvaluation) agentEnv(env ...string) []string {
	if ev.params.AgentID != "" {
		env = append(env, "SR_AGENT_ID="+ev.params.AgentID)
	}
	return env
}

// guardSubjects runs the rule's `subjects:` script: the changeset payload on stdin, no
// session (it must give the same list in `run` and in `verify`), the subjects as JSON on
// stdout. The citations it is handed are the commit trailers' quotes in both modes (what
// `verify` has), never the session-resolved ones, so a script cannot key the two differently.
func (ev *changesetEvaluation) guardSubjects(g declaration.FileGuard, r gitrepo.Range, cs changeset.Changeset, tree string) ([]changeset.Subject, error) {
	trusted := cs
	TrustTrailers(&trusted)
	req := ev.requestFor(g, r, trusted, changeset.Whole(trusted), tree, nil)
	out, v, err := ev.runner.RunSubjects(withoutSession(req), g.Subjects)
	if err != nil {
		return nil, fmt.Errorf("its subjects script %q could not run: %w", g.Subjects, err)
	}
	if v.Refused {
		return nil, fmt.Errorf("its subjects script %q failed: %s", g.Subjects, v.Reason)
	}
	subs, err := changeset.ParseSubjects(out, cs)
	if err != nil {
		return nil, fmt.Errorf("its subjects script %q: %w", g.Subjects, err)
	}
	return subs, nil
}

// fail is engineFailure in prepare's three-value shape.
func (ev *changesetEvaluation) fail(g declaration.FileGuard, run checkstore.CheckRun, err error) ([]*ruleRun, FileGuardResult, bool) {
	r, refused := ev.engineFailure(g, run, err)
	return nil, r, refused
}

// dropTree removes a rule's snapshot.
func (ev *changesetEvaluation) dropTree(g declaration.FileGuard, tree *gitrepo.Snapshot, head string) {
	if tree == nil {
		return
	}
	ev.sharedMu.Lock()
	shared := tree == ev.sharedTree
	ev.sharedMu.Unlock()
	if shared {
		return // released by Evaluate, once every rule is done
	}
	ev.snapshots.Lock()
	defer ev.snapshots.Unlock()
	if err := tree.Remove(); err != nil {
		fmt.Fprintf(ev.log(g), "sloprail: snapshot of %s not removed: %v\n", head, err)
	}
}

// runCheap runs a rule's requirements and the script checks before its first judge,
// settling the rule if it refused or has no judge to wait on.
func (ev *changesetEvaluation) runCheap(rr *ruleRun) {
	t := time.Now()
	defer func() { rr.cheap = time.Since(t) }()
	if v, err, settled := ev.lookup(rr); settled {
		ev.finish(rr, v, err)
		return
	}
	v, err := ev.runRequires(rr)
	if err == nil && !v.Refused {
		for rr.next < len(rr.g.Checks) && rr.g.Checks[rr.next].Script != "" {
			if v, err = ev.runCheck(rr, rr.next); err != nil || v.Refused {
				break
			}
			rr.next++
		}
	}
	if err != nil || v.Refused || rr.next >= len(rr.g.Checks) {
		if err == nil && v.Refused {
			ev.skipDeferred(rr)
		}
		ev.finish(rr, v, err)
	}
}

// runRest runs what is left of a rule: its judges, in declared order, and the checks
// after them, stopping at the first refusal.
func (ev *changesetEvaluation) runRest(rr *ruleRun) {
	t := time.Now()
	defer func() { rr.slow = time.Since(t) }()
	defer ev.releaseInflight(rr) // after finish: the verdict is stored before the key is let go
	if v, err, settled := ev.claimInflight(rr); settled {
		ev.finish(rr, v, err)
		return
	}
	var v dispatchcore.Verdict
	var err error
	for ; rr.next < len(rr.g.Checks); rr.next++ {
		if v, err = ev.runCheck(rr, rr.next); err != nil || v.Refused {
			break
		}
	}
	ev.finish(rr, v, err)
}

// skipDeferred records, for a rule that refused on a cheap check, each judge it did not
// reach as a skip with the reason, so a judge is never silently absent.
func (ev *changesetEvaluation) skipDeferred(rr *ruleRun) {
	for i := rr.next; i < len(rr.g.Checks); i++ {
		c := rr.g.Checks[i]
		if c.Judge == "" {
			continue
		}
		reason := "judge deferred: this rule refused on a cheap check (" + rr.g.Qualified() + "); fix that and the judge runs next"
		fmt.Fprintf(ev.log(rr.g), "sloprail: file-guard %s: judge %q not run; the rule's own cheap check refused first\n", rr.g.Attribution(), c.Judge)
		ev.note(CheckOutcome{Rule: rr.g.Qualified(), Subject: rr.subject.ID, Kind: checkKind(i, c), Status: "skipped", Source: "ran", Reason: reason})
		rec := checkstore.CheckRecord{Subject: rr.subject.ID, Kind: checkKind(i, c), Status: checkstore.StatusSkip,
			Metadata: map[string]any{"reasoning": reason, "model": c.Model}}
		if err := ev.recordCheck(rr.runID, rec); err != nil {
			fmt.Fprintln(ev.log(rr.g), "sloprail:", err)
		}
	}
}

// finish settles a rule: its run is finished, and its outcome is the verdict (or the engine
// error, which is a refusal and leaves the run unfinished).
func (ev *changesetEvaluation) finish(rr *ruleRun, verdict dispatchcore.Verdict, failed error) {
	g := rr.g
	defer func() {
		rr.settled = true
		n := ev.done.Add(1)
		if ev.locks != nil {
			status := "ok"
			if rr.refused {
				status = "refused"
			} else if rr.replayed {
				status = "ok (stored verdict)"
			}
			ev.progressf("sloprail: file-guard %s %s: %s (%d/%d settled, %s)", g.Attribution(), rr.subject.ID, status, n, ev.total.Load(), ev.elapsed())
		}
	}()
	ev.dropTree(rr.g, rr.tree, rr.head)
	if failed != nil {
		rr.result, rr.refused = refusal(g, namingFiles(failed.Error(), rr.payload.Changeset.Files)), true
		var down *judgesUnavailableError
		if errors.As(failed, &down) {
			rr.result.Unavailable = down.cause
		}
		return
	}
	if ev.store != nil && !ev.verify && rr.runID != "" {
		if err := ev.store.FinishRun(rr.runID); err != nil {
			rr.result, rr.refused = refusal(g, fmt.Sprintf("the file-guard %q could not finish recording its run (%v); refusing because a run that was not recorded cannot be trusted", g.Name, err)), true
			return
		}
	}
	// The verdict is stored only once the run is: a run that could not be finished is not trusted.
	ev.recordGuard(rr, verdict)
	if verdict.Refused {
		rr.result, rr.refused = refusal(g, namingFiles(verdict.Reason, rr.payload.Changeset.Files)), true
	}
}

// maxNamedFiles bounds the files a refusal lists when its check named none.
const maxNamedFiles = 10

// namingFiles is a rule's refusal, guaranteed to name the file(s) it is about: a check
// is free to word its reason as it likes, and one that names no file leaves the agent
// guessing which of its files or commits was refused (and blaming its own commits). When
// the reason already names a file of the changeset it is returned as it is; otherwise the
// files the rule judged are listed after it.
func namingFiles(reason string, files []changeset.File) string {
	if len(files) == 0 {
		return reason
	}
	paths := make([]string, 0, len(files))
	for _, f := range files {
		if strings.Contains(reason, f.Path) {
			return reason
		}
		paths = append(paths, f.Path)
	}
	more := ""
	if len(paths) > maxNamedFiles {
		more = fmt.Sprintf(" (and %d more)", len(paths)-maxNamedFiles)
		paths = paths[:maxNamedFiles]
	}
	return reason + "\nThe files this refusal is about: " + strings.Join(paths, ", ") + more
}

// runRequires runs a rule's `require` entries in order, recording each and stopping
// at the first refusal. The error is an engine failure to run one (already
// recorded as such): the caller refuses on it.
func (ev *changesetEvaluation) runRequires(rr *ruleRun) (dispatchcore.Verdict, error) {
	g := rr.g
	seen := map[string]int{}
	for _, p := range g.Require {
		kind := requireKind(p)
		if n := seen[kind]; n > 0 {
			kind += "#" + strconv.Itoa(n+1)
		}
		seen[requireKind(p)]++

		if p.Citation != nil && ev.needsSession(rr) {
			// The quotes of the trailers can only be grounded in the session's transcript: a
			// run without one cannot judge, and says so. Whatever it said is no verdict about
			// this key (a real session computes the same key), so nothing is stored.
			rr.volatile = true
			return dispatchcore.Verdict{Refused: true, Reason: needsSessionReason}, nil
		}
		v, err := ev.runRequirement(g, rr.req, p, kind, rr.payload, rr.runID, rr.unresolved)
		if err != nil {
			return dispatchcore.Verdict{}, err
		}
		if v.Refused {
			// A real refusal is a complete FAIL verdict and is stored; only "no session
			// available to judge" (above) is not.
			return v, nil
		}
	}
	return dispatchcore.Verdict{}, nil
}

const needsSessionReason = "needs a session to judge: this range carries citation trailers, which can only be grounded in the session's transcript, and this run has none (run `sr-checks run` from the agent's session)"

// needsSession says a citation requirement cannot be judged here: the range quotes
// citations in its trailers but this run has no session transcript to ground them in.
func (ev *changesetEvaluation) needsSession(rr *ruleRun) bool {
	if ev.verify || ev.params.Transcript != "" {
		return false
	}
	cs := rr.payload.Changeset
	TrustTrailers(&cs)
	return len(cs.Citations) > 0
}

// runRequirement evaluates one `require` entry for every subject of the changeset
// (changeset.Requirement: one selected file each, until `subjects:` supplies the
// list) and records one row per subject, keyed by its id. A subject's `when` runs
// on that subject's own payload — `subject.files` is what it is asked about, the
// whole changeset only its context — and the requirement applies only to the
// subjects whose `when` applies. A citation is read per subject: it must be quoted
// in the commit that last changed the subject's files, so a commit that cites one
// file does not ground another, and an uncited change on top of a cited one is
// refused. Every subject that fails is named.
func (ev *changesetEvaluation) runRequirement(g declaration.FileGuard, req dispatchcore.Request, p declaration.Prerequisite,
	kind string, whole changeset.Payload, runID string, unresolved []changeset.Unresolved) (dispatchcore.Verdict, error) {

	cs := whole.Changeset
	var failed, reasons []string
	for _, s := range changeset.Subjects(cs, changeset.Requirement) {
		if !slices.Contains(whole.Subject.Files, s.ID) {
			continue // another subject's file: its own verdict covers it
		}
		payload := changeset.NewPayload(cs, s, whole.TranscriptPath)
		one := req
		one.Changeset = &payload
		one.Require = []declaration.Prerequisite{p}
		one.Event = event.Event{Kind: changeset.Kind, Fields: map[string]any{
			"path":                   s.ID,
			grounding.FieldCitations: grounding.ToWire(changeset.Plain(cs.ForSubject(s))),
		}}
		v, err := ev.runner.CheckRequire(one)
		out := CheckOutcome{Rule: g.Qualified(), Subject: s.ID, Kind: kind, Status: checkstore.StatusPass, Source: "ran"}
		rec := checkstore.CheckRecord{Subject: s.ID, Kind: kind}
		switch {
		case err != nil:
			rec.Status, rec.Metadata = checkstore.StatusError, map[string]any{"reasoning": err.Error()}
			_ = ev.recordCheck(runID, rec) // already failing
			return dispatchcore.Verdict{}, engineError(g, err)
		case v.Refused:
			rec.Status, rec.Metadata = checkstore.StatusFail, map[string]any{"reasoning": v.Reason}
			out.Status, out.Reason = checkstore.StatusFail, v.Reason
		default:
			rec.Status = checkstore.StatusPass
		}
		if p.Citation != nil {
			rec.Items = citationItems(one.Event, unresolved)
			if v.Refused {
				rec.Metadata["reasoning"] = v.Reason + unresolvedNote(unresolved)
			}
		}
		if err := ev.recordCheck(runID, rec); err != nil {
			return dispatchcore.Verdict{}, engineError(g, err)
		}
		ev.note(out)
		if v.Refused {
			failed = append(failed, s.ID)
			if !slices.Contains(reasons, v.Reason) {
				reasons = append(reasons, v.Reason)
			}
		}
	}
	if len(failed) == 0 {
		return dispatchcore.Verdict{}, nil
	}
	// Each failed subject's own reason (what its `when` said about it), once.
	body := strings.Join(reasons, "\n\n")
	if p.Citation == nil {
		return dispatchcore.Verdict{Refused: true, Reason: "Not met for: " + strings.Join(failed, ", ") + ".\n" + body}, nil
	}
	reason := "a citation grounds only the commit it is in; an empty commit carrying only the trailer does not count. " +
		"Not grounded by a citation in the commit that last changed it: " + strings.Join(failed, ", ") + ".\n" +
		citeHowToFix(cs, failed, changeset.TrailerFor(p.Citation.Pools()), ev.amendSafe(), ev.recordedQuotes(failed, p.Citation.Pools())) + "\n" + body + unresolvedNote(unresolved)
	return dispatchcore.Verdict{Refused: true, Reason: reason}, nil
}

// citeHowToFix says, for the files a citation does not ground, how to ground them.
// A trailer grounds the commit it is on, so it has to be on the commit that last
// changed each file. In order:
//
//  1. RECOMMENDED: a follow-up commit that changes each file and carries the trailer.
//     The file has to change in that commit (an empty commit carrying only the
//     trailer grounds nothing), so when no change is needed the content is restated
//     through a cited `sr-file write`, or the file is touched minimally.
//  2. An amend of HEAD, offered ONLY when it is safe to rewrite: every file's last
//     commit is HEAD, HEAD is on no remote branch, and the working tree is clean.
//
// `git reset --soft` is never suggested (it rewrites the whole range, and a range that
// starts before the first commit has no commit to reset to). Undoing is `git revert`,
// never `git reset --hard`.
// Several quotes on one commit are fine.
func citeHowToFix(cs changeset.Changeset, files []string, trailer string, amendSafe bool, recorded []recordedQuote) string {
	var b strings.Builder
	b.WriteString("Last changed by:")
	allHead := true
	for _, path := range files {
		tip := ""
		for _, f := range cs.Files {
			if f.Path == path && len(f.Commits) > 0 {
				tip = f.Commits[len(f.Commits)-1]
				if len(f.Substantive) > 0 {
					tip = f.Substantive[len(f.Substantive)-1] // whitespace-only commits ground nothing
				}
			}
		}
		allHead = allHead && tip != "" && tip == cs.Head
		fmt.Fprintf(&b, "\n  %s: %s", path, describeCommit(cs, tip))
	}
	line := trailer + ": <exact quote>"
	if len(recorded) > 0 {
		// The agent already cited these files (sr-file --cite): hand back the
		// quote it found, as the exact trailer to paste, never a placeholder.
		line = recorded[0].Trailer + ": " + recorded[0].Quote
		b.WriteString("\nQuotes already recorded for these files this session (sr-file --cite), each the trailer line to paste into the commit message:")
		for _, r := range recorded {
			fmt.Fprintf(&b, "\n  %s: %s: %s", r.Path, r.Trailer, r.Quote)
		}
	}
	quoted := make([]string, len(files))
	for i, f := range files {
		quoted[i] = "'" + f + "'"
	}
	fmt.Fprintf(&b, "\nRecommended: ground them with a FOLLOW-UP commit that makes a REAL change to each file and carries the trailer. "+
		"Never wash a change through a whitespace-only or restated-content commit just to carry a citation: "+
		"if no real change is needed, amend your own unpushed commit with the trailer (below) or revert. Then:\n"+
		"  git add %s && git commit -m '<what changed>' -m %s", strings.Join(quoted, " "), shellQuote(line))
	if allHead && amendSafe {
		fmt.Fprintf(&b, amendOfferStart+"\n"+
			"  git commit --amend --no-edit --trailer %s", shellQuote(line))
	}
	// Undoing the whole range is one command, and it needs no citation: the tree is
	// then as it was at the base, so there is nothing to ground.
	fmt.Fprintf(&b, undoOfferStart+" instead (the files are then as they were, so nothing is left to ground):\n"+
		"  git revert --no-commit %s..HEAD && git commit --no-edit\n"+
		"Never `git reset --hard`, which destroys work.", cs.Base)
	return b.String()
}

const (
	amendOfferStart = "\nOr, since HEAD is the commit that changed them, is not pushed, and the tree is clean, amend it:"
	undoOfferStart  = "\nTo undo the whole range"
)

// currentAdvice is a stored refusal's text with the one piece of advice that depends on the
// repository NOW rather than on the key's input: the offer to amend HEAD is dropped from a
// replay when HEAD has been pushed (or the tree is dirty) since the refusal was stored.
func (ev *changesetEvaluation) currentAdvice(reason string) string {
	start := strings.Index(reason, amendOfferStart)
	if start < 0 || ev.amendSafe() {
		return reason
	}
	end := strings.Index(reason[start+1:], undoOfferStart)
	if end < 0 {
		return reason
	}
	return reason[:start] + reason[start+1+end:]
}

// amendSafe is whether rewriting HEAD is safe: it is on no remote branch and the
// working tree is clean (an amend would sweep in staged work). Anything that
// cannot be established reads as not safe: the amend is only ever an offer.
func (ev *changesetEvaluation) amendSafe() bool {
	if head, err := gitrepo.Head(ev.root); err != nil || head.Commit != ev.rng.Head {
		return false // HEAD is not the commit being judged
	}
	pushed, err := gitrepo.HeadPushed(ev.root)
	if err != nil || pushed {
		return false
	}
	dirty, err := gitrepo.UncommittedChanges(ev.root)
	return err == nil && len(dirty) == 0
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func describeCommit(cs changeset.Changeset, sha string) string {
	if sha == "" {
		return "no commit of the range"
	}
	for _, c := range cs.Commits {
		if c.SHA == sha {
			where := "an earlier commit"
			if sha == cs.Head {
				where = "HEAD"
			}
			return fmt.Sprintf("%s (%s) %q", short(sha), where, c.Subject)
		}
	}
	return short(sha)
}

// runCheck runs one check of a rule and records it, after its prepare (run up front, see
// prepareAll). Whether it needs running at all was settled by the guard's key (see lookup):
// a hit never gets here, and a step an interrupted run already passed is carried over.
func (ev *changesetEvaluation) runCheck(rr *ruleRun, i int) (dispatchcore.Verdict, error) {
	g, req, runID, c := rr.g, rr.req, rr.runID, rr.g.Checks[i]
	rec := checkstore.CheckRecord{Subject: rr.subject.ID, Kind: checkKind(i, c)}
	out := CheckOutcome{Rule: g.Qualified(), Subject: rec.Subject, Kind: rec.Kind, Source: "ran"}
	fail := func(err error) (dispatchcore.Verdict, error) {
		rec.Status, rec.Metadata = checkstore.StatusError, map[string]any{"reasoning": err.Error()}
		_ = ev.recordCheck(runID, rec) // already failing
		return dispatchcore.Verdict{}, engineError(g, err)
	}
	settle := func(v dispatchcore.Verdict, meta map[string]any) (dispatchcore.Verdict, error) {
		rec.Status = checkstore.StatusPass
		if meta == nil {
			meta = map[string]any{}
		}
		// Every verdict keeps its reasoning, a pass included: what the judge said is the
		// record of why the range was let through.
		if v.Refused {
			rec.Status = checkstore.StatusFail
		}
		if v.Refused || v.Reason != "" {
			meta["reasoning"] = v.Reason
		}
		rec.Metadata = meta
		if err := ev.recordCheck(runID, rec); err != nil {
			return dispatchcore.Verdict{}, engineError(g, err)
		}
		out.Status, out.Reason = rec.Status, ""
		if v.Refused {
			out.Reason = v.Reason
		}
		ev.note(out)
		return v, nil
	}

	// prepare (a judge's or a script's) adds its additionalContext to what the check is given.
	prep, pv, err := ev.runner.PrepareJudge(req, c)
	if err != nil {
		return fail(err)
	}
	if pv.Refused && pv.NoVerdict {
		// prepare could not run: an engine-side failure, recorded as an error, never as a verdict.
		rec.Status, rec.Metadata = checkstore.StatusError, map[string]any{"reasoning": pv.Reason, noVerdictMeta: true}
		_ = ev.recordCheck(runID, rec) // already failing
		return dispatchcore.Verdict{}, engineError(g, errors.New(pv.Reason))
	}
	if pv.Refused {
		// A refusal of prepare is the check's verdict (fail-closed): stored and replayed like
		// any other, so the Stop's verify reads it instead of "not judged yet".
		return settle(pv, map[string]any{"model": c.Model})
	}
	if prep.Skip {
		rec.Status, rec.Metadata = checkstore.StatusSkip, map[string]any{"reasoning": "prepare asked to skip the check", "model": c.Model}
		if err := ev.recordCheck(runID, rec); err != nil {
			return dispatchcore.Verdict{}, engineError(g, err)
		}
		out.Status, out.Reason = "skipped", "prepare asked to skip the check"
		ev.note(out)
		return dispatchcore.Verdict{}, nil
	}
	var v dispatchcore.Verdict
	meta := map[string]any{}
	if c.Script != "" {
		v, err = ev.runner.RunScript(req, c, prep)
	} else {
		meta["model"] = c.Model
		v, err = ev.judgeInSlot(rr, c, func() (dispatchcore.Verdict, error) { return ev.runner.Judge(req, c, prep) })
	}
	if err != nil {
		return fail(err)
	}
	if returnedNoVerdict(v) {
		// The judge never produced a verdict (it answered nothing parseable): an engine-side
		// failure, not a FAIL verdict on the content. It is recorded as an error, never as a
		// verdict under the key, so `verify` says "not judged yet" and the next `run` asks again.
		rec.Status, rec.Metadata = checkstore.StatusError, map[string]any{"reasoning": v.Reason, noVerdictMeta: true}
		_ = ev.recordCheck(runID, rec) // already failing
		failed := engineError(g, errors.New(v.Reason))
		if v.Unavailable != "" {
			failed = &judgesUnavailableError{cause: v.Unavailable, err: failed}
		}
		return dispatchcore.Verdict{}, failed
	}
	return settle(v, meta)
}

// incompleteReason is why the latest run left the rule's check over this subject without a
// verdict, when it was the judge returning none ("" otherwise). The run is left incomplete, so
// its row reads error or interrupted, never a verdict.
func (ev *changesetEvaluation) incompleteReason(rr *ruleRun) string {
	rows, err := ev.checkStatus(rr.g.Qualified())
	if err != nil {
		return ""
	}
	var at, why string
	for _, r := range rows {
		reason, _ := r.Metadata["reasoning"].(string)
		if r.Subject == rr.subject.ID && r.Status != checkstore.StatusPass && r.Status != checkstore.StatusFail && r.Metadata[noVerdictMeta] == true && r.RunAt >= at {
			who := "the judge"
			if !strings.Contains(r.Kind, ":judge:") {
				who = "the check" // a script, or a prepare that could not run
			}
			at, why = r.RunAt, who+" returned no verdict: "+reason
		}
	}
	return why
}

// noVerdictMeta marks a stored row as the judge having returned no verdict.
const noVerdictMeta = "no_verdict"

// returnedNoVerdict says a check was refused for want of an answer, not for a verdict on the
// content: a judge that answered nothing parseable, a script that could not run or said it
// errored. A script's own refusal is its verdict.
func returnedNoVerdict(v dispatchcore.Verdict) bool {
	return v.Refused && v.NoVerdict
}

// stepKey names a step of a guard: its subject and its kind.
func stepKey(subject, kind string) string { return subject + "\x00" + kind }

// guardKind is the one stored verdict of a guard over a subject: the fixed step id its
// cache key is under, whatever steps the guard has.
const guardKind = "guard"

// withoutSession is a request stripped of the session: no transcript, no session id, the
// payload's transcript path blank.
func withoutSession(req dispatchcore.Request) dispatchcore.Request {
	req.TranscriptPath, req.SessionID = "", ""
	if req.Changeset != nil {
		pl := *req.Changeset
		pl.TranscriptPath = ""
		req.Changeset = &pl
	}
	return req
}

// guardKey is the fingerprint a guard's verdict over its subject is kept under. The rule
// hash (part of the cache key) already covers every script and template of the rule folder
// and the subject id is the key's own; this adds what the verdict is about: the content of
// the subject's files, its fingerprint from the `subjects:` script (when it gave one), and,
// the quotes of the range's citations (and, for a rule that requires one, which ground the subject). No commit
// SHA, branch, session or snapshot path is in it, so two branches with identical content share
// their verdicts and the run that stored one and the verify that reads it compute one key.
func guardKey(g declaration.FileGuard, payload changeset.Payload) (string, error) {
	// The key is over the trailers' quotes as `verify` reads them (trusted, each in its own
	// pool), never over how a transcript resolved them: a quote `run` could not resolve
	// (ambiguous, or said in no session) is still a quote of the range, and a key that left it
	// out would never be the one `verify` computes. Every check can read the range's
	// citations, so they are in every guard's key, not only a `require: citation` one's.
	trusted := payload
	cs := trusted.Changeset
	TrustTrailers(&cs)
	trusted.Changeset = cs
	citations := changeset.RangeCitationPart(cs)
	for _, r := range g.Require {
		if r.Citation != nil {
			part, err := changeset.CitationPart(trusted)
			if err != nil {
				return "", err
			}
			citations += part
			break
		}
	}
	return changeset.GuardFingerprint(changeset.FilesPart(payload), payload.Subject.Fingerprint, citations), nil
}

// stepRow is one step of a guard's stored verdict.
type stepRow struct {
	Subject string `json:"subject"`
	Kind    string `json:"kind"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
}

// stepsOf are the steps this evaluation ran (or carried) for a guard over its subject, in
// order: the guard's own steps (keyed by the subject's id) and its requirements' (keyed by a
// file of the subject). Another subject's steps of the same rule are not this verdict's.
func (ev *changesetEvaluation) stepsOf(rr *ruleRun) []stepRow {
	rule := rr.g.Qualified()
	ev.mu.Lock()
	defer ev.mu.Unlock()
	var rows []stepRow
	for _, o := range ev.outcomes {
		if o.Rule == rule && o.Status != "missing" && (o.Subject == rr.subject.ID || slices.Contains(rr.subject.Files, o.Subject)) {
			rows = append(rows, stepRow{Subject: o.Subject, Kind: o.Kind, Status: o.Status, Reason: o.Reason})
		}
	}
	return rows
}

// stepStatus maps an outcome's status to the store's.
func stepStatus(s string) string {
	switch s {
	case checkstore.StatusPass, checkstore.StatusFail:
		return s
	}
	return checkstore.StatusSkip
}

// lookup keys a rule's guard over its subject and asks the store.
//
//   - `verify` only ever reads: a stored pass or fail is the verdict, anything else is red,
//     "not judged yet". Nothing is executed.
//   - `run` replays a stored pass or fail without running the guard (a fail is terminal until
//     the input changes); on a miss it runs the steps in order and stores the verdict. An
//     engine error stores nothing: the next run starts again from the first step.
//
// settled says the rule's verdict is v (and err an engine failure).
func (ev *changesetEvaluation) lookup(rr *ruleRun) (v dispatchcore.Verdict, err error, settled bool) {
	g := rr.g
	rr.key, err = guardKey(g, rr.payload)
	if err != nil {
		return dispatchcore.Verdict{}, engineError(g, err), true
	}
	missing := func(why string) (dispatchcore.Verdict, error, bool) {
		o := CheckOutcome{Rule: g.Qualified(), Subject: rr.subject.ID, Kind: guardKind, Status: "missing", Source: "stored", Reason: why}
		ev.note(o)
		return dispatchcore.Verdict{Refused: true, Reason: why}, nil, true
	}
	if ev.store == nil {
		if ev.verify {
			return missing("no results are kept here; run `sr-checks run`")
		}
		return dispatchcore.Verdict{}, nil, false
	}
	cached, have, err := ev.store.CachedCheck(g.Qualified(), rr.hash, rr.subject.ID, guardKind, rr.key)
	if err != nil {
		return dispatchcore.Verdict{}, engineError(g, err), true
	}
	reused := ""
	if ev.verify && !have {
		// A squash merge carries the judged branch's net change in a commit of another message
		// (so another citation key): the same two trees are the same change, judged already.
		if byTrees, ok, err := ev.store.CachedByTrees(g.Qualified(), rr.hash, rr.subject.ID, guardKind, rr.baseTree, rr.headTree); err != nil {
			return dispatchcore.Verdict{}, engineError(g, err), true
		} else if ok {
			cached, have = byTrees, true
			reused = fmt.Sprintf("stored, same trees as %s..%s", shortRev(byTrees.Run.BaseRef), shortRev(byTrees.Run.HeadRef))
		}
	}
	if ev.verify && !have {
		why := ""
		if !ev.params.FailuresOnly {
			if inc := ev.incompleteReason(rr); inc != "" {
				why = " (" + inc + ")"
			}
		}
		return missing(fmt.Sprintf("not judged yet%s — run `sr-checks run --base %s --head %s` in %s", why, ev.rng.Base, ev.rng.Head, ev.root))
	}
	if !have {
		return dispatchcore.Verdict{}, nil, false
	}
	if !ev.verify && cached.Status == checkstore.StatusFail {
		if len(rr.unresolved) == 0 && marked(cached.Metadata["unresolvedCitations"]) {
			// The stored refusal was "these quotes are not in the session"; they all resolve
			// now, so it is no longer the verdict on this key: judge again.
			return dispatchcore.Verdict{}, nil, false
		}
		if cached.Run.HeadRef != rr.head && onlyCitationFailed(storedSteps(cached.Metadata)) {
			// A citation requirement is cheap, and its reason names the commits of the range it
			// was judged in (possibly another branch's, with the same content and quotes):
			// from another head it is judged again so the reason is about THIS range.
			return dispatchcore.Verdict{}, nil, false
		}
	}
	steps := storedSteps(cached.Metadata)
	reasoning, _ := cached.Metadata["reasoning"].(string)
	reasoning = ev.currentAdvice(reasoning)
	verdict := dispatchcore.Verdict{Refused: cached.Status == checkstore.StatusFail, Reason: reasoning}
	for _, st := range steps {
		src := "cached"
		if ev.verify && st.Status == checkstore.StatusFail {
			src = "stored"
		}
		if reused != "" {
			src = reused
		}
		o := CheckOutcome{Rule: g.Qualified(), Subject: st.Subject, Kind: st.Kind, Status: st.Status, Source: src, Reason: ev.currentAdvice(st.Reason)}
		ev.note(o)
		if !ev.verify {
			rec := checkstore.CheckRecord{Subject: st.Subject, Kind: st.Kind, Status: stepStatus(st.Status), Metadata: map[string]any{"replayed": true}}
			if st.Reason != "" {
				rec.Metadata["reasoning"] = st.Reason
			}
			if err := ev.recordCheck(rr.runID, rec); err != nil {
				return dispatchcore.Verdict{}, engineError(g, err), true
			}
		}
	}
	if !ev.verify {
		meta := map[string]any{"replayed": true}
		for k, val := range cached.Metadata {
			meta[k] = val
		}
		rec := checkstore.CheckRecord{Subject: rr.subject.ID, Kind: guardKind, Status: cached.Status, Fingerprint: rr.key, Metadata: meta}
		if err := ev.recordCheck(rr.runID, rec); err != nil {
			return dispatchcore.Verdict{}, engineError(g, err), true
		}
	}
	rr.replayed = true
	return verdict, nil, true
}

// storedSteps reads a guard verdict's steps back out of its metadata.
func storedSteps(meta map[string]any) []stepRow {
	body, err := json.Marshal(meta["steps"])
	if err != nil {
		return nil
	}
	var rows []stepRow
	_ = json.Unmarshal(body, &rows)
	return rows
}

// recordGuard stores the guard's verdict over its subject, with each step inside it. An
// engine error never reaches here (it is no verdict), and a refusal reached without a session is not stored.
func (ev *changesetEvaluation) recordGuard(rr *ruleRun, verdict dispatchcore.Verdict) {
	if ev.store == nil || ev.verify || rr.runID == "" || rr.key == "" || rr.replayed {
		return
	}
	meta := map[string]any{"steps": ev.stepsOf(rr)}
	rec := checkstore.CheckRecord{Subject: rr.subject.ID, Kind: guardKind, Fingerprint: rr.key, Metadata: meta}
	switch {
	case verdict.Refused:
		// A refusal reached without a session is no verdict about the key: any check may read
		// the transcript, which this run does not have, so the author's real `run` must judge
		// fresh. Passes are stored (a pass without the transcript holds for every session).
		if rr.volatile || ev.params.Transcript == "" {
			// Still a refusal of THIS run: its run holds a failing row (under no fingerprint, so
			// no lookup finds it), or the run would read as a pass and advance the effective base.
			rec.Kind, rec.Status, rec.Fingerprint, meta["reasoning"] = guardKind+":unstored", checkstore.StatusFail, "", verdict.Reason
			if err := ev.recordCheck(rr.runID, rec); err != nil {
				fmt.Fprintln(ev.log(rr.g), "sloprail:", err)
			}
			return
		}
		rec.Status, meta["reasoning"] = checkstore.StatusFail, verdict.Reason
		if len(rr.unresolved) > 0 && onlyCitationFailed(meta["steps"].([]stepRow)) {
			// The key is over the quotes, not over how the session resolved them: say that
			// this refusal is the citation requirement's, resting on quotes the session did
			// not hold, so `run` asks again once they resolve (a tool printed them since). A
			// content judge's or script's refusal is never marked: it is replayed as is.
			meta["unresolvedCitations"] = len(rr.unresolved)
		}
	default:
		rec.Status = checkstore.StatusPass
		if verdict.Reason != "" {
			meta["reasoning"] = verdict.Reason
		}
	}
	if err := ev.recordCheck(rr.runID, rec); err != nil {
		fmt.Fprintln(ev.log(rr.g), "sloprail:", err)
	}
}

// marked says a stored count is positive, whether the store kept it as an int or read it back as JSON.
func marked(v any) bool {
	switch n := v.(type) {
	case int:
		return n > 0
	case float64:
		return n > 0
	}
	return false
}

// onlyCitationFailed says every failed step of a verdict is a citation requirement's.
func onlyCitationFailed(steps []stepRow) bool {
	any := false
	for _, st := range steps {
		if st.Status != checkstore.StatusFail {
			continue
		}
		if !strings.HasPrefix(st.Kind, "require:citation") {
			return false
		}
		any = true
	}
	return any
}

// judgesUnavailableError is an engine failure whose cause is the judge substrate being down
// (the harness behind sr-agent died with a named cause), not anything about the work judged.
type judgesUnavailableError struct {
	cause string
	err   error
}

func (e *judgesUnavailableError) Error() string { return e.err.Error() }
func (e *judgesUnavailableError) Unwrap() error { return e.err }

// collapseUnavailable replaces the refusals that are one judge outage with a single refusal
// naming the cause, the number of checks it left undecided and which rules: N identical "could
// not be evaluated" refusals read as N verdicts, and the one real finding hides among them. It
// still refuses: a guard that could not decide must not be read as approval. The other refusals
// keep their order and the outage goes last.
func collapseUnavailable(refusals []FileGuardResult) []FileGuardResult {
	var kept, down []FileGuardResult
	for _, r := range refusals {
		if r.Unavailable != "" {
			down = append(down, r)
		} else {
			kept = append(kept, r)
		}
	}
	if len(down) == 0 {
		return refusals
	}
	var causes, names []string
	seenCause, seenName := map[string]bool{}, map[string]bool{}
	for _, r := range down {
		if !seenCause[r.Unavailable] {
			seenCause[r.Unavailable] = true
			causes = append(causes, r.Unavailable)
		}
		if !seenName[r.Name] {
			seenName[r.Name] = true
			names = append(names, r.Name)
		}
	}
	reason := fmt.Sprintf("judges unavailable: %d rules not evaluated: %s. Not decided: %s. "+
		"This is no verdict on the work: no judge ran. Run it again once the judges are available "+
		"(refusing because a guard that could not decide must not be read as approval).\nFirst failure: %s",
		len(down), strings.Join(causes, ", "), strings.Join(names, ", "), firstLine(down[0].Reason))
	return append(kept, FileGuardResult{Name: "judges-unavailable", Attribution: "sloprail", Refused: true, Reason: reason, Unavailable: causes[0]})
}

// firstLine is a text's first line.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// engineError is the refusal for something that went wrong in the engine while a
// rule was being evaluated: a guard that could not decide must not read as approval.
func engineError(g declaration.FileGuard, err error) error {
	return fmt.Errorf("the file-guard %q could not be evaluated (%v); refusing because a guard that could not decide must not be read as approval", g.Name, err)
}

func requireKind(p declaration.Prerequisite) string {
	switch {
	case p.Citation != nil:
		return "require:citation"
	case p.Skill != "":
		return "require:skill:" + p.Skill
	case p.Context != "":
		return "require:context:" + p.Context
	}
	return "require"
}

func checkKind(i int, c declaration.Check) string {
	if c.Script != "" {
		return fmt.Sprintf("check[%d]:script:%s", i, c.Script)
	}
	return fmt.Sprintf("check[%d]:judge:%s", i, c.Judge)
}

// citationItems records what the citation prerequisite saw: each citation that resolved,
// and each trailer that did not.
func citationItems(e event.Event, unresolved []changeset.Unresolved) []checkstore.CheckItem {
	var items []checkstore.CheckItem
	for _, c := range grounding.FromWire(e.Fields[grounding.FieldCitations]) {
		items = append(items, checkstore.CheckItem{Key: c.Quote, Passed: true,
			Metadata: map[string]any{"path": c.Path, "line": c.Line, "sourceTypes": c.SourceTypes}})
	}
	for _, u := range unresolved {
		items = append(items, checkstore.CheckItem{Key: u.Quote, Passed: false,
			Metadata: map[string]any{"trailer": u.Trailer, "commit": u.Commit, "error": u.Err.Error()}})
	}
	return items
}

// unresolvedNote says which citation trailers did not resolve, so an agent that
// cited something the session never said hears why it does not count.
func unresolvedNote(unresolved []changeset.Unresolved) string {
	if len(unresolved) == 0 {
		return ""
	}
	note := "\nThese trailers in the range did not resolve to a citation:"
	for _, u := range unresolved {
		note += "\n  - " + u.String()
	}
	return note
}

// log is where a rule's diagnostics go: its own buffer during a concurrent
// evaluation (flushed in declaration order), the command's stderr when a single rule
// is evaluated on its own.
func (ev *changesetEvaluation) log(g declaration.FileGuard) io.Writer {
	if b, ok := ev.diags[g.Qualified()]; ok {
		return b
	}
	return ev.errw
}

// evaluate runs one rule on its own, start to finish: its cheap checks, then its
// judges. The bool is whether it refused.
func (ev *changesetEvaluation) evaluate(g declaration.FileGuard) (FileGuardResult, bool) {
	rrs, r, refused := ev.prepare(g)
	if len(rrs) == 0 {
		return r, refused
	}
	var last FileGuardResult
	any := false
	for _, rr := range rrs {
		if ev.runCheap(rr); !rr.settled {
			ev.runRest(rr)
		}
		if rr.refused {
			last, any = rr.result, true
		}
	}
	return last, any
}

// BrokenFileGuards names every file-guard that failed to load, with why: a rule that cannot be
// read judges nothing, so a run (or a Stop) that passes over it must fail instead of reading as clean.
func BrokenFileGuards(l declaration.Loaded) []string {
	var out []string
	for _, iv := range l.Invalid {
		if iv.Nature == declaration.NatureFileGuard {
			out = append(out, "file-guard "+iv.Attribution()+" could not be loaded: "+iv.Reason)
		}
	}
	return out
}

// requiresCitation says whether a rule has a citation requirement.
func requiresCitation(g declaration.FileGuard) bool {
	for _, r := range g.Require {
		if r.Citation != nil {
			return true
		}
	}
	return false
}

// rangeTrees are the tree ids of a range's base and head, "" when either cannot be read (such
// a run is never matched by trees).
func rangeTrees(root string, r gitrepo.Range) (base, head string) {
	b, err1 := gitrepo.TreeOf(root, r.Base)
	h, err2 := gitrepo.TreeOf(root, r.Head)
	if err1 != nil || err2 != nil {
		return "", ""
	}
	return b, h
}

func shortRev(rev string) string {
	if len(rev) > 8 {
		return rev[:8]
	}
	return rev
}

// lockedWriter serialises writes to a shared stderr.
type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (l lockedWriter) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(b)
}

func (ev *changesetEvaluation) progressf(format string, a ...any) {
	ev.errMu.Lock()
	defer ev.errMu.Unlock()
	fmt.Fprintf(ev.errw, format+"\n", a...)
}

func (ev *changesetEvaluation) elapsed() time.Duration {
	return time.Since(ev.started).Round(time.Second)
}

// heartbeatEvery is how often a long `run` says it is alive.
const heartbeatEvery = 30 * time.Second

// heartbeat says, every 30s until stop closes, that the run is alive and how far it has got, so
// a long run is never silent and nobody has to poll the process list to know.
func (ev *changesetEvaluation) heartbeat(stop <-chan struct{}) {
	t := time.NewTicker(heartbeatEvery)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			ev.progressf("sloprail: sr-checks run still working: %d/%d checks settled, %d judging (%s elapsed)",
				ev.done.Load(), ev.total.Load(), ev.busy.Load(), ev.elapsed())
		}
	}
}

// judgeInSlot runs one judge once the machine has a free judge slot (shared by every
// parallel `sr-checks run`); it waits for one and never fails for want of it.
func (ev *changesetEvaluation) judgeInSlot(rr *ruleRun, c declaration.Check, judge func() (dispatchcore.Verdict, error)) (dispatchcore.Verdict, error) {
	if ev.locks == nil {
		return judge()
	}
	release, err := ev.locks.AcquireSlot()
	if err != nil {
		fmt.Fprintln(ev.log(rr.g), "sloprail: judge slots unavailable, judging without a limit:", err)
		return judge()
	}
	defer release()
	ev.busy.Add(1)
	defer ev.busy.Add(-1)
	t := time.Now()
	ev.progressf("sloprail: judging %s %s with %q", rr.g.Attribution(), rr.subject.ID, c.Judge)
	defer func() {
		ev.progressf("sloprail: judged %s %s in %s", rr.g.Attribution(), rr.subject.ID, time.Since(t).Round(time.Second))
	}()
	return judge()
}

// claimInflight takes the machine-wide in-flight lock of the rule's verdict key before its
// judges run. When another process held it, that process has judged this very key meanwhile:
// the cache is read again and a stored verdict settles the rule (settled), else it judges.
func (ev *changesetEvaluation) claimInflight(rr *ruleRun) (v dispatchcore.Verdict, err error, settled bool) {
	if ev.locks == nil || ev.store == nil || ev.verify || rr.key == "" {
		return dispatchcore.Verdict{}, nil, false
	}
	rr.lockKey = judgelimit.Name(ev.identity.RepoID, rr.g.Qualified(), rr.hash, rr.subject.ID, rr.key)
	release, waited, lerr := ev.locks.AcquireInflight(rr.lockKey, "another run judging the same check ("+rr.g.Attribution()+" "+rr.subject.ID+")")
	if lerr != nil {
		fmt.Fprintln(ev.log(rr.g), "sloprail: in-flight lock unavailable, judging anyway:", lerr)
		return dispatchcore.Verdict{}, nil, false
	}
	rr.unlockInflight = release
	if !waited {
		return dispatchcore.Verdict{}, nil, false
	}
	return ev.lookup(rr)
}

// releaseInflight stores the verdict for a waiting process (when one left its marker), then
// lets the key go.
func (ev *changesetEvaluation) releaseInflight(rr *ruleRun) {
	if rr.unlockInflight == nil {
		return
	}
	if ev.locks.Contended(rr.lockKey) {
		if err := ev.store.FlushRun(rr.runID); err != nil {
			fmt.Fprintln(ev.log(rr.g), "sloprail: the verdict could not be stored for the run waiting on it:", err)
		}
		ev.locks.ClearContended(rr.lockKey)
	}
	rr.unlockInflight()
	rr.unlockInflight = nil
}
