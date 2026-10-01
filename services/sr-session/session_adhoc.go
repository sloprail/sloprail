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
	"github.com/sloprail/sloprail/internal/natures"
	"github.com/sloprail/sloprail/internal/sessionstate"
)

// Repositories an agent works in outside its own tree (ad-hoc folders).
//
// A session that runs `git -C ../other commit` or `cd ../other && git commit` makes
// commits in a repository nobody registered. Before the Bash call runs, the repository
// the command names is registered as an ad-hoc folder of the agent that ran it, started
// at the HEAD it has right then (before any commit this call makes), with a baseline of
// its branch tips. At Stop the agent judges that folder like its own: HEAD and every ref
// it moved there, by THAT repository's rules (its own .sloprail plus the plugins the
// session has enabled), never by the session root's.

// historyMoving are the git subcommands that make commits or move a branch's history.
var historyMoving = map[string]bool{
	"commit": true, "merge": true, "cherry-pick": true, "revert": true, "am": true,
	"pull": true, "rebase": true, "push": true, "reset": true, "commit-tree": true,
}

// gitTarget is the directory a git invocation runs in and its subcommand: where the
// line started, moved by `cd` ahead of it and by `-C` flags. ok is false when the
// directory cannot be known from the line.
func gitTarget(inv commandmod.Invocation, base string) (dir, sub string, ok bool) {
	if inv.Bin != "git" || len(inv.Argv) < 2 {
		return "", "", false
	}
	dir = base
	if inv.Cwd == "" {
		return "", "", false
	}
	if inv.Cwd != "." {
		dir = joinDir(dir, inv.Cwd)
	}
	argv := inv.Argv[1:]
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		switch {
		case a == "-C" && i+1 < len(argv):
			dir = joinDir(dir, argv[i+1])
			i++
		case a == "-c" && i+1 < len(argv):
			i++
		case strings.HasPrefix(a, "--work-tree="):
			dir = joinDir(dir, strings.TrimPrefix(a, "--work-tree="))
		case strings.HasPrefix(a, "-"):
		default:
			return dir, a, true
		}
	}
	return "", "", false
}

func joinDir(base, d string) string {
	if filepath.IsAbs(d) {
		return filepath.Clean(d)
	}
	return filepath.Join(base, d)
}

// commandFolders is the directories a Bash call's git commands move history in.
func commandFolders(p HookPayload) []string {
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
		dir, sub, ok := gitTarget(inv, p.Cwd)
		if !ok || !historyMoving[sub] || seen[dir] {
			continue
		}
		seen[dir] = true
		out = append(out, dir)
	}
	return out
}

// refsAtStartKey is the per-folder baseline of branch tips an ad-hoc folder started with.
func refsAtStartKey(folder string) string { return "refs_at_start:" + filepath.Clean(folder) }

// registerCommandFolders registers each repository outside the agent's own tree that
// this Bash call is about to move history in, and observes its refs.
func registerCommandFolders(reg sessionstate.Store, rs rootSession, p HookPayload) error {
	rootTree, err := gitrepo.Root(rs.Cwd)
	if err != nil || rootTree == "" {
		return nil
	}
	ownTree, _ := gitrepo.Root(p.Cwd)
	for _, dir := range commandFolders(p) {
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			continue
		}
		tree, err := gitrepo.Root(dir)
		if err != nil || tree == "" {
			continue
		}
		if sameDir(tree, rootTree) || (ownTree != "" && sameDir(tree, ownTree)) {
			continue // the session's own tree, or the one registered from the agent's cwd
		}
		tree = filepath.Clean(tree)
		if _, found, err := reg.Folder(rs.ID, tree); err != nil {
			return err
		} else if !found {
			start := sessionstate.FolderBaseUnborn
			f := sessionstate.Folder{SessionID: rs.ID, Path: tree, Role: sessionstate.FolderAdHoc, GitRoot: tree, AgentID: p.AgentID}
			if pos, herr := gitrepo.Head(tree); herr == nil {
				f.Branch, f.HeadRef = pos.Branch, pos.Commit
				if pos.Commit != "" {
					start = pos.Commit
				}
			}
			f.BaseRef = start
			if id, err := gitrepo.RootCommit(tree); err == nil {
				f.RepoID = id
			}
			if _, err := reg.RegisterFolder(f); err != nil {
				return err
			}
			if tips, terr := gitrepo.RefTips(tree); terr == nil {
				if b, merr := json.Marshal(tips); merr == nil {
					_ = reg.SetMeta(refsAtStartKey(tree), string(b))
				}
			}
		}
		if err := observeRefs(reg, rs.ID, tree, tree, p.AgentID); err != nil {
			return err
		}
	}
	return nil
}

// evaluateAdHocFolders judges, at Stop, every repository this agent registered outside
// its own tree: HEAD and every ref it moved there, under that repository's own rules.
func evaluateAdHocFolders(cmd *cobra.Command, p HookPayload, scope hookScope, mods *module.Registry,
	contextMap map[string]natures.ContextState, state sessionstate.Store) []fileGuardResult {
	rs, err := resolveRootSession(p)
	if err != nil {
		return nil
	}
	if _, err := os.Stat(rs.Path); err != nil {
		return nil
	}
	reg, err := sessionstate.Open(rs.Path)
	if err != nil {
		return nil
	}
	folders, err := reg.Folders(rs.ID)
	reg.Close()
	if err != nil {
		return nil
	}
	var out []fileGuardResult
	var results = openChecksStore(cmd, p, scope)
	if results != nil {
		defer results.Close()
	}
	for _, f := range folders {
		if f.Role != sessionstate.FolderAdHoc || f.AgentID != p.AgentID {
			continue
		}
		if st, err := os.Stat(f.Path); err != nil || !st.IsDir() {
			continue
		}
		loaded := newNatureDeclarations(cmd, f.Path, mods)
		for _, r := range evaluateChangesets(cmd, loaded.FileGuards, p, scope, f.Path, contextMap, state, results) {
			if r.Reason != "" && !strings.Contains(r.Reason, f.Path) {
				r.Reason = "In " + f.Path + " (a repository outside the session's own tree): " + r.Reason
			}
			out = append(out, r)
		}
	}
	return out
}
