package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/internal/sessionstate"
	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// Each folder of a session owes commits under ITS OWN rules (adapted from 003_08, 003_40, 003_61b
// on main before the cut: only the folder/rules parts; which commits a file-guard judges is
// `sr-checks run`'s and the tracked ranges', not these tests').

const docsGuard = "match: \"docs/**\"\nchecks:\n  - script: ./check.sh\n"

var passing = map[string]string{"check.sh": "#!/bin/sh\ncat >/dev/null\nexit 0\n"}

// declareNothing leaves proj with no rule in force at all: every rule the sloprail plugin
// ships is disabled in its config, and it declares none of its own.
func declareNothing(t *testing.T, e *Env, proj string) {
	t.Helper()
	root := filepath.Join("..", "..", "..", "..", "..", "marketplace", "plugins", "sloprail", ".sloprail")
	var names []string
	for _, nature := range []string{"gate", "file-guard"} {
		entries, err := os.ReadDir(filepath.Join(root, nature))
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range entries {
			if d.IsDir() {
				names = append(names, "sloprail/"+nature+"/"+d.Name())
			}
		}
	}
	e.DisablePluginGuardrail(proj, names...)
	e.CommitAll(proj, "no rules here")
}

// T055_05: commit-required covers a registered folder's tree even when the session's own
// project declares no rule at all (the folder's own file-guard owes a commit there).
func TestT055_05_AFoldersCommitsAreOwedWhenTheOwnProjectHasNoRules(t *testing.T) {
	e, proj, other := two(t)
	declareNothing(t, e, proj)
	const sess = "s-055-05"
	e.FileGuard(other, "docs", docsGuard, passing)
	e.CommitAll(other, "the rules")

	e.Run(proj, sess, "work elsewhere", Turns("done",
		Bash("b0", "git -C "+other+" commit -q --allow-empty -m 'register this folder'"),
		Bash("b1", "mkdir -p "+other+"/docs && echo hi > "+other+"/docs/new.md"),
	))
	got := strings.Join(e.BlockingErrorsFrom(proj, sess, "Stop"), "\n")
	if !strings.Contains(got, "Commit your work") || !strings.Contains(got, "docs/new.md") {
		t.Fatalf("commit-required did not cover the other folder when the project declares nothing:\n%s", got)
	}
}

// T055_06 (003_40): a worktree the agent adds with `git worktree add` is a folder of the
// session; an uncommitted guarded change in it owes a commit at Stop, named by its path, and
// committing it there releases the Stop.
func TestT055_06_AWorktreeTheAgentAddedOwesItsCommits(t *testing.T) {
	e, proj, _ := two(t)
	e.FileGuard(proj, "docs", docsGuard, passing)
	e.CommitAll(proj, "the rule")
	wt := filepath.Join(t.TempDir(), "side-tree")
	const sess = "s-055-06"

	e.Run(proj, sess, "side worktree", Turns("done",
		Bash("b1", "git worktree add -q -b side "+wt),
		Bash("b2", "mkdir -p "+wt+"/docs && echo hi > "+wt+"/docs/w.md"),
	))
	if f, ok := folderAt(e.SessionFolders(proj, sess), real(t, wt)); !ok || f.Role != sessionstate.FolderAdHoc {
		t.Fatalf("a worktree the agent added is not a folder of the session: %+v", e.SessionFolders(proj, sess))
	}
	got := strings.Join(e.BlockingErrorsFrom(proj, sess, "Stop"), "\n")
	if !strings.Contains(got, "Commit your work") || !strings.Contains(got, "side-tree") || !strings.Contains(got, "docs/w.md") {
		t.Fatalf("an uncommitted guarded change in an added worktree did not owe a commit:\n%s", got)
	}

	before := len(e.AllBlockingErrorsFrom(proj, sess, "Stop"))
	e.Run(proj, sess, "commit it", Turns("done", Bash("b3", "cd "+wt+" && git add -A && git commit -q -m 'docs'")))
	if after := len(e.AllBlockingErrorsFrom(proj, sess, "Stop")); after != before {
		t.Fatalf("the committed worktree still owed a commit (%d refusals, had %d)", after, before)
	}
}

// T055_07 (003_08): a sub-agent in a worktree of its own is a folder of the session, and its
// uncommitted guarded work is owed at ITS Stop; a sub-agent in the root's tree is not refused
// for it — the root, which owns that tree, is.
func TestT055_07_ASubagentOwesCommitsInItsOwnWorktreeOnly(t *testing.T) {
	e, proj, _ := two(t)
	e.FileGuard(proj, "docs", docsGuard, passing)
	e.CommitAll(proj, "the rule")

	write := func(name string) string {
		return harness.SubagentScript(t, Turns("sub done", Bash("sb1", "mkdir -p docs && echo hi > docs/"+name)))
	}

	const own = "s-055-07a"
	res := e.Run(proj, own, "delegate into isolation", Turns("root done",
		harness.Dispatch("d1", "write the doc", write("isolated.md"), "worktree"),
	))
	var wt bool
	for _, f := range e.SessionFolders(proj, own) {
		wt = wt || f.Role == sessionstate.FolderSubagentWorktree
	}
	if !wt {
		t.Fatalf("a sub-agent's own worktree is not a folder of the session: %+v", e.SessionFolders(proj, own))
	}
	if !e.SubagentStopBlocked(proj, own, "docs/isolated.md") {
		t.Fatalf("a sub-agent in its own worktree was not owed a commit for its uncommitted work:\n%s", res.Output)
	}

	const shared = "s-055-07b"
	res = e.Run(proj, shared, "delegate here", Turns("root done",
		harness.Dispatch("d1", "write the doc", write("shared.md"), ""),
	))
	if !e.NoSubagentStopBlock(proj, shared) {
		t.Fatalf("a sub-agent in the root's tree was refused for the root's tree:\n%s", res.Output)
	}
	if got := strings.Join(e.BlockingErrorsFrom(proj, shared, "Stop"), "\n"); !strings.Contains(got, "docs/shared.md") {
		t.Fatalf("the root, which owns the tree, was not owed the commit:\n%s", got)
	}
}

// T055_08 (003_61b): a SubagentStop whose identity cannot be resolved still runs its folder's
// rules: uncommitted guarded work there is refused, not passed silently.
// sr:proves subagents/unidentifiable-subagent-still-judged
func TestT055_08_ASubagentWithoutAnIdentityStillOwesItsCommits(t *testing.T) {
	e, proj, _ := two(t)
	e.FileGuard(proj, "docs", docsGuard, passing)
	e.CommitAll(proj, "the rule")
	e.WriteFile(proj, "docs/b.md", "uncommitted")

	payload, _ := json.Marshal(map[string]any{
		"cwd": proj, "agent_id": "a-unresolvable", "agent_transcript_path": filepath.Join(t.TempDir(), "gone.jsonl"),
		"stop_hook_active": false, "hook_event_name": "SubagentStop",
	})
	res := e.CLIDirectStdinEnv(proj, string(payload), e.SessionEnv(""), "sr-session", "subagent-stop")
	if !harness.Blocked(res) || !strings.Contains(res.Output, "docs/b.md") {
		t.Fatalf("a sub-agent that could not be identified went unjudged:\n%s", res.Output)
	}
}

// T055_09: the root's Stop verifies the range a sub-agent tracked in ANOTHER repository even
// when the root's tree declares no rule at all: the root must not end its Stop on "no rules
// here". The other repository's rule refuses; the sub-agent judged its range (`sr-checks run`
// at its own turn end), and the root's Stop reports that stored verdict.
func TestT055_09_ARootWithoutRulesStillVerifiesASubagentsRange(t *testing.T) {
	harness.RequireCap(t, harness.CapSubagentParentLink)
	e, proj, other := two(t)
	declareNothing(t, e, proj)
	const sess = "s-055-09"
	e.FileGuard(other, "docs", "match: \"docs/**\"\nchecks:\n  - script: ./refuse.sh\n",
		map[string]string{"refuse.sh": "#!/bin/sh\ncat >/dev/null\necho '{\"reason\":\"SUBAGENT-RANGE-VERDICT\"}'\nexit 1\n"})
	e.CommitAll(other, "the rules")
	script := harness.SubagentScript(t, Turns("sub done",
		Bash("sb0", "git -C "+other+" commit -q --allow-empty -m 'register this folder'"),
		Bash("sb1", "mkdir -p "+other+"/docs && echo hi > "+other+"/docs/a.md && git -C "+other+" add -A && git -C "+other+" commit -q -m 'the sub-agent work'"),
	))

	e.Run(proj, sess, "delegate", Turns("root done",
		harness.Dispatch("d1", "write the doc elsewhere", script, ""),
	))
	got := strings.Join(e.BlockingErrorsFrom(proj, sess, "Stop"), "\n")
	if !strings.Contains(got, "SUBAGENT-RANGE-VERDICT") {
		t.Fatalf("the root's Stop did not verify the sub-agent's tracked range when the root declares no rule:\n%s", got)
	}
}
