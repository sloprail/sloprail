package e2e

import (
	"fmt"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// mergeSpellings are the ways an agent that wants a pull request merged writes it: flags in
// every order, an env prefix, a shell wrapper, the binary's path, a subshell, a chain, a
// directory change, the gh front flag before the subcommand. All of them run `gh pr merge`.
var mergeSpellings = []string{
	`gh pr merge 5 --squash`,
	`gh pr merge --admin --merge --delete-branch`,
	`gh pr merge 5 --auto --rebase`,
	`gh pr merge`,
	`GH_PROMPT_DISABLED=1 gh pr merge 5`,
	`env GH_TOKEN=x gh pr merge 5 --squash`,
	`bash -c 'gh pr merge 5 --admin'`,
	`sh -c "gh pr merge --auto --merge"`,
	`(gh pr merge --rebase)`,
	`/usr/bin/gh pr merge 5`,
	`command gh pr merge 5`,
	`gh -R owner/repo pr merge 5`,
	`true && gh pr merge 5 --delete-branch`,
	`gh pr merge 5; true`,
	`cd /tmp && gh pr merge 5 --admin`,
	`echo go | gh pr merge 5`,
}

// T033_04: an open refusal is not walked around by respelling the merge.
func TestT033_04_EverySpellingOfAMergeOverAnOpenRefusalIsRefused(t *testing.T) {
	const sess = "s-033-04"
	e, proj := refusedProject(t, sess)

	for i, cmd := range mergeSpellings {
		res := e.Run(proj, sess, "merge it", Turns("done", Bash(fmt.Sprintf("m%d", i), cmd)))
		if !res.Refused() || !res.Saw("no-merge-over-refusals") {
			t.Errorf("%q merges over an open refusal and was not refused:\n%s", cmd, res.Output)
		}
	}

	// Words that are not a merge are left alone.
	for i, cmd := range []string{
		`echo "gh pr merge 5"`,
		`gh pr view 5`,
		`gh pr list --state merged`,
		`git commit -q --allow-empty -m "explain gh pr merge"`,
	} {
		res := e.Run(proj, sess, "look", Turns("done", Bash(fmt.Sprintf("n%d", i), cmd)))
		if res.Saw("no-merge-over-refusals") {
			t.Errorf("%q does not merge anything but was refused:\n%s", cmd, res.Output)
		}
	}
}

// T033_05: the same spellings over a tip no rule has judged are refused too.
func TestT033_05_EverySpellingOfAMergeOverAnUnjudgedTipIsRefused(t *testing.T) {
	const sess = "s-033-05"
	e, proj := unjudgedProject(t)
	e.Run(proj, sess, "write the docs", Turns("done", Bash("w", "true")))

	for i, cmd := range mergeSpellings {
		res := e.Run(proj, sess, "write and merge", Turns("done",
			harness.CommitFile(fmt.Sprintf("c%d", i), fmt.Sprintf("docs/f%d.md", i), "clean words", fmt.Sprintf("add f%d", i)),
			Bash(fmt.Sprintf("m%d", i), cmd),
		))
		if !res.Refused() || !res.Saw("no-merge-over-refusals") {
			t.Errorf("%q merges a tip no rule judged and was not refused:\n%s", cmd, res.Output)
		}
	}
}

// T033_06: the merge API is the same act as `gh pr merge`.
func TestT033_06_MergingThroughTheAPIOverAnOpenRefusalIsRefused(t *testing.T) {
	const sess = "s-033-06"
	e, proj := refusedProject(t, sess)

	for i, cmd := range []string{
		`gh api -X PUT repos/o/r/pulls/5/merge`,
		`gh api repos/o/r/pulls/5/merge --method PUT -f merge_method=squash`,
		`gh api --method=PUT /repos/o/r/pulls/5/merge`,
	} {
		res := e.Run(proj, sess, "merge it", Turns("done", Bash(fmt.Sprintf("a%d", i), cmd)))
		if !res.Refused() || !res.Saw("no-merge-over-refusals") {
			t.Errorf("%q merges through the API over an open refusal and was not refused:\n%s", cmd, res.Output)
		}
	}
}
