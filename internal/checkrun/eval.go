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
	"github.com/sloprail/sloprail/internal/natures"
	"github.com/sloprail/sloprail/internal/transcript"
)

// Evaluating file-guards over an explicit range: one changeset per rule.
//
// A file-guard judges COMMITS. `sr check run|verify` evaluates each rule once, over the
// range merge-base(--base, --head)..--head, as the squashed net change: one payload,
// one run of its require and checks.
//
//   - A judge's verdict is a fact about (rule, rule hash, kind, subject, fingerprint of
//     everything it was given) and is kept in the check cache. A finished PASS with the
//     same key is a cache hit: the judge is not asked again, whatever session, agent,
//     branch or commit range made it. A stored FAIL with the same key is
//     replayed by `run` (terminal until the input changes); `verify` shows it.
//   - A judge is pure: it judges its slice. The key is the sha256 of its fully rendered
//     prompt plus prepare's optional "fingerprint" string, so anything else the verdict
//     depends on (files the judge opens with its own tools) must reach the prompt through
//     prepare's additionalContext or be declared through that fingerprint.
//   - `verify` is deterministic: it never calls a judge and never writes. A judge key
//     with no stored pass is red (missing, or the stored fail's reasons).
//   - A script is never cached: it is cheap and deterministic, and re-running it is
//     what keeps it honest.
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
	// ContextMap is each declared context's state (every one present, inactive by default).
	ContextMap map[string]natures.ContextState
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
	errw       io.Writer
	diags      map[string]*bytes.Buffer // each rule's diagnostics, emitted in declaration order after the pool
	root       string
	params     Params
	contextMap map[string]natures.ContextState
	context    map[string]any
	store      checkstore.Store // nil: nothing is recorded or looked up
	identity   checkstore.RunIdentity
	batch      string
	verify     bool // judges are looked up, never asked; nothing is written
	rng        gitrepo.Range
	runner     dispatchcore.Runner
	snapshots  sync.Mutex // `git worktree add/remove` race on the worktree names

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
	unresolved []changeset.Unresolved
	tree       *gitrepo.Snapshot
	head       string
	runID      string
	next       int // index of the first check not yet run

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
		errw: errw, diags: map[string]*bytes.Buffer{}, root: p.Root, params: p, contextMap: p.ContextMap,
		context: ContextMatchValue(p.ContextMap), store: p.Store, verify: p.Verify, rng: p.Range,
		batch: "check-" + strconv.FormatInt(time.Now().UnixNano(), 10),
	}
	ev.identity = ev.runIdentity()
	limit := stopConcurrency()
	for _, g := range guards {
		ev.diags[g.Qualified()] = &bytes.Buffer{} // filled before the pool: read-only map after
	}

	runs := make([]*ruleRun, len(guards)) // rules with checks still to run
	out := make([]*ruleRun, len(guards))  // every rule's outcome, by declaration order
	forEach(len(guards), limit, func(i int) {
		g := guards[i]
		if IsLaunchedBy(os.Getenv, g.Name) {
			fmt.Fprintf(ev.log(g),
				"sloprail: file-guard %q not enforced here — this session was launched by its own check (%s)\n",
				g.Name, LaunchedByEnv)
			return
		}
		rr, r, refused := ev.prepare(g)
		if rr == nil {
			out[i] = &ruleRun{g: g, result: r, refused: refused, settled: true}
			return
		}
		runs[i], out[i] = rr, rr
	})

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
func (ev *changesetEvaluation) prepare(g declaration.FileGuard) (*ruleRun, FileGuardResult, bool) {
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
		Select:    Selector(match, ev.context),
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

	ev.snapshots.Lock()
	tree, err := gitrepo.AddSnapshot(ev.root, "", r.Head)
	ev.snapshots.Unlock()
	if err != nil {
		return ev.fail(g, run, err)
	}

	payload := changeset.NewPayload(cs, changeset.Whole(cs), ev.params.Transcript, ev.context)
	req := dispatchcore.Request{
		Nature:         dispatchcore.NatureFileGuard,
		Event:          event.Event{Kind: changeset.Kind, Fields: map[string]any{grounding.FieldCitations: grounding.ToWire(changeset.Plain(cs.Citations))}},
		TranscriptPath: ev.params.Transcript,
		Subagent:       ev.params.Subagent,
		Context:        ev.contextMap,
		Dir:            g.Dir,
		GuardName:      g.Name,
		Workspace:      ev.params.Workspace,
		ProjectRoot:    tree.Path,
		SessionID:      ev.params.SessionID,
		LaunchedBy:     AppendLaunchedBy(os.Getenv, g.Name),
		Changeset:      &payload,
		// SR_SESSION_START is kept for rules written against it: what stood before the work is the base.
		Env: append(changeset.Env(tree.Path, r.Base, r.Head), "SR_SESSION_START="+r.Base),
	}

	// Recorded RUNNING, finished only once every check is stored: a run that dies half-way
	// never reads as a pass.
	runID, err := ev.record(run)
	if err != nil {
		ev.dropTree(g, tree, r.Head)
		return ev.fail(g, run, err)
	}
	return &ruleRun{g: g, hash: hash, req: req, payload: payload, runID: runID, unresolved: unresolved, tree: tree, head: r.Head}, FileGuardResult{}, false
}

// fail is engineFailure in prepare's three-value shape.
func (ev *changesetEvaluation) fail(g declaration.FileGuard, run checkstore.CheckRun, err error) (*ruleRun, FileGuardResult, bool) {
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
	v, err := ev.runRequires(rr)
	if err == nil && !v.Refused {
		for rr.next < len(rr.g.Checks) && rr.g.Checks[rr.next].Script != "" {
			if v, err = ev.runCheck(rr.g, rr.hash, rr.req, rr.payload, rr.runID, rr.next, rr.g.Checks[rr.next]); err != nil || v.Refused {
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
		if v, err = ev.runCheck(rr.g, rr.hash, rr.req, rr.payload, rr.runID, rr.next, rr.g.Checks[rr.next]); err != nil || v.Refused {
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
		ev.note(CheckOutcome{Rule: rr.g.Qualified(), Subject: changeset.DefaultSubjectID, Kind: checkKind(i, c), Status: "skipped", Source: "ran", Reason: reason})
		rec := checkstore.CheckRecord{Subject: changeset.DefaultSubjectID, Kind: checkKind(i, c), Status: checkstore.StatusSkip,
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

		v, err := ev.runRequirement(g, rr.req, p, kind, rr.payload, rr.runID, rr.unresolved)
		if err != nil {
			return dispatchcore.Verdict{}, err
		}
		if v.Refused {
			return v, nil
		}
	}
	return dispatchcore.Verdict{}, nil
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
		payload := changeset.NewPayload(cs, s, whole.TranscriptPath, whole.Context)
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
		fmt.Fprintf(&b, "\nOr, since HEAD is the commit that changed them, is not pushed, and the tree is clean, amend it:\n"+
			"  git commit --amend --no-edit --trailer %s", shellQuote(line))
	}
	// Undoing the whole range is one command, and it needs no citation: the tree is
	// then as it was at the base, so there is nothing to ground.
	fmt.Fprintf(&b, "\nTo undo the whole range instead (the files are then as they were, so nothing is left to ground):\n"+
		"  git revert --no-commit %s..HEAD && git commit --no-edit\n"+
		"Never `git reset --hard`, which destroys work.", cs.Base)
	return b.String()
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

// runCheck runs one check of a rule and records it. A script is run every time. A judge is
// keyed on its rendered prompt (see judgeKey) — everything the model is about to be given — and the store asked before the
// model: a stored verdict — a fail too — is reused, whoever recorded it. `verify` never asks
// the model: a key without a stored verdict is red.
func (ev *changesetEvaluation) runCheck(g declaration.FileGuard, hash string, req dispatchcore.Request,
	payload changeset.Payload, runID string, i int, c declaration.Check) (dispatchcore.Verdict, error) {

	rule := g.Qualified()
	rec := checkstore.CheckRecord{Subject: changeset.DefaultSubjectID, Kind: checkKind(i, c)}
	out := CheckOutcome{Rule: rule, Subject: rec.Subject, Kind: rec.Kind, Source: "ran"}
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

	if c.Script != "" {
		v, err := ev.runner.RunScript(req, c)
		if err != nil {
			return fail(err)
		}
		return settle(v, nil)
	}

	// A judge: prepare first, then — unless it asked to skip — fingerprint exactly what the
	// model is about to be given and ask the store before asking the model.
	prep, v, err := ev.runner.PrepareJudge(req, c)
	if err != nil {
		return fail(err)
	}
	if v.Refused {
		return settle(v, map[string]any{"model": c.Model})
	}
	if prep.Skip {
		rec.Status, rec.Metadata = checkstore.StatusSkip, map[string]any{"reasoning": "prepare asked to skip the judge", "model": c.Model}
		if err := ev.recordCheck(runID, rec); err != nil {
			return dispatchcore.Verdict{}, engineError(g, err)
		}
		out.Status, out.Reason = "skipped", "prepare asked to skip the judge"
		ev.note(out)
		return dispatchcore.Verdict{}, nil
	}
	fp, refusal, err := judgeKey(ev.runner, g, req, payload, c, prep)
	if err != nil {
		return fail(err)
	}
	if refusal != "" {
		// The prompt cannot be rendered: nothing to key. Judge refuses with the same words.
		return settle(dispatchcore.Verdict{Refused: true, Reason: refusal}, map[string]any{"model": c.Model})
	}
	rec.Fingerprint = fp
	meta := map[string]any{"model": c.Model}

	if ev.store != nil {
		if ev.verify {
			stored, have, err := ev.store.CachedCheck(rule, hash, rec.Subject, rec.Kind, fp)
			if err != nil {
				return fail(err)
			}
			out.Source = "stored"
			reasoning, _ := stored.Metadata["reasoning"].(string)
			switch {
			case have && stored.Status == checkstore.StatusPass:
				out.Status, out.Source = checkstore.StatusPass, "cached"
				ev.note(out)
				return dispatchcore.Verdict{Reason: reasoning}, nil
			case have:
				out.Status, out.Reason = checkstore.StatusFail, reasoning
				ev.note(out)
				return dispatchcore.Verdict{Refused: true, Reason: reasoning}, nil
			}
			out.Status = "missing"
			out.Reason = fmt.Sprintf("not judged yet (%s) — run `sr-checks run --base %s --head %s` in %s", rec.Kind, ev.rng.Base, ev.rng.Head, ev.root)
			ev.note(out)
			return dispatchcore.Verdict{Refused: true, Reason: out.Reason}, nil
		}
		cached, hit, err := ev.store.CachedCheck(rule, hash, rec.Subject, rec.Kind, fp)
		if err != nil {
			return fail(err)
		}
		if hit {
			// Asked before, on exactly this input: replay the verdict — a fail included, which
			// is terminal until the input changes.
			reasoning, _ := cached.Metadata["reasoning"].(string)
			meta["replayed"] = true
			out.Source = "cached"
			return settle(dispatchcore.Verdict{Refused: cached.Status == checkstore.StatusFail, Reason: reasoning}, meta)
		}
	} else if ev.verify {
		out.Status, out.Reason = "missing", "no results are kept here; run `sr check run`"
		ev.note(out)
		return dispatchcore.Verdict{Refused: true, Reason: out.Reason}, nil
	}

	v, err = ev.runner.Judge(req, c, prep)
	if err != nil {
		return fail(err)
	}
	return settle(v, meta)
}

// judgeKey is the fingerprint a judge's verdict is kept under: the sha256 of the prompt the
// judge is about to be given, fully rendered, plus prepare's own "fingerprint" string and,
// for a rule that requires a citation, the commit messages and citation quotes (the
// prompt need not render them). The snapshot's temp path (SR_TREE) is a fresh directory
// every run and is named the same way, `<tree>`, for every run and every verify, so the
// run that stored a verdict and the verify that looks it up compute one key. A refusal
// is the reason the prompt could not be rendered.
func judgeKey(runner dispatchcore.Runner, g declaration.FileGuard, req dispatchcore.Request,
	payload changeset.Payload, c declaration.Check, prep dispatchcore.Prepared) (fp, refusal string, err error) {

	// Where a quote was found (its transcript path, line, whole message and the tool call that
	// printed it) is only known where the transcript is: `run` resolves it, `verify` trusts
	// the trailer and has none. A prompt that renders them would key differently in the two,
	// so the prompt is keyed with those fields held to placeholders; the quote and its pool
	// stay, and so do the judge's own words (it is asked with the real ones).
	keyed := payload
	keyed.Changeset.Citations = slices.Clone(payload.Changeset.Citations)
	for i, cit := range keyed.Changeset.Citations {
		cit.Citation.Path, cit.Citation.Line, cit.Citation.Message, cit.Citation.Call = "<path>", 0, "<message>", ""
		if slices.Contains(cit.Citation.SourceTypes, transcript.SourceToolResult) {
			cit.Citation.Call = "<call>"
		}
		keyed.Changeset.Citations[i] = cit
	}
	req.Changeset = &keyed
	// prepare ran over the real payload, so a citation it inlined (`asks`, `cited_results`) carries
	// the same transcript-found fields: hold them to the same placeholders.
	prep.Context = heldContext(prep.Context)
	prompt, refusal, err := runner.RenderJudge(req, c, prep)
	if err != nil || refusal != "" {
		return "", refusal, err
	}
	norm := func(s string) string {
		if req.ProjectRoot != "" {
			s = strings.ReplaceAll(s, req.ProjectRoot, "<tree>")
		}
		return s
	}
	citations := ""
	for _, r := range g.Require {
		if r.Citation != nil {
			if citations, err = changeset.CitationPart(payload); err != nil {
				return "", "", err
			}
			break
		}
	}
	return changeset.JudgeFingerprint(norm(prompt), norm(prep.Fingerprint), citations), "", nil
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
	rr, r, refused := ev.prepare(g)
	if rr == nil {
		return r, refused
	}
	if ev.runCheap(rr); !rr.settled {
		ev.runRest(rr)
	}
	return rr.result, rr.refused
}

// heldContext is prepare's context with every citation in it (an object with a quote and its
// source types) holding where it was found to placeholders, and the commits it was found in to
// their count, as judgeKey holds the changeset's own citations.
func heldContext(ctx declaration.PreparedContext) declaration.PreparedContext {
	var walk func(v any) any
	walk = func(v any) any {
		switch x := v.(type) {
		case map[string]any:
			_, quoted := x["quote"]
			_, typed := x["sourceTypes"]
			out := make(map[string]any, len(x))
			for k, e := range x {
				if quoted && typed {
					switch k {
					case "path":
						e = "<path>"
					case "message":
						e = "<message>"
					case "line":
						e = 0
					case "call":
						if e != nil && e != "" {
							e = "<call>"
						}
					case "commits":
						if shas, ok := e.([]any); ok {
							e = make([]any, len(shas))
						}
					}
				}
				out[k] = walk(e)
			}
			return out
		case []any:
			out := make([]any, len(x))
			for i, e := range x {
				out[i] = walk(e)
			}
			return out
		}
		return v
	}
	b, err := json.Marshal(ctx)
	if err != nil {
		return ctx
	}
	var v any
	if json.Unmarshal(b, &v) != nil {
		return ctx
	}
	if m, ok := walk(v).(map[string]any); ok {
		return declaration.PreparedContext(m)
	}
	return ctx
}
