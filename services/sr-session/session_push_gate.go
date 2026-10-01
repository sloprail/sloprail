package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/commandmod"
	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/module"
	"github.com/sloprail/sloprail/internal/sessionstate"
)

// The push gate: commits leave the machine at `git push` and `gh pr create`, and a pull
// request merges what was pushed. Before either runs, every file-guard is evaluated over
// what the agent has committed in the repository the command names (HEAD and every ref it
// moved there), exactly as at Stop, and a refusal blocks the command. What was judged is
// recorded like any other run, so the Stop that follows finds the range already passed.

// pushTargets is the directories a Bash call's `git push` / `gh pr create` run in.
func pushTargets(p HookPayload) []string {
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
		var dir string
		switch {
		case inv.Bin == "git":
			d, sub, _, ok := gitTarget(inv, p.Cwd)
			if !ok || sub != "push" {
				continue
			}
			dir = d
		case inv.Bin == "gh" && len(inv.Argv) >= 3 && inv.Argv[1] == "pr" && inv.Argv[2] == "create":
			if inv.Cwd == "" {
				continue
			}
			dir = p.Cwd
			if inv.Cwd != "." {
				dir = joinDir(dir, inv.Cwd)
			}
		default:
			continue
		}
		if !seen[dir] {
			seen[dir] = true
			out = append(out, dir)
		}
	}
	return out
}

// pushGate refuses a push or pull-request creation while a file-guard refuses the
// commits that would leave. "" lets the command run.
func pushGate(cmd *cobra.Command, p HookPayload, mods *module.Registry, store sessionstate.Store) string {
	targets := pushTargets(p)
	if len(targets) == 0 {
		return ""
	}
	scope := natureHookScope(cmd, p)
	var reasons []string
	for _, dir := range targets {
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			continue
		}
		tree, err := gitrepo.Root(dir)
		if err != nil || tree == "" {
			continue
		}
		tree = filepath.Clean(tree)
		loaded := newNatureDeclarations(cmd, tree, mods)
		if len(loaded.FileGuards) == 0 {
			continue
		}
		contextMap := loadContextMap(cmd, store, loaded.Contexts)
		results := openChecksStore(cmd, p, scope)
		refusals := evaluateChangesets(cmd, loaded.FileGuards, p, scope, tree, contextMap, store, results)
		if results != nil {
			results.Close()
		}
		for _, r := range refusals {
			reasons = append(reasons, r.Reason+" (file-guard "+r.Attribution+")")
		}
	}
	if len(reasons) == 0 {
		return ""
	}
	return "this would push commits a file-guard refuses; fix them (and commit) first, then push again:\n  - " + strings.Join(reasons, "\n  - ")
}

