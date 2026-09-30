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
func resolveRuleRange(root string, g declaration.FileGuard, results checkstore.Store, state sessionstate.Store) (gitrepo.Range, error) {
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
	}
	if state != nil {
		var err error
		if sessionStart, _, err = state.Meta(sessionstate.MetaBaselineCommit); err != nil {
			return gitrepo.Range{}, err
		}
		// A baseline first taken at a sub-agent's own Stop is where its work ENDED, not
		// where it began: not a floor. Without another, the range fails closed.
		if _, atStop, err := state.Meta(sessionstate.MetaBaselineAtStop); err != nil {
			return gitrepo.Range{}, err
		} else if atStop {
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
