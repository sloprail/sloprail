package main

import (
	"encoding/json"
	"path/filepath"

	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/sessionstate"
)

// Recording what a session touched, at every hook.
//
// The reflog is only a backfill (stopTips reads it for what happened before the engine
// was watching). The data itself is a snapshot taken at every hook: for each branch that
// has been checked out in this folder, its tip, compared with the one the previous hook
// saw. A tip that moved to commits not already in the history of what was there before is
// the session's own work, and is recorded as a ref of this folder; a detached HEAD is
// recorded the same way. Only branches checked out in THIS folder are watched, so a
// sibling's branch in the shared repository is never blamed on this agent. A checkout away
// leaves the ref watched and recorded: nothing is lost by switching.

type refSnapshot struct {
	// Tips is the last tip seen for each watched branch (full ref name).
	Tips map[string]string `json:"tips"`
	// Detached is the last detached-HEAD commit seen, "" when HEAD was on a branch.
	Detached string `json:"detached,omitempty"`
}

func refSnapshotKey(folder string) string { return "refs_snapshot:" + filepath.Clean(folder) }

// observeRefs snapshots the folder's watched refs and records the ones the session moved.
// dir is where git runs; agent owns the rows ("" for the session itself).
func observeRefs(reg sessionstate.Store, sessionID, folder, dir, agent string) error {
	key := refSnapshotKey(folder)
	var prev refSnapshot
	if v, had, err := reg.Meta(key); err != nil {
		return err
	} else if had {
		_ = json.Unmarshal([]byte(v), &prev)
	}
	if prev.Tips == nil {
		prev.Tips = map[string]string{}
	}
	atStart := map[string]string{}
	haveBaseline := false
	if v, had, err := reg.Meta(sessionstate.MetaRefsAtStart); err == nil && had {
		haveBaseline = json.Unmarshal([]byte(v), &atStart) == nil
	}
	var known []string // everything the session did not make
	for _, s := range atStart {
		known = append(known, s)
	}
	for _, s := range prev.Tips {
		known = append(known, s)
	}
	if prev.Detached != "" {
		known = append(known, prev.Detached)
	}

	head, err := gitrepo.Head(dir)
	if err != nil {
		return nil // unborn or not a repository: nothing to observe
	}
	next := refSnapshot{Tips: map[string]string{}}
	watch := map[string]bool{}
	for ref := range prev.Tips {
		watch[ref] = true
	}
	if head.Branch != "" {
		watch["refs/heads/"+head.Branch] = true
	} else if head.Commit != "" {
		next.Detached = head.Commit
	}
	fresh := func(sha string) bool {
		held, err := gitrepo.InHistoryOf(dir, sha, known)
		return err == nil && !held
	}
	record := func(name, sha string) error {
		return reg.RecordRef(sessionstate.Ref{SessionID: sessionID, Folder: filepath.Clean(folder), Name: name, Tip: sha, AgentID: agent})
	}
	for ref := range watch {
		cur, err := gitrepo.RefTip(dir, ref)
		if err != nil || cur == "" {
			continue // deleted: what was recorded stays recorded
		}
		next.Tips[ref] = cur
		was, seen := prev.Tips[ref]
		if !seen {
			// First time this folder watches the ref: a branch that existed at the start
			// is measured from where it was; one made since is all the session's.
			if s, existed := atStart[ref]; existed {
				was, seen = s, true
			}
		}
		if seen && was == cur {
			continue
		}
		if !seen && !haveBaseline {
			continue // cannot tell what was already there; the reflog backfill decides
		}
		if fresh(cur) {
			if err := record(ref, cur); err != nil {
				return err
			}
		}
	}
	if next.Detached != "" && next.Detached != prev.Detached && fresh(next.Detached) {
		if err := record(gitrepo.DetachedRef(next.Detached), next.Detached); err != nil {
			return err
		}
	}
	b, err := json.Marshal(next)
	if err != nil {
		return err
	}
	return reg.SetMeta(key, string(b))
}
