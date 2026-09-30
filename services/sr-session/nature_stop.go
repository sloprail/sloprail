package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/cyclemod"
	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/module"
	"github.com/sloprail/sloprail/internal/natures"
	"github.com/sloprail/sloprail/internal/sessionstate"
)

// This file is the Stop half of the new nature dispatch, run at the end of a
// cycle after the old-format Post dispatch. It orchestrates ALL THREE natures'
// end-of-cycle work in the one order the spec's reversal requires, then returns
// the text to block the turn with (file-guard + gate refusals; a context's exit
// never blocks).
//
// # The order, and why it is load-bearing
//
//	1. file-guard AFTER-checks on the cycle's Post file events
//	     — records each verdict into revalidation, so a not-fine file RE-FIRES
//	       next cycle; refusals block the turn.
//	2. context ENTERS on the cycle's Post events
//	     — a context that recognises itself only from settled content
//	       (a goal.yaml whose active:true exists once the write landed) enters
//	       here, populating context[] BEFORE any gate reads it.
//	3. Stop GATES
//	     — a gate bound to Stop reads context[]/gates[] and blocks the turn on a
//	       refusal. It must see the contexts from step 2 already active.
//	4. context EXITS
//	     — pure lifecycle (the reversal): each active context's exit runs AFTER
//	       the gates decided, so a gate requiring a context read it still open;
//	       then the context closes for the next cycle. Never blocks the turn.
//
// Steps 1–3 can each contribute a turn block; step 4 cannot. All the maps are
// loaded once and threaded through, so a context that entered in step 2 is the
// same one a gate reads in step 3 and that closes in step 4.

// dispatchNatureStop runs the new-format end-of-cycle dispatch and reports the
// text to block the turn with (or "" to let it end).
//
// It computes the cycle's Post events itself (the same postEvents the old Post
// dispatch uses, which also re-adds outstanding files so a file-guard's prior
// refusal re-fires) and reuses the caller's already-open store for revalidation,
// so a file-guard's verdict lands in the same file_checks table the old format's
// re-fire reads.
func dispatchNatureStop(cmd *cobra.Command, p HookPayload, reg *module.Registry, scope hookScope, store sessionstate.Store) string {
	loaded := newNatureDeclarations(cmd, p.Cwd, reg)
	if len(loaded.Gates) == 0 && len(loaded.Contexts) == 0 && len(loaded.FileGuards) == 0 {
		return ""
	}

	// The state maps, loaded once and shared across all four steps.
	contextMap := loadContextMap(cmd, store, loaded.Contexts)
	gatesMap := loadGatesMap(cmd, store)

	// The cycle's Post file events, and the repository root their paths resolve
	// against. postEvents also re-adds every outstanding (still-refused) path to the
	// difference, so a file-guard that refused a file last cycle sees it again this
	// cycle even if the tree no longer shows it changed — the re-fire mechanism,
	// reused whole. bound names only the file kinds so extraction does the minimum.
	bound := natureStopBoundKinds(loaded)
	postFileEvents, root := postEvents(cmd, store, p, reg, bound)
	if root == "" {
		root = p.Cwd
	}
	// Which of those files an earlier Stop was already handed with this content
	// (`seen`), before any rule reads them. See seen.go.
	fileSnapshot := markSeenFiles(cmd, store, postFileEvents, root)
	// And which citations each file's change was made with, and the history
	// that says which parts of it no citation rode on (see cited_changes.go):
	// the cycle's first hook, if this Stop is it, records what changed while
	// the agent was not running; the last call's pending changes settle; the
	// sessions sharing this tree contribute their cited changes.
	if err := beginCycle(store, p.Cwd, nowNano(), citedPathsOf(loaded.FileGuards), otherMarks(p, scope.Transcript)); err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail:", err)
	}
	if err := settleCitedChanges(store); err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail:", err)
	}
	others, otherContents := otherHistories(p, scope.Transcript)
	histories := attachHistories(store, postFileEvents, others, otherContents)
	if err := endCycle(store, postFileEvents, citedPathsOf(loaded.FileGuards), backgroundOf(p)); err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail:", err)
	}

	// The cycle's PostTagWrite events too, for a context that recognises itself from
	// a tag the agent wrote (research-rigor enters on #research). Gathered separately
	// from the tree difference — a tag lives in prose, not a file change — the same
	// split the old Post dispatch keeps. File-guards do not trigger on tags (they are
	// file-STATE), so these go only to the context enters below.
	tagWriteEvents, recordEnd := tagEvents(cmd, store, p, reg, bound)

	var refusals []string

	// 0. commit required: a file-guard judges commits, so uncommitted work on a
	//    path some rule selects is refused before anything is judged. See
	//    commit_required.go.
	commitOwed := false
	if reason := commitRequired(cmd, p, loaded.FileGuards, store, contextMatchValue(contextMap)); reason != "" {
		refusals = append(refusals, reason+" (commit required)")
		commitOwed = true
	}

	// 1. file-guards: each rule is evaluated once, over the changeset of commits it
	//    has not yet passed, and every run is recorded (changeset_eval.go). Not while
	//    work is owed a commit: judging HEAD would judge an incomplete set, and the
	//    agent has a commit to make first.
	if !commitOwed {
		for _, r := range evaluateStopChangesets(cmd, p, scope, loaded.FileGuards, contextMap, store) {
			refusals = append(refusals, r.Reason+" (file-guard "+r.Attribution+")")
		}
	}

	if err := clearCitedUnknown(store); err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail:", err)
	}

	// 2. context enters on the Post file events AND the tag events, populating
	//    context[] before gates read it. Never blocks.
	contextEvents := append(append([]event.Event{}, postFileEvents...), tagWriteEvents...)
	runContextEnters(cmd, reg, loaded.Contexts, contextEvents, scope, store, contextMap, gatesMap, histories)

	// 3. Stop gates, reading the now-populated context[]/gates[]. The Stop event is
	//    the subjectless one cyclemod produces. Refusals block the turn.
	stop := cyclemod.Event()
	for _, r := range runGatesForEvents(cmd, reg, loaded.Gates, []event.Event{stop}, scope, store, contextMap, gatesMap, resolveNotes{}) {
		if r.Refused {
			refusals = append(refusals, r.Reason+" (gate "+r.Attribution+")")
		}
	}

	// 4. context exits, AFTER gates decided. Pure lifecycle: flips active/inactive,
	//    never blocks the turn.
	runContextExits(cmd, loaded.Contexts, stop, scope, store, contextMap, gatesMap)

	// What this Stop was shown, so the next Stop — if this cycle is still open —
	// can mark the same text and files `seen`. Recorded whether or not a rule
	// refused: a refused reply is exactly what the retry re-sends.
	recordStopSeen(cmd, store, fileSnapshot, recordEnd)

	return joinRefusals(refusals)
}

// natureStopBoundKinds is every file event kind the Stop dispatch needs extracted
// — the Post file events (for file-guard after-checks and context Post enters)
// and PostTagWrite (a context may enter on a tag). The Pre kinds and Stop are not
// extracted here; the Stop event is synthesised by cyclemod, not by a module.
//
// File-guards bind to a file's STATE rather than an event, so all three Post file
// kinds are bound whenever any file-guard exists — the delete even when no guard
// includes deletions, because a PostFileDelete is also how the after-check learns
// a file it refused is gone and settles that refusal (settleIfGone). Which guard
// is actually run on which kind is FileGuard.Covers'. Contexts contribute their own
// Post `on` kinds. Gates bound to Stop need no extraction (the Stop event is
// synthesised), so they add nothing here.
func natureStopBoundKinds(loaded declaration.Loaded) []string {
	var bound []string
	if len(loaded.FileGuards) > 0 {
		bound = append(bound,
			declaration.KindPostFileCreate,
			declaration.KindPostFileUpdate,
			declaration.KindPostFileDelete)
	}
	for _, c := range loaded.Contexts {
		for _, trig := range c.On {
			kinds, _ := declaration.ExpandContextEvent(trig.Event)
			bound = append(bound, kinds...)
		}
	}
	return bound
}

// joinRefusals renders the collected refusals as the block text, naming each
// rule — a refusal an agent cannot attribute to a rule is one it cannot act on.
// Every refusal, not just the first: the agent is about to spend a turn on this,
// and one at a time turns one correction into as many turns as there are rules
// (the same reason the old Post dispatch collects all its objections).
func joinRefusals(refusals []string) string {
	if len(refusals) == 0 {
		return ""
	}
	if len(refusals) == 1 {
		return refusals[0]
	}
	return "the following rules refused this turn's work:\n  - " + strings.Join(refusals, "\n  - ")
}

// evaluateStopChangesets opens what a changeset evaluation needs — the repository
// and the session's check results — and evaluates every file-guard. A tree that
// is not a repository has no commits to judge; a repository that cannot be read
// refuses, since a state that could not be read must not be read as clean.
func evaluateStopChangesets(cmd *cobra.Command, p HookPayload, scope hookScope, guards []declaration.FileGuard,
	contextMap map[string]natures.ContextState, state sessionstate.Store) []fileGuardResult {
	if len(guards) == 0 {
		return nil
	}
	root, err := gitrepo.Root(p.Cwd)
	if err != nil {
		if isNotARepo(err) {
			return nil
		}
		return []fileGuardResult{{Name: "file-guards", Attribution: "file-guards", Refused: true, Reason: failClosed(err)}}
	}
	results := openChecksStore(cmd, p, scope)
	if results != nil {
		defer results.Close()
	}
	return evaluateChangesets(cmd, guards, p, scope, root, contextMap, state, results)
}
