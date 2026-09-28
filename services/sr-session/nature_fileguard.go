package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/declaration"
	dispatchcore "github.com/sloprail/sloprail/internal/dispatch"
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/filemod"
	"github.com/sloprail/sloprail/internal/guardrail"
	"github.com/sloprail/sloprail/internal/natures"
	"github.com/sloprail/sloprail/internal/transcript"
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

// fileGuardResult is one file-guard's outcome on one file: the guard's name, how a
// refusal should attribute it, whether it refused, and the reason to relay.
//
// Attribution carries the plugin-aware name (bare for a project's guard, plus
// " from plugin X" for a shipped one), so a Stop refusal names where a guard the
// project never wrote lives — the same reason the old format attributes by Origin.
// Name stays for the diagnostics keyed on the bare folder name (the re-entry
// guard, the revalidation key).
type fileGuardResult struct {
	Name        string
	Attribution string
	Refused     bool
	Reason      string
}

// runFileGuardsPreventive runs the PREVENTIVE file-guards against a cycle's PRE
// file events and returns the refusal to block on (or "").
//
// Only guards with `preventive: true` fire here — the default (preventive false)
// is checked only after the write lands, at Stop. A preventive guard whose match
// selects the file runs the check-runner on the Pre event; a refusal blocks the
// write before it lands, via the same deny() the gate and structure paths use.
//
// EVERY file the call changes is asked about, by every preventive guard that
// selects it — not the first. One tool call can change several files (`rm a.go
// b.go`, `sed -i … a b`, two sr-file calls joined by &&), and the call runs
// whole or not at all: a guard that passed the first file and was never asked
// about the second would admit the second's not-fine write before it lands,
// leaving only the Stop after-check to see it. Every refusal is collected, and
// the one deny names each refused file (see preventiveRefusals.render) — a
// pre-tool hook can deny only once, so the agent should hear every file it must
// fix in that one answer. The context[] map is read once and threaded into both
// the match and the Request, so a guard's `match` reading `context[<name>]` and
// its checks reading the same map see one consistent world.
func runFileGuardsPreventive(
	cmd *cobra.Command,
	guards []declaration.FileGuard,
	events []event.Event,
	scope hookScope,
	contextMap map[string]natures.ContextState,
	resolveNote string,
) string {
	runner := dispatchcore.Runner{}
	refusals := newPreventiveRefusals(events, scope.Workspace)

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
			// Unreachable for a loaded guard (the loader compiled the same match), but
			// on the off chance the compile disagrees with load it must fail CLOSED,
			// not decide enforcement silently: a preventive guard whose match the engine
			// cannot even build has not established the write is fine, and admitting it
			// would read the engine's own gap as approval. Refuse, naming the guard and
			// quoting the expression — the same fail-closed direction matcher.go:121
			// takes for a rule that could not be prepared.
			fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: file-guard %q match: %v\n", g.Name, err)
			refusals.add("", fmt.Sprintf(
				"the file-guard %q could not be evaluated: its match %q could not be compiled (%v); "+
					"refusing because a guard that could not decide must not be read as approval (file-guard %s)",
				g.Name, g.Match, err, g.Attribution()))
			continue
		}

		for _, e := range events {
			if !isPreFileEvent(e.Kind) {
				continue
			}
			// The guard's `deletions:` decides whether this kind is its business at
			// all: a PreFileDelete reaches only a guard that includes deletions
			// (include / only), and a create or update never reaches a
			// deletions-only guard. Filtered BEFORE the match, so a guard that does
			// not cover the kind can neither refuse nor fail closed on it.
			if !g.Covers(e.Kind) {
				continue
			}
			selected, err := fileGuardSelects(match, e, contextMap)
			if err != nil {
				// The match COMPILED at load but could not be EVALUATED against this
				// event (e.g. `int(path) > 0` on path "notes.md", or `len(.flags.access)`
				// where the accessor is nil). That is not the guard cleanly declining —
				// it is the engine unable to ANSWER whether this write is fine. Fail
				// CLOSED: refuse the write, the same direction the old dispatch takes
				// (internal/guardrail/matcher.go:121 — the pre-tool path refuses the
				// events the broken rule was bound to) and the sibling runner-error
				// branch below. A preventive guard exists precisely to keep the file
				// always-fine, and a match it cannot evaluate must not be read as
				// approval. The raw expression is quoted so an author can find and fix it.
				fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: file-guard %q match on %s: %v\n", g.Name, e.Kind, err)
				refusals.add(eventPath(e), fmt.Sprintf(
					"the file-guard %q could not be evaluated: its match %q could not be evaluated against this %s (%v); "+
						"refusing because a guard that could not decide must not be read as approval (file-guard %s)",
					g.Name, g.Match, e.Kind, err, g.Attribution()))
				continue
			}
			if !selected {
				continue
			}

			// A preventive guard on a Pre write whose result the engine could NOT
			// compute cannot verify the file will be fine — newContent is empty with
			// ResultKnown false, so the checks would judge a file whose settled
			// content is unknown. Fail CLOSED: refuse the write, because a preventive
			// guard exists precisely to keep the file always-fine and a write it
			// cannot verify must not be admitted. (spec: preventive is best-effort; a
			// guard that must prevent cannot treat "unknown" as "fine".)
			//
			// BOTH the update and the create case, not update alone. A command-derived
			// PreFileUpdate marks its result unknown — the case this originally
			// covered. But a NotebookEdit creating a fresh .ipynb emits a
			// PreFileCreate with an empty newContent whose bytes are NOT derivable
			// (its cell source is not the document); resultKnown is now carried on the
			// create kind for exactly this reason, and false there means the same
			// "unknown result" it means on an update. Checking only the update let that
			// create through — the preventive guard false-passed and the write landed
			// transiently (the Stop after-check still caught it, but the preventive
			// GUARANTEE was silently lost). A derivable create (a stated body,
			// including a genuinely-empty one) has resultKnown true and is judged
			// normally, so this refuses only the truly-underivable write.
			if isUnderivablePreWrite(e) {
				refusals.add(eventPath(e), underivableRefusal(cmd, runner, g, e, scope, contextMap, resolveNote))
				continue
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
				// Re-entry provenance for a preventive check that spawns sr-agent:
				// this guard appended to any launched checks already on the stack, so
				// the launched agent's own Write does not re-fire this guard on itself
				// (isLaunchedBy above, one exec down). See the gate dispatch.
				LaunchedBy: appendLaunchedBy(os.Getenv, g.Name),
			})
			if err != nil {
				// The runner itself could not decide. Fail-closed: refuse, naming
				// the guard, the same as the gate dispatch.
				fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: file-guard %s: %v\n", g.Attribution(), err)
				refusals.add(eventPath(e), fmt.Sprintf(
					"the file-guard %q could not be evaluated (%v); refusing because a guard that could not decide must not be read as approval (file-guard %s)",
					g.Name, err, g.Attribution()))
				continue
			}
			if verdict.Refused {
				refusals.add(eventPath(e), fmt.Sprintf("%s (file-guard %s)", verdict.Reason, g.Attribution()))
			}
			// Passed or refused, go on to the call's next file: this guard is asked
			// about every file the call would change, not only the first it selects.
		}
	}
	return refusals.render()
}

// underivableRefusal is the refusal a preventive guard gives a Pre write whose
// result the engine could NOT compute (isUnderivablePreWrite) — the guard cannot
// verify the file will be fine, so it fails CLOSED. Always a refusal; what it
// returns is the most useful reason for it.
//
// require FIRST, even here. `{skill}`/`{context}` need no content at all — they
// read the trajectory, not the write — so when BOTH a missing prerequisite and an
// unverifiable write are true of this event, the missing prerequisite is the
// reason worth giving: it names exactly what to fix ("load the skill"), where
// "could not verify this write" is true of a Bash-derived write to this path
// REGARDLESS of the skill, and does not tell the agent what would have made it
// pass. This was the smoke test's P2 finding — a raw Bash write refused for the
// generic content-unverifiable reason even though require was the rule that
// actually applied. Same fail-closed direction either way: a missing require
// still refuses, so this is a message improvement, not a change in what is
// enforced.
func underivableRefusal(
	cmd *cobra.Command,
	runner dispatchcore.Runner,
	g declaration.FileGuard,
	e event.Event,
	scope hookScope,
	contextMap map[string]natures.ContextState,
	resolveNote string,
) string {
	reqReq := dispatchcore.Request{
		Nature:         dispatchcore.NatureFileGuard,
		Require:        g.Require,
		Event:          e,
		TranscriptPath: scope.Transcript,
		Context:        contextMap,
		// A prerequisite's `when` script runs from the guard's folder.
		Dir:        g.Dir,
		GuardName:  g.Name,
		Workspace:  scope.Workspace,
		SessionID:  scope.SessionID,
		LaunchedBy: appendLaunchedBy(os.Getenv, g.Name),
	}
	if v, err := runner.CheckRequire(reqReq); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: file-guard %s require: %v\n", g.Attribution(), err)
		return fmt.Sprintf(
			"the file-guard %q could not be evaluated (%v); refusing because a guard that could not decide must not be read as approval (file-guard %s)",
			g.Name, err, g.Attribution())
	} else if v.Refused {
		if resolveNote != "" {
			// A citation the rule required did not resolve, and sr-file
			// said which one and why — worth more than "cites nothing".
			// Its words already carry the sub-agent advice when it applies.
			reason := v.Reason
			if strings.Contains(resolveNote, transcript.SubagentUserAdvice) {
				reason = strings.TrimSuffix(reason, "\n"+transcript.SubagentUserAdvice)
			}
			return fmt.Sprintf("%s sr-file said:\n%s\n(file-guard %s)", reason, resolveNote, g.Attribution())
		}
		return fmt.Sprintf("%s (file-guard %s)", v.Reason, g.Attribution())
	}
	if resolveNote != "" {
		// The line was pure sr-file and its dry run failed: sr-file's
		// own reason is the cause, and the generic one below would
		// only send the agent guessing.
		return fmt.Sprintf(
			"the %q file-guard is preventive and could not verify this write before it lands: sr-file could not compute the change, and said:\n%s\n"+
				"Refusing: a preventive guard must not admit a write it cannot verify. Fix what sr-file named and run it again; "+
				"to check a quote on its own: `sr-session trajectory cite '<quote>'`. (file-guard %s)",
			g.Name, resolveNote, g.Attribution())
	}
	return fmt.Sprintf(
		"the %q file-guard is preventive and could not verify this write before it lands: the engine could not compute the result of this %s "+
			"(a change whose settled bytes are not known ahead of time — a command-derived edit, or a notebook create whose cell source is not the document), "+
			"so whether the file would still be fine is unknown. "+
			"Refusing: a preventive guard must not admit a write it cannot verify. "+
			"Write the file's content directly, or make the change with sr-file ON ITS OWN in the command (nothing else in the line but sr-file calls, && and echo; no cd, no VAR= prefix, no $ expansion — quote every value verbatim) "+
			"so its result is computed before it runs — and check that each --cite: quote resolves to exactly one message: `sr-session trajectory cite '<quote>'`. (file-guard %s)",
		g.Name, underivableKindNoun(e.Kind), g.Attribution())
}

// preventiveRefusals collects every refusal the preventive guards give one tool
// call, keyed by the file each is about, and renders them as the call's one deny.
//
// Why one deny names every file: a pre-tool hook answers once per call, and a
// call that changes several files is refused whole. Naming only the first
// refused file sends the agent back to fix that one, run again, and be refused
// for the next — as many turns as there are refused files — or, worse, to guess
// the call was refused for the file it does not know about. So each refused file
// is named, beside the reason it was refused for.
//
// The same reason given for several files (`sed -i` over three files, each an
// update the engine cannot compute) is said once, followed by every file it
// applies to, rather than repeated per file.
type preventiveRefusals struct {
	// multiFile is true when the call changes more than one file. Only then is a
	// refusal prefixed with the file it is about: a single-file call's refusal
	// names its file by being about the only one, and keeps the wording it had.
	multiFile bool
	workspace string
	entries   []preventiveRefusal
}

// preventiveRefusal is one distinct refusal reason and the files it was given for,
// in the order they were refused.
type preventiveRefusal struct {
	reason string
	paths  []string
}

// newPreventiveRefusals starts an empty collection for a call whose Pre events
// are events. It counts the distinct files the call changes, which decides
// whether each refusal must name its file.
func newPreventiveRefusals(events []event.Event, workspace string) *preventiveRefusals {
	files := map[string]bool{}
	for _, e := range events {
		if isPreFileEvent(e.Kind) {
			if p := eventPath(e); p != "" {
				files[displayPath(p, workspace)] = true
			}
		}
	}
	return &preventiveRefusals{multiFile: len(files) > 1, workspace: workspace}
}

// add records a refusal about path ("" for one that is about the guard itself,
// such as a match that does not compile, rather than about any one file).
func (r *preventiveRefusals) add(path, reason string) {
	if path != "" {
		path = displayPath(path, r.workspace)
	}
	for i := range r.entries {
		if r.entries[i].reason == reason {
			if path != "" && !containsString(r.entries[i].paths, path) {
				r.entries[i].paths = append(r.entries[i].paths, path)
			}
			return
		}
	}
	entry := preventiveRefusal{reason: reason}
	if path != "" {
		entry.paths = []string{path}
	}
	r.entries = append(r.entries, entry)
}

// render is the call's deny text, or "" when nothing refused.
//
// One refusal of a single-file call is its reason exactly as before. Anything
// more — several refusals, or any refusal of a call that changes several files —
// is a list, one line per distinct reason, each naming the files it refused.
func (r *preventiveRefusals) render() string {
	if len(r.entries) == 0 {
		return ""
	}
	if len(r.entries) == 1 && !r.multiFile {
		return r.entries[0].reason
	}
	var refused []string
	lines := make([]string, 0, len(r.entries))
	for _, entry := range r.entries {
		line := entry.reason
		if r.multiFile && len(entry.paths) > 0 {
			line = strings.Join(entry.paths, ", ") + ": " + entry.reason
		}
		for _, p := range entry.paths {
			if !containsString(refused, p) {
				refused = append(refused, p)
			}
		}
		lines = append(lines, line)
	}
	head := "this call was refused before it ran"
	if r.multiFile && len(refused) > 0 {
		head += "; every file it would change was checked, and the file-guards refused " + strings.Join(refused, ", ")
	}
	return head + ":\n  - " + strings.Join(lines, "\n  - ")
}

// eventPath is the file a file event is about, as the event spells it.
func eventPath(e event.Event) string {
	p, _ := e.Fields[filemod.FieldPath].(string)
	return p
}

// displayPath is path as the agent would name it: relative to the workspace when
// it lies inside it, as given otherwise.
func displayPath(path, workspace string) string {
	if workspace == "" || !filepath.IsAbs(path) {
		return path
	}
	if rel, err := filepath.Rel(workspace, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return rel
	}
	return path
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
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
			// Unreachable for a loaded guard (the loader compiled the same match), but
			// on the off chance the compile disagrees with load it must fail CLOSED
			// rather than skip the guard silently: a match the engine cannot build has
			// not decided the file is fine. Collect a refusal that holds the turn
			// (matcher.go:186), naming the guard and quoting the expression.
			fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: file-guard %q match: %v\n", g.Name, err)
			results = append(results, fileGuardResult{
				Name:        g.Name,
				Attribution: g.Attribution(),
				Refused:     true,
				Reason: fmt.Sprintf(
					"the file-guard %q could not be evaluated: its match %q could not be compiled (%v); "+
						"refusing because a guard that could not decide must not be read as approval",
					g.Name, g.Match, err),
			})
			continue
		}

		for _, e := range events {
			if !isPostFileEvent(e.Kind) {
				continue
			}
			if !g.Covers(e.Kind) {
				// Not this guard's business (see declaration.FileGuard.Covers): a
				// PostFileDelete for a guard that skips deletions, or a create/update
				// for a deletions-only guard. On a delete the file is gone, and a
				// refusal this guard left outstanding on it could never be cleared —
				// it will never be asked about this path again — so settle it.
				settleIfGone(cmd, rev, g, e)
				continue
			}
			selected, err := fileGuardSelects(match, e, contextMap)
			if err != nil {
				// The match COMPILED at load but could not be EVALUATED against this
				// settled file. As at the preventive path and in the old dispatch
				// (matcher.go:186 — the caller refuses the action and says why), a match
				// the engine cannot answer is NOT a rule that cleanly did not match: it
				// is the engine unable to decide, which must not be read as approval.
				// This path COLLECTS refusals (it does not return early), so append a
				// refusal that holds the turn — mirroring the runner-error branch below.
				// A verdict is deliberately NOT recorded: writing a pass would exempt a
				// file nobody judged, a refusal would blame the rule for the machine; the
				// turn is held and the file re-enters the diff next cycle by the ordinary
				// difference. The raw expression is quoted so an author can fix it.
				fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: file-guard %q match on %s: %v\n", g.Name, e.Kind, err)
				results = append(results, fileGuardResult{
					Name:        g.Name,
					Attribution: g.Attribution(),
					Refused:     true,
					Reason: fmt.Sprintf(
						"the file-guard %q could not be evaluated: its match %q could not be evaluated against this %s (%v); "+
							"refusing because a guard that could not decide must not be read as approval",
						g.Name, g.Match, e.Kind, err),
				})
				continue
			}
			if !selected {
				// A deleted file this guard's match no longer selects (a marker or
				// context match can stop selecting once the content is gone) is
				// likewise one it will never be asked about again.
				settleIfGone(cmd, rev, g, e)
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
				// Re-entry provenance for an after-check that spawns sr-agent: this
				// guard appended to any launched checks already on the stack, so the
				// launched agent's own Write does not re-fire this guard on itself
				// (isLaunchedBy above, one exec down). See the gate dispatch.
				LaunchedBy: appendLaunchedBy(os.Getenv, g.Name),
			})
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: file-guard %s: %v\n", g.Attribution(), err)
				results = append(results, fileGuardResult{
					Name:        g.Name,
					Attribution: g.Attribution(),
					Refused:     true,
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
				results = append(results, fileGuardResult{Name: g.Name, Attribution: g.Attribution(), Refused: true, Reason: verdict.Reason})
				// One refusal per (guard, file); keep judging the remaining files so
				// the agent hears every not-fine one at once.
				continue
			}

			// A guard that covers deletions and PASSED this delete has judged the
			// file's last state — gone — and found it fine. A refusal it left on the
			// content that preceded the delete is answered, so it ends here rather
			// than re-adding the path, and re-asking about the delete, every cycle.
			settleIfGone(cmd, rev, g, e)
		}
	}
	return results
}

// settleIfGone ends a file-guard's outstanding refusal on a file that is now
// DELETED, when the guard has not refused the delete itself — it does not cover
// deletions, its match no longer selects the file, or it judged the delete and
// passed it. A no-op on any other kind.
//
// Why this is needed. A refusal is outstanding while the latest verdict for
// (path, guard) is a refusal, and readdOutstanding re-adds every outstanding
// path to the tree difference each cycle; a re-added path that is no longer on
// disk comes back as a PostFileDelete. A delete has no content to fingerprint
// (revalidation.Subject), so nothing on the delete path ever RECORDS a verdict
// — the refusal on the pre-delete content would stay outstanding for the rest
// of the session, re-adding a path that no longer exists (and handing a
// PostFileDelete to every guard that includes deletions) every cycle, with no
// action the agent could take to clear it. For a guard that does not even see
// deletions (the default) that is a refusal it can never clear by construction.
//
// A delete this guard REFUSED is left alone: the refusal is the guard's live
// answer, and it must keep re-firing until the file is back and fine. A run
// that errored is left alone too — nobody judged anything.
func settleIfGone(cmd *cobra.Command, rev *revalidation, g declaration.FileGuard, e event.Event) {
	if e.Kind != declaration.KindPostFileDelete {
		return
	}
	path, _ := e.Fields[filemod.FieldPath].(string)
	if err := rev.SettleGone(fileGuardRevKey(g.Name), path); err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), err)
	}
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
// `newMarkers`, reused rather than re-scanned. On a delete, which has no result,
// it is the markers the file carried — see fileMarkers.
func fileMatchScopeEvent(e event.Event, contextMap map[string]natures.ContextState) event.Event {
	fields := map[string]any{
		"path":       e.Fields[filemod.FieldPath],
		"markers":    fileMarkers(e),
		"oldMarkers": fileOldMarkers(e),
		"context":    contextMatchValue(contextMap),
	}
	return event.Event{Kind: e.Kind, Fields: fields}
}

// fileMarkers returns the markers a file-guard's `markers` scope reads for an
// event, as the wire-form list expr's `any(markers, .kind == …)` quantifies over.
//
// would-be result's on a Pre. A create/update declares `newMarkers`.
//
// A delete declares no `newMarkers` — nothing remains to carry any — and only
// reaches a guard that opted into deletions (`deletions: include` / `only`).
// For that guard the file it is being asked about is the one being removed, so
// its `markers` are the markers that file CARRIED: `oldMarkers`. Without this a
// marker-scoped guard (`any(markers, .kind == "invariant")`) that includes
// deletions would never select a delete at all, whatever the lost file held.
// Chosen by KIND, not by the absence of `newMarkers`: an update whose result
// carries no markers must read as marker-less, not fall back to the ones it
// just removed.
//
// Either way the result is a list, never nil — the same "always a list"
// discipline the event keeps, so `any(markers, …)` evaluates to false rather
// than erroring on a missing field.
func fileMarkers(e event.Event) []any {
	field := filemod.FieldNewMarkers
	if declaration.IsFileDeleteKind(e.Kind) {
		field = filemod.FieldOldMarkers
	}
	if v, ok := e.Fields[field].([]any); ok {
		return v
	}
	if v, ok := e.Fields[filemod.FieldOldMarkers].([]any); ok {
		return v
	}
	return []any{}
}

// fileOldMarkers returns the markers the file carried before this change — the
// scope's `oldMarkers`: from disk on a Pre update or delete, from the session's
// baseline on a Post one, and none on a create, which nothing preceded. Always a
// list, never nil, like fileMarkers. It is what lets a marker-scoped guard see a
// write that REMOVES a marker: `markers` alone reads that write as a file with
// none, and the guard never selects it.
func fileOldMarkers(e event.Event) []any {
	if v, ok := e.Fields[filemod.FieldOldMarkers].([]any); ok {
		return v
	}
	return []any{}
}

// resultKnown reports whether a Pre write event's result was computable — the
// `resultKnown` field filemod carries on the two Pre kinds whose result can
// arrive either way (PreFileUpdate and, since the notebook-create fix,
// PreFileCreate). Absent or false means the engine could not compute the write's
// outcome (a command-derived update, or a notebook create whose cell source is
// not the document), which a preventive guard treats as unverifiable and fails
// closed on. On a kind that does not carry the field, the value is absent and
// this returns false — which is why isUnderivablePreWrite gates on the kind
// first, so a delete (no result, no field) is not mistaken for an unknown one.
func resultKnown(e event.Event) bool {
	v, ok := e.Fields[filemod.FieldResultKnown].(bool)
	return ok && v
}

// isUnderivablePreWrite reports whether a Pre event is a create or update whose
// result the engine could NOT derive — the case a preventive guard must fail
// closed on, because it cannot verify a file whose settled bytes are unknown.
//
// Gated on the kind so it fires ONLY where resultKnown is a meaningful signal: a
// create or an update. A delete carries no result and no `resultKnown` field, so
// it is never "underivable" in this sense — a preventive guard on a deletion
// judges the bytes about to be lost, which are known. Both the create and the
// update case are covered (the create was the silently-lost one: a NotebookEdit
// fresh-.ipynb PreFileCreate marks resultKnown false, and checking only the
// update let it false-pass).
func isUnderivablePreWrite(e event.Event) bool {
	switch e.Kind {
	case declaration.KindPreFileCreate, declaration.KindPreFileUpdate:
		return !resultKnown(e)
	default:
		return false
	}
}

// underivableKindNoun is the word for what an underivable Pre write is, for the
// refusal message — "create" or "update", so the agent hears which write could
// not be verified rather than a generic "write".
func underivableKindNoun(kind string) string {
	if kind == declaration.KindPreFileCreate {
		return "create"
	}
	return "update"
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
// can fire on. A delete is included — a preventive guard with `deletions:
// include` or `only` may refuse an unasked deletion — and whether a particular
// guard sees it is FileGuard.Covers' decision, applied next to this one.
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
