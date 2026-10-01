package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/sloprail/sloprail/internal/gitrepo"
)

// stopTip is one line of history judged at Stop. The zero value is HEAD.
type stopTip struct {
	Sha string
	Ref string
	// Start is where the ref was created (its oldest reflog entry), or "": a floor for
	// the range so upstream commits merged before the branch was cut are not judged.
	Start string
	// Landed is true when the tip's changes are already upstream (squash-merged) yet it is
	// still owed a judgement: only what still stands upstream is judged (see prepare).
	Landed bool
	// Gone is true when the ref no longer exists (its branch was deleted, or its worktree
	// removed): the tip is held by the engine's pin, and judged on its own tree.
	Gone bool
	// Origin says whose work this was when another agent's folder is gone and the work was
	// handed to this agent ("sub-agent <id> in <path>"), or "".
	Origin string
}

func orphanOriginKey(folder, name string) string {
	return "orphan_origin:" + filepath.Clean(folder) + "|" + name
}

func goneRef(root, name string) bool {
	cur, err := gitrepo.RefTip(root, name)
	return err == nil && cur == ""
}

var slugUnsafe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// slug is a branch name made safe to use inside a path and a new branch name.
func slug(name string) string {
	name = strings.TrimPrefix(strings.TrimPrefix(name, "refs/heads/"), "refs/remotes/")
	if strings.HasPrefix(name, "detached/") {
		name = "work"
	}
	name = strings.Trim(slugUnsafe.ReplaceAllString(name, "-"), "-.")
	if name == "" {
		return "work"
	}
	return name
}

// freshPath is a directory beside the folder that does not exist yet, to name in a
// command the agent can run as it stands.
func freshPath(folder, suffix string) string {
	base := filepath.Join(filepath.Dir(folder), filepath.Base(folder)+"-"+suffix)
	path := base
	for i := 2; i < 50; i++ {
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			return path
		}
		path = fmt.Sprintf("%s-%d", base, i)
	}
	return path
}

// upstreamName is the default branch work lands on, as a name a command can use
// (origin/main), or "".
func upstreamName(root string) string {
	return strings.TrimPrefix(gitrepo.UpstreamRef(root), "refs/remotes/")
}

// citationOnly is what to do when the finding needs no change to the work, only the user's
// word on it (a rule that asks for grounding): ask them now, never record it and move on.
func citationOnly(up, folder string, t stopTip) string {
	base := up
	if base == "" {
		base = "the default branch"
	}
	name := strings.TrimPrefix(strings.TrimPrefix(t.Ref, "refs/heads/"), "refs/remotes/")
	return fmt.Sprintf("If the finding is only a missing citation (the work itself needs no change), do not park it and do not record it and move on: "+
		"ask the user NOW with AskUserQuestion, quoting the exact change (the file and what changed) and offering exactly two choices, \"keep it\" and \"revert it\". "+
		"If they keep it, cite their answer in a follow-up commit that makes the change real on a new branch from %s, or, when nothing remains to change, run "+
		"`sr-session refs abandon --ref %s --folder %s --cite-user '<their exact words>'`. If they revert it, make a revert commit on a new branch from %s and open a revert pull request. ",
		base, shellQuote(name), shellQuote(folder), base)
}

// dropSentence is how the user can have a branch dropped.
func (t stopTip) dropSentence(folder string) string {
	name := strings.TrimPrefix(strings.TrimPrefix(t.Ref, "refs/heads/"), "refs/remotes/")
	return fmt.Sprintf("If the USER wants this branch dropped, ask them, then run `sr-session refs abandon --ref %s --folder %s "+
		"--cite-user '<their exact words>'` citing what they said. ", shellQuote(name), shellQuote(folder))
}

// describe is what a refusal for this tip says first: which work, in which folder, and
// what state it is in. remedy is what the refusal ends with: the commands that fix it.
// Both are empty for HEAD.
func (t stopTip) describe(folder string) (head, remedy string) {
	if t.Sha == "" {
		return "", ""
	}
	name := strings.TrimPrefix(strings.TrimPrefix(t.Ref, "refs/heads/"), "refs/remotes/")
	from := ""
	if t.Origin != "" {
		from = fmt.Sprintf("You now own this: %s is gone and its work at %s was never judged (its commits were pinned and are judged here, on their own tree). "+
			"It is not someone else's problem: nobody else will judge it. ", t.Origin, short(t.Sha))
	}
	// Never tell the agent to switch this folder's checkout: it is the coordination
	// worktree, and the ref is judged on its own tree wherever it lives.
	if strings.HasPrefix(t.Ref, "detached/") {
		return fmt.Sprintf("%sOn commits made on a detached HEAD and left (%s, in %s), which are not checked out: "+
			"they are judged on their own tree. Give them a branch and a worktree of their own "+
			"(`git -C %s branch <name> %s`, then `git -C %s worktree add <path> <name>`), fix there, commit, and stop again; "+
			"do not switch this folder's checkout. %s",
			from, short(t.Sha), folder, shellQuote(folder), short(t.Sha), shellQuote(folder), t.dropSentence(folder)), ""
	}
	if up := upstreamName(folder); t.Landed && up != "" {
		carrier := ""
		if c := gitrepo.CommitCarrying(folder, up, t.Sha); c != "" {
			carrier = fmt.Sprintf(" (carried by %s)", c)
		}
		head = fmt.Sprintf("%sOn branch %s (tip %s, in %s), whose work is already on %s%s, but no rule had judged it. "+
			"Its branch is not the place to fix it any more: what the default branch holds is what counts. ",
			from, name, short(t.Sha), folder, up, carrier)
		path := freshPath(folder, "fix-"+slug(name))
		remedy = fmt.Sprintf("To fix it: create a branch from %s (`git -C %s worktree add %s -b fix/%s %s`), make the real fix to the files named above "+
			"(it supersedes the landed content), commit there (with a citation trailer when the rule asks for one), and stop again; "+
			"once %s no longer holds the refused content the work is settled as superseded. Do not switch this folder's checkout. %s%s",
			up, shellQuote(folder), shellQuote(path), slug(name), up, up, citationOnly(up, folder, t), t.dropSentence(folder))
		return head, remedy
	}
	if t.Gone {
		path := freshPath(folder, "restore-"+slug(name))
		up := upstreamName(folder)
		return fmt.Sprintf("%sThe work on %s (tip %s, in %s) is on no branch any more (it was deleted, or its worktree removed), and no rule had judged it: "+
				"its commits are pinned and judged on their own tree. ", from, name, short(t.Sha), folder),
			fmt.Sprintf("To fix it: restore the branch from the pinned tip (`git -C %s worktree add %s -b %s %s`), fix in that worktree, commit, and stop again. "+
				"Do not switch this folder's checkout. %s%s",
				shellQuote(folder), shellQuote(path), slug(name), t.Sha, citationOnly(up, folder, t), t.dropSentence(folder))
	}
	if other := gitrepo.CheckedOutAt(folder, t.Ref); other != "" {
		return fmt.Sprintf("%sOn branch %s, which is checked out in another worktree (%s): this session committed on it, "+
			"so its commits are judged too, on that branch's own tree. Fix it in that worktree (`cd %s`), commit, and stop again; "+
			"do not switch this folder (%s) to it. %s",
			from, name, other, shellQuote(other), folder, t.dropSentence(folder)), ""
	}
	path := freshPath(folder, slug(name))
	return fmt.Sprintf("%sOn branch %s (in %s), which is not checked out: this session committed on it, "+
		"so its commits are judged too, on that branch's own tree. Fix it in a new worktree "+
		"(`git -C %s worktree add %s %s`), commit there, and stop again; do not switch this folder's checkout. %s",
		from, name, folder, shellQuote(folder), shellQuote(path), shellQuote(name), t.dropSentence(folder)), ""
}
