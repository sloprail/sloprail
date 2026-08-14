package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/cyclemod"
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/guardrail"
	"github.com/sloprail/sloprail/internal/module"
	"github.com/sloprail/sloprail/internal/module/modules"
	"github.com/sloprail/sloprail/internal/sessionstate"
)

// objection is one guardrail's refusal of one event, kept until the cycle has
// finished dispatching.
//
// Kept rather than acted on immediately, because refusing at the first one
// would hide the rest: the agent would fix one violation, end the turn, be told
// about the second, and so on — the slow version of the same bug, and it also
// silences every rule bound after the refusing one. See dispatchAll.
type objection struct {
	Guardrail string
	Reason    string
}

// runPostDispatch establishes what the cycle changed, runs the guardrails bound
// to it, and reports that the cycle ended — then blocks the turn if anything
// objected.
//
// It returns whether the cycle COMPLETED, because the read mark waits on that:
// a cycle that dispatched nothing has judged nothing and has no position to
// claim as judged.
//
// A refusal reports false, and that is not the same claim as "nothing was
// judged". The hooks ran and some of them reached a verdict. But a Post refusal
// BLOCKS the turn, so the agent is sent round again in this same session to fix
// what was refused — and if the mark advanced, the very turns it must correct
// would be behind it and would never be offered again. The cycle did not
// finish; it was stopped mid-judging with an objection outstanding.
//
// The asymmetry is what decides it, and it is the spec's own: re-reading a turn
// costs a second look, while skipping one loses a violation for good. See
// T019_01, which is exactly this case — a cycle whose judging was cut short
// must leave its turns available to the next one.
//
// What a refusal here means. It cannot undo the write: the file is on disk, the
// cycle is over, and an engine claiming otherwise would be promising a rollback
// it never performed. What it can do — and must — is stop the TURN from ending,
// which is the whole mechanism by which an after-the-fact rule gets anything
// corrected. The agent is handed the objections and made to go again.
//
// Those two are easy to conflate and this comment exists because they were:
// "a Post event cannot prevent the work" is a statement about the write, not a
// licence to let the turn end with the violation unaddressed.
func runPostDispatch(cmd *cobra.Command, store sessionstate.Store, p HookPayload) bool {
	reg, err := modules.Registry()
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail:", err)
		return false
	}

	// The invalid list is KEPT, and that is the correction this line carries.
	//
	// It was discarded — `decls, _, err :=` — which made a declaration that could
	// not be loaded contribute nothing to a cycle: no hook, no objection, and no
	// word to the agent. That is the defect refuseForBroken closed on the Pre
	// side, and the argument transfers whole. An author who mistyped a field on a
	// Post binding believes their cycle is guarded; the engine knows it is not;
	// letting the turn end tells them they were right.
	//
	// The channel argument transfers too, and is why reporting on stderr is not
	// an answer here either. A Stop hook exits 0 and blocks by writing
	// {"decision":"block"} on stdout, so its stderr reaches no agent at all —
	// measured on harness.BlockingErrors. A diagnostic beside a turn that ended
	// cleanly is the silence, with a line of code that looks like it addressed it.
	decls, invalid, err := guardrail.New(dotDir(p.Cwd)).LoadWith(reg)
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail:", err)
		return false
	}

	// For a person tailing logs. Not how the agent learns of it — see above and
	// brokenObjections, which is what actually carries these words.
	reportInvalid(cmd, invalid)

	// Only what something actually binds to. An extractor runs when a binding
	// names a kind it produces and not otherwise — the same rule the pre-tool
	// point keeps, for the same reason: comparing trees is not free, and a
	// project with no rule about files should not pay for the fact that files
	// can be compared.
	//
	// The broken declarations' kinds are included for the same reason the
	// pre-tool point includes them: their bindings are exactly what has stopped
	// being enforced, and the events they named are the ones whose occurrence has
	// to be noticed in order to say so. Leaving them out would mean the one case
	// that must be reported is the one case no event is produced for.
	bound := boundKinds(decls, invalid)

	// Who this session is, resolved ONCE for the whole dispatch and used for
	// both things that need it: the store of what has already been judged, and
	// the environment every hook is given. Two calls to stableID would be two
	// derivations free to drift, which this codebase has already had to
	// converge more than once — and here the drift would be worse than
	// cosmetic, because a hook landing in a different store than the dispatcher
	// opened would record its verdict where the next cycle does not look.
	//
	// A session that cannot be identified is not a reason to refuse. The rules
	// that need no memory still work. It does mean nothing can be exempted — an
	// engine that could not find its record must re-judge, never skip — and
	// that is what the empty id yields, since openRevalidation is not called
	// without one.
	//
	// The same shape as the pre-tool point, deliberately: one identity, one
	// scope, both carried down rather than re-derived where they are used.
	scope := hookScope{Workspace: p.Cwd}
	if id, err := stableID(p); err == nil {
		scope.SessionID = id
	} else {
		fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: %v\n", err)
	}
	// The record itself, for a rule that reads the trajectory rather than the
	// tree. From the same p.record() stableID is built on, so the id and the
	// path cannot name different files.
	if path, err := p.record(); err == nil {
		scope.Transcript = path
	}

	// What this session has already judged. Opened once for the whole dispatch,
	// and left nil when the session cannot be identified.
	var rev *revalidation
	if scope.SessionID != "" {
		var err error
		if rev, err = openRevalidation(scope.SessionID, p.Cwd); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: session state unavailable, judging everything afresh: %v\n", err)
		}
	}
	defer rev.Close()

	events := postEvents(cmd, store, p, reg, bound)

	// TurnEnd last, and unconditionally.
	//
	// Last because a rule about the cycle as a whole — that every entity of a
	// kind is linked from somewhere, that a required artifact was produced — is
	// asking about the state the per-file rules have just been shown, and a hook
	// that reads what one of them wrote needs that write to have happened.
	//
	// Unconditional because the cycle ended whatever the tree looks like. This
	// is the one event whose absence would be a lie: a cycle that changed no
	// files still ended, and the rules that fire on completeness — a required
	// artifact never produced — are exactly the ones whose violation looks like
	// nothing having happened.
	//
	// Built by cyclemod rather than here, so the kind's name and the fact that
	// it carries no fields are stated in the same place they are declared.
	events = append(events, cyclemod.Event())

	// A declaration that could not be loaded objects BEFORE any hook is asked,
	// and it objects to the events it was bound to. Collected alongside the real
	// verdicts rather than returned early, for the same reason dispatchAll
	// collects rather than stopping: the agent is told everything at once.
	objections := brokenObjections(invalid, events)

	objections = append(objections, dispatchAll(cmd, reg, decls, rev, scope, events)...)
	if len(objections) > 0 {
		// The turn does not end. Reported through the one channel measured to
		// both block and carry its words — see block().
		if err := block(cmd, refusalText(objections)); err != nil {
			fmt.Fprintln(cmd.ErrOrStderr(), "sloprail:", err)
		}
		// And the mark does not move. The agent is about to go again over these
		// same turns, which it cannot do if the cycle has just declared them
		// judged.
		return false
	}
	return true
}

// brokenObjections is what the declarations that could not be loaded have to
// say about this cycle.
//
// # Why a broken rule objects at all
//
// The same argument refuseForBroken carries at the pre-tool point, and it is
// not weakened by the timing. A Post refusal cannot undo the write — true, and
// beside the point. What it does is stop the TURN from ending, which is the
// whole mechanism an after-the-fact rule has. Ending the turn because the rule
// would not load is the engine deciding, on its own account, that an
// unenforceable rule is a satisfied one.
//
// # Scoping, in the two shapes the Pre side already distinguishes
//
// A declaration that PARSED and then failed validation names its kinds, so it
// objects to a cycle only when that cycle produced one of them. A typo in a
// rule about commands must not block a cycle no rule was written about —
// otherwise the only way out is deleting the rule, which is the mistake
// guardrail.Fault warns about one door along. Since PreCommandInvoke is never
// produced here, such a rule is correctly silent at Stop.
//
// A declaration that could not be PARSED names nothing, and "no evidence of
// what it guarded" is not "evidence it guarded nothing". It objects to every
// cycle. The asymmetry is deliberate and is the same one refuseForUnreadable
// makes: the cost of refusing too broadly is loud, immediate and cleared by
// fixing the file, while the cost of permitting is silent.
//
// TurnEnd is always among the events, so an unreadable declaration always has
// something to object to and needs no special case.
func brokenObjections(invalid []guardrail.Invalid, events []event.Event) []objection {
	produced := make(map[string]bool, len(events))
	for _, e := range events {
		produced[e.Kind] = true
	}

	var objections []objection
	for _, iv := range invalid {
		if iv.Has(guardrail.ErrMalformed) {
			objections = append(objections, objection{
				Guardrail: iv.Name,
				Reason: fmt.Sprintf(
					"guardrail %q could not be read at all, so there is no way to know what it was guarding: %s. "+
						"The turn is held because a file the project keeps as a guardrail must not be read as approval "+
						"merely for being unreadable — fix the declaration in %s, or remove that folder if it is not a guardrail.",
					iv.Name, iv.Reason, iv.Name),
			})
			continue
		}

		// Scoped: only the kinds this cycle actually produced. Every fault is
		// named, not the first, so one pass fixes the declaration.
		for _, k := range iv.AffectedKinds() {
			if !produced[k] {
				continue
			}
			objections = append(objections, objection{
				Guardrail: iv.Name,
				Reason: fmt.Sprintf(
					"guardrail %q is bound to %s but could not be loaded, so it did not guard this cycle: %s. "+
						"The turn is held because a guardrail that cannot load must not be read as approval — "+
						"fix the declaration in %s, or disable it with `enabled: false` if it is not ready.",
					iv.Name, k, iv.Reason, iv.Name),
			})
			// One objection per broken declaration, not one per kind. The fault
			// is the same fault whichever event surfaced it, and repeating it
			// per kind would pad the block with the same sentence.
			break
		}
	}
	return objections
}

// refusalText is what the agent is told when a cycle is refused.
//
// Every objection, not the first. The agent is about to spend a turn on this,
// and being handed one violation at a time turns one correction into as many
// turns as there are rules — while the rules bound after the first would not
// even have been named.
//
// Each names its guardrail, because an agent told only that it was blocked
// cannot find the rule it broke.
func refusalText(objections []objection) string {
	if len(objections) == 1 {
		return fmt.Sprintf("%s (%s)", objections[0].Reason, objections[0].Guardrail)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d guardrails refused this turn's work:", len(objections))
	for _, o := range objections {
		fmt.Fprintf(&b, "\n  - %s (%s)", o.Reason, o.Guardrail)
	}
	return b.String()
}

// postEvents turns the cycle's difference into one event per changed file.
//
// Returns nothing rather than failing the cycle when the difference cannot be
// established. A baseline that was never recorded, a tree that is not a
// repository, a git that will not answer — none of them is a rule being
// violated, and refusing the agent's work over the engine's own inability to
// look would be a refusal no guardrail asked for.
func postEvents(cmd *cobra.Command, store sessionstate.Store, p HookPayload, reg *module.Registry, bound []string) []event.Event {
	commit, ok, err := store.Meta(sessionstate.MetaBaselineCommit)
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: baseline not read:", err)
		return nil
	}
	if !ok || commit == "" {
		// No point to measure from — a project without git, or one with no
		// commit yet. The cycle still ends and TurnEnd still fires; there is
		// simply no difference to report.
		return nil
	}

	// The difference is taken FIRST and the problem reported after, the same
	// way the module's own events are taken below.
	//
	// A difference that came back is a difference worth dispatching even when
	// something in it could not be read: git may have named one path with a
	// status this engine does not know, and the other ninety-nine are still
	// real changes that rules are bound to. Throwing them away would turn one
	// unclassifiable file into a whole cycle judged by nothing — a silence
	// larger than the one it was reporting.
	diff, err := newTreeDifference(p.Cwd, commit)
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: tree not fully compared:", err)
	}
	if diff == nil {
		return nil
	}

	// What is still unfixed, whatever the tree now says.
	//
	// `refusal_outlives_baseline`, and the half the difference cannot supply. A
	// refused file leaves the difference as soon as the point moves past it —
	// the agent commits its own unfixed work, or switches to a line of history
	// that already holds the same content — and from that cycle on the tree is
	// silent about a file that is still broken. Read from the record instead,
	// which is the only thing that still remembers.
	//
	// Added BEFORE the emptiness check, because the case this exists for is
	// exactly a cycle whose difference is empty: nothing changed, and a
	// violation is nonetheless outstanding.
	readdOutstanding(cmd, store, diff)

	if diff.empty() {
		return nil
	}

	in := module.Input{
		module.InputPhase:   module.PhasePost,
		module.InputPayload: diff,
	}

	var events []event.Event
	for _, m := range reg.Needed(bound) {
		evs, err := m.Extract(in)
		// The events are taken FIRST, and the error reported after.
		//
		// A module returns both together by contract, and the error describes
		// the paths it could not classify rather than the ones it could. The
		// caller at the pre-tool point does the opposite — it prints and
		// `continue`s, throwing the events away — which is exactly the silence
		// the module documents: ninety-nine correct classifications dropped
		// because one path would not stat.
		events = append(events, evs...)
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: module %q: %v\n", m.Name(), err)
		}
	}
	return events
}

// readdOutstanding puts every still-unfixed file back into the difference.
//
// The reader `refusal_outlives_baseline` turns on. Retention alone does not
// enforce the invariant: a refusal sitting in the record changes nothing unless
// something asks for it, and until this existed the only reader was Skippable,
// which answers false for a refused file and for an unjudged one alike. So a
// refused file that left the difference was never put in front of its rule
// again — still broken, and nothing left to say so.
//
// Re-added rather than re-refused from the record. The verdict is not replayed
// here, and deliberately: the file is handed back to the rule that objected and
// judged again, so a violation the agent has since fixed outside the diff
// clears itself, and one it has not is refused afresh by the rule rather than
// by a cached answer. The dispatcher's own skip check is what keeps that from
// being wasteful — content already judged at this fingerprint is skipped, and a
// refusal never licenses a skip, so the hook re-runs exactly on what is unfixed.
//
// A path git already reported is left alone; see include.
//
// Failure is reported and swallowed, the way every other read in this
// bookkeeping is. The cost is a cycle that does not re-report an unfixed file,
// which is the same cost the engine paid on every cycle before this existed;
// refusing the agent's work because the record would not open is a refusal no
// guardrail asked for.
func readdOutstanding(cmd *cobra.Command, store sessionstate.Store, diff *treeDifference) {
	outstanding, err := store.OutstandingRefusals()
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: unfixed violations not re-checked:", err)
		return
	}
	for _, r := range outstanding {
		diff.include(r.Path)
	}
}

// dispatchAll runs every binding admitting each event and collects what
// refused, without stopping.
//
// Nothing here returns early on a refusal, and that is the point. One rule
// objecting must not cost the other files their judging, nor the rules bound
// after it their run — an engine that stopped at the first would let one noisy
// guardrail silence every other, and the agent would fix them one turn at a
// time. Everything is dispatched; the objections are answered once, together,
// by the caller.
func dispatchAll(cmd *cobra.Command, reg *module.Registry, decls []guardrail.Declaration, rev *revalidation, scope hookScope, events []event.Event) []objection {
	var objections []objection

	for _, e := range events {
		kindDecl, known := reg.KindDeclFor(e.Kind)
		if !known {
			// An event from a kind the registry does not own cannot be matched
			// against anything. Unreachable in this build — every kind
			// dispatched here is declared by a registered module, TurnEnd
			// included, which is the whole reason cyclemod exists.
			continue
		}

		for _, d := range decls {
			if !d.IsEnabled() {
				continue
			}
			for _, b := range d.Hooks[e.Kind] {
				admitted, err := admits(d, b, e, kindDecl)
				if err != nil {
					// The engine could not ANSWER whether this rule applies —
					// not the rule being satisfied. Reporting false here made
					// the two indistinguishable, and did it on a stream no
					// agent reads.
					//
					// Two things reach this. A matcher that will not compile,
					// which is unreachable for a declaration in `decls` because
					// the identical compile ran at load and would have made it
					// Invalid — kept because this is where the compile actually
					// happens, and a second opinion that disagreed with the
					// load check must not decide enforcement quietly. And a
					// matcher that compiles and cannot be EVALUATED on the value
					// that arrived, which is reachable and is the live one:
					// `int(path) > 0` type-checks and then meets a path that is
					// not a number.
					//
					// The same rule the Pre side already follows, and the same
					// family as a hook that cannot run: a guardrail must not
					// have a path where the machinery breaking reads as consent.
					fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: guardrail %q: %v\n", d.Name, err)
					objections = append(objections, objection{
						Guardrail: d.Name,
						Reason: fmt.Sprintf(
							"guardrail %q could not decide whether it applies to this %s: %v. "+
								"The turn is held because a matcher that cannot be evaluated is not the same as a rule that was satisfied. "+
								"Fix the matcher, or disable the guardrail with `enabled: false` if it is not ready.",
							d.Name, e.Kind, err),
					})
					continue
				}
				if !admitted {
					continue
				}

				// The subject is resolved HERE, per guardrail, rather than once
				// per event as the pre-tool point does — and the difference is
				// not stylistic.
				//
				// A Post subject is fingerprinted from the file ON DISK, and a
				// guardrail's hook is an arbitrary script that may rewrite the
				// very file the event is about. A formatter bound to
				// PostFileUpdate is the ordinary case, not a contrived one. Hoist
				// this out of the loop and the second rule is asked about content
				// that no longer exists, and — worse — its verdict is RECORDED
				// against the first rule's fingerprint, so a pass licenses bytes
				// nobody judged.
				//
				// The pre-tool point is safe hoisting it because its only
				// fingerprinted kind is PreFileCreate, whose content comes off
				// the event and reads no disk.
				subj, fingerprinted := rev.Subject(e, scope.Workspace)

				// This guardrail has already seen this exact content and let it
				// through. Asking again is not merely waste: a judge hook is a
				// model call rather than a function, so a second look can return
				// a different answer and block the agent over work it already
				// fixed.
				//
				// Asked per guardrail. Whether content has been judged is each
				// rule's own fact — a file one rule passed is a file another may
				// never have seen — which is why a verdict is keyed on the pair
				// and never pooled per file.
				if fingerprinted {
					skip, err := rev.Skip(d.Name, subj)
					if err != nil {
						// Reported, never acted on. The false is already the safe
						// answer; saying so is what keeps a session that has
						// silently lost its record from looking like one that
						// simply has nothing settled.
						fmt.Fprintln(cmd.ErrOrStderr(), err)
					}
					if skip {
						continue
					}
				}

				v, err := runHooks(d, b, e, scope)
				if err != nil {
					// The hook could not be STARTED. Nothing was asked, so
					// nothing approved — and `continue` made that read as
					// approval, which is the third member of the same family
					// and the one whose own comment on the Pre side already
					// said it is "not something to proceed through either".
					//
					// Reachable, and not only by a hook type this engine does
					// not understand. A NUL byte in the command is a valid
					// double-quoted YAML scalar that checkExecutable does not
					// judge, so the declaration loads SOUND and the exec of the
					// shell itself then fails with "invalid argument" — never an
					// ExitError, so none of the exit-status handling sees it.
					//
					// Nothing is RECORDED, and that half was already right: the
					// hook reached no verdict, so writing a pass would exempt
					// content nobody judged and writing a refusal would blame
					// the rule for the machine. Holding the turn is a statement
					// about this cycle, not a verdict stored against the file.
					fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: guardrail %q: %v\n", d.Name, err)
					objections = append(objections, objection{
						Guardrail: d.Name,
						Reason: fmt.Sprintf(
							"guardrail %q could not run its hook for this %s: %v. "+
								"The turn is held because a guardrail that cannot run must not be read as approval.",
							d.Name, e.Kind, err),
					})
					continue
				}

				if fingerprinted {
					// Recorded whichever way it went, and the refusal is the half
					// that is easy to lose. A refusal that is forgotten stops
					// being enforced: with no row the content reads as unjudged
					// rather than as refused, so the next cycle asks again and the
					// first thing to record a pass is believed.
					//
					// Keeping it is also what makes the violation RESURFACE. The
					// row fails the passing half of the exemption for as long as
					// the content stays as it is, so the hook is asked again next
					// cycle, refuses again, and blocks again — until the agent
					// changes the file or the rule permits it. That is the entire
					// mechanism by which an after-the-fact rule gets a correction
					// rather than merely complaining once.
					if err := rev.Record(d.Name, subj, !v.Refused); err != nil {
						fmt.Fprintln(cmd.ErrOrStderr(), err)
					}
				}

				if v.Refused {
					// Collected, not returned. The remaining events still have to
					// be dispatched, and the agent is told about all of them at
					// once — see runPostDispatch, which blocks with all of them.
					objections = append(objections, objection{Guardrail: d.Name, Reason: v.Reason})

					// Also written out one refusal at a time, and this line is
					// NOT how the agent learns of it.
					//
					// This command exits 0 and blocks by writing
					// {"decision":"block"} on stdout, so its stderr reaches no
					// agent at all — measured, along with every other way a Stop
					// hook can refuse, on harness.BlockingErrors. What carries
					// the words is block(), once, at the end.
					//
					// It stays because it is the only per-refusal record a person
					// debugging a session can read: the blocking reason is one
					// joined string built after every event was dispatched, while
					// these arrive in dispatch order, interleaved with the other
					// diagnostics on this stream. Dropping it is invisible to the
					// agent and costs an operator the order things happened in.
					fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: %s (%s)\n", v.Reason, d.Name)
				}
			}
		}
	}
	return objections
}

// admits reports whether a binding's matcher lets this event through, or an
// error when it could not be asked.
//
// The matcher is compiled against the kind's own declaration, which is what
// makes a matcher naming a field the kind does not carry a load-time error
// rather than a rule that silently never fires. On TurnEnd that declaration has
// no fields at all, so any matcher naming one is refused — see cyclemod.
//
// The error is RETURNED rather than reported and swallowed, and that is the
// whole of the correction. This used to answer false for a matcher it could not
// evaluate, which the caller could not tell apart from a rule that legitimately
// did not match — so the binding was skipped, the cycle completed, and the
// diagnostic went to a stream a Stop hook does not deliver on. "Never matches
// everything, never matches nothing" was the right instinct and there is a third
// answer it was missing: could not tell. Only the caller can act on that.
func admits(d guardrail.Declaration, b guardrail.Binding, e event.Event, kindDecl module.KindDecl) (bool, error) {
	m, err := guardrail.CompileMatcherFor(b.Matcher, kindDecl)
	if err != nil {
		return false, err
	}
	return m.Match(e)
}
