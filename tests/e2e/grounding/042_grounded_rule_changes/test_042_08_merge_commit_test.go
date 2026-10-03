package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

const asUpstream = "-c user.name=upstream -c user.email=up@example.com"

// bigScript is the demo rule's check with room between its lines, so two edits to
// different places merge with no conflict.
const bigScript = "#!/bin/sh\ncat >/dev/null\n# 1\n# 2\n# 3\n# 4\n# 5\n# 6\n# 7\n# 8\n# 9\nexit 0\n"

// remoteWithUpstream pushes proj's default branch to the origin GitInit gave it, and returns
// a function that lands one more commit upstream (from a second clone).
func remoteWithUpstream(t *testing.T, e *harness.Env, proj string) (land func(path, content, msg string)) {
	t.Helper()
	main := e.Git(proj, "branch", "--show-current")
	e.Git(proj, "push", "-q", "origin", "HEAD:refs/heads/"+main)
	e.Git(proj, "fetch", "-q", "origin")
	up := e.CloneFresh(proj)
	return func(path, content, msg string) {
		t.Helper()
		e.WriteFile(up, path, content)
		e.Git(up, "add", "-A")
		args := append(strings.Fields(asUpstream), "commit", "-q", "-m", msg)
		e.Git(up, args...)
		e.Git(up, "push", "-q", "origin", "HEAD:refs/heads/"+main)
	}
}

// authoringDocs is the authoring skill and the script-checks pages, which the plugin's own
// gates want read before a rule is edited.
func authoringDocs(t *testing.T) []harness.Turn {
	t.Helper()
	return []harness.Turn{
		harness.Skill("sk", "authoring-guardrails"),
		harness.ToolUse("r1", "Read", map[string]string{"file_path": harness.ShippedSkillFile(t, "script-checks.md")}),
		harness.ToolUse("r2", "Read", map[string]string{"file_path": harness.ShippedSkillFile(t, "check-template.sh")}),
	}
}

// citedEditThenMerge is the session editing the demo rule on a branch of its own with
// the user's words behind it (committed with the citation), then merging main into it.
func citedEditThenMerge(t *testing.T, e *harness.Env, proj, sess, ask, edited, main string, after ...harness.Turn) harness.Scenario {
	t.Helper()
	turns := append(authoringDocs(t),
		Bash("b1", "git switch -q -c feat"),
		Bash("c1", "sr-file write .sloprail/file-guard/demo/check.sh --content "+shq(edited)+" --cite:user "+shq(ask)),
		harness.Commit("c2", "edit the demo rule", harness.CitesUser(ask)),
		Bash("m1", "git fetch -q origin && { git merge --no-ff -q -m 'Merge remote-tracking branch origin/"+main+"' origin/"+main+" || true; }"),
	)
	return Turns("done", append(turns, after...)...)
}

// T042_30: a clean merge of upstream into the session's branch is not the commit that
// "last changed" the protected file. The session edited the rule at the end (cited),
// upstream at the top; the merge resolved nothing, so it needs no citation of its own
// and the refusal never names it. The range is the branch against the upstream it
// merged (base origin/<main>), the way a caller judges a branch for its target: the
// upstream commit is the target's, not the branch's.
func TestT042_30_ACleanMergeIsNotTheLastChanger(t *testing.T) {
	e := New(t)
	proj := project(t, e)
	e.WriteFile(proj, ".sloprail/file-guard/demo/check.sh", bigScript)
	e.CommitAll(proj, "a bigger demo rule")
	land := remoteWithUpstream(t, e, proj)
	main := e.Git(proj, "branch", "--show-current")
	land(".sloprail/file-guard/demo/check.sh", "# upstream\n"+bigScript, "upstream edits the demo rule")

	const ask = "tighten the demo rule at its end"
	e.Run(proj, "s-042-30", ask, citedEditThenMerge(t, e, proj, "s-042-30", ask,
		strings.Replace(bigScript, "# 9\n", "# 9\n# tighter\n", 1), main))
	res := e.CheckRunRaw(proj, "s-042-30", "origin/"+main, "HEAD")
	if got, out := res.Code != 0 && strings.Contains(res.Output, "grounded-rule-changes"), res.Output; got {
		t.Fatalf("a clean merge of upstream was refused for the rule change it carried:\n%s", out)
	}
}

// T042_31: a merge that RESOLVED a conflict in a protected file did change it: refused,
// naming the merge, until the merge commit carries the citation.
func TestT042_31_AConflictResolvingMergeStillMustCite(t *testing.T) {
	e := NewUncited(t)
	proj := project(t, e)
	e.WriteFile(proj, ".sloprail/file-guard/demo/check.sh", bigScript)
	e.CommitAll(proj, "a bigger demo rule")
	land := remoteWithUpstream(t, e, proj)
	main := e.Git(proj, "branch", "--show-current")
	land(".sloprail/file-guard/demo/check.sh", strings.Replace(bigScript, "# 5\n", "# five, upstream\n", 1), "upstream edits the demo rule")

	const ask = "tighten the demo rule in the middle"
	resolved := strings.Replace(bigScript, "# 5\n", "# five, resolved by hand\n", 1)
	e.Run(proj, "s-042-31", ask, citedEditThenMerge(t, e, proj, "s-042-31", ask,
		strings.Replace(bigScript, "# 5\n", "# five, tighter\n", 1), main,
		Bash("m2", "printf '%s' "+shq(resolved)+" > .sloprail/file-guard/demo/check.sh && git add -A && git commit -q --no-edit"),
	))
	got, out := blocked(e, proj, "s-042-31")
	if !got || !strings.Contains(out, "Merge remote-tracking branch") {
		t.Fatalf("a merge that resolved a conflict in a protected file was not refused as its last changer:\n%s", out)
	}
	e.Run(proj, "s-042-31", "keep the resolution", Turns("done",
		harness.AmendLast("amend", "Merge remote-tracking branch origin/"+main, harness.CitesUser(ask))))
	if got, out := blocked(e, proj, "s-042-31"); got {
		t.Fatalf("the merge was still refused once its commit carried the citation:\n%s", out)
	}
}
