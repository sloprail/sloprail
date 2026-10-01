package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/sessionstate"
)

// ORPHANED WORK. Each agent judges its own folders, so a tip recorded under a folder whose
// agent will never stop there again is judged by nobody: a sub-agent's worktree the harness
// removed when the agent finished, a worktree removed by hand, rows under a folder that is
// not a folder of the session at all (a manual data patch, a store from an older engine). The
// PARENT (the root agent) inherits them: its next Stop judges each one on its own tip's
// tree, in the repository the folder belonged to, restricted as every tip is (rule age,
// what still stands), and the refusal says whose work it was and how to fix it.
//
// A folder is orphaned when the WorktreeRemove hook fired for it, or its path no longer
// exists. A live agent's folder is never claimed: it judges its own at its own Stop.

func folderRemovedKey(path string) string { return "folder_removed:" + filepath.Clean(path) }
func adoptedKey(folder, name string) string {
	return "orphan_adopted:" + filepath.Clean(folder) + "|" + name
}

// folderRemoved reports whether a registered folder is gone: removed on disk, or reported
// removed by the harness (the hook fires before the directory goes).
func folderRemoved(reg sessionstate.Store, path string) bool {
	if v, had, _ := reg.Meta(folderRemovedKey(path)); had && v == "1" {
		return true
	}
	st, err := os.Stat(path)
	return err != nil || !st.IsDir()
}

// newSessionWorktreeRemoveCmd is the hook point that fires when the harness removes a
// worktree (a sub-agent finished, the session ended, a background session was deleted). It
// never blocks the removal: whatever it cannot do it reports on stderr and exits 0. What it
// does is the last look at the folder before it goes: every ref the session recorded there
// and the folder's current HEAD are snapshotted and pinned (so nothing a rule has not passed
// is lost with the directory), and the folder is marked removed, which hands what it holds
// to the parent agent's next Stop.
func newSessionWorktreeRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "worktree-remove",
		Short: "A worktree is being removed: keep what the session committed in it judgeable",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p := readPayload(cmd)
			warn := func(format string, a ...any) {
				fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: worktree-remove: "+format+"\n", a...)
			}
			if p.WorktreePath == "" {
				warn("the payload names no worktree_path; nothing to keep")
				return nil
			}
			rs, err := resolveRootSession(p)
			if err != nil {
				warn("the session is not known (%v); nothing to keep", err)
				return nil
			}
			if _, err := os.Stat(rs.Path); err != nil {
				return nil
			}
			reg, err := sessionstate.Open(rs.Path)
			if err != nil {
				warn("the session's store did not open: %v", err)
				return nil
			}
			defer reg.Close()
			folders, err := reg.Folders(rs.ID)
			if err != nil {
				warn("%v", err)
				return nil
			}
			for _, f := range folders {
				if f.Role == sessionstate.FolderRoot || !sameDir(f.Path, p.WorktreePath) {
					continue
				}
				dir := f.Path
				if st, err := os.Stat(dir); err != nil || !st.IsDir() {
					dir = p.WorktreePath
				}
				if err := observeRefs(reg, rs.ID, f.Path, dir, f.AgentID); err != nil {
					warn("%v", err)
				}
				noteFolderHome(reg, f.Path)
				rows, err := reg.Refs(rs.ID, f.Path)
				if err != nil {
					warn("%v", err)
					continue
				}
				for _, r := range rows {
					pinTip(dir, rs.ID, f.Path, r.Name, r.Tip)
					refStart(reg, dir, f.Path, r.Name, r.Tip)
				}
				if err := reg.SetMeta(folderRemovedKey(f.Path), "1"); err != nil {
					warn("%v", err)
				}
			}
			return nil
		},
	}
}

// adoptOrphans hands the orphaned rows of the session's store to the root agent, before
// it judges. Only the root agent claims them.
func adoptOrphans(cmd *cobra.Command, p HookPayload) {
	if p.AgentID != "" {
		return
	}
	rs, err := resolveRootSession(p)
	if err != nil {
		return
	}
	if _, err := os.Stat(rs.Path); err != nil {
		return
	}
	reg, err := sessionstate.Open(rs.Path)
	if err != nil {
		return
	}
	defer reg.Close()
	rootTree, err := gitrepo.Root(rs.Cwd)
	if err != nil || rootTree == "" {
		return
	}
	adoptOrphanedRows(cmd, reg, rs, filepath.Clean(rootTree))
}

// adoptOrphanedRows re-records each orphaned row as the root agent's own, in the folder of
// the repository the work belongs to: the root's, or another repository's (registered as
// an ad-hoc folder of the root agent, started where the work was cut).
func adoptOrphanedRows(cmd *cobra.Command, reg sessionstate.Store, rs rootSession, rootTree string) {
	warn := func(err error) {
		fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: orphaned work was not handed over: %v\n", err)
	}
	folders, err := reg.Folders(rs.ID)
	if err != nil {
		warn(err)
		return
	}
	registered := map[string]sessionstate.Folder{}
	for _, f := range folders {
		registered[filepath.Clean(f.Path)] = f
	}
	rows, err := reg.Refs(rs.ID, "")
	if err != nil {
		warn(err)
		return
	}
	for _, r := range rows {
		f, isFolder := registered[filepath.Clean(r.Folder)]
		if isFolder && (f.Role == sessionstate.FolderRoot || !folderRemoved(reg, f.Path)) {
			continue // the root's own, or a live folder: its agent judges it
		}
		if r.Abandoned != "" {
			continue
		}
		if done, _, _ := reg.Meta(adoptedKey(r.Folder, r.Name)); done == r.Tip {
			continue
		}
		home := ""
		if isFolder {
			home, _, _ = reg.Meta(folderHomeKey(f.Path))
		} else if st, err := os.Stat(r.Folder); err == nil && st.IsDir() {
			if tree, err := gitrepo.Root(r.Folder); err == nil {
				home = tree
			}
		}
		if st, err := os.Stat(home); home == "" || err != nil || !st.IsDir() {
			continue // the repository is not reachable: nothing can judge it
		}
		target := filepath.Clean(home)
		if sameDir(target, rootTree) {
			target = rootTree
		} else if _, found, err := reg.Folder(rs.ID, target); err != nil {
			warn(err)
			continue
		} else if !found {
			start := r.Tip
			if mb, err := gitrepo.MergeBaseOf(target, r.Tip, "HEAD"); err == nil && mb != "" {
				start = mb
			}
			nf := sessionstate.Folder{SessionID: rs.ID, Path: target, Role: sessionstate.FolderAdHoc, GitRoot: target, BaseRef: start}
			if pos, herr := gitrepo.Head(target); herr == nil {
				nf.Branch, nf.HeadRef = pos.Branch, pos.Commit
			}
			if id, err := gitrepo.RootCommit(target); err == nil {
				nf.RepoID = id
			}
			if _, err := reg.RegisterFolder(nf); err != nil {
				warn(err)
				continue
			}
		}
		// A row the root already holds under this name keeps its own (newer) tip.
		held := false
		same := filepath.Clean(r.Folder) == target && r.AgentID == "" // the row is already the root's, in place
		if mine, err := reg.Refs(rs.ID, target); err == nil {
			for _, m := range mine {
				if m.Name == r.Name && m.AgentID == "" {
					held = true
				}
			}
		}
		if same {
			_ = reg.SetMeta(orphanOriginKey(target, r.Name), fmt.Sprintf("an agent of this session in %s (a folder that is no folder of the session)", target))
		} else if !held {
			if start, had, _ := reg.Meta(refStartKey(r.Folder, r.Name)); had && start != "" {
				_ = reg.SetMeta(refStartKey(target, r.Name), start)
			}
			if err := recordKept(reg, target, sessionstate.Ref{SessionID: rs.ID, Folder: target, Name: r.Name, Tip: r.Tip, AgentID: ""}); err != nil {
				warn(err)
				continue
			}
			who := "an agent of this session"
			if r.AgentID != "" {
				who = "sub-agent " + r.AgentID
			}
			_ = reg.SetMeta(orphanOriginKey(target, r.Name), fmt.Sprintf("%s in %s", who, filepath.Clean(r.Folder)))
		}
		_ = reg.SetMeta(adoptedKey(r.Folder, r.Name), r.Tip)
	}
}
