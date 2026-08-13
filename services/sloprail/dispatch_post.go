package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/cyclemod"
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/guardrail"
	"github.com/sloprail/sloprail/internal/module"
	"github.com/sloprail/sloprail/internal/module/modules"
	"github.com/sloprail/sloprail/internal/sessionstate"
)

// runPostDispatch establishes what the cycle changed, runs the guardrails bound
// to it, and then reports that the cycle ended.
//
// It returns whether it ran, because the read mark waits on that: a cycle that
// dispatched nothing has judged nothing and has no position to claim as judged.
// "Ran" means the dispatch happened, not that it found anything — a cycle that
// changed no files still ended, still fired TurnEnd, and still put the session's
// record in front of whatever bound to it.
//
// Nothing here can refuse. Every event this dispatches describes work that has
// already landed, so a hook refusing one is demanding a correction rather than
// preventing anything, and blocking would claim a rollback the engine cannot
// perform. Refusals are reported and the cycle ends.
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

	dispatch(cmd, reg, decls, events)
	return true
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

	diff, err := newTreeDifference(p.Cwd, commit)
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: tree not compared:", err)
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

// dispatch runs every binding admitting each event, in order, and never blocks.
//
// The matching is the pre-tool point's, deliberately: which occurrences a rule
// sees is the binding's business at both timings, and a Post event narrowed
// differently would make one hook, bound to both, judge two different sets of
// files.
//
// What differs is the verdict's consequence. A refusal here is reported and the
// cycle continues, because the work it describes has already landed. That is
// not the refusal being ignored — the rule ran, it objected, and its objection
// is on stderr where the agent and the person both see it. It is the engine
// declining to claim it undid something it did not undo.
func dispatch(cmd *cobra.Command, reg *module.Registry, decls []guardrail.Declaration, events []event.Event) {
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
				v, err := runHooks(d, b, e)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: guardrail %q: %v\n", d.Name, err)
					continue
				}
				if v.Refused {
					// Reported, not returned. Refusing an event that describes
					// work already done demands the work be corrected; it does
					// not and cannot prevent it.
					fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: %s (%s)\n", v.Reason, d.Name)
				}
			}
		}
	}
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
