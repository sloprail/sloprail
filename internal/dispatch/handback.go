package dispatch

import (
	"fmt"
	"strings"

	"github.com/sloprail/sloprail/internal/changeset"
)

// handbackPatch is where a sub-agent saves what it hands back: outside the
// repository, so it is no change of the branch and no ref any Stop judges.
const handbackPatch = `"${TMPDIR:-/tmp}/handback.patch"`

// subagentHandback is the refusal for a SUB-AGENT whose change must cite the
// user's own words. A sub-agent cannot ask the user, and its prompt is the
// parent's, not the user's. Left with only "cite the user", it tells its parent
// to add trailers at merge, and the parent then blankets the squash with one —
// a citation that grounds nothing. So it is told to hand the change back: save
// it as a patch FILE, revert it on its own branch so the branch carries nothing
// uncited, and report to the parent, who asks the user, re-applies the patch and
// commits it with the user's exact answer as the trailer.
//
// The backup is a file and never a branch, a tag or a stash: every branch is
// recorded and judged at the sub-agent's Stop, so a backup branch would be
// refused again.
// sr:invariant subagents/citation-refusal-hands-the-change-back
func subagentHandback(subject string, req Request) string {
	base, files := "<base>", "<files>"
	if req.Changeset != nil {
		if b := req.Changeset.Changeset.Base; b != "" {
			base = b
		}
		if len(req.Changeset.Changeset.Files) > 0 {
			var paths []string
			for _, f := range req.Changeset.Changeset.Files {
				paths = append(paths, quotePath(f.Path))
			}
			files = strings.Join(paths, " ")
		}
	}
	if p, _ := req.Event.Fields["path"].(string); p != "" {
		files = quotePath(p)
	}
	return fmt.Sprintf("%s\n"+
		"You are a sub-agent: you cannot get the user's words yourself (your prompt is your parent's, not the user's), and you cannot ask the user. "+
		"Do not leave this for the parent to cover at merge with a trailer — that grounds nothing. Hand the change back instead:\n"+
		"1. Save it as a patch file outside the repository (a file, never a branch, tag or stash: any ref holding it is judged at your Stop too):\n"+
		"  `git diff --binary %s..HEAD -- %s > %s`\n"+
		"2. Revert those files on your branch in a real commit, so your branch carries nothing uncited:\n"+
		"  `git apply -R --index %s && git commit -m 'revert: handed back to the parent for the user approval'`\n"+
		"3. Report to your parent: where the patch is (%s), and EXACTLY what needs the user's approval. "+
		"The parent asks the user (AskUserQuestion), re-applies the patch (`git apply --index <patch>`), and commits it with `%s: <the user's exact answer>`.",
		subject, base, files, handbackPatch, handbackPatch, handbackPatch, changeset.TrailerCitesUser)
}

// quotePath single-quotes s unless it is plainly safe bare.
func quotePath(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._/-") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
