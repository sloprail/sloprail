package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// An owed tip (a recorded line of history no rule has passed) is judged on what its rule
// could judge and on what still stands. Two principles, applied to every tip alike:
//
//   - RULE AGE: a rule judges only commits made after it came into force for the session.
//   - WHAT STILL STANDS: a tip already landed upstream is judged only on the paths whose
//     content upstream still holds as the tip left it.

// rewriteUpstream is a later commit on the upstream default branch that rewrites one
// file: a fix made, and merged, by someone (or another session) after the tip landed.
func rewriteUpstream(id, path, content string) harness.Turn {
	return Bash(id, "idx=$(mktemp -u) && GIT_INDEX_FILE=$idx git read-tree refs/remotes/origin/main"+
		" && GIT_INDEX_FILE=$idx git update-index --add --cacheinfo 100644,$(printf '%s\\n' '"+content+"' | git hash-object -w --stdin),"+path+
		" && git update-ref refs/remotes/origin/main $(git commit-tree $(GIT_INDEX_FILE=$idx git write-tree) -p refs/remotes/origin/main -m 'later fix of "+path+"')"+
		" && rm -f $idx")
}

const libsRule = "match: \"lib/**\"\nchecks:\n  - script: ./check.sh\n"

// T003_55: a rule that arrives mid-session (a plugin's, in no commit's history) judges only
// commits made after the session first loaded it. An older owed branch with a violation
// that predates it is not refused; a violation committed after it is, and fixing passes.
func TestT003_55_APluginRuleEnabledMidSessionDoesNotJudgeOlderOwedBranches(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	main := e.Git(proj, "branch", "--show-current")
	e.Git(proj, "update-ref", "refs/remotes/origin/main", "HEAD")
	const sess = "s-003-55"
	// The plugin is installed from the start, shipping one unrelated rule; the rule under
	// test arrives in a plugin update mid-session.
	root := e.EnablePluginShippingFileGuard(proj, "acme", "other", "match: \"nothing/**\"\nchecks:\n  - script: ./check.sh\n",
		map[string]string{"check.sh": "#!/bin/sh\nexit 0\n"})
	e.CommitAll(proj, "enable the plugin") // the settings are the project's: not swept into a branch the agent cuts

	e.Run(proj, sess, "an old branch", Turns("done",
		Bash("b1", "git switch -q -c stale"),
		harness.CommitFile("c1", "lib/old.md", "FORBIDDEN words", "add old"),
		Bash("b2", "git switch -q "+main),
	))
	if got := stopRefusals(e, proj, sess); got != "" {
		t.Fatalf("no rule covers lib/ yet, but Stop refused:\n%s", got)
	}
	blocks := stopBlocks(e, proj, sess)

	time.Sleep(1200 * time.Millisecond) // commit dates have a second's resolution
	led := t.TempDir() + "/plugin-ledger.jsonl"
	libs := filepath.Join(root, ".sloprail", "file-guard", "libs")
	if err := os.MkdirAll(libs, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"file-guard.yaml": libsRule, "check.sh": recorder(led)} {
		if err := os.WriteFile(filepath.Join(libs, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	e.Run(proj, sess, "a new branch", Turns("done",
		Bash("b3", "git switch -q -c fresh"),
		harness.CommitFile("c2", "lib/new.md", "FORBIDDEN words", "add new"),
		Bash("b4", "git switch -q "+main),
	))
	got := newBlocks(e, proj, sess, blocks)
	if !strings.Contains(got, refusalText) || !strings.Contains(got, "fresh") {
		t.Fatalf("a violation committed after the rule arrived was not refused; refusals:\n%s", got)
	}
	if strings.Contains(got, "stale") || strings.Contains(got, "lib/old.md") {
		t.Fatalf("a commit made before the rule arrived was judged by it:\n%s", got)
	}
	for _, run := range ledger(t, led) {
		if joined := strings.Join(paths(run.Files), " "); strings.Contains(joined, "old.md") {
			t.Fatalf("a file committed before the rule arrived was handed to it: %v", paths(run.Files))
		}
	}
	blocks = stopBlocks(e, proj, sess)

	e.Run(proj, sess, "fix it", Turns("fixed",
		Bash("b5", "git switch -q fresh"),
		harness.CommitFile("c3", "lib/new.md", "clean words", "fix new"),
		Bash("b6", "git switch -q "+main),
	))
	if n := stopBlocks(e, proj, sess); n != blocks {
		t.Fatalf("the fixed branch was still refused:\n%s", newBlocks(e, proj, sess, blocks))
	}
}

// T003_56: a landed owed tip is judged only on the files upstream still holds as the tip
// left them. One rewritten upstream since is not its debt; one upstream still has as-is is,
// and fixing it passes.
func TestT003_56_ALandedOwedTipIsJudgedOnlyOnWhatStillStands(t *testing.T) {
	e, proj, led := project(t, docsRule)
	main := e.Git(proj, "branch", "--show-current")
	e.Git(proj, "update-ref", "refs/remotes/origin/main", "HEAD")
	const sess = "s-003-56"

	e.Run(proj, sess, "merge it before any Stop", Turns("done",
		Bash("b1", "git switch -q -c landed"),
		harness.CommitFile("c1", "docs/rewritten.md", "FORBIDDEN words", "add rewritten"),
		harness.CommitFile("c2", "docs/standing.md", "FORBIDDEN words", "add standing"),
		Bash("b2", "git switch -q "+main),
		landUpstream("s1", "landed"),
		rewriteUpstream("s2", "docs/rewritten.md", "clean words"),
	))
	got := stopRefusals(e, proj, sess)
	if !strings.Contains(got, refusalText) || !strings.Contains(got, "docs/standing.md") {
		t.Fatalf("a file upstream still holds as-is was not refused; refusals:\n%s", got)
	}
	if strings.Contains(got, "docs/rewritten.md") {
		t.Fatalf("a file rewritten upstream since was judged as this tip's debt:\n%s", got)
	}
	for _, run := range ledger(t, led) {
		if joined := strings.Join(paths(run.Files), " "); strings.Contains(joined, "rewritten.md") {
			t.Fatalf("a superseded file was handed to the rule: %v", paths(run.Files))
		}
	}
	blocks := stopBlocks(e, proj, sess)

	// Fixed on the branch: no longer landed as it was, so judged in full, and clean.
	e.Run(proj, sess, "fix it", Turns("fixed",
		Bash("b3", "git switch -q landed"),
		harness.CommitFile("c3", "docs/standing.md", "clean words", "fix standing"),
		harness.CommitFile("c4", "docs/rewritten.md", "clean words", "fix rewritten"),
		Bash("b4", "git switch -q "+main),
	))
	if n := stopBlocks(e, proj, sess); n != blocks {
		t.Fatalf("the fixed branch was still refused:\n%s", newBlocks(e, proj, sess, blocks))
	}
}

// T003_57: a landed owed tip every file of which was rewritten upstream has nothing that
// stands: it is settled by a passing run that says "superseded", not silently dropped.
func TestT003_57_AFullySupersededLandedTipIsSettledAndSaysSo(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	main := e.Git(proj, "branch", "--show-current")
	e.Git(proj, "update-ref", "refs/remotes/origin/main", "HEAD")
	const sess = "s-003-57"

	e.Run(proj, sess, "merge it and have it rewritten", Turns("done",
		Bash("b1", "git switch -q -c landed"),
		harness.CommitFile("c1", "docs/one.md", "FORBIDDEN words", "add one"),
		harness.CommitFile("c2", "docs/two.md", "FORBIDDEN words", "add two"),
		Bash("b2", "git switch -q "+main),
		landUpstream("s1", "landed"),
		rewriteUpstream("s2", "docs/one.md", "clean words"),
		rewriteUpstream("s3", "docs/two.md", "clean words"),
	))
	if got := stopRefusals(e, proj, sess); got != "" {
		t.Fatalf("a tip whose every file was superseded upstream was refused:\n%s", got)
	}
	res := e.ChecksSQL(proj, sess, "select json_extract(metadata, '$.reason') as reason, exit_code from check_runs where check_id = 'file-guard/docs' and json_extract(metadata, '$.reason') = 'superseded'")
	if !strings.Contains(res.Output, "superseded") {
		t.Fatalf("the settled tip left no visible \"superseded\" run:\n%s", res.Output)
	}
}

// T003_58: guard. An UNLANDED owed tip is judged in full, whatever upstream did meanwhile.
func TestT003_58_AnUnlandedOwedTipWithAnOldViolationIsStillRefused(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	main := e.Git(proj, "branch", "--show-current")
	e.Git(proj, "update-ref", "refs/remotes/origin/main", "HEAD")
	const sess = "s-003-58"

	e.Run(proj, sess, "a branch nobody merged", Turns("done",
		Bash("b1", "git switch -q -c unmerged"),
		harness.CommitFile("c1", "docs/old.md", "FORBIDDEN words", "add old"),
		Bash("b2", "git switch -q "+main),
		rewriteUpstream("s1", "docs/elsewhere.md", "unrelated words"),
	))
	got := stopRefusals(e, proj, sess)
	if !strings.Contains(got, refusalText) || !strings.Contains(got, "unmerged") {
		t.Fatalf("an unlanded owed branch with a violation was not refused; refusals:\n%s", got)
	}
}
