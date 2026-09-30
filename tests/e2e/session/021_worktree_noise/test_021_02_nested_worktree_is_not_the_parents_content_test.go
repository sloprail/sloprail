package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/tests/e2e/harness"
	"github.com/sloprail/sloprail/tests/e2e/session/changesetkit"
)

// A sub-agent's own worktree is not the parent's changed content.
//
// FIXED — task: strategy/memories/tasks/worktree-noise-in-parent-diff/TASK.md.
//
// The mechanism that was wrong. A sub-agent dispatched with isolation
// "worktree" gets a real `git worktree add` at `.claude/worktrees/agent-<id>` —
// a path INSIDE the parent's working tree — and nothing gitignores it.
// gitrepo.Changed unions the tracked diff with `git ls-files -z --others
// --exclude-standard`, and that second question reported the nested checkout as
// the parent's own untracked content.
//
// WHAT WAS MEASURED, because it is narrower than the task originally predicted
// and the difference decided where the fix could go:
//
//   - git does NOT descend into the child's checkout. It stops at the nested
//     `.git` and reports the whole thing as ONE path with a TRAILING SLASH:
//     `.claude/worktrees/agent-d1/`. So the pollution was a single directory
//     entry, not every file the sub-agent wrote.
//   - That trailing slash turned out to be the fix rather than an incidental
//     detail. In this listing git emits it for a nested repository and for
//     nothing else — an ordinary untracked directory is walked into and its
//     files named individually — so it is git's own report of a repository
//     boundary. untrackedPaths drops those paths;
//     TestUntrackedPaths_ATrailingSlashIsOnlyEverANestedRepository in
//     internal/gitrepo pins the claim the exclusion rests on.
//   - The noise never reached a rule, because filemod's lookAt stats a
//     directory and reports ErrPathIsNotAFile rather than emitting a kind. It
//     cost one diagnostic per cycle, absorbed one layer below the rules by a
//     check that exists for an unrelated reason. T021_03 records that, and
//     records it as a fact that must keep holding rather than as a consolation.
//
// T021_01 reproduced the defect and was deleted when the fix landed, on its own
// instructions. T021_02 is the invariant it guarded and is now live.

// recordEverything is a NEW-FORMAT file-guard that records every Changeset the cycle dispatches, so a test can read exactly which paths were put
// in front of a rule bound to the ROOT's own work. `match: "**/*.md"` fires on
// every committed change. ; used by T021_03, which drives the real
// dispatch. T021_02 above reads gitrepo.Changed directly and is format-neutral.)
// The ledger (`seen`, no `.md`) is not matched, so the guard cannot re-observe its
// own bookkeeping.
const recordEverything = `match: "**/*.md"
checks:
  - script: ./record.sh
`

// underWorktree reports the observed paths that lie inside a nested worktree.
func underWorktree(got []changesetkit.Observed) []string {
	var out []string
	for _, o := range got {
		if strings.Contains(o.Path, ".claude/worktrees/") {
			out = append(out, o.Path)
		}
	}
	return out
}

// repoWithNestedWorktree builds a repository holding a real nested worktree at
// the path Claude Code uses, plus one ordinary change of the root's own.
//
// Built with git directly rather than through a dispatch, because what these two
// tests are about is the DIFFERENCE — what gitrepo.Changed reports about a tree
// in this shape. Driving a sub-agent to produce the shape would add a dependency
// on the mock's dispatch behaviour to a claim that has nothing to do with it.
// T021_03 covers the real dispatch separately, and asserts that the shape it
// produces is this one.
//
// Returns the directory and the baseline commit the difference is measured from.
func repoWithNestedWorktree(t *testing.T) (dir, baseline string) {
	t.Helper()
	dir = t.TempDir()
	harness.InitRepo(t, dir)

	if err := os.WriteFile(filepath.Join(dir, "tracked.md"), []byte("before\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	baseline = harness.CommitAllIn(t, dir, "the project before the session")

	// The sub-agent's tree, at the path Claude Code binds it to.
	harness.Git(t, dir, "worktree", "add", filepath.Join(".claude", "worktrees", "agent-d1"))

	// The root's own work, so every assertion below has a positive to rest on.
	if err := os.WriteFile(filepath.Join(dir, "root-own.md"), []byte("the root's own work\n"), 0o644); err != nil {
		t.Fatalf("root write: %v", err)
	}

	// The premise, asserted rather than assumed: git really does consider that
	// directory a separate checkout. Without it this is a test about an
	// ordinary untracked directory with a suggestive name.
	if list := harness.Git(t, dir, "worktree", "list"); !strings.Contains(list, filepath.Join(".claude", "worktrees", "agent-d1")) {
		t.Fatalf("git does not report a nested worktree:\n%s", list)
	}
	return dir, baseline
}

// T021_01 was the reproduction: it asserted the DEFECT, so it passed while the
// defect was present and failed the day the fix landed. Deleted then, on its own
// instructions, because a reproduction of a defect that no longer exists can
// only ever fail.
//
// What it was guarding against — T021_02 becoming vacuous by the arrangement
// silently stopping producing a nested worktree — did not go away with it, so it
// was not simply dropped. It moved INTO T021_02 as a premise: that test now
// requires git to report the worktree in its raw listing before it asserts the
// engine is silent about it, which holds the same line without needing a test
// that must be deleted to succeed.

// T021_02: THE INVARIANT — a cycle in the root's tree reports what the root's
// session changed, and a nested sub-agent worktree is not part of that.
//
// The task's stated outcome, asserted against the difference itself.
//
// The fix is NOT ignoring `.claude/` wholesale — the guardrail declarations live
// there and are exactly the content the engine must watch change. It is
// excluding a nested WORKING TREE: a directory git itself reports as a separate
// checkout is not this tree's content, whoever put it there.
//
// It is also not derived from `git worktree list`, which is what this comment
// used to propose. That command knows only about linked worktrees of THIS
// repository, and the noise is not particular to those — an unrelated clone
// sitting in the tree produces the identical entry for the identical reason.
// What the exclusion reads instead is the TRAILING SLASH git puts on a path in
// `ls-files --others` when it declines to descend into a nested repository. It
// is the same fact, already in the output being read, and it keeps the rule
// about what the thing IS rather than about the path one harness happens to use
// — which matters because sloprail is harness-agnostic and
// `.claude/worktrees/` is Claude Code's layout, not a contract.
//
// TWO premises are required before the silence is read, and the second is the
// one T021_01 used to supply: the root's own change must be present (or the
// differ is reporting nothing and any absence is meaningless), and git must
// still be naming the worktree in its own raw listing (or the arrangement has
// stopped producing the noise and the engine's silence is not the engine's).
func TestT021_02_ANestedWorktreeIsNotTheParentsChangedContent(t *testing.T) {
	dir, baseline := repoWithNestedWorktree(t)

	// The anti-vacuity premise, inherited from the deleted T021_01: the noise
	// this test asserts the absence of must actually be on offer. git names the
	// nested checkout in the untracked listing — as one path with a trailing
	// slash — and gitrepo.Changed is what declines to carry it.
	others := harness.Git(t, dir, "ls-files", "-z", "--others", "--exclude-standard", "--full-name")
	var offered []string
	for _, p := range strings.Split(others, "\x00") {
		if strings.Contains(p, ".claude/worktrees/") {
			offered = append(offered, p)
		}
	}
	if len(offered) == 0 {
		t.Fatalf("git no longer names the nested checkout in its untracked listing:\n%q\n"+
			"there is no noise here for the engine to be excluding, so this test's silence "+
			"proves nothing and the exclusion is now untested", others)
	}
	for _, p := range offered {
		if !strings.HasSuffix(p, "/") {
			t.Fatalf("git named the nested checkout as %q, without the trailing slash the "+
				"exclusion keys on — git's behaviour has changed and the fix no longer "+
				"rests on what it was measured against", p)
		}
	}

	changes, err := gitrepo.Changed(dir, baseline)
	if err != nil {
		t.Fatalf("the difference could not be taken at all: %v", err)
	}

	// The positive first, always. "Nothing was reported" is the easiest
	// assertion here to write so that it can never fail.
	var sawRoot bool
	var nested []string
	for _, c := range changes {
		if c.Path == "root-own.md" {
			sawRoot = true
		}
		if strings.Contains(c.Path, ".claude/worktrees/") {
			nested = append(nested, c.Path)
		}
	}
	if !sawRoot {
		t.Fatalf("the root's own change is missing from the difference (%v) — nothing was "+
			"observed, so the silence asserted below proves nothing", changes)
	}

	if len(nested) > 0 {
		t.Fatalf("the root's difference includes %d path(s) belonging to a sub-agent's own "+
			"checkout: %v\nthe root's cycle is accounting for a tree that is not its own", len(nested), nested)
	}
}

// T021_03: through the real wiring — a dispatched sub-agent's worktree, and what
// a guardrail bound to the root's files is actually shown.
//
// The end-to-end half. It asserts the same silence as T021_02 but one layer up
// and through the real dispatch rather than a hand-built tree, which is what
// makes it a different test rather than a restatement: T021_02 proves
// gitrepo.Changed does not carry the path, this proves nothing else downstream
// puts it back.
//
// Its history is worth keeping, because it explains why this directory's e2e
// could not bite before the fix. The noise never reached a rule even when the
// difference was polluted — filemod's lookAt stats
// `.claude/worktrees/agent-<id>/`, finds a directory, and reports
// ErrPathIsNotAFile instead of emitting an event. So the leakage this test would
// have caught was already absorbed one layer below the rules by a check that
// exists for an unrelated reason, and the defect showed up as a diagnostic per
// cycle rather than as a guardrail judging another tree's file.
//
// That absorption is now defence in depth rather than the only defence, and this
// test is what would notice if a future kind were bound to something other than
// a regular file — at which point the exclusion in gitrepo is the only thing
// standing between a sub-agent's checkout and a rule about this project.
//
// The root's own file is asserted first so the silence about the worktree is
// read against a ledger that has just been shown to register something.
func TestT021_03_TheNoiseDoesNotReachAFileRule(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	led := e.NewLedger("seen")
	e.FileGuard(proj, "watcher", recordEverything, map[string]string{"record.sh": changesetkit.RecordScript(led.Path())})
	e.DisableShippedFileGuards(proj)
	e.CommitAll(proj, "the project before the session")

	// The mock does not APPLY a sub-agent's tool calls, so this script's Write
	// creates nothing. What matters is the dispatch itself, which binds a real
	// `git worktree add` inside the parent's tree.
	subScript := filepath.Join(t.TempDir(), "sub.sh")
	if err := Turns("delegated done", harness.CommitFile("s1", "child.md", "written in the child tree\n", "the child's work")).
		Script(subScript); err != nil {
		t.Fatalf("write sub-agent scenario: %v", err)
	}

	e.InstallClaudeShim(proj)
	e.Run(proj, "s-021-03", "delegate into a worktree", Turns("done",
		Write("w1", "root-own.md", "the root's own work\n"),
		Dispatch("d1", "do the delegated thing", subScript, "worktree"),
	).ThenCommit("the root's work"))

	// The premise: a worktree really was bound inside the parent's tree, and git
	// reports it as untracked content of the parent. Without both, the silence
	// below is about a cycle that had no noise to be shielded from.
	entries, err := os.ReadDir(filepath.Join(proj, ".claude", "worktrees"))
	if err != nil || len(entries) == 0 {
		t.Fatalf("no worktree was bound under .claude/worktrees (%v) — isolation=%q did not "+
			"create a nested checkout, so this proves nothing", err, "worktree")
	}
	// The agent's own `git add -A` commits the nested checkout as an embedded
	// repository (a mode-160000 entry), so it is in the parent's committed range
	// rather than merely untracked: the entry the rule must not be shown.
	if staged := harness.Git(t, proj, "ls-files", "-s", "--full-name", "--", ".claude/worktrees"); !strings.Contains(staged, "160000") {
		t.Fatalf("the parent's history does not hold the nested checkout as an embedded repository:\n%s\n"+
			"the pollution this test is about is not present, so its conclusion would be vacuous", staged)
	}

	got := changesetkit.Files(t, led.Lines())

	// The control: the root's own work reached the rule.
	if !changesetkit.Saw(got, "root-own.md") {
		t.Fatalf("the root's own file never reached the rule: %v — this cycle dispatched nothing "+
			"recognisable, so what it did or did not include proves nothing", got)
	}

	// The recorded fact: no worktree path arrived as a file event, because it is
	// a directory and filemod refuses to call one a file.
	if leaked := underWorktree(got); len(leaked) > 0 {
		t.Fatalf("a path inside the sub-agent's own checkout reached a rule bound to this "+
			"project's files: %v\nthis is the defect arriving one layer higher than it does "+
			"today — the guardrail is judging a file of a different tree", leaked)
	}
}
