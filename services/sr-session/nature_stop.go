package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/cyclemod"
	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/event"
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
//	0. context ENTERS on the cycle's Post events
//	     — a context that recognises itself only from settled content
//	       (a goal.yaml whose active:true exists once the write landed) enters
//	       here, populating context[] BEFORE anything reads it: a file-guard's
//	       match, `when` and checks, commit-required's match, and the gates all
//	       read context[], and each must see this turn's enters, not the last
//	       turn's state.
//	1. commit required
//	     — uncommitted work on a path some file-guard selects (its match may read
//	       context[]) is refused first.
//	2. tracked ranges: file-guards are VERIFIED (never judged) over each range of commits this
//	   agent's folders track (session_ranges.go): a range with no stored verdict is refused with
//	   the `sr-checks run` that produces it.
//	3. Stop GATES
//	     — a gate bound to Stop reads context[]/gates[] and blocks the turn on a
//	       refusal. It must see the contexts from step 0 already active.
//	4. context EXITS
//	     — pure lifecycle (the reversal): each active context's exit runs AFTER
//	       the file-guards and gates decided, so a rule requiring or matching on a
//	       context read it still open; then the context closes for the next
//	       cycle. Never blocks the turn.
//
// Steps 1–3 can each contribute a turn block; steps 0 and 4 cannot. All the maps
// are loaded once and threaded through, so a context that entered in step 0 is the
// same one commit-required, the file-guards and the gates read and that closes in
// step 4.

// dispatchNatureStop runs the new-format end-of-cycle dispatch and reports the
// text to block the turn with (or "" to let it end).
//
// It computes the cycle's Post events itself (postEvents), which the contexts'
// enters read. A file-guard is not evaluated here: it judges the explicit range
// `sr check run` is given (changeset_eval.go).
func dispatchNatureStop(cmd *cobra.Command, p HookPayload, reg *module.Registry, scope hookScope, store sessionstate.Store) string {
	if store == nil {
		return dispatchNatureStopStoreless(cmd, p, reg, scope)
	}
	start := sessionStartOf(store)
	loaded := newNatureDeclarations(cmd, p.Cwd, reg, start)
	// An unreadable folder registry is not "no folders": it falls through to the steps that refuse.
	registered, foldersErr := sessionFolders(p)
	if len(loaded.Gates) == 0 && len(loaded.Contexts) == 0 && len(loaded.FileGuards) == 0 && len(registered) == 0 && foldersErr == nil {
		return "" // no rule here, and no other folder whose rules commit-required covers
	}

	// The state maps, loaded once and shared across all four steps.
	contextMap := loadContextMap(cmd, store, loaded.Contexts)
	gatesMap := loadGatesMap(cmd, store)

	// The cycle's Post file events, and the repository root their paths resolve
	// against: what the contexts' enters read. bound names only the file kinds so
	// extraction does the minimum.
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

	// 0. context enters on the Post file events AND the tag events, populating
	//    context[] before commit-required, the file-guards and the gates read it. Never blocks.
	contextEvents := append(append([]event.Event{}, postFileEvents...), tagWriteEvents...)
	runContextEnters(cmd, reg, loaded.Contexts, contextEvents, scope, store, contextMap, gatesMap, histories)

	// 1. commit required: a file-guard judges commits, so uncommitted work on a
	//    path some rule selects is refused before anything is judged. See
	//    commit_required.go.
	commitOwed := false
	if reason := commitRequired(cmd, p, loaded.FileGuards, store, reg); reason != "" {
		refusals = append(refusals, reason+" (commit required)")
		commitOwed = !strings.HasPrefix(reason, unknownCommitState) // work owed, not a state that could not be read
	}

	// 2. tracked ranges: each range of commits this agent's folders track is VERIFIED against
	//    the stored check results — never judged: no model is asked, nothing is written. A range
	//    whose judges have not been asked is refused with the `sr-checks run` that asks them.
	//    Not while work is owed a commit (judging HEAD would judge an incomplete set), and only
	//    for an agent that owns the tree.
	if !commitOwed && ownsTree(p) {
		refusals = append(refusals, verifyTrackedRanges(cmd, p, reg, store)...)
	}

	// 3. Stop gates, reading the now-populated context[]/gates[]. The Stop event is
	//    the subjectless one cyclemod produces. Refusals block the turn.
	stop := cyclemod.Event()
	for _, r := range runGatesForEvents(cmd, reg, loaded.Gates, []event.Event{stop}, scope, store, contextMap, gatesMap, resolveNotes{}) {
		if r.Refused {
			refusals = append(refusals, r.Reason+" (gate "+r.Attribution+")")
		}
	}

	// 4. context exits, AFTER file-guards and gates decided. Pure lifecycle: flips active/inactive,
	//    never blocks the turn.
	runContextExits(cmd, loaded.Contexts, stop, scope, store, contextMap, gatesMap)

	// What this Stop was shown, so the next Stop — if this cycle is still open —
	// can mark the same text and files `seen`. Recorded whether or not a rule
	// refused: a refused reply is exactly what the retry re-sends.
	recordStopSeen(cmd, store, fileSnapshot, recordEnd)

	return joinRefusals(refusals)
}

// dispatchNatureStopStoreless is the Stop dispatch when the session's state cannot be
// opened. Commit-required runs exactly as usual, and Stop gates run over empty context and gate maps. What is lost is
// bookkeeping only (context enter/exit, seen marks, cited-change history), none of which
// is read here. The caller says the state was unavailable; this never skips a rule.
func dispatchNatureStopStoreless(cmd *cobra.Command, p HookPayload, reg *module.Registry, scope hookScope) string {
	loaded := newNatureDeclarations(cmd, p.Cwd, reg)
	contextMap := map[string]natures.ContextState{}
	gatesMap := map[string]natures.GateState{}
	var refusals []string

	commitOwed := false
	if reason := commitRequired(cmd, p, loaded.FileGuards, nil, reg); reason != "" {
		refusals = append(refusals, reason+" (commit required)")
		commitOwed = !strings.HasPrefix(reason, unknownCommitState) // work owed, not a state that could not be read
	}
	// The tracked ranges are still verified: the verification opens the root session's registry
	// itself, and one it cannot read is a refusal, never "nothing to judge".
	if !commitOwed && ownsTree(p) {
		refusals = append(refusals, verifyTrackedRanges(cmd, p, reg, nil)...)
	}
	for _, r := range runGatesForEvents(cmd, reg, loaded.Gates, []event.Event{cyclemod.Event()}, scope, nil, contextMap, gatesMap, resolveNotes{}) {
		if r.Refused {
			refusals = append(refusals, r.Reason+" (gate "+r.Attribution+")")
		}
	}
	return joinRefusals(refusals)
}

// natureStopBoundKinds is every event kind the Stop dispatch needs extracted:
// the kinds a context binds to (its Post file kinds and PostTagWrite). The Pre
// kinds and Stop are not extracted here; the Stop event is synthesised by
// cyclemod, not by a module.
//
// File-guards contribute nothing. They judge commits, not per-file Post events:
// their changesets are read from git (changeset_eval.go), so a project with only
// file-guards computes no tree difference at Stop. Gates bound to Stop need no
// extraction either.
func natureStopBoundKinds(loaded declaration.Loaded) []string {
	var bound []string
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
