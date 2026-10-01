package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/changeset"
	"github.com/sloprail/sloprail/internal/checkstore"
	"github.com/sloprail/sloprail/internal/declaration"
	dispatchcore "github.com/sloprail/sloprail/internal/dispatch"
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/grounding"
	"github.com/sloprail/sloprail/internal/guardrail"
	"github.com/sloprail/sloprail/internal/natures"
	"github.com/sloprail/sloprail/internal/sessionpath"
	"github.com/sloprail/sloprail/internal/sessionstate"
)

// Evaluating file-guards at Stop: one changeset per rule.
//
// A file-guard judges COMMITS. At Stop each rule is evaluated once, over the range
// of commits it has not yet passed — from its watermark (or, failing that, its
// floor) to HEAD — as the squashed net change: one payload, one run of its
// require and checks, one row of the check-results store.
//
//   - A run is recorded whatever the outcome (checkstore), and the watermark is
//     derived from it: only a run whose every check passed moves the rule's base
//     to that head. A refused range is never partly passed.
//   - A failing judge is TERMINAL: its verdict is stored under a fingerprint of
//     everything it was given, and replayed from the store on every Stop until
//     that input changes. A fix changes the input, so the rule re-runs on the
//     whole squashed range — on purpose.
//   - A script is never replayed: it is cheap and deterministic, and re-running it
//     is what keeps it honest.
//   - Anything that goes wrong in the engine — git, the rule's own folder, the
//     snapshot — fails CLOSED and is recorded as an engine failure, which passes
//     nothing and moves nothing. A range that could not be read is never an empty
//     one; a range where `match` selects nothing is a pass.
//
// Checks run against a read-only snapshot of head (SR_TREE), never the working
// tree, with SR_BASE and SR_HEAD naming the range.

// changesetEvaluation is what one Stop's evaluation of every file-guard shares.
// Its rules are evaluated concurrently (see evaluateChangesets), so everything here
// is read-only or safe to use from several goroutines: the stores serialise their
// own writes, and stderr and git's worktree registry are guarded below.
type changesetEvaluation struct {
	cmd        *cobra.Command
	diags      map[string]*bytes.Buffer // each rule's diagnostics, emitted in declaration order after the pool
	root       string
	p          HookPayload
	scope      hookScope
	contextMap map[string]natures.ContextState
	context    map[string]any
	state      sessionstate.Store
	results    checkstore.Store // nil: nothing is recorded or replayed this Stop
	identity   checkstore.RunIdentity
	batch      string
	runner     dispatchcore.Runner
	snapshots  sync.Mutex // `git worktree add/remove` race on the worktree names
}

// StopConcurrencyEnv bounds how many file-guard rules are evaluated at once at Stop.
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

// ruleRun is one rule mid-evaluation: prepared (range, changeset, snapshot, run
// recorded RUNNING) and carrying how far its checks have got.
type ruleRun struct {
	g          declaration.FileGuard
	hash       string
	req        dispatchcore.Request
	payload    changeset.Payload
	runID      string
	unresolved []changeset.Unresolved
	tree       *gitrepo.Snapshot
	head       string
	next       int // index of the first check not yet run

	cheap, slow time.Duration // time spent before / at the first judge
	result      fileGuardResult
	refused     bool
	settled     bool // result is final
}

// evaluateChangesets evaluates every file-guard against its own range and returns
// each refusal, in the order the guards were declared. root is the repository root;
// results may be nil.
//
// Rules are independent, so they run concurrently (SLOPRAIL_STOP_CONCURRENCY, default
// 6) in three steps:
//
//  1. prepare every rule: its range, changeset and snapshot;
//  2. the CHEAP checks of every rule: its requirements and the script checks that
//     precede its first judge, in declared order. A refusal here is returned at once
//     and the judges of the other rules are not waited on: their runs stay
//     unfinished (so never a watermark) and the next Stop evaluates them;
//  3. only when nothing refused, the rest of each rule (its judges and what follows)
//     concurrently.
//
// Within a rule the declared order and first-refusal-ends are kept.
func evaluateChangesets(cmd *cobra.Command, guards []declaration.FileGuard, p HookPayload, scope hookScope, root string,
	contextMap map[string]natures.ContextState, state sessionstate.Store, results checkstore.Store) []fileGuardResult {
	if len(guards) == 0 {
		return nil
	}
	start := time.Now()
	ev := &changesetEvaluation{
		cmd: cmd, diags: map[string]*bytes.Buffer{}, root: root, p: p, scope: scope, contextMap: contextMap,
		context: contextMatchValue(contextMap), state: state, results: results,
		batch: "stop-" + strconv.FormatInt(time.Now().UnixNano(), 10),
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
		if isLaunchedBy(os.Getenv, g.Name) {
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
	cheapRefused := false
	for _, o := range out {
		if o != nil && o.settled && o.refused {
			cheapRefused = true
		}
	}

	if cheapRefused {
		for _, rr := range runs {
			if rr != nil && !rr.settled {
				ev.abandon(rr)
			}
		}
	} else {
		forEach(len(runs), limit, func(i int) {
			if runs[i] != nil && !runs[i].settled {
				ev.runRest(runs[i])
			}
		})
	}

	var refusals []fileGuardResult
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
			cmd.ErrOrStderr().Write(b.Bytes())
		}
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: file-guards evaluated in %s (%d rules, concurrency %d)\n",
		time.Since(start).Round(time.Millisecond), len(guards), limit)
	return refusals
}

// runIdentity fills the identity columns: the repository (its root commit), the
// branch, and the session. Best effort — an unreadable one is left empty rather
// than costing the evaluation.
func (ev *changesetEvaluation) runIdentity() checkstore.RunIdentity {
	id := checkstore.RunIdentity{SessionID: ev.scope.SessionID}
	if root, err := gitrepo.RootCommit(ev.root); err == nil {
		id.RepoID = root
	}
	if pos, err := gitrepo.Head(ev.root); err == nil {
		id.Branch = pos.Branch
	}
	return id
}

// refusal is the result for a rule that could not be evaluated or that refused.
func refusal(g declaration.FileGuard, reason string) fileGuardResult {
	return fileGuardResult{Name: g.Name, Attribution: g.Attribution(), Refused: true, Reason: reason}
}

// engineFailure records a run that failed as an engine — passing nothing, moving
// nothing — and returns the refusal that holds the turn.
func (ev *changesetEvaluation) engineFailure(g declaration.FileGuard, run checkstore.CheckRun, err error) (fileGuardResult, bool) {
	fmt.Fprintf(ev.log(g), "sloprail: file-guard %s: %v\n", g.Attribution(), err)
	run.ExitCode, run.Error, run.Complete = 1, err.Error(), true
	if _, recErr := ev.record(run); recErr != nil {
		fmt.Fprintln(ev.log(g), "sloprail:", recErr) // already refusing
	}
	return refusal(g, fmt.Sprintf(
		"the file-guard %q could not be evaluated (%v); refusing because a guard that could not decide must not be read as approval",
		g.Name, err)), true
}

// record stores a run, returning its id ("" when there is no store to record in).
func (ev *changesetEvaluation) record(run checkstore.CheckRun) (string, error) {
	if ev.results == nil {
		return "", nil
	}
	run.RunIdentity, run.BatchID = ev.identity, ev.batch
	return ev.results.RecordRun(run)
}

// recordCheck stores one check of a run. A check that could not be stored is an
// ENGINE failure, not something to print and carry on past: the run would then
// hold fewer checks than it ran, and read as more passed than it was.
func (ev *changesetEvaluation) recordCheck(runID string, c checkstore.CheckRecord) error {
	if ev.results == nil || runID == "" {
		return nil
	}
	if _, err := ev.results.RecordCheck(runID, c); err != nil {
		return fmt.Errorf("could not record the %s check: %w", c.Kind, err)
	}
	return nil
}

// prepare readies one rule over its range: the changeset, the snapshot, and the run
// recorded RUNNING. A nil ruleRun means there is nothing more to do — the result
// and whether it refused are the outcome.
func (ev *changesetEvaluation) prepare(g declaration.FileGuard) (*ruleRun, fileGuardResult, bool) {
	rule := g.Qualified()
	run := checkstore.CheckRun{CheckID: rule, Metadata: map[string]any{"eventKind": changeset.Kind}}

	hash, err := changeset.RuleHash(g.Root())
	if err != nil {
		return ev.fail(g, run, err)
	}
	run.Metadata["ruleHash"] = hash

	match, err := guardrail.CompileFileMatch(g.Match)
	if err != nil {
		return ev.fail(g, run, fmt.Errorf("its match %q could not be compiled: %w", g.Match, err))
	}

	r, err := resolveRuleRange(ev.root, g, ev.results, ev.state)
	if errors.Is(err, gitrepo.ErrNoCommits) {
		return nil, fileGuardResult{}, false // nothing has been committed, so nothing can be judged
	}
	if err != nil {
		return ev.fail(g, run, fmt.Errorf("its range is not computable: %w", err))
	}
	run.BaseRef, run.HeadRef = r.Base, r.Head
	run.Metadata["baseOrigin"] = string(r.Origin)
	if r.DroppedWatermark != "" {
		run.Metadata["droppedWatermark"] = r.DroppedWatermark
	}

	cs, err := changeset.Build(ev.root, r, changeset.Options{
		Deletions: changeset.DeletionMode(g.Deletions),
		Scan:      changesetMarkers,
		Select:    changesetSelector(match, ev.context),
	})
	if err != nil {
		return ev.fail(g, run, err)
	}
	if len(cs.Files) == 0 {
		// `match` selected nothing in a range that WAS computed: a pass, and the
		// watermark advances to this head.
		run.Complete = true
		if _, err := ev.record(run); err != nil {
			return ev.fail(g, run, err)
		}
		return nil, fileGuardResult{}, false
	}

	unresolved := resolveChangesetCitations(&cs, ev.scope.Transcript, ev.p.Cwd)

	ev.snapshots.Lock()
	tree, err := gitrepo.AddSnapshot(ev.root, "", r.Head)
	ev.snapshots.Unlock()
	if err != nil {
		return ev.fail(g, run, err)
	}

	payload := changeset.NewPayload(cs, changeset.Whole(cs), ev.scope.Transcript, ev.context)
	req := dispatchcore.Request{
		Nature:         dispatchcore.NatureFileGuard,
		Event:          event.Event{Kind: changeset.Kind, Fields: map[string]any{grounding.FieldCitations: grounding.ToWire(changeset.Plain(cs.Citations))}},
		TranscriptPath: ev.scope.Transcript,
		Context:        ev.contextMap,
		Dir:            g.Dir,
		GuardName:      g.Name,
		Workspace:      ev.scope.Workspace,
		SessionID:      ev.scope.SessionID,
		LaunchedBy:     appendLaunchedBy(os.Getenv, g.Name),
		Changeset:      &payload,
		Env:            changeset.Env(tree.Path, r.Base, r.Head),
	}

	// Recorded RUNNING, finished only once every check is stored: a run that dies
	// half-way must not be a watermark.
	runID, err := ev.record(run)
	if err != nil {
		ev.dropTree(g, tree, r.Head)
		return ev.fail(g, run, err)
	}
	return &ruleRun{g: g, hash: hash, req: req, payload: payload, runID: runID, unresolved: unresolved, tree: tree, head: r.Head}, fileGuardResult{}, false
}

// fail is engineFailure in prepare's three-value shape.
func (ev *changesetEvaluation) fail(g declaration.FileGuard, run checkstore.CheckRun, err error) (*ruleRun, fileGuardResult, bool) {
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

// abandon drops a rule whose judges were not reached because another rule refused
// on a cheap check. Its run stays unfinished — recorded RUNNING, so never a
// watermark — and the next Stop evaluates it again.
func (ev *changesetEvaluation) abandon(rr *ruleRun) {
	fmt.Fprintf(ev.log(rr.g), "sloprail: file-guard %s: judges not run this Stop; another rule refused first\n", rr.g.Attribution())
	ev.dropTree(rr.g, rr.tree, rr.head)
	rr.settled = true
}

// finish settles a rule: its run is finished and its stale failures resolved, and its
// outcome is the verdict (or the engine error, which is a refusal and leaves the run
// unfinished).
func (ev *changesetEvaluation) finish(rr *ruleRun, verdict dispatchcore.Verdict, failed error) {
	g := rr.g
	defer func() { rr.settled = true }()
	ev.dropTree(rr.g, rr.tree, rr.head)
	if failed != nil {
		rr.result, rr.refused = refusal(g, failed.Error()), true
		return
	}
	if ev.results != nil && rr.runID != "" {
		if err := ev.results.FinishRun(rr.runID); err != nil {
			rr.result, rr.refused = refusal(g, fmt.Sprintf("the file-guard %q could not finish recording its run (%v); refusing because a run that was not recorded cannot be trusted", g.Name, err)), true
			return
		}
		if _, err := ev.results.ResolveStale(g.Qualified(), rr.hash, rr.runID); err != nil {
			fmt.Fprintln(ev.log(g), "sloprail:", err)
		}
	}
	if verdict.Refused {
		rr.result, rr.refused = refusal(g, verdict.Reason), true
	}
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
		rec := checkstore.CheckRecord{Subject: s.ID, Kind: kind}
		switch {
		case err != nil:
			rec.Status, rec.Metadata = checkstore.StatusError, map[string]any{"reasoning": err.Error()}
			_ = ev.recordCheck(runID, rec) // already failing
			return dispatchcore.Verdict{}, engineError(g, err)
		case v.Refused:
			rec.Status, rec.Metadata = checkstore.StatusFail, map[string]any{"reasoning": v.Reason}
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
		citeHowToFix(cs, failed, changeset.TrailerFor(p.Citation.Pools())) + "\n" + body + unresolvedNote(unresolved)
	return dispatchcore.Verdict{Refused: true, Reason: reason}, nil
}

// citeHowToFix says, for the files a citation does not ground, the ONE command that
// grounds them. A trailer grounds the commit it is on, so it has to be on the commit
// that last changed each file: when every such commit is HEAD that is an amend of
// it; when one is earlier, the range is squashed into one commit (which also takes
// the trailers of the commits it replaces, so each quote the range needs is written
// again). Several quotes on one commit are fine.
func citeHowToFix(cs changeset.Changeset, files []string, trailer string) string {
	var b strings.Builder
	b.WriteString("Last changed by:")
	allHead := true
	for _, path := range files {
		tip := ""
		for _, f := range cs.Files {
			if f.Path == path && len(f.Commits) > 0 {
				tip = f.Commits[len(f.Commits)-1]
			}
		}
		allHead = allHead && tip != "" && tip == cs.Head
		fmt.Fprintf(&b, "\n  %s: %s", path, describeCommit(cs, tip))
	}
	line := trailer + ": <exact quote>"
	if allHead {
		fmt.Fprintf(&b, "\nGround them by amending HEAD, the commit that changed them:\n  git commit --amend --no-edit --trailer '%s'", line)
	} else if cs.Base != "" {
		fmt.Fprintf(&b, "\nGround them by squashing the range into one commit that carries the quote(s) (repeat the trailer, one per quote; "+
			"the squash drops the earlier commits' messages, so write again the quotes they carried):\n"+
			"  git reset --soft %s && git commit -m '<what changed>' -m '%s'", short(cs.Base), line)
	} else {
		fmt.Fprintf(&b, "\nGround them by making the next change to each file in a commit that carries the trailer `%s`.", line)
	}
	return b.String()
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

// runCheck runs one check of a rule and records it. A judge is fingerprinted and
// replayed from the store when it has been asked exactly this before; a script
// never is.
func (ev *changesetEvaluation) runCheck(g declaration.FileGuard, hash string, req dispatchcore.Request,
	payload changeset.Payload, runID string, i int, c declaration.Check) (dispatchcore.Verdict, error) {

	rec := checkstore.CheckRecord{Subject: changeset.DefaultSubjectID, Kind: checkKind(i, c)}
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
		if v.Refused {
			rec.Status = checkstore.StatusFail
			meta["reasoning"] = v.Reason
		}
		rec.Metadata = meta
		if err := ev.recordCheck(runID, rec); err != nil {
			return dispatchcore.Verdict{}, engineError(g, err)
		}
		return v, nil
	}

	if c.Script != "" {
		v, err := ev.runner.RunScript(req, c)
		if err != nil {
			return fail(err)
		}
		return settle(v, nil)
	}

	// A judge: prepare first, then — unless it asked to skip — fingerprint exactly
	// what the model is about to be given and ask the store before asking the model.
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
		return dispatchcore.Verdict{}, nil
	}
	extra, err := json.Marshal(prep.Context)
	if err != nil {
		return fail(err)
	}
	fp, err := changeset.Fingerprint(payload, hash, c.Model, string(extra))
	if err != nil {
		return fail(err)
	}
	rec.Fingerprint = fp
	meta := map[string]any{"model": c.Model}

	if ev.results != nil {
		cached, hit, err := ev.results.CachedCheck(rec.Subject, rec.Kind, fp)
		if err != nil {
			return fail(err)
		}
		if hit {
			// Asked before, on exactly this input: replay the verdict — a fail
			// included, which is terminal until the input changes.
			reasoning, _ := cached.Metadata["reasoning"].(string)
			meta["replayed"] = true
			return settle(dispatchcore.Verdict{Refused: cached.Status == checkstore.StatusFail, Reason: reasoning}, meta)
		}
	}
	v, err = ev.runner.Judge(req, c, prep)
	if err != nil {
		return fail(err)
	}
	return settle(v, meta)
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

// citationItems records what the citation prerequisite saw: each citation that
// resolved, and each trailer that did not.
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

// openChecksStore opens the session's check results for writing, or nil (reported)
// when the session has no identity or the store will not open: the evaluation then
// runs unrecorded — every judge is asked, every range starts at its floor.
func openChecksStore(cmd *cobra.Command, p HookPayload, scope hookScope) checkstore.Store {
	if scope.SessionID == "" {
		return nil
	}
	path, err := sessionpath.ChecksDB(p.Cwd, scope.SessionID)
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: check results unavailable:", err)
		return nil
	}
	store, err := checkstore.Open(path)
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: check results unavailable:", err)
		return nil
	}
	return store
}

// log is where a rule's diagnostics go: its own buffer during a concurrent
// evaluation (flushed in declaration order), the command's stderr when a single rule
// is evaluated on its own.
func (ev *changesetEvaluation) log(g declaration.FileGuard) io.Writer {
	if b, ok := ev.diags[g.Qualified()]; ok {
		return b
	}
	return ev.cmd.ErrOrStderr()
}

// evaluate runs one rule on its own, start to finish: its cheap checks, then its
// judges. The bool is whether it refused.
func (ev *changesetEvaluation) evaluate(g declaration.FileGuard) (fileGuardResult, bool) {
	rr, r, refused := ev.prepare(g)
	if rr == nil {
		return r, refused
	}
	if ev.runCheap(rr); !rr.settled {
		ev.runRest(rr)
	}
	return rr.result, rr.refused
}
