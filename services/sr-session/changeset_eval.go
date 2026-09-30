package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
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
type changesetEvaluation struct {
	cmd        *cobra.Command
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
}

// evaluateChangesets evaluates every file-guard against its own range and returns
// each refusal. root is the repository root; results may be nil.
func evaluateChangesets(cmd *cobra.Command, guards []declaration.FileGuard, p HookPayload, scope hookScope, root string,
	contextMap map[string]natures.ContextState, state sessionstate.Store, results checkstore.Store) []fileGuardResult {
	if len(guards) == 0 {
		return nil
	}
	ev := &changesetEvaluation{
		cmd: cmd, root: root, p: p, scope: scope, contextMap: contextMap,
		context: contextMatchValue(contextMap), state: state, results: results,
		batch: "stop-" + strconv.FormatInt(time.Now().UnixNano(), 10),
	}
	ev.identity = ev.runIdentity()
	var refusals []fileGuardResult
	for _, g := range guards {
		if isLaunchedBy(os.Getenv, g.Name) {
			fmt.Fprintf(cmd.ErrOrStderr(),
				"sloprail: file-guard %q not enforced here — this session was launched by its own check (%s)\n",
				g.Name, LaunchedByEnv)
			continue
		}
		if r, refused := ev.evaluate(g); refused {
			refusals = append(refusals, r)
		}
	}
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
	fmt.Fprintf(ev.cmd.ErrOrStderr(), "sloprail: file-guard %s: %v\n", g.Attribution(), err)
	run.ExitCode, run.Error, run.Complete = 1, err.Error(), true
	if _, recErr := ev.record(run); recErr != nil {
		fmt.Fprintln(ev.cmd.ErrOrStderr(), "sloprail:", recErr) // already refusing
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

// evaluate runs one rule over its range. The bool is whether it refused.
func (ev *changesetEvaluation) evaluate(g declaration.FileGuard) (fileGuardResult, bool) {
	rule := g.Qualified()
	run := checkstore.CheckRun{CheckID: rule, Metadata: map[string]any{"eventKind": changeset.Kind}}

	hash, err := changeset.RuleHash(g.Root())
	if err != nil {
		return ev.engineFailure(g, run, err)
	}
	run.Metadata["ruleHash"] = hash

	match, err := guardrail.CompileFileMatch(g.Match)
	if err != nil {
		return ev.engineFailure(g, run, fmt.Errorf("its match %q could not be compiled: %w", g.Match, err))
	}

	r, err := resolveRuleRange(ev.root, g, hash, ev.results, ev.state)
	if errors.Is(err, gitrepo.ErrNoCommits) {
		return fileGuardResult{}, false // nothing has been committed, so nothing can be judged
	}
	if err != nil {
		return ev.engineFailure(g, run, fmt.Errorf("its range is not computable: %w", err))
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
		return ev.engineFailure(g, run, err)
	}
	if len(cs.Files) == 0 {
		// `match` selected nothing in a range that WAS computed: a pass, and the
		// watermark advances to this head.
		run.Complete = true
		if _, err := ev.record(run); err != nil {
			return ev.engineFailure(g, run, err)
		}
		return fileGuardResult{}, false
	}

	unresolved := resolveChangesetCitations(&cs, ev.scope.Transcript, ev.p.Cwd)

	tree, err := gitrepo.AddSnapshot(ev.root, "", r.Head)
	if err != nil {
		return ev.engineFailure(g, run, err)
	}
	defer func() {
		if err := tree.Remove(); err != nil {
			fmt.Fprintf(ev.cmd.ErrOrStderr(), "sloprail: snapshot of %s not removed: %v\n", r.Head, err)
		}
	}()

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
		return ev.engineFailure(g, run, err)
	}
	verdict, failed := ev.runRule(g, hash, req, payload, runID, unresolved)
	if failed != nil {
		return refusal(g, failed.Error()), true
	}
	if ev.results != nil && runID != "" {
		if err := ev.results.FinishRun(runID); err != nil {
			return refusal(g, fmt.Sprintf("the file-guard %q could not finish recording its run (%v); refusing because a run that was not recorded cannot be trusted", g.Name, err)), true
		}
		if _, err := ev.results.ResolveStale(rule, hash, runID); err != nil {
			fmt.Fprintln(ev.cmd.ErrOrStderr(), "sloprail:", err)
		}
	}
	if verdict.Refused {
		return refusal(g, verdict.Reason), true
	}
	return fileGuardResult{}, false
}

// runRule runs a rule's require and then its checks in order, recording each and
// stopping at the first refusal. The error is an engine failure to run a check
// (already recorded as such): the caller refuses on it.
func (ev *changesetEvaluation) runRule(g declaration.FileGuard, hash string, req dispatchcore.Request,
	payload changeset.Payload, runID string, unresolved []changeset.Unresolved) (dispatchcore.Verdict, error) {

	seen := map[string]int{}
	for _, p := range g.Require {
		kind := requireKind(p)
		if n := seen[kind]; n > 0 {
			kind += "#" + strconv.Itoa(n+1)
		}
		seen[requireKind(p)]++

		one := req
		one.Require = []declaration.Prerequisite{p}
		var v dispatchcore.Verdict
		var err error
		if p.Citation != nil {
			v, err = ev.requireCitationPerFile(one, p, payload.Changeset)
		} else {
			v, err = ev.runner.CheckRequire(one)
		}
		rec := checkstore.CheckRecord{Subject: changeset.DefaultSubjectID, Kind: kind}
		switch {
		case err != nil:
			rec.Status, rec.Metadata = checkstore.StatusError, map[string]any{"reasoning": err.Error()}
			_ = ev.recordCheck(runID, rec) // already failing
			return dispatchcore.Verdict{}, fmt.Errorf("the file-guard %q could not be evaluated (%v); refusing because a guard that could not decide must not be read as approval", g.Name, err)
		case v.Refused:
			rec.Status, rec.Metadata = checkstore.StatusFail, map[string]any{"reasoning": v.Reason}
		default:
			rec.Status = checkstore.StatusPass
		}
		if p.Citation != nil {
			rec.Items = citationItems(req.Event, unresolved)
			if v.Refused {
				v.Reason += unresolvedNote(unresolved)
				rec.Metadata["reasoning"] = v.Reason
			}
		}
		if err := ev.recordCheck(runID, rec); err != nil {
			return dispatchcore.Verdict{}, engineError(g, err)
		}
		if v.Refused {
			return v, nil
		}
	}

	for i, c := range g.Checks {
		v, err := ev.runCheck(g, hash, req, payload, runID, i, c)
		if err != nil {
			return dispatchcore.Verdict{}, err
		}
		if v.Refused {
			return v, nil
		}
	}
	return dispatchcore.Verdict{}, nil
}

// requireCitationPerFile is `require: citation` on a changeset: each selected file
// must be grounded by a citation quoted in the commit that last changed it, so a
// commit that cites one file does not ground another, and an uncited change on top
// of a cited one is refused. The prerequisite (its pools, its `when`) is asked once
// per file, about that file's own citations; a `when` script still receives the
// whole changeset. Every file that is not grounded is named.
func (ev *changesetEvaluation) requireCitationPerFile(req dispatchcore.Request, p declaration.Prerequisite, cs changeset.Changeset) (dispatchcore.Verdict, error) {
	var first dispatchcore.Verdict
	var uncited []string
	for _, f := range cs.Files {
		one := req
		one.Event = event.Event{Kind: changeset.Kind, Fields: map[string]any{
			"path":                   f.Path,
			grounding.FieldCitations: grounding.ToWire(changeset.Plain(cs.ForFile(f))),
		}}
		v, err := ev.runner.CheckRequire(one)
		if err != nil {
			return dispatchcore.Verdict{}, err
		}
		if !v.Refused {
			continue
		}
		if len(uncited) == 0 {
			first = v
		}
		uncited = append(uncited, f.Path)
	}
	if len(uncited) == 0 {
		return dispatchcore.Verdict{}, nil
	}
	first.Reason = fmt.Sprintf("a citation grounds only the commit it is in. Not grounded by a citation in the commit that last changed it: %s.\n%s",
		strings.Join(uncited, ", "), first.Reason)
	return first, nil
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
