package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/declaration"
	dispatchcore "github.com/sloprail/sloprail/internal/dispatch"
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/filemod"
	"github.com/sloprail/sloprail/internal/guardrail"
	"github.com/sloprail/sloprail/internal/natures"
)

// This file is the FILE-GUARD half of the new nature dispatch (3c): a rule bound
// to a FILE'S STATE, not to an event trigger (dot-dir-file-store/main.tsp
// FileGuardDeclaration). It runs at two moments, mirroring the old format's
// preventive/after split and the spec's `preventive` doc:
//
//   - PREVENTIVE, at pre-tool: a guard with `preventive: true` also fires on the
//     PRE file event, to refuse a not-fine write BEFORE it lands. This is
//     best-effort (the engine cannot always predict a write), and when the Pre
//     event could not compute the result (a command-derived update whose
//     newContent is absent), a preventive guard fails CLOSED — it cannot verify,
//     so it must not admit.
//   - AFTER, at Stop: EVERY guard (preventive or not) fires on the POST file
//     event, checking the settled content. A refusal here does not undo the
//     write (it is on disk); it blocks the TURN and, crucially, is RECORDED in
//     the same revalidation store the old format uses — which is what makes a
//     not-fine file RE-FIRE every cycle until its content satisfies the checks,
//     the file-guard's defining "re-fires until fine" semantics.
//
// # How the file's STATE is matched (not an event trigger)
//
// A gate matches an event's fields; a file-guard matches a FILE. Its `match` is a
// FileMatchExpression over FileMatchScope — the file's own path, the `sr:`
// markers it carries, and `context[<name>]`. So this builds a FileMatchScope
// from a file event (path off the event, markers off the event's new markers —
// the settled file's markers on a Post, the would-be result's on a Pre) and the
// context[] map, and evaluates the compiled match against THAT, not against the
// event nested under `event`. guardrail.CompileFileMatch is the same compiler the
// loader validated the match with, so a glob and a full expression behave
// identically here and at load.
//
// # Reuse
//
// The CHECK-RUNNER is dispatch-core's Runner (Nature=file-guard). This file only
// MATCHES a guard to a file event and calls Run — it re-implements no check,
// judge, or payload assembly. The re-fire integrates with the old format's
// revalidation machinery (readdOutstanding re-adds an outstanding path to the
// diff next cycle, so a not-fine file produces a Post event again), which this
// file records into via the same revalidation.Record the old dispatch uses.

// fileGuardResult is one file-guard's outcome on one file: the guard's name,
// whether it refused, and the reason to relay.
type fileGuardResult struct {
	Name    string
	Refused bool
	Reason  string
}

// runFileGuardsPreventive runs the PREVENTIVE file-guards against a cycle's PRE
// file events and returns the first refusal to block on (or "").
//
// Only guards with `preventive: true` fire here — the default (preventive false)
// is checked only after the write lands, at Stop. A preventive guard whose match
// selects the file runs the check-runner on the Pre event; a refusal blocks the
// write before it lands, via the same deny() the gate and structure paths use.
//
// The FIRST refusal (guards in name order, and within a guard the first matching
// file event) is what blocks — a pre-tool hook can deny only once. The context[]
// map is read once and threaded into both the match and the Request, so a guard's
// `match` reading `context[<name>]` and its checks reading the same map see one
// consistent world.
func runFileGuardsPreventive(
	cmd *cobra.Command,
	guards []declaration.FileGuard,
	events []event.Event,
	scope hookScope,
	contextMap map[string]natures.ContextState,
) string {
	runner := dispatchcore.Runner{}

	for _, g := range guards {
		if !g.Preventive {
			// Non-preventive guards are checked only after the write settles.
			continue
		}
		// This session is running underneath THIS guard's own launched check (a
		// judge launches sr-agent, whose writes fire pre-tool). Do not enforce the
		// guard against itself — the same re-entry guard the gate dispatch applies.
		if isLaunchedBy(os.Getenv, g.Name) {
			fmt.Fprintf(cmd.ErrOrStderr(),
				"sloprail: file-guard %q not enforced here — this session was launched by its own check (%s)\n",
				g.Name, LaunchedByEnv)
			continue
		}

		match, err := guardrail.CompileFileMatch(g.Match)
		if err != nil {
			// Unreachable for a loaded guard (the loader compiled the same match),
			// but a compile that disagrees with load must surface loudly, not decide
			// enforcement silently. Reported and treated as not-matching, the same
			// "an unloadable rule blocks nothing" the rest of the dispatch keeps.
			fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: file-guard %q match: %v\n", g.Name, err)
			continue
		}

		for _, e := range events {
			if !isPreFileEvent(e.Kind) {
				continue
			}
			selected, err := fileGuardSelects(match, e, contextMap)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: file-guard %q match on %s: %v\n", g.Name, e.Kind, err)
				continue
			}
			if !selected {
				continue
			}

			// A preventive guard on a Pre UPDATE whose result the engine could not
			// compute cannot verify the file will be fine — newContent is absent
			// (ResultKnown false), so the checks would judge a file whose future
			// content is unknown. Fail CLOSED: refuse the write, because a
			// preventive guard exists precisely to keep the file always-fine and a
			// write it cannot verify must not be admitted. (spec: preventive is
			// best-effort, and PreFileUpdate marks the unknown result absent; a
			// guard that must prevent cannot treat "unknown" as "fine".)
			if e.Kind == declaration.KindPreFileUpdate && !resultKnown(e) {
				return fmt.Sprintf(
					"the %q file-guard is preventive and could not verify this update before it lands: the engine could not compute the result of this write "+
						"(a command-derived change whose outcome is not known ahead of time), so whether the file would still be fine is unknown. "+
						"Refusing: a preventive guard must not admit a write it cannot verify. (file-guard %s)", g.Name, g.Name)
			}

			verdict, err := runner.Run(dispatchcore.Request{
				Nature:         dispatchcore.NatureFileGuard,
				Require:        g.Require,
				Checks:         g.Checks,
				Event:          e,
				TranscriptPath: scope.Transcript,
				Context:        contextMap,
				Dir:            g.Dir,
				GuardName:      g.Name,
				Workspace:      scope.Workspace,
				SessionID:      scope.SessionID,
			})
			if err != nil {
				// The runner itself could not decide. Fail-closed: refuse, naming
				// the guard, the same as the gate dispatch.
				fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: file-guard %q: %v\n", g.Name, err)
				return fmt.Sprintf(
					"the file-guard %q could not be evaluated (%v); refusing because a guard that could not decide must not be read as approval (file-guard %s)",
					g.Name, err, g.Name)
			}
			if verdict.Refused {
				return fmt.Sprintf("%s (file-guard %s)", verdict.Reason, g.Name)
			}
			// This guard passed this file; it does not fire again on another Pre
			// event this dispatch — a preventive guard decides the write in front of
			// it, and the after-check at Stop is the authoritative re-firing one.
			break
		}
	}
	return ""
}

// runFileGuardsPost runs EVERY file-guard against a cycle's POST file events,
// recording each verdict into the revalidation store so a not-fine file re-fires,
// and returns every refusal (in guard-then-file order).
//
// This is the authoritative file-guard check: the file has settled, its content
// is on disk, and the guard judges the settled state. A refusal is collected
// (not returned early) so the agent hears every not-fine file at once, the same
// as the old Post dispatch collects all objections.
//
// # Re-fire integrates with revalidation
//
// The verdict is recorded via rev.Record(guardKey, subject, passed) — the same
// machinery the old format uses. A refusal is retained (revalidation keeps a
// failing FileCheck), so next cycle readdOutstanding re-adds the path to the tree
// difference, a Post event is produced for it again, and this guard is asked
// again — until the content changes and the checks pass. A pass at a fingerprint
// lets the guard SKIP that exact content next cycle (rev.Skip), so a fine file is
// not re-judged every cycle. The guard's revalidation key is namespaced
// (file-guard:<name>) so it never collides with an old-format guardrail of the
// same folder name, keeping each rule's verdicts its own (verdict_per_guardrail).
func runFileGuardsPost(
	cmd *cobra.Command,
	guards []declaration.FileGuard,
	events []event.Event,
	rev *revalidation,
	scope hookScope,
	root string,
	contextMap map[string]natures.ContextState,
) []fileGuardResult {
	if len(guards) == 0 {
		return nil
	}
	runner := dispatchcore.Runner{}
	var results []fileGuardResult

	for _, g := range guards {
		if isLaunchedBy(os.Getenv, g.Name) {
			fmt.Fprintf(cmd.ErrOrStderr(),
				"sloprail: file-guard %q not enforced here — this session was launched by its own check (%s)\n",
				g.Name, LaunchedByEnv)
			continue
		}

		match, err := guardrail.CompileFileMatch(g.Match)
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: file-guard %q match: %v\n", g.Name, err)
			continue
		}

		for _, e := range events {
			if !isPostFileEvent(e.Kind) {
				continue
			}
			selected, err := fileGuardSelects(match, e, contextMap)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: file-guard %q match on %s: %v\n", g.Name, e.Kind, err)
				continue
			}
			if !selected {
				continue
			}

			// What content this guard is about, and whether it has already judged
			// this exact content and passed it. The subject is fingerprinted from
			// the settled file on disk (the Post branch of rev.Subject), so a guard
			// that passed this content once skips it now — a judge is a model call,
			// and asking twice can block work already fixed. Keyed per guard.
			guardKey := fileGuardRevKey(g.Name)
			subj, fingerprinted := rev.Subject(e, root)
			if fingerprinted {
				skip, serr := rev.Skip(guardKey, subj)
				if serr != nil {
					fmt.Fprintln(cmd.ErrOrStderr(), serr)
				}
				if skip {
					continue
				}
			}

			verdict, err := runner.Run(dispatchcore.Request{
				Nature:         dispatchcore.NatureFileGuard,
				Require:        g.Require,
				Checks:         g.Checks,
				Event:          e,
				TranscriptPath: scope.Transcript,
				Context:        contextMap,
				Dir:            g.Dir,
				GuardName:      g.Name,
				Workspace:      scope.Workspace,
				SessionID:      scope.SessionID,
			})
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: file-guard %q: %v\n", g.Name, err)
				results = append(results, fileGuardResult{
					Name:    g.Name,
					Refused: true,
					Reason: fmt.Sprintf(
						"the file-guard %q could not be evaluated (%v); refusing because a guard that could not decide must not be read as approval",
						g.Name, err),
				})
				// Do not record a verdict for a run that did not reach one — writing
				// a pass would exempt content nobody judged, a refusal would blame
				// the rule for the machine. The turn is held; the file re-enters the
				// diff next cycle by the ordinary difference, not by an outstanding
				// refusal this did not record.
				continue
			}

			// Record whichever way it went, so a refusal RESURFACES next cycle and a
			// pass lets the fine content be skipped. This is the entire re-fire
			// mechanism, reusing the old format's file_checks table.
			if fingerprinted {
				if rerr := rev.Record(guardKey, subj, !verdict.Refused); rerr != nil {
					fmt.Fprintln(cmd.ErrOrStderr(), rerr)
				}
			}

			if verdict.Refused {
				results = append(results, fileGuardResult{Name: g.Name, Refused: true, Reason: verdict.Reason})
				// One refusal per (guard, file); keep judging the remaining files so
				// the agent hears every not-fine one at once.
			}
		}
	}
	return results
}

// fileGuardSelects reports whether a file-guard's compiled match selects a file
// event, evaluating the match against the FileMatchScope built from that event
// and the context[] map.
//
// The scope is FLAT (the spec's FileMatchScope): `path`, `markers`, `context` at
// the top level — NOT the event nested under `event` a gate reads. So this builds
// an event.Event whose Fields ARE the scope, and hands it to the compiled
// matcher, which reads those names directly (CompileFileMatch compiles against
// fileMatchScope() with no declared kind, so the env is exactly the fields
// passed, no fill-in). markers is the file's own markers in wire form; context is
// every declared context in the wire form expr can index (`context[<name>].active`).
func fileGuardSelects(match *guardrail.Matcher, e event.Event, contextMap map[string]natures.ContextState) (bool, error) {
	return match.Match(fileMatchScopeEvent(e, contextMap))
}

// fileMatchScopeEvent builds the FileMatchScope an event presents to a
// file-guard's match: the file's path and markers off the event, and the context
// map in wire form, all flat.
//
// markers comes from the event's NEW markers — on a Post event these are the
// settled file's markers (Scan of what is on disk), on a Pre create/update the
// would-be result's. That is the "the markers the file carries" FileMatchScope
// names: for a settled file it is exactly what the file holds; for a preventive
// pre-check it is what the write would leave. The wire form (a list of
// {kind,fqn,line} objects) is what filemod already puts on the event under
// `newMarkers`, reused rather than re-scanned.
func fileMatchScopeEvent(e event.Event, contextMap map[string]natures.ContextState) event.Event {
	fields := map[string]any{
		"path":    e.Fields[filemod.FieldPath],
		"markers": fileMarkers(e),
		"context": contextMatchValue(contextMap),
	}
	return event.Event{Kind: e.Kind, Fields: fields}
}

// fileMarkers returns the markers a file-guard's `markers` scope reads for an
// event, as the wire-form list expr's `any(markers, .kind == …)` quantifies over.
//
// The NEW markers are the file's own settled markers on a Post event and the
// would-be result's on a Pre. A create/update declares `newMarkers`; a delete
// declares none (a deleted file carries no state to guard), so this is an empty
// (non-nil) list there — the same "always a list" discipline the event keeps, so
// `any(markers, …)` evaluates to false rather than erroring on a missing field.
func fileMarkers(e event.Event) []any {
	if v, ok := e.Fields[filemod.FieldNewMarkers].([]any); ok {
		return v
	}
	return []any{}
}

// resultKnown reports whether a PreFileUpdate event's result was computable — the
// `resultKnown` field filemod carries on that kind. Absent or false means the
// engine could not compute the write's outcome (a command-derived update), which
// a preventive guard treats as unverifiable and fails closed on.
func resultKnown(e event.Event) bool {
	v, ok := e.Fields[filemod.FieldResultKnown].(bool)
	return ok && v
}

// fileGuardRevKey namespaces a file-guard's revalidation key so it cannot collide
// with an old-format guardrail of the same folder name.
//
// revalidation keys a FileCheck by (path, guardrail, fingerprint); the "guardrail"
// string here is the file-guard's name. An old-format guardrail and a new-format
// file-guard could both be named `no-secrets`, and pooling their verdicts would
// let one exempt the other's file (breaking verdict_per_guardrail). The prefix
// keeps them apart in the one shared file_checks table.
func fileGuardRevKey(name string) string { return "file-guard:" + name }

// isPreFileEvent reports whether a kind is a PRE file event a preventive guard
// can fire on. A delete is included — a preventive guard may refuse an unasked
// deletion — though its match sees no `newMarkers`.
func isPreFileEvent(kind string) bool {
	switch kind {
	case declaration.KindPreFileCreate, declaration.KindPreFileUpdate, declaration.KindPreFileDelete:
		return true
	}
	return false
}

// isPostFileEvent reports whether a kind is a POST file event a guard's
// after-check fires on.
func isPostFileEvent(kind string) bool {
	switch kind {
	case declaration.KindPostFileCreate, declaration.KindPostFileUpdate, declaration.KindPostFileDelete:
		return true
	}
	return false
}
