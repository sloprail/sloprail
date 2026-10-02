package checkrun

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sloprail/sloprail/internal/changeset"
	"github.com/sloprail/sloprail/internal/checkstore"
	"github.com/sloprail/sloprail/internal/declaration"
	dispatchcore "github.com/sloprail/sloprail/internal/dispatch"
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/grounding"
	"github.com/sloprail/sloprail/internal/guardrail"
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
//   - An engine error is no verdict: nothing is stored, and the next `run` starts again. A
//     refusal that read the session (a skill or a context requirement, a prepare that
//     refused) is not stored either: it is asked again once the agent has done what it asks.
//   - Anything that goes wrong in the engine — git, the rule's own folder, the
//     snapshot — fails CLOSED and refuses. A range that could not be read is never an
//     empty one; a range where `match` selects nothing is a pass.
//
// Checks run against a read-only snapshot of head (SR_TREE), never the working
// tree, with SR_BASE and SR_HEAD naming the range.

// Params is everything one evaluation of the file-guards is given. Nothing here knows about a
// session's store or a hook: the caller states the range and where the transcript is.
type Params struct {
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
	// Recorded is the citations the session already recorded per file (sr-file --cite),
	// oldest first; a citation refusal hands them back as the trailer to paste.
	Recorded map[string][]transcript.Citation
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

// defaultStopConcurrency is 6, not GOMAXPROCS: a judge waits on a model, not a CPU.
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
	runID      string
	next       int // index of the first check not yet run

	// key is the guard's verdict key over its subject ("" when it cannot be keyed).
	key string
	// volatile: a refusal this evaluation reached read the session, so it is not stored.
	volatile bool
	replayed bool // the verdict is a stored one, already recorded

	cheap, slow time.Duration // time spent before / at the first judge
	result      FileGuardResult
	refused     bool
	settled     bool // result is final
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
			skipped[i] = &ruleRun{g: g, result: r, refused: refused, settled: true}
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
	for _, o := range out {
		if o != nil && o.tree != nil {
			fmt.Fprintf(ev.log(o.g), "sloprail: file-guard %s: cheap checks %s, judges %s\n", o.g.Attribution(),
				o.cheap.Round(time.Millisecond), o.slow.Round(time.Millisecond))
		}
	}
	// Each rule's diagnostics, in declaration order: concurrent rules must not make
	// the log's order depend on who finished first.
	for _, g := range guards {
		if b := ev.diags[g.Qualified()]; b != nil {
			errw.Write(b.Bytes())
		}
	}
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

// runIdentity fills the provenance columns: the repository (its root commit), the branch and
// the session. Best effort — an unreadable one is left empty rather than costing the run.
func (ev *changesetEvaluation) runIdentity() checkstore.RunIdentity {
	id := checkstore.RunIdentity{SessionID: ev.params.SessionID, AgentID: ev.params.AgentID}
	if root, err := gitrepo.RootCommit(ev.root); err == nil {
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
	r := ev.rng
	if rel, err := filepath.Rel(ev.root, g.Dir); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		raised, err := gitrepo.RaiseBaseToRuleFloor(ev.root, r, filepath.ToSlash(rel))
		if err != nil {
			return ev.fail(g, checkstore.CheckRun{CheckID: rule, BaseRef: r.Base, HeadRef: r.Head, Metadata: map[string]any{"eventKind": changeset.Kind}}, fmt.Errorf("its range is not computable: %w", err))
		}
		r = raised
	}
	run := checkstore.CheckRun{CheckID: rule, BaseRef: r.Base, HeadRef: r.Head, Metadata: map[string]any{"eventKind": changeset.Kind}}

	hash, err := changeset.RuleHash(g.Root())
	if err != nil {
		return ev.fail(g, run, err)
	}
	run.RuleHash = hash
	match, err := guardrail.CompileFileMatch(g.Match)
	if err != nil {
		return ev.fail(g, run, fmt.Errorf("its match %q could not be compiled: %w", g.Match, err))
	}
	cs, err := changeset.Build(ev.root, r, changeset.Options{
		Deletions: changeset.DeletionMode(g.Deletions),
		Scan:      Markers,
		Select:    Selector(match),
	})
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
	tree, err := snapshot()
	if err != nil {
		return ev.fail(g, run, err)
	}
	base := ev.requestFor(g, r, cs, changeset.Whole(cs), tree.Path, unresolved)
	if g.Subjects != "" {
		if subjects, err = ev.guardSubjects(g, cs, base); err != nil {
			dropAll()
			return ev.fail(g, run, err)
		}
	}

	rrs := make([]*ruleRun, 0, len(subjects))
	for i, sub := range subjects {
		if i > 0 {
			if tree, err = snapshot(); err != nil {
				dropAll()
				return ev.fail(g, run, err)
			}
		}
		req := ev.requestFor(g, r, cs, sub, tree.Path, unresolved)
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
		rrs = append(rrs, &ruleRun{g: g, hash: hash, req: req, payload: *req.Changeset, subject: sub, runID: runID, unresolved: unresolved, tree: tree, head: r.Head})
	}
	return rrs, FileGuardResult{}, false
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
		Workspace:      ev.params.Workspace,
		ProjectRoot:    tree,
		SessionID:      ev.params.SessionID,
		LaunchedBy:     AppendLaunchedBy(os.Getenv, g.Name),
		Changeset:      &payload,
		// SR_SESSION_START is kept for rules written against it: what stood before the work is the base.
		Env: append(changeset.Env(tree, r.Base, r.Head), "SR_SESSION_START="+r.Base),
	}
}

// guardSubjects runs the rule's `subjects:` script: the changeset payload on stdin, no
// session (it must give the same list in `run` and in `verify`), the subjects as JSON on
// stdout.
func (ev *changesetEvaluation) guardSubjects(g declaration.FileGuard, cs changeset.Changeset, req dispatchcore.Request) ([]changeset.Subject, error) {
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
	defer func() { rr.settled = true }()
	ev.dropTree(rr.g, rr.tree, rr.head)
	if failed != nil {
		rr.result, rr.refused = refusal(g, namingFiles(failed.Error(), rr.payload.Changeset.Files)), true
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
			// A skill or a context is read from the session's state, and a citation is
			// grounded in it: a refusal on one is asked again once the agent has done what it
			// asks, never replayed. Only a citation refusal about trailers that resolved (or
			// that there are none) is a verdict about the key's input.
			rr.volatile = rr.volatile || p.Citation == nil || len(rr.unresolved) > 0
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
		v, err = ev.runner.Judge(req, c, prep)
	}
	if err != nil {
		return fail(err)
	}
	return settle(v, meta)
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
// for a rule that requires a citation, the quotes of the citations that ground the subject. No commit
// SHA, branch, session or snapshot path is in it, so two branches with identical content share
// their verdicts and the run that stored one and the verify that reads it compute one key.
func guardKey(g declaration.FileGuard, payload changeset.Payload) (string, error) {
	citations := ""
	for _, r := range g.Require {
		if r.Citation != nil {
			// The key is over the trailers' quotes as `verify` reads them (trusted, each in its
			// own pool), never over how a transcript resolved them: a quote `run` could not
			// resolve (ambiguous, or said in no session) is still a quote of the range, and a
			// key that left it out would never be the one `verify` computes.
			trusted := payload
			cs := trusted.Changeset
			TrustTrailers(&cs)
			trusted.Changeset = cs
			var err error
			if citations, err = changeset.CitationPart(trusted); err != nil {
				return "", err
			}
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
	if ev.verify && !have {
		return missing(fmt.Sprintf("not judged yet — run `sr-checks run --base %s --head %s` in %s", ev.rng.Base, ev.rng.Head, ev.root))
	}
	if !have {
		return dispatchcore.Verdict{}, nil, false
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
// engine error never reaches here (it is no verdict), and a refusal that read the session is not stored.
func (ev *changesetEvaluation) recordGuard(rr *ruleRun, verdict dispatchcore.Verdict) {
	if ev.store == nil || ev.verify || rr.runID == "" || rr.key == "" || rr.replayed {
		return
	}
	meta := map[string]any{"steps": ev.stepsOf(rr)}
	rec := checkstore.CheckRecord{Subject: rr.subject.ID, Kind: guardKind, Fingerprint: rr.key, Metadata: meta}
	switch {
	case verdict.Refused:
		if rr.volatile {
			return
		}
		rec.Status, meta["reasoning"] = checkstore.StatusFail, verdict.Reason
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
