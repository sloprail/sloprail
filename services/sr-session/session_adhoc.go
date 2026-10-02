package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/sloprail/sloprail/internal/commandmod"
	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/sessionstate"
)

// Repositories an agent works in outside its own tree (ad-hoc folders).
//
// A session that runs `git -C ../other commit` or `cd ../other && git commit` makes
// commits in a repository nobody registered. Before the Bash call runs, the repository
// the command names is registered as an ad-hoc folder of the agent that ran it, started
// at the HEAD it has right then. That repository's own rules (its .sloprail plus the
// plugins the session has enabled) apply to what is done there: gates judge the call, and
// commit-required covers its uncommitted work.

// historyMoving are the git subcommands that make commits or move a branch's history.
var historyMoving = map[string]bool{
	"commit": true, "merge": true, "cherry-pick": true, "revert": true, "am": true,
	"pull": true, "rebase": true, "push": true, "reset": true, "commit-tree": true,
}

// gitTarget is the directory a git invocation runs in and its subcommand: where the
// line started, moved by `cd` ahead of it and by `-C` flags. ok is false when the
// directory cannot be known from the line.
func gitTarget(inv commandmod.Invocation, base string) (dir, sub string, rest []string, ok bool) {
	if inv.Bin != "git" || len(inv.Argv) < 2 {
		return "", "", nil, false
	}
	dir = base
	if inv.Cwd == "" {
		return "", "", nil, false
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
			return dir, a, argv[i+1:], true
		}
	}
	return "", "", nil, false
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
		dir, sub, _, ok := gitTarget(inv, p.Cwd)
		if !ok || !historyMoving[sub] || seen[dir] {
			continue
		}
		seen[dir] = true
		out = append(out, dir)
	}
	return out
}

// worktreeAdds is the directories a Bash call's `git worktree add` will create.
func worktreeAdds(p HookPayload) []string {
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
	for _, inv := range commandmod.ExtractCommand(in.Command).Invocations {
		dir, sub, rest, ok := gitTarget(inv, p.Cwd)
		if !ok || sub != "worktree" || len(rest) < 2 || rest[0] != "add" {
			continue
		}
		args := rest[1:]
		for i := 0; i < len(args); i++ {
			a := args[i]
			switch {
			case a == "-b" || a == "-B" || a == "--reason":
				i++
			case strings.HasPrefix(a, "-"):
			default:
				out = append(out, joinDir(dir, a))
				i = len(args)
			}
		}
	}
	return out
}

const pendingWorktreesKey = "pending_worktrees"

// notePendingWorktrees remembers the worktrees a command is about to create, so the next
// hook registers them once they exist.
func notePendingWorktrees(reg sessionstate.Store, p HookPayload) {
	add := worktreeAdds(p)
	if len(add) == 0 {
		return
	}
	var pending map[string]string
	if v, had, err := reg.Meta(pendingWorktreesKey); err == nil && had {
		_ = json.Unmarshal([]byte(v), &pending)
	}
	if pending == nil {
		pending = map[string]string{}
	}
	for _, d := range add {
		pending[filepath.Clean(d)] = p.AgentID
	}
	if b, err := json.Marshal(pending); err == nil {
		_ = reg.SetMeta(pendingWorktreesKey, string(b))
	}
}

// registerPendingWorktrees registers the worktrees earlier calls created, as ad-hoc
// folders of the agent that made them, started where each was created (its own HEAD
// reflog's oldest entry), so a commit made in the same call it was created in is judged.
func registerPendingWorktrees(reg sessionstate.Store, rs rootSession, agent string) {
	v, had, err := reg.Meta(pendingWorktreesKey)
	if err != nil || !had {
		return
	}
	var pending map[string]string
	if json.Unmarshal([]byte(v), &pending) != nil {
		return
	}
	for dir, owner := range pending {
		if owner != agent {
			continue
		}
		tree, err := gitrepo.Root(dir)
		if err != nil || tree == "" {
			continue // not created (yet)
		}
		tree = filepath.Clean(tree)
		delete(pending, dir)
		if _, found, err := reg.Folder(rs.ID, tree); err != nil || found {
			continue
		}
		f := sessionstate.Folder{SessionID: rs.ID, Path: tree, Role: sessionstate.FolderAdHoc, GitRoot: tree, AgentID: agent, BaseRef: sessionstate.FolderBaseUnborn}
		if pos, herr := gitrepo.Head(tree); herr == nil {
			f.Branch, f.HeadRef = pos.Branch, pos.Commit
			f.BaseRef = pos.Commit
		}
		if id, err := gitrepo.RootCommit(tree); err == nil {
			f.RepoID = id
		}
		if _, err := reg.RegisterFolder(f); err != nil {
			continue
		}
	}
	if b, err := json.Marshal(pending); err == nil {
		_ = reg.SetMeta(pendingWorktreesKey, string(b))
	}
}

// registerCommandFolders registers each repository outside the agent's own tree that
// this Bash call is about to move history in, and observes its refs.
func registerCommandFolders(reg sessionstate.Store, rs rootSession, p HookPayload) error {
	registerPendingWorktrees(reg, rs, p.AgentID)
	notePendingWorktrees(reg, p)
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
		}
	}
	return nil
}

// sessionFoldersOf is the folders the session registered for this agent besides its own
// tree — ad-hoc repositories a command ran in — whose rules apply to what is done there.
func sessionFoldersOf(p HookPayload) []sessionstate.Folder {
	rs, err := resolveRootSession(p)
	if err != nil {
		return nil // no session identity, so no registry to read
	}
	if _, err := os.Stat(rs.Path); err != nil {
		return nil
	}
	reg, err := sessionstate.Open(rs.Path)
	if err != nil {
		return nil
	}
	defer reg.Close()
	folders, err := reg.Folders(rs.ID)
	if err != nil {
		return nil
	}
	var out []sessionstate.Folder
	for _, f := range folders {
		if f.AgentID != p.AgentID || f.Role == sessionstate.FolderRoot {
			continue
		}
		if st, err := os.Stat(f.Path); err != nil || !st.IsDir() {
			continue
		}
		out = append(out, f)
	}
	return out
}
