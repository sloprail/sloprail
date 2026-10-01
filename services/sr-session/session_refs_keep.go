package main

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/sloprail/sloprail/internal/checkstore"
	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/sessionstate"
)

// Keeping what the session committed from being lost to git housekeeping or a rewrite.
//
// A recorded tip is owed a judgement until a rule passes it. Four ways to lose one are
// closed here:
//   - the branch is deleted and `git gc --prune=now` deletes its commits: every recorded tip
//     is PINNED under refs/sloprail/pins (gitrepo.PinPrefix) until it is settled;
//   - the branch's reflog goes with the branch, and with it where the branch was cut from: the
//     start is remembered when the ref is first recorded;
//   - the ref is moved off the recorded tip by something that is not a fast-forward (`branch -f`,
//     `checkout -B`, `update-ref`, `reset`): the old tip is RETIRED, kept and pinned, and a
//     branch that contains it later (a copy made before the move, one recreated from the reflog
//     afterwards) is recorded in its place. A tip no branch holds is dropped: an amend or a
//     rebase leaves the rewritten commit on the ref, and that is what is judged;
//   - the worktree the ref was recorded in is removed while the branch lives on in its
//     repository: the rows are adopted by the repository's own folder.

func shortHash(s string) string {
	h := sha1.Sum([]byte(s))
	return hex.EncodeToString(h[:])[:10]
}

// pinName is the hidden ref that holds a recorded tip.
func pinName(sessionID, folder, name string) string {
	return gitrepo.PinPrefix + shortHash(sessionID) + "/" + shortHash(filepath.Clean(folder)) + "/" + strings.TrimPrefix(name, "refs/")
}

func pinTip(root, sessionID, folder, name, tip string) {
	if tip == "" {
		return
	}
	pin := pinName(sessionID, folder, name)
	if cur, err := gitrepo.RefTip(root, pin); err == nil && cur == tip {
		return
	}
	_ = gitrepo.UpdateRef(root, pin, tip) // best effort: the pin only guards against a later gc
}

func unpinTip(root, sessionID, folder, name string) {
	_ = gitrepo.DeleteRef(root, pinName(sessionID, folder, name))
}

func refStartKey(folder, name string) string {
	return "ref_start:" + filepath.Clean(folder) + "|" + name
}

// refStart is where a ref was created: from its reflog while the ref is alive, remembered
// the first time so it survives the ref's deletion (a deleted branch loses its reflog).
//
// A start that is the tip itself is no start: it would leave the tip's own commits out of
// the range (a branch made by `checkout -B copy side` begins at the commit holding the work).
func refStart(reg sessionstate.Store, root, folder, name, tip string) string {
	if !strings.HasPrefix(name, "refs/") {
		return ""
	}
	key := refStartKey(folder, name)
	s, had, _ := reg.Meta(key)
	if !had || s == "" {
		if s, _ = gitrepo.RefCreation(root, name); s != "" {
			_ = reg.SetMeta(key, s)
		}
	}
	if s == tip {
		return ""
	}
	return s
}

// recordKept records a ref and everything that keeps its tip judgeable.
func recordKept(reg sessionstate.Store, root string, r sessionstate.Ref) error {
	if err := reg.RecordRef(r); err != nil {
		return err
	}
	refStart(reg, root, r.Folder, r.Name, r.Tip)
	pinTip(root, r.SessionID, r.Folder, r.Name, r.Tip)
	return nil
}

type retiredTip struct {
	Tip   string `json:"tip"`
	Name  string `json:"name"`
	Agent string `json:"agent,omitempty"`
	// Start is where the ref it was moved off was cut, for the branch that holds it later.
	Start string `json:"start,omitempty"`
}

const maxRetired = 200

func retiredKey(folder string) string { return "retired_tips:" + filepath.Clean(folder) }

func loadRetired(reg sessionstate.Store, folder string) []retiredTip {
	var out []retiredTip
	if v, had, err := reg.Meta(retiredKey(folder)); err == nil && had {
		_ = json.Unmarshal([]byte(v), &out)
	}
	return out
}

func saveRetired(reg sessionstate.Store, folder string, list []retiredTip) {
	if len(list) > maxRetired {
		list = list[len(list)-maxRetired:]
	}
	if b, err := json.Marshal(list); err == nil {
		_ = reg.SetMeta(retiredKey(folder), string(b))
	}
}

func retiredPin(tip string) string { return "retired/" + tip }

// retireTip keeps the tip a ref was moved off, when the move did not keep it (not a
// fast-forward), so a branch that holds it later is judged.
func retireTip(reg sessionstate.Store, root, sessionID, folder string, r sessionstate.Ref) {
	list := loadRetired(reg, folder)
	for _, t := range list {
		if t.Tip == r.Tip && t.Agent == r.AgentID {
			return
		}
	}
	saveRetired(reg, folder, append(list, retiredTip{Tip: r.Tip, Name: r.Name, Agent: r.AgentID, Start: refStart(reg, root, folder, r.Name, r.Tip)}))
	pinTip(root, sessionID, folder, retiredPin(r.Tip), r.Tip)
}

// retireMovedRows retires the recorded tip of every ref of this agent that has been moved off
// it by something that is not a fast-forward, before the rows are refreshed to the ref's tip.
// A move that keeps the tip (a fast-forward, a merge) retires nothing; an amend or a rebase
// retires a tip no branch holds, which resurrectRetired leaves alone, and the rewritten
// commit on the ref is what is judged.
func retireMovedRows(reg sessionstate.Store, root, sessionID, folder, agent string) {
	rows, err := reg.Refs(sessionID, folder)
	if err != nil {
		return
	}
	for _, r := range rows {
		if r.AgentID != agent || !strings.HasPrefix(r.Name, "refs/") {
			continue
		}
		cur, err := gitrepo.RefTip(root, r.Name)
		if err != nil || cur == "" || cur == r.Tip {
			continue
		}
		if kept, err := gitrepo.IsAncestor(root, r.Tip, cur); err == nil && !kept {
			retireTip(reg, root, sessionID, folder, r)
		}
	}
}

// resurrectRetired records, for each retired tip some branch holds again, that branch as
// a ref of the session, so it is judged like any other. A tip every rule has passed is
// settled and forgotten; a tip no branch holds is left alone.
func resurrectRetired(reg sessionstate.Store, root, sessionID, folder, agent string, guards []declaration.FileGuard, results checkstore.Store) error {
	list := loadRetired(reg, folder)
	if len(list) == 0 {
		return nil
	}
	rows, err := reg.Refs(sessionID, folder)
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for _, r := range rows {
		if r.AgentID == agent {
			have[r.Name] = true
		}
	}
	var keep []retiredTip
	for _, t := range list {
		if t.Agent != agent {
			keep = append(keep, t)
			continue
		}
		if judgedByEvery(root, t.Tip, guards, results) {
			unpinTip(root, sessionID, folder, retiredPin(t.Tip))
			continue
		}
		keep = append(keep, t)
		heads, err := gitrepo.LocalBranchesContaining(root, t.Tip)
		if err != nil {
			return err
		}
		name := ""
		switch {
		case len(heads) > 0:
			name = heads[0]
			for _, h := range heads {
				if have[h] {
					name = ""
					break
				}
			}
		default:
			if onRemote, err := gitrepo.OnRemote(root, t.Tip); err == nil && onRemote {
				name = gitrepo.DetachedRef(t.Tip)
			}
		}
		if name == "" || have[name] {
			continue
		}
		// The branch that holds it is judged from where the one it was moved off was cut (its
		// own creation is the commit holding the work, which would leave the work out).
		start := t.Start
		if start == "" {
			start, _ = gitrepo.MergeBaseOf(root, t.Tip, "HEAD")
		}
		if start != "" && start != t.Tip {
			_ = reg.SetMeta(refStartKey(folder, name), start)
		}
		if err := recordKept(reg, root, sessionstate.Ref{SessionID: sessionID, Folder: filepath.Clean(folder), Name: name, Tip: t.Tip, AgentID: agent}); err != nil {
			return err
		}
		have[name] = true
	}
	saveRetired(reg, folder, keep)
	return nil
}

func folderHomeKey(folder string) string  { return "folder_home:" + filepath.Clean(folder) }
func folderAdoptKey(folder string) string { return "folder_adopted:" + filepath.Clean(folder) }

// noteFolderHome remembers which working tree is the repository's own (the one holding
// its .git directory) for a folder that is a linked worktree: the folder may be removed,
// the repository and its branches stay.
func noteFolderHome(reg sessionstate.Store, folder string) {
	home := gitrepo.MainWorktree(folder)
	if home == "" || filepath.Clean(home) == filepath.Clean(folder) {
		return
	}
	_ = reg.SetMeta(folderHomeKey(folder), filepath.Clean(home))
}

// observeAdHocFolders snapshots the refs of every ad-hoc folder this agent has registered, at
// every hook, not only when a command names the folder: the call that removes a worktree (or
// the repository around it) names it as the thing to remove, and what was committed in it
// before then must already be on record when the folder is gone.
func observeAdHocFolders(reg sessionstate.Store, sessionID, agent string) {
	folders, err := reg.Folders(sessionID)
	if err != nil {
		return
	}
	for _, f := range folders {
		if f.Role != sessionstate.FolderAdHoc || f.AgentID != agent {
			continue
		}
		if st, err := os.Stat(f.Path); err != nil || !st.IsDir() {
			continue
		}
		_ = observeRefs(reg, sessionID, f.Path, f.Path, agent)
	}
}
