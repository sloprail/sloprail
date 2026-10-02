package e2e

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T015_11: a sub-agent working in a SUBDIRECTORY is judged on paths relative to
// its own tree's root.
//
// The layout case that decides whether a rule can be written about a path at
// all. A sub-agent bound to a worktree has its own root, and the file it touches
// deep inside that tree must be named the same way a rule's author would name it
// — relative to the root of the tree being judged, not to the sub-agent's
// current directory and not as an absolute path into a temporary worktree.
//
// An absolute path here would be worse than untidy: it embeds the worktree's
// generated name, so a rule matching on path could never be written to fire in a
// sub-agent, and the verdicts recorded under it would key on a path that never
// occurs again.
//
// A file-guard is judged by `sr check run --base --head`, not at the sub-agent's
// stop, so the sub-agent's work is judged the way a caller in its worktree would
// judge it: `sr check run` from inside the worktree, over the commits since the
// session began. The path reported must be pkg/deep/nested.md — relative to the
// worktree root, exactly as a file in the project's own tree would be.
func TestT015_11_ASubagentInASubdirectoryIsJudgedOnTreeRelativePaths(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.FileGuard(proj, "recorder", recordsPathAndSession, map[string]string{"record.sh": recordScript})
	e.GitInit(proj)

	sub := harness.SubagentScript(t, harness.Turns("sub done",
		Bash("sb1", "mkdir -p pkg/deep && echo nested > pkg/deep/nested.md"),
	).ThenCommit("the sub-agent's work"))

	res := e.Run(proj, "s-015-11", "delegate work in a subdirectory", Turns("root done",
		Dispatch("d1", "work deep in the tree", sub, "worktree"),
	))

	if !res.Saw("root done") {
		t.Fatalf("the delegated cycle did not complete:\n%s", res.Output)
	}
	if hitRetryCap(res.Output) {
		t.Fatalf("the sub-agent hit the retry cap:\n%s", res.Output)
	}

	wt := theWorktree(t, proj)
	e.CheckRunRange(filepath.Join(proj, ".claude", "worktrees", wt), "s-015-11", e.RunBase("s-015-11"), "HEAD")
	lines := subLedger(t, proj, wt, "recorder", "log")
	if len(lines) == 0 {
		t.Fatalf("judging the sub-agent's worktree judged nothing, though it created a file in "+
			"its own tree:\n%s", res.Output)
	}
	if !containsPath(lines, "pkg/deep/nested.md") {
		var got []string
		for _, l := range lines {
			got = append(got, pathOf(l))
		}
		t.Fatalf("the sub-agent's file was not reported at its tree-relative path. Want "+
			"pkg/deep/nested.md, got %v. A path reported absolutely embeds the worktree's "+
			"generated name, so no rule could ever be written to match it and every verdict "+
			"recorded under it keys on a path that never recurs", got)
	}

	// And specifically NOT absolute. containsPath above would still pass if some
	// other line carried the relative form while the real one was absolute.
	for _, l := range lines {
		if strings.HasPrefix(pathOf(l), "/") {
			t.Fatalf("a path was reported absolutely (%s) — see above for why that makes a rule "+
				"unwritable. Ledger: %v", pathOf(l), lines)
		}
	}
}
