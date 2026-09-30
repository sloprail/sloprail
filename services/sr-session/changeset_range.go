package main

import (
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
// base is the first usable of: the rule's watermark — derived from the check
// results as the newest FINISHED run that passed at the rule's current
// definition and whose head is still an ancestor of HEAD — then the last commit
// touching the rule's folder (a rule in this repository), then the HEAD
// recorded when the session began. results and state may be nil (nothing
// recorded yet); any failure of either, or of git, is an error and no range.
//
// A watermark that had to be passed over — an amend or a rebase orphaned it — is
// reported on the returned range as DroppedWatermark, so a range that widened
// says why. gitrepo.ErrNoCommits is returned as itself: nothing is committed, so
// nothing can be judged.
func resolveRuleRange(root string, g declaration.FileGuard, ruleHash string, results checkstore.Store, state sessionstate.Store) (gitrepo.Range, error) {
	var watermark, dropped, sessionStart string
	if results != nil {
		heads, err := results.PassedHeads(g.Qualified(), ruleHash)
		if err != nil {
			return gitrepo.Range{}, err
		}
		if watermark, dropped, err = changeset.PickWatermark(heads, func(sha string) (bool, error) {
			return gitrepo.Contains(root, sha)
		}); err != nil {
			return gitrepo.Range{}, err
		}
	}
	if state != nil {
		var err error
		if sessionStart, _, err = state.Meta(sessionstate.MetaBaselineCommit); err != nil {
			return gitrepo.Range{}, err
		}
	}
	r, err := gitrepo.ResolveRange(root, repoRelative(root, g.Dir), watermark, sessionStart)
	if err != nil {
		return gitrepo.Range{}, err
	}
	if r.Origin != gitrepo.FromWatermark && r.DroppedWatermark == "" {
		r.DroppedWatermark = dropped
	}
	return r, nil
}
