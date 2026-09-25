package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/declaration"
	dispatchcore "github.com/sloprail/sloprail/internal/dispatch"
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/module"
	"github.com/sloprail/sloprail/internal/sessionstate"
)

// This file is the pre-tool half of the new nature dispatch: gates on pre-action
// events, and the structure gate on file-write paths. It is called from
// runSessionPreTool BEFORE the old-format dispatch, so both run and a refusal from
// either blocks the tool call.
//
// The return convention matches the old path: it returns an error to end the hook
// command (a deny already written to stdout), or nil to permit. A caller that gets
// a non-nil error stops — the deny has been emitted — exactly as the old dispatch's
// `return deny(...)` does.

// natureVerdict is the outcome of a new-format pre-tool dispatch: the first
// blocking refusal to deny on, if any.
type natureVerdict struct {
	// Blocked is the reason to deny, or "" to permit.
	Blocked string
}

// dispatchNaturePreTool runs the new-format gate and structure-gate dispatch for a
// pre-tool hook, and reports the first refusal to block on (or "" to permit).
//
// It extracts the pre-action events itself from the payload — the same modules the
// old path drives, so a Write produces a PreFileCreate and a Bash a
// PreCommandInvoke — because the new dispatch decides what to enforce from those
// events independently of the old one. Sharing the old path's already-extracted
// events would couple the two dispatches; extracting again is cheap next to a
// model call and keeps the new dispatch's inputs its own.
//
// The FIRST refusal (structure gate checked before gates, then gates in name
// order) is what blocks — a pre-tool hook can only deny once, and denying on the
// first refusal is the same shape the old path takes.
func dispatchNaturePreTool(cmd *cobra.Command, p HookPayload, reg *module.Registry, scope hookScope, store sessionstate.Store) natureVerdict {
	loaded := newNatureDeclarations(cmd, p.Cwd, reg)
	preventiveGuards := preventiveFileGuards(loaded.FileGuards)
	if len(loaded.Gates) == 0 && len(loaded.Structures) == 0 && len(loaded.Contexts) == 0 && len(preventiveGuards) == 0 {
		// Nothing new-format can act at pre-tool: no gate to block, no structure
		// gate, no context to enter, no preventive file-guard to pre-check.
		// (Non-preventive file-guards act only at Stop.)
		return natureVerdict{}
	}

	events := extractPreEvents(cmd, p, reg, naturePreToolBoundKinds(loaded))

	// The state maps, loaded once so contexts/gates/guards this dispatch runs read
	// one consistent world. Contexts enter FIRST, so a gate or a preventive
	// file-guard whose match/require reads a context sees what the enter left.
	contextMap := loadContextMap(cmd, store, loaded.Contexts)
	gatesMap := loadGatesMap(cmd, store)

	// Context enters on the pre-action events, before anything reads context[].
	// A context does not block; this only populates the map (and persists it).
	runContextEnters(cmd, reg, loaded.Contexts, events, scope, store, contextMap, gatesMap)

	// The structure gate next: a write outside the allowlist is refused before any
	// gate or file-guard is consulted — the cheapest "may you write here at all"
	// question, and a forbidden path should not also pay for a check. Blocks
	// immediately on a refusal.
	if len(loaded.Structures) > 0 {
		if reason := checkStructureGate(cmd, loaded.Structures, events); reason != "" {
			return natureVerdict{Blocked: reason}
		}
	}

	// Preventive file-guards next: a `preventive: true` guard whose match selects a
	// pre-write file refuses a not-fine write before it lands. Blocks on the first
	// refusal.
	if reason := runFileGuardsPreventive(cmd, preventiveGuards, events, scope, contextMap); reason != "" {
		return natureVerdict{Blocked: reason}
	}

	// Then the gates bound to these pre-events. The first that refuses blocks.
	results := runGatesForEvents(cmd, reg, loaded.Gates, events, scope, store, contextMap, gatesMap)
	for _, r := range results {
		if r.Refused {
			return natureVerdict{Blocked: fmt.Sprintf("%s (gate %s)", r.Reason, r.Attribution)}
		}
	}
	return natureVerdict{}
}

// preventiveFileGuards filters the loaded file-guards to the preventive ones — the
// only file-guards that act at pre-tool. A small helper so the empty-work check
// and the dispatch read the same set.
func preventiveFileGuards(guards []declaration.FileGuard) []declaration.FileGuard {
	var out []declaration.FileGuard
	for _, g := range guards {
		if g.Preventive {
			out = append(out, g)
		}
	}
	return out
}

// checkStructureGate refuses the first file-write event whose target path the
// COMPOSED structure gate does not allow, and returns the refusal reason (or
// "").
//
// Every loaded structure gate — the project's own and each enabled plugin's —
// composes into one decision per path (dispatchcore.CompileStructureGates), so
// this checks each write against all of them at once rather than one at a time.
// It is checked against every path a file-write pre-event names — a create or an
// update (a delete does not write NEW content under a path, so it is not gated
// by an allowlist of where writes may go; the spec frames the structure gate as
// "is writing HERE allowed"). The compiled gate is built once per dispatch; a
// compile failure (unreachable for loaded structure gates) is reported and
// treated as permitting, since a gate the engine could not compile has not
// established that any path is forbidden — the same "an unloadable rule blocks
// nothing" the rest of the dispatch keeps.
func checkStructureGate(cmd *cobra.Command, sgs []declaration.StructureGate, events []event.Event) string {
	compiled, err := dispatchcore.CompileStructureGates(sgs)
	if err != nil {
		// A structure gate that loaded but will not compile is a disagreement
		// between the loader and the runtime. Reported, not enforced: refusing on a
		// gate the engine could not build would blame the author for the engine's
		// gap, and this rule blocks nothing until it compiles.
		fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: structure gate could not be compiled, so it is NOT enforced: %v\n", err)
		return ""
	}
	for _, e := range events {
		path, ok := writePath(e)
		if !ok {
			continue
		}
		if allowed, reason := compiled.Allows(path); !allowed {
			return reason
		}
	}
	return ""
}

// writePath returns the target path of a file-write pre-event, and whether the
// event is one the structure gate has any business judging.
//
// Only PreFileCreate and PreFileUpdate are file WRITES to a path the structure
// gate governs. A PreFileDelete removes a path rather than writing content under
// one, so it is not subject to the write-allowlist — the structure gate answers
// "may a write go here", and a delete is not a write. The path is read off the
// event's `path` field, the flat field filemod declares.
//
// A path OUTSIDE the project root is excluded here, before any gate's scope is
// even consulted — the structure gate (project's own or any plugin's) has NO
// OPINION on a path that does not resolve inside the project's own tree.
//
// # Why this matters
//
// filemod's reportable() (internal/filemod/extract.go) already canonicalises
// every in-workspace path to be workspace-relative, and — deliberately — leaves
// an OUT-of-workspace path in a spelling no project-relative matcher can admit:
// absolute, or a lexically-outside relative spelling (`.`, `..`, or a path
// starting `../`). That guarantee is what a NARROWED rule (`path startsWith
// "secret/"`) relies on to never accidentally admit an outside path. The
// structure gate's OWN allow entries are also project-relative globs/regexes
// authored the same way (`memories/**`, never `/Users/...`), so they were
// already unable to ADMIT an outside path — but deny-by-default means an
// unmatched path is REFUSED, not ignored, and a structure gate's refusal had no
// such exemption: a session whose project root is one repo but whose agent (or a
// hook it launched) writes to an absolute path in a SIBLING repo was refused by
// a structure gate that was never meant to have an opinion about that sibling
// tree at all. isOutsideProject makes that refusal impossible at the source: the
// structure gate is asked about a path only when that path is actually one it
// could be authored against.
func writePath(e event.Event) (string, bool) {
	switch e.Kind {
	case declaration.KindPreFileCreate, declaration.KindPreFileUpdate:
		p, ok := e.Fields["path"].(string)
		if !ok || p == "" {
			return "", false
		}
		if isOutsideProject(p) {
			return "", false
		}
		return p, true
	}
	return "", false
}

// isOutsideProject reports whether a reported path names something outside the
// project's own tree — the spelling filemod.reportable() leaves an out-of-
// workspace path in: absolute, or a relative path that lexically climbs out
// (`.`, `..`, or starting with `../`).
//
// A path already inside the project is ALWAYS reported relative and cleaned
// (see filemod's reportable doc), so any of these spellings is conclusive: no
// in-project path is ever reported this way. Checked lexically rather than by
// re-resolving against the project root, because reportable() already did that
// resolution once (including the symlink-escape and macOS /tmp-vs-/private/tmp
// cases) and its OUTPUT spelling is the one signal this needs — re-deriving it
// here would be a second, potentially disagreeing, containment check.
func isOutsideProject(path string) bool {
	if filepath.IsAbs(path) {
		return true
	}
	return path == "." || path == ".." || strings.HasPrefix(path, "../")
}

// extractPreEvents produces the pre-action events for the new dispatch from the
// hook payload, using the same modules the old path uses.
//
// Only the modules something new-format actually binds to are asked — the same
// economy the old path keeps (reg.Needed), for the same reason: producing an event
// nobody bound to is work done to be discarded, and finding the programs a command
// runs is not free. The bound kinds come from the loaded gates and structure gate.
//
// A module returning events ALONGSIDE an error has its events kept and the error
// reported, the module contract the old path also honours — one path a module
// could not classify must not drop the events it did produce.
func extractPreEvents(cmd *cobra.Command, p HookPayload, reg *module.Registry, bound []string) []event.Event {
	in := module.Input{
		module.InputPhase:   module.PhasePre,
		module.InputPayload: p,
	}
	var events []event.Event
	for _, m := range reg.Needed(bound) {
		evs, err := m.Extract(in)
		events = append(events, evs...)
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: module %q: %v\n", m.Name(), err)
		}
	}
	return events
}

// natureBoundKinds is every event kind the new-format declarations at a pre-tool
// hook actually ask about — the union of each gate's `on` event kinds (with the
// PreFileWrite alias expanded to its concrete pair) plus the structure gate's two
// write kinds.
//
// A gate's `on` carries the event string the author wrote — the loader validates
// but does not rewrite it — so a `PreFileWrite` trigger is expanded here through
// declaration.ExpandGateEvent to the PreFileCreate/PreFileUpdate the modules
// actually emit; otherwise reg.Needed would be asked for a kind no module produces
// and no events would be extracted. Stop is included when a gate names it,
// harmlessly — the pre-tool extraction produces no Stop, so a Stop-only gate simply
// contributes a kind no module here emits. The structure gate binds the two
// file-write kinds so its paths are extracted even when no gate names them.
func natureBoundKinds(loaded declaration.Loaded) []string {
	var bound []string
	for _, g := range loaded.Gates {
		for _, trig := range g.On {
			kinds, _ := declaration.ExpandGateEvent(trig.Event)
			bound = append(bound, kinds...)
		}
	}
	if len(loaded.Structures) > 0 {
		bound = append(bound, declaration.KindPreFileCreate, declaration.KindPreFileUpdate)
	}
	return bound
}

// naturePreToolBoundKinds is every event kind the new-format declarations ask
// about AT PRE-TOOL — the gate/structure kinds (natureBoundKinds) plus each
// context's `on` trigger kinds (a context enters on pre events during a cycle)
// and each preventive file-guard's file-write kinds (a preventive guard fires on
// the pre write).
//
// A context's Post triggers are included harmlessly — the pre-tool extraction
// emits no Post event, so a Post-only context contributes a kind no module here
// produces. A preventive file-guard is bound to a file's STATE, not an event, but
// it fires on the PRE file events, so the ones it covers are bound so those
// events are extracted for it to match against — create/update unless it is
// `deletions: only`, and the delete only when its `deletions:` includes it
// (FileGuard.Covers, the same filter the dispatch applies).
func naturePreToolBoundKinds(loaded declaration.Loaded) []string {
	bound := natureBoundKinds(loaded)
	for _, c := range loaded.Contexts {
		for _, trig := range c.On {
			kinds, _ := declaration.ExpandContextEvent(trig.Event)
			bound = append(bound, kinds...)
		}
	}
	for _, g := range loaded.FileGuards {
		if !g.Preventive {
			continue
		}
		for _, k := range []string{declaration.KindPreFileCreate, declaration.KindPreFileUpdate, declaration.KindPreFileDelete} {
			if g.Covers(k) {
				bound = append(bound, k)
			}
		}
	}
	return bound
}
