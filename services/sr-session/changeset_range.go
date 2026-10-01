package main

import (
	"errors"

	"github.com/sloprail/sloprail/internal/changeset"
	"github.com/sloprail/sloprail/internal/checkstore"
	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/sessionstate"
)

// resolveRuleRange is THE way a file-guard's range is chosen, for the Stop
// evaluation and for `sr-session changeset` alike, so what the command shows is
// what Stop judges.
//
// base is the rule's watermark — derived from the check results as the newest
// FINISHED run that passed, at ANY definition of the rule (the work up to it was
// approved, even under an older rule), and whose head is still an ancestor of
// HEAD. Without one, it is the EARLIER of the parent of the last commit touching
// the rule's whole .sloprail root (a rule in this repository) and the HEAD
// recorded when the session began: nothing made in this session is skipped, and
// history from before the session and the rule stays grandfathered. results and state may be nil (nothing
// recorded yet); any failure of either, or of git, is an error and no range.
//
// A watermark that had to be passed over — an amend or a rebase orphaned the
// newest pass, whether the base then falls to an older pass or to a floor — is
// reported on the returned range as DroppedWatermark, so a range that widened
// says why. gitrepo.ErrNoCommits is returned as itself: nothing is committed, so
// nothing can be judged.
// errSessionStartNotKept: the session recorded a baseline but not the HEAD it first
// began at, so the baseline (re-taken whenever the tree leaves its history) cannot stand
// in for it.
var errSessionStartNotKept = errors.New("this session did not keep the commit it began at (it began before that was recorded), " +
	"so which commits are new cannot be told; start a new session")

func resolveRuleRange(root string, g declaration.FileGuard, results checkstore.Store, state sessionstate.Store) (gitrepo.Range, error) {
	return resolveRuleRangeIn(root, g, results, state, nil)
}

// resolveRuleRangeIn is resolveRuleRange for a tree that is a registered session
// folder: the folder's BaseRef, the HEAD its own work began at, is the session
// start. That is what keeps a sub-agent dispatched into a worktree of its own from
// being judged over everything its PARENT session's start predates. With no folder
// (nil) the agent's own recorded start is used, as before.
func resolveRuleRangeIn(root string, g declaration.FileGuard, results checkstore.Store, state sessionstate.Store, folder *sessionstate.Folder) (gitrepo.Range, error) {
	var watermark, dropped, sessionStart string
	if results != nil {
		heads, err := results.PassedHeads(g.Qualified())
		if err != nil {
			return gitrepo.Range{}, err
		}
		if watermark, dropped, err = changeset.PickWatermark(heads, func(sha string) (bool, error) {
			return gitrepo.Contains(root, sha)
		}); err != nil {
			return gitrepo.Range{}, err
		}
		// The newest pass was rewritten (an amend, a rebase, a reset): it is re-anchored at
		// its merge base with HEAD. That OVERRIDES an older pass that survives, on
		// purpose: the merge base is never earlier than the older pass (an ancestor of
		// both), so it keeps everything the newest pass approved and still exists
		// approved, and judges only what was rewritten or is new. When git no longer has
		// the newest pass (or it shares no history with HEAD) there is nothing to
		// re-anchor and the surviving older pass, or the floors, apply.
		if dropped != "" {
			mb, found, err := gitrepo.ReanchorWatermark(root, dropped)
			if err != nil {
				return gitrepo.Range{}, err
			}
			if found {
				watermark = mb
			}
		}
	}
	// Only a sub-agent's worktree supplies the start. The session's own (root) folder
	// records the same HEAD its own start does, and the root's range is unchanged:
	// it keeps reading its recorded start, so a session that lost it still fails closed.
	if folder != nil && folder.Role == sessionstate.FolderSubagentWorktree && folder.BaseRef != "" {
		sessionStart = folder.BaseRef
		if sessionStart == sessionstate.FolderBaseUnborn {
			sessionStart = gitrepo.EmptyTree // began before the first commit
		}
	} else if state != nil {
		var err error
		// The FIRST start, never the re-taken baseline: an amend that rewrites it
		// would otherwise move the start past the work already done. A session that
		// has a baseline but never kept its first start (begun before it was kept) can
		// not say where it began, and the re-taken baseline would reopen the hole: the
		// range fails closed.
		var ok bool
		if sessionStart, ok, err = state.Meta(sessionstate.MetaSessionStart); err != nil {
			return gitrepo.Range{}, err
		}
		if sessionStart == sessionstate.SessionStartUnborn {
			sessionStart = gitrepo.EmptyTree // began before the first commit
		}
		if !ok {
			if baseline, had, err := state.Meta(sessionstate.MetaBaselineCommit); err != nil {
				return gitrepo.Range{}, err
			} else if had && baseline != "" {
				return gitrepo.Range{}, errSessionStartNotKept
			}
		}
		// A baseline first taken at a sub-agent's own Stop is where its work ENDED, not
		// where it began: not a floor. Without another, the range fails closed.
		if _, atStop, err := state.Meta(sessionstate.MetaBaselineAtStop); err != nil {
			return gitrepo.Range{}, err
		} else if atStop && sessionStart != gitrepo.EmptyTree {
			// (A session recorded as begun before the first commit keeps its empty-tree
			// start: that is where the work began, wherever the Stop first looked.)
			sessionStart = ""
		}
	}
	r, err := gitrepo.ResolveRange(root, repoRelative(root, g.Root()), watermark, sessionStart)
	if err != nil {
		return gitrepo.Range{}, err
	}
	if r.DroppedWatermark == "" {
		r.DroppedWatermark = dropped
	}
	return r, nil
}
