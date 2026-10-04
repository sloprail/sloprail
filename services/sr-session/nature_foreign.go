package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/commandmod"
	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/module"
	"github.com/sloprail/sloprail/internal/natures"
	"github.com/sloprail/sloprail/internal/sessionstate"
)

// dispatchNaturePreTool runs the session project's own gates and structure
// (dispatchOwnNaturePreTool), then the gates of every OTHER git repository the call
// targets. Each project's gates govern its own tree, so writing from project A into
// sibling repo B (a Write, an Edit, a delete, or a Bash command that writes there or
// runs in B: `git -C B commit`, `cd B && ...`) is judged by B's own gates too.
func dispatchNaturePreTool(cmd *cobra.Command, p HookPayload, reg *module.Registry, scope hookScope, store sessionstate.Store) natureVerdict {
	if v := dispatchOwnNaturePreTool(cmd, p, reg, scope, store); v.Blocked != "" {
		return v
	}
	return dispatchForeignGates(cmd, p, reg, scope)
}

// dispatchForeignGates decides a call by the direct gates of each foreign repo it
// targets. Per repo it costs one stat of `<root>/.sloprail` (no cache); a repo without
// one is nobody's to refuse. A `.sloprail` that cannot be loaded refuses: a gate that
// may exist was not consulted. Only the repo's own declarations load — its plugins are
// not applied (see the issue "Foreign repo plugins are not loaded at pre-tool").
//
// The repo is the workspace its gates run in, and the paths its gates see are relative
// to it. Its verdicts and contexts stay out of the session's state: a gate name may
// collide with one of the session's.
func dispatchForeignGates(cmd *cobra.Command, p HookPayload, reg *module.Registry, scope hookScope) natureVerdict {
	if reg == nil {
		return natureVerdict{}
	}
	switch p.ToolName {
	case "Bash", "Write", "Edit", "MultiEdit", "NotebookEdit":
	default:
		return natureVerdict{}
	}
	own := p.Root()
	if real, err := filepath.EvalSymlinks(own); err == nil {
		own = real
	}
	fileEvents := extractPreEvents(cmd, p, reg, []string{
		declaration.KindPreFileCreate, declaration.KindPreFileUpdate, declaration.KindPreFileDelete})

	var roots []string
	seen := map[string]bool{}
	add := func(owner string) {
		if owner != "" && owner != own && !seen[owner] {
			seen[owner] = true
			roots = append(roots, owner)
		}
	}
	for _, e := range fileEvents {
		if path := eventPath(e); path != "" {
			add(owningRepo(absEventPath(p, path)))
		}
	}
	for _, dir := range commandTargetDirs(p) {
		add(owningRepo(filepath.Join(dir, "x")))
	}

	for _, root := range roots {
		if _, err := os.Stat(filepath.Join(root, ".sloprail")); err != nil {
			continue
		}
		loaded, err := declaration.New(filepath.Join(root, ".sloprail")).Load(reg)
		if err != nil {
			return natureVerdict{Blocked: fmt.Sprintf("this call targets the project at %s, whose sloprail declarations could not be read, so whether its gates allow it is unknown. Refusing.", root)}
		}
		if len(loaded.Gates) == 0 {
			continue
		}
		var bound []string
		for _, g := range loaded.Gates {
			for _, trig := range g.On {
				kinds, _ := declaration.ExpandGateEvent(trig.Event)
				bound = append(bound, kinds...)
			}
		}
		events := foreignEvents(extractPreEvents(cmd, p, reg, bound), p, root)
		if len(events) == 0 {
			continue
		}
		scopeB := scope
		scopeB.Workspace = root
		results := runGatesForEvents(cmd, reg, loaded.Gates, events, scopeB, nil,
			map[string]natures.ContextState{}, map[string]natures.GateState{}, resolveNotes{})
		if reason := gateRefusal(results, events, root); reason != "" {
			return natureVerdict{Blocked: fmt.Sprintf("in the project at %s (its own gates govern its tree): %s", root, reason)}
		}
	}
	return natureVerdict{}
}

// foreignEvents is the events of a call that belong to the repo at root: the file
// events under it, their paths made relative to it, and every other event (the call
// targets the repo, which is why it is judged at all).
func foreignEvents(events []event.Event, p HookPayload, root string) []event.Event {
	var out []event.Event
	for _, e := range events {
		if !isPreFileEvent(e.Kind) {
			out = append(out, e)
			continue
		}
		path := absEventPath(p, eventPath(e))
		if owningRepo(path) != root {
			continue
		}
		rel, err := filepath.Rel(root, resolveExistingPrefix(path))
		if err != nil {
			continue
		}
		fields := make(map[string]any, len(e.Fields))
		for k, v := range e.Fields {
			fields[k] = v
		}
		fields["path"] = filepath.ToSlash(rel)
		out = append(out, event.Event{Kind: e.Kind, Fields: fields})
	}
	return out
}

// absEventPath is a file event's path as an absolute one: the extraction reports a
// path relative to the session's root, or absolute when it lies outside it.
func absEventPath(p HookPayload, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(p.Root(), path)
}

// commandTargetDirs is every directory a Bash call runs a command in: where the line
// started, moved by `cd` ahead of a command and by git's `-C`. A command whose
// directory cannot be known from the line is skipped.
func commandTargetDirs(p HookPayload) []string {
	if p.ToolName != "Bash" {
		return nil
	}
	var in struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(p.ToolInput, &in) != nil || in.Command == "" {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, inv := range commandmod.ExtractCommand(in.Command).Invocations {
		if inv.Cwd == "" {
			continue
		}
		dir := p.Cwd
		if inv.Cwd != "." {
			dir = joinDir(dir, inv.Cwd)
		}
		if d, _, _, ok := gitTarget(inv, p.Cwd); ok {
			dir = d
		}
		if !seen[dir] {
			seen[dir] = true
			out = append(out, dir)
		}
	}
	return out
}
