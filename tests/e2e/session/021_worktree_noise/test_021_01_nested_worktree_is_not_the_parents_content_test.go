package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/internal/gitrepo"
)

// A sub-agent's own worktree is not the parent's changed content.
//
// THIS DIRECTORY PINS A KNOWN DEFECT — task:
// strategy/memories/tasks/worktree-noise-in-parent-diff/TASK.md, not started.
//
// The mechanism. A sub-agent dispatched with isolation "worktree" gets a real
// `git worktree add` at `.claude/worktrees/agent-<id>` — a path INSIDE the
// parent's working tree — and nothing gitignores it. gitrepo.Changed unions the
// tracked diff with `git ls-files -z --others --exclude-standard`, and that
// second question reports the nested checkout as the parent's own untracked
// content.
//
// WHAT WAS MEASURED, because it differs from what the task predicts and the
// difference decides where the test can bite:
//
//   - git does NOT descend into the child's checkout. It stops at the nested
//     `.git` and reports the whole thing as ONE path with a trailing slash:
//     `.claude/worktrees/agent-d1/`. So the parent's difference is polluted by a
//     single directory entry, not by every file the sub-agent wrote. The task's
//     "walks the child's whole checkout" overstates it; the pollution is real
//     but its shape is one path.
//   - That path DOES reach gitrepo.Changed, which is where T021_01 pins it.
//   - It does NOT reach a guardrail as a file event, because filemod's lookAt
//     stats it, finds a directory, and reports ErrPathIsNotAFile rather than
//     emitting a kind. So today the noise is absorbed one layer below the rules
//     by a check that exists for an unrelated reason.
//
// That last point is why the e2e half (T021_03) currently observes silence
// rather than leakage — and why it is written as a REPRODUCTION of the present
// behaviour rather than as the invariant. The invariant test is T021_02, which
// asserts what must be true of the difference itself and fails today.

// bindPostFileEvents records every after-the-fact file event the cycle
// dispatches, so a test can read exactly which paths were put in front of a rule
// bound to the ROOT's own work.
const bindPostFileEvents = `---
hooks:
  PostFileCreate:
    - hooks:
        - type: command
          command: ./record.sh
  PostFileUpdate:
    - hooks:
        - type: command
          command: ./record.sh
  PostFileDelete:
    - hooks:
        - type: command
          command: ./record.sh
---

# Records every after-the-fact file event it is handed
`

const recordScript = `#!/bin/sh
cat >> "$PWD/seen"
echo >> "$PWD/seen"
exit 0
`

type observed struct {
	Kind string
	Path string
}

func observedFiles(t *testing.T, lines []string) []observed {
	t.Helper()
	var got []observed
	for _, line := range lines {
		var p struct {
			Event struct {
				Kind   string         `json:"kind"`
				Fields map[string]any `json:"fields"`
			} `json:"event"`
		}
		if err := json.Unmarshal([]byte(line), &p); err != nil {
			t.Fatalf("hook was handed something that is not an event payload: %v\n%s", err, line)
		}
		path, _ := p.Event.Fields["path"].(string)
		got = append(got, observed{Kind: p.Event.Kind, Path: path})
	}
	return got
}

func sawPath(got []observed, path string) bool {
	for _, o := range got {
		if o.Path == path {
			return true
		}
	}
	return false
}

// underWorktree reports the observed paths that lie inside a nested worktree.
func underWorktree(got []observed) []string {
	var out []string
	for _, o := range got {
		if strings.Contains(o.Path, ".claude/worktrees/") {
			out = append(out, o.Path)
		}
	}
	return out
}

// git runs a git command in dir and returns its output, failing the test if it
// cannot run at all.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
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
	git(t, dir, "init", "--initial-branch=main", ".")
	git(t, dir, "config", "user.email", "e2e@example.invalid")
	git(t, dir, "config", "user.name", "E2E")

	if err := os.WriteFile(filepath.Join(dir, "tracked.md"), []byte("before\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-m", "the project before the session")
	baseline = strings.TrimSpace(git(t, dir, "rev-parse", "HEAD"))

	// The sub-agent's tree, at the path Claude Code binds it to.
	git(t, dir, "worktree", "add", filepath.Join(".claude", "worktrees", "agent-d1"))

	// The root's own work, so every assertion below has a positive to rest on.
	if err := os.WriteFile(filepath.Join(dir, "root-own.md"), []byte("the root's own work\n"), 0o644); err != nil {
		t.Fatalf("root write: %v", err)
	}

	// The premise, asserted rather than assumed: git really does consider that
	// directory a separate checkout. Without it this is a test about an
	// ordinary untracked directory with a suggestive name.
	if list := git(t, dir, "worktree", "list"); !strings.Contains(list, filepath.Join(".claude", "worktrees", "agent-d1")) {
		t.Fatalf("git does not report a nested worktree:\n%s", list)
	}
	return dir, baseline
}

// T021_01: THE REPRODUCTION — the nested checkout really is in the parent's
// difference.
//
// Asserts the DEFECT, so it passes today and is expected to FAIL the day the
// task lands. That inversion is deliberate: it makes the defect a measured fact
// rather than a claim, and it means the invariant below cannot quietly become
// vacuous — if this stops reproducing, this test says so.
//
// Delete this test and un-skip T021_02 when nested worktrees are excluded.
func TestT021_01_ANestedWorktreeIsCurrentlyInTheParentsDifference(t *testing.T) {
	dir, baseline := repoWithNestedWorktree(t)

	changes, err := gitrepo.Changed(dir, baseline)
	if err != nil {
		t.Fatalf("the difference could not be taken at all: %v", err)
	}

	// The control: the root's own work IS in the difference. Without it, "the
	// worktree is in the difference" could be read off a differ that reports
	// everything, and the absence T021_02 wants would be unreadable.
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
		t.Fatalf("the root's own change is missing from the difference (%v) — the differ is not "+
			"reporting this tree at all, so nothing here can be concluded", changes)
	}

	if len(nested) == 0 {
		t.Fatalf("no path under .claude/worktrees/ is in the difference: %v\n"+
			"This test PINS A DEFECT and passes while the defect is present. Nothing arriving "+
			"means either the defect is FIXED — delete this test and un-skip T021_02 — or the "+
			"arrangement stopped reproducing it, in which case T021_02 is vacuous and must be "+
			"re-established before it is trusted.", changes)
	}

	// The measured shape, recorded so the next reader does not have to re-derive
	// it: ONE directory path, trailing slash, because git stops at the nested
	// .git rather than descending.
	t.Logf("the parent's difference includes the sub-agent's checkout as %d path(s): %v", len(nested), nested)
	for _, p := range nested {
		if !strings.HasSuffix(p, "/") {
			t.Errorf("expected the nested checkout to appear as a directory path with a trailing "+
				"slash, got %q — git's behaviour here has changed and the note in this file's "+
				"header is now wrong", p)
		}
	}
}

// T021_02: THE INVARIANT — a cycle in the root's tree reports what the root's
// session changed, and a nested sub-agent worktree is not part of that.
//
// The task's stated outcome, asserted against the difference itself. It FAILS
// today; T021_01 is the standing proof that the failure is the engine's rather
// than the arrangement's.
//
// The fix is NOT ignoring `.claude/` wholesale — the guardrail declarations live
// there and are exactly the content the engine must watch change. It is
// excluding a nested WORKING TREE: a directory git itself reports as a separate
// checkout is not this tree's content, whoever put it there. Deriving it from
// `git worktree list` keeps the rule about what the thing IS rather than about
// the path one harness happens to use, which matters because sloprail is
// harness-agnostic and `.claude/worktrees/` is Claude Code's layout, not a
// contract.
func TestT021_02_ANestedWorktreeIsNotTheParentsChangedContent(t *testing.T) {
	t.Skip("PINS A KNOWN DEFECT (task worktree-noise-in-parent-diff, not started): the nested " +
		"checkout at .claude/worktrees/agent-<id> is reported by `git ls-files --others` and " +
		"reaches gitrepo.Changed as the parent's own untracked content. T021_01 reproduces it. " +
		"Un-skip this and DELETE T021_01 when nested worktrees are excluded from the difference.")

	dir, baseline := repoWithNestedWorktree(t)

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
// This is the end-to-end half, and what it records is that the noise does NOT
// currently reach a rule: filemod's lookAt stats `.claude/worktrees/agent-<id>/`,
// finds a directory, and reports ErrPathIsNotAFile instead of emitting an event.
// So the difference is polluted (T021_01) while the file rules are, today,
// shielded from it by a check that exists for an unrelated reason.
//
// Worth pinning as its own fact rather than left implicit, because it is load
// bearing in both directions: it is why this directory's e2e cannot assert
// leakage, and it is exactly what would change if a future kind were bound to
// something other than a regular file — at which point the pollution T021_01
// measures would arrive at a rule.
//
// The root's own file is asserted first so the silence about the worktree is
// read against a ledger that has just been shown to register something.
func TestT021_03_TheNoiseDoesNotCurrentlyReachAFileRule(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Guardrail(proj, "watcher", bindPostFileEvents, map[string]string{"record.sh": recordScript})
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "the project before the session")

	// The mock does not APPLY a sub-agent's tool calls, so this script's Write
	// creates nothing. What matters is the dispatch itself, which binds a real
	// `git worktree add` inside the parent's tree.
	subScript := filepath.Join(t.TempDir(), "sub.sh")
	if err := Turns("delegated done", Write("s1", "child.md", "written in the child tree\n")).
		Script(subScript); err != nil {
		t.Fatalf("write sub-agent scenario: %v", err)
	}

	e.InstallClaudeShim(proj)
	e.Run(proj, "s-021-03", "delegate into a worktree", Turns("done",
		Write("w1", "root-own.md", "the root's own work\n"),
		Dispatch("d1", "do the delegated thing", subScript, "worktree"),
	))

	// The premise: a worktree really was bound inside the parent's tree, and git
	// reports it as untracked content of the parent. Without both, the silence
	// below is about a cycle that had no noise to be shielded from.
	entries, err := os.ReadDir(filepath.Join(proj, ".claude", "worktrees"))
	if err != nil || len(entries) == 0 {
		t.Fatalf("no worktree was bound under .claude/worktrees (%v) — isolation=%q did not "+
			"create a nested checkout, so this proves nothing", err, "worktree")
	}
	if others := git(t, proj, "ls-files", "-o", "--exclude-standard", "--full-name"); !strings.Contains(others, ".claude/worktrees/") {
		t.Fatalf("git does not report the nested checkout as the parent's untracked content:\n%s\n"+
			"the pollution this test is about is not present, so its conclusion would be vacuous", others)
	}

	got := observedFiles(t, e.Ledger(proj, "watcher", "seen"))

	// The control: the root's own work reached the rule.
	if !sawPath(got, "root-own.md") {
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
