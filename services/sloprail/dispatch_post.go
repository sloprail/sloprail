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

	decls, _, err := guardrail.New(dotDir(p.Cwd)).LoadWith(reg)
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail:", err)
		return false
	}

	// Only what something actually binds to. An extractor runs when a binding
	// names a kind it produces and not otherwise — the same rule the pre-tool
	// point keeps, for the same reason: comparing trees is not free, and a
	// project with no rule about files should not pay for the fact that files
	// can be compared.
	var bound []string
	for _, d := range decls {
		if d.IsEnabled() {
			bound = append(bound, d.BoundKinds()...)
		}
	}

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

	objections := dispatchAll(cmd, reg, decls, rev, scope, events)
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
				if !admits(cmd, d, b, e, kindDecl) {
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
					// The hook did not reach a verdict, so there is nothing to
					// record. Writing a pass here would exempt content nobody
					// judged; writing a refusal would blame the rule for the
					// machine.
					fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: guardrail %q: %v\n", d.Name, err)
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

// admits reports whether a binding's matcher lets this event through.
//
// The matcher is compiled against the kind's own declaration, which is what
// makes a matcher naming a field the kind does not carry a load-time error
// rather than a rule that silently never fires. On TurnEnd that declaration has
// no fields at all, so any matcher naming one is refused — see cyclemod.
func admits(cmd *cobra.Command, d guardrail.Declaration, b guardrail.Binding, e event.Event, kindDecl module.KindDecl) bool {
	m, err := guardrail.CompileMatcherFor(b.Matcher, kindDecl)
	if err != nil {
		// A matcher that will not compile disables its binding and says so.
		// Never "matches everything", which would turn a typo into a rule that
		// objects to every cycle; never "matches nothing", which would turn one
		// into a rule that quietly went away.
		fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: guardrail %q: %v\n", d.Name, err)
		return false
	}
	admitted, err := m.Match(e)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: guardrail %q: %v\n", d.Name, err)
		return false
	}
	return admitted
}
