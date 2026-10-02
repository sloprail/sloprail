package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/declaration"
	dispatchcore "github.com/sloprail/sloprail/internal/dispatch"
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/module"
	"github.com/sloprail/sloprail/internal/sessionstate"
)

// This file is the pre-tool half of the new nature dispatch: gates on pre-action
// events, and the combined structure gates on file-write paths. It is called from
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
	start := sessionStartOf(store)
	loaded := newNatureDeclarations(cmd, p.Cwd, reg, start)
	grounds := requiresCitation(loaded)
	folders := folderGates(p, reg)
	if len(loaded.Gates) == 0 && len(loaded.Structures) == 0 && len(loaded.Contexts) == 0 && !grounds && len(folders) == 0 {
		// Nothing new-format can act at pre-tool: no gate to block, no structure
		// gate, no context to enter, no other folder's gate. (File-guards act only at Stop.)
		return natureVerdict{}
	}

	// The call before this one has run (or never will), so the cited changes it
	// left pending are settled now — before anything this call records.
	if grounds {
		now := nowNano()
		if err := beginCycle(store, p.Cwd, now, citedPathsOf(loaded.FileGuards), otherMarks(p, scope.Transcript)); err != nil {
			fmt.Fprintln(cmd.ErrOrStderr(), "sloprail:", err)
		}
		if err := settleCitedChanges(store); err != nil {
			fmt.Fprintln(cmd.ErrOrStderr(), "sloprail:", err)
		}
	}

	bound := naturePreToolBoundKinds(loaded)
	for _, f := range folders {
		bound = append(bound, naturePreToolBoundKinds(f.loaded)...)
	}
	if grounds {
		// A rule requires citations, so every change this call makes must be
		// seen here — even for a rule that only judges at Stop, whose Post
		// events carry the citations recorded now.
		bound = append(bound, declaration.KindPreFileCreate, declaration.KindPreFileUpdate,
			declaration.KindPreFileDelete, declaration.KindPreCommandInvoke)
	}
	events := extractPreEvents(cmd, p, reg, bound)
	// Citations are grounded in the session's records: the user pool resolves
	// in the END USER's (root) record only — never a sub-agent's, whose "user"
	// messages are the parent's dispatch — and the tool_result pool in the
	// root's and every sub-agent's, so a sub-agent can cite what its own tools
	// printed. ResolveCitation climbs to the root from whichever record it is
	// handed, so this names the CALLER's own record where it is on disk: for a
	// sub-agent's call, that is what lets a citation that does not resolve say
	// "you are a sub-agent" (transcript.UnresolvedUserHint).
	//
	// A sub-agent's own record is named as such (citeRecord.Subagent): its
	// payload says it is one, so it is never read as a root even when nothing
	// in or around the file says so — the user pool then resolves in the root
	// climbed to from it, or is refused.
	cite := citeRecord{Path: scope.Transcript}
	if fi, err := os.Stat(cite.Path); cite.Path == "" || err != nil || fi.IsDir() {
		cite.Path = p.TranscriptPath
	}
	if cite.Path == "" {
		cite.Path = scope.Transcript
	}
	cite.Subagent = p.IsSubagent() && cite.Path != "" && cite.Path != p.TranscriptPath
	grounded := groundPreEvents(cmd, p, cite, events)
	events = grounded.events

	// The state maps, loaded once so contexts/gates/guards this dispatch runs read
	// one consistent world. Contexts enter FIRST, so a gate whose
	// match/require reads a context sees what the enter left.
	contextMap := loadContextMap(cmd, store, loaded.Contexts)
	gatesMap := loadGatesMap(cmd, store)

	// Context enters on the pre-action events, before anything reads context[].
	// A context does not block; this only populates the map (and persists it).
	runContextEnters(cmd, reg, loaded.Contexts, events, scope, store, contextMap, gatesMap, nil)

	// The structure gates next: a write outside the allowlist is refused before
	// any gate or file-guard is consulted — the cheapest "may you write here at
	// all" question, and a forbidden path should not also pay for a check. Every
	// loaded structure (the project's and each plugin's) is combined by scope.
	// Blocks immediately on a refusal.
	if len(loaded.Structures) > 0 {
		if reason := checkStructureGate(cmd, loaded.Structures, events, p.Root(), reg); reason != "" {
			return natureVerdict{Blocked: reason}
		}
	}

	// Then the gates bound to these pre-events. Every file the call would change
	// is asked about (runGatesForEvents), and the one deny names each refused
	// file; the first refusal of any other kind (a command, a tool) is what blocks.
	if reason := gateRefusal(runGatesForEvents(cmd, reg, loaded.Gates, events, scope, store, contextMap, gatesMap, grounded.notes), events, scope.Workspace); reason != "" {
		return natureVerdict{Blocked: reason}
	}
	// A command that runs in another repository of the session (`git -C ../other commit`,
	// `cd ../other && …`) is also judged by THAT repository's own gates: its rules apply there.
	for _, f := range folders {
		fctx := loadContextMap(quietCmd(), nil, f.loaded.Contexts)
		if reason := gateRefusal(runGatesForEvents(cmd, reg, f.loaded.Gates, events, scope, store, fctx, gatesMap, grounded.notes), events, scope.Workspace); reason != "" {
			return natureVerdict{Blocked: "in " + f.dir + " (a repository this session works in; its own rules apply there): " + reason}
		}
	}
	// Permitted: the cited changes this call makes are pending until the next
	// hook finds them landed (settleCitedChanges).
	if err := recordPending(store, pendingChanges(store, events, p.Root(), grounded.wholes, nowNano())); err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail:", err)
	}
	if grounds {
		if err := noteBackground(store, p); err != nil {
			fmt.Fprintln(cmd.ErrOrStderr(), "sloprail:", err)
		}
	}
	return natureVerdict{}
}

// gateRefusal is the one deny a pre-tool call gets from its gates' results, or ""
// when nothing refused. Refusals about a file are collected across every file the
// call changes and every gate that refused one (preRefusals names each refused
// file in the one deny); the first refusal about the call as a whole (a command, a
// tool, a trigger that could not be decided) is added in the order it was reached.
func gateRefusal(results []gateResult, events []event.Event, workspace string) string {
	refusals := newPreRefusals(events, workspace)
	sawWhole := false
	for _, r := range results {
		if !r.Refused {
			continue
		}
		reason := fmt.Sprintf("%s (gate %s)", r.Reason, r.Attribution)
		if r.Path != "" {
			// The first refusal of a file is the one the agent hears: a second gate
			// refusing the same file adds nothing to fix.
			if !refusals.refused(r.Path) {
				refusals.add(r.Path, reason)
			}
			continue
		}
		if !sawWhole {
			sawWhole = true
			refusals.add("", reason)
		}
	}
	return refusals.render()
}

// checkStructureGate refuses the first file-write event whose target path the
// combined structure gates do not permit, and returns the refusal reason (or "").
//
// Every loaded structure gate — the project's, which covers the whole tree, and
// each plugin's, which covers only its declared scope — is compiled into one
// dispatchcore.StructureSet, and each written path is decided by ownership
// (StructureSet.Decide): a path in two plugins' scopes is an ownership conflict,
// a path in one plugin's scope is that plugin's to decide (the project's deny
// still vetoes), and every other path is the project's.
//
// It is checked against every path a file-write pre-event names — a create or an
// update (a delete does not write NEW content under a path, so it is not gated by
// an allowlist of where writes may go; the spec frames the structure gate as "is
// writing HERE allowed"). The set is built once per dispatch; a compile failure
// (unreachable for loaded structure gates) is reported and treated as permitting,
// since a gate the engine could not compile has not established that any path is
// forbidden — the same "an unloadable rule blocks nothing" the rest of the
// dispatch keeps.
func checkStructureGate(cmd *cobra.Command, structures []declaration.StructureGate, events []event.Event, root string, reg *module.Registry) string {
	compiled, err := dispatchcore.CompileStructureSet(structures)
	if err != nil {
		// Structure gates that loaded but will not compile are a disagreement
		// between the loader and the runtime. Reported, not enforced: refusing on a
		// gate the engine could not build would blame the author for the engine's
		// gap, and this rule blocks nothing until it compiles.
		fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: structure gates could not be compiled, so they are NOT enforced: %v\n", err)
		return ""
	}
	for _, e := range events {
		path, ok := writePath(e)
		if !ok {
			continue
		}
		if outsideProject(root, path) {
			if reason := checkForeignStructure(root, path, reg); reason != "" {
				return reason
			}
			continue
		}
		if allowed, reason := compiled.Decide(path); !allowed {
			return reason
		}
	}
	return ""
}

// outsideProject reports whether a write path lies outside the project tree
// rooted at root. The project's structure governs its own tree, so a path
// outside it is not its to decide: measured in the onboarding eval, where an
// agent's new structure refused its own scratch copy under /tmp, the very place
// the plugin tells agents to keep throwaway files.
//
// A path inside the tree usually arrives relative to the root (`src/app.ts`),
// but not always: with no git root, or a root spelled through a symlink
// (/var vs /private/var on macOS), an in-tree path can stay absolute. So an
// absolute path is outside only when a root is known AND the path, resolved as
// far as it exists, is not under the resolved root. With no root there is no
// tree to be outside of, and the path stays the structure's — refused unless
// allowed, as before.
func outsideProject(root, path string) bool {
	if !filepath.IsAbs(path) {
		clean := filepath.Clean(path)
		return clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator))
	}
	if root == "" {
		return false
	}
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	rel, err := filepath.Rel(root, resolveExistingPrefix(path))
	if err != nil {
		return false
	}
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// checkForeignStructure decides a write outside this project by the structure
// of the project that DOES own the path, if any. Each project's structure
// governs its own tree: this one's has no say over /tmp or a sibling checkout,
// but a sibling checkout with its own .sloprail/ keeps its rules when written
// into from here. A path under no git repository, or under one with no
// declarations, is nobody's to refuse. The owner's declarations load quietly
// (their load report belongs to that project's own sessions); if they cannot
// be read at all the write is refused, since a structure that may exist was
// not consulted.
func checkForeignStructure(root, path string, reg *module.Registry) string {
	if reg == nil {
		return ""
	}
	owner := owningRepo(path)
	if owner == "" {
		return ""
	}
	if real, err := filepath.EvalSymlinks(root); err == nil && real == owner {
		return ""
	}
	if _, err := os.Stat(filepath.Join(owner, ".sloprail")); err != nil {
		return ""
	}
	quiet := &cobra.Command{}
	quiet.SetOut(io.Discard)
	quiet.SetErr(io.Discard)
	store, _ := natureDeclarationStore(quiet, owner)
	loaded, err := store.Load(reg)
	if err != nil {
		return fmt.Sprintf("writing to %q, inside %s: that project's sloprail declarations could not be read, so whether its structure allows this write is unknown. Refusing.", path, owner)
	}
	if len(loaded.Structures) == 0 {
		return ""
	}
	compiled, err := dispatchcore.CompileStructureSet(loaded.Structures)
	if err != nil {
		return ""
	}
	rel, err := filepath.Rel(owner, resolveExistingPrefix(path))
	if err != nil {
		return ""
	}
	if allowed, reason := compiled.Decide(filepath.ToSlash(rel)); !allowed {
		return fmt.Sprintf("in the project at %s (its own structure governs its tree): %s", owner, reason)
	}
	return ""
}

// owningRepo is the resolved root of the git repository containing path — or
// its deepest existing ancestor, for a file not yet created — or "".
func owningRepo(path string) string {
	dir := filepath.Dir(resolveExistingPrefix(path))
	for {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
	r, err := gitrepo.Root(dir)
	if err != nil || r == "" {
		return ""
	}
	if real, err := filepath.EvalSymlinks(r); err == nil {
		return real
	}
	return r
}

// resolveExistingPrefix is dispatchcore.ResolveExistingPrefix under the local
// name this file's own callers already use — see its doc comment for why this
// exists and why it is now shared (internal/dispatch's `{skill, files}`
// prerequisite needed the identical symlink-resolution fix this file already
// carried).
func resolveExistingPrefix(path string) string {
	return dispatchcore.ResolveExistingPrefix(path)
}

// writePath returns the target path of a file-write pre-event, and whether the
// event is one.
//
// Only PreFileCreate and PreFileUpdate are file WRITES to a path the structure
// gate governs. A PreFileDelete removes a path rather than writing content under
// one, so it is not subject to the write-allowlist — the structure gate answers
// "may a write go here", and a delete is not a write. The path is read off the
// event's `path` field, the flat field filemod declares.
func writePath(e event.Event) (string, bool) {
	switch e.Kind {
	case declaration.KindPreFileCreate, declaration.KindPreFileUpdate:
		if p, ok := e.Fields["path"].(string); ok && p != "" {
			return p, true
		}
	}
	return "", false
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
// context's `on` trigger kinds (a context enters on pre events during a cycle).
// A file-guard binds nothing here: it judges the settled file at Stop.
//
// A context's Post triggers are included harmlessly — the pre-tool extraction
// emits no Post event, so a Post-only context contributes a kind no module here
// produces.
func naturePreToolBoundKinds(loaded declaration.Loaded) []string {
	bound := natureBoundKinds(loaded)
	for _, c := range loaded.Contexts {
		for _, trig := range c.On {
			kinds, _ := declaration.ExpandContextEvent(trig.Event)
			bound = append(bound, kinds...)
		}
	}
	return bound
}

// folderRules is a folder of the session and the rules in force there.
type folderRules struct {
	dir    string
	loaded declaration.Loaded
}

// folderGates is each registered folder a Bash call's git commands run in (foldersTargeted)
// that declares a gate, with its rules: the gates that judge the call there besides the
// agent's own tree's.
func folderGates(p HookPayload, reg *module.Registry) []folderRules {
	var out []folderRules
	for _, dir := range foldersTargeted(p) {
		if loaded := newNatureDeclarations(quietCmd(), dir, reg); len(loaded.Gates) > 0 {
			out = append(out, folderRules{dir: dir, loaded: loaded})
		}
	}
	return out
}

// quietCmd is a command whose output goes nowhere: loading another folder's rules reports
// that folder's faults where its own sessions read them, not into this call's hook output.
func quietCmd() *cobra.Command {
	c := &cobra.Command{}
	c.SetOut(io.Discard)
	c.SetErr(io.Discard)
	return c
}
