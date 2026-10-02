package e2e

import (
	"path/filepath"
	"strings"
	"testing"
)

const libsRule = "match: \"lib/**\"\nchecks:\n  - script: ./check.sh\n"

// T003_66: a rule belongs to the repository that declares it. A commit in another repository
// (reached with `git -C`, no `cd`) is judged by THAT repository's rules and by the session's
// enabled plugins, never by the root's: the root's `docs/**` rule does not follow the agent
// into a repository that has no such rule, and the other repository's `lib/**` rule fires
// there although the root declares nothing about lib. A folder that is not a repository at
// all has no commits, so nothing in it is judged and nothing in the engine breaks on it.
func TestT003_66_AnotherRepositoryIsJudgedByItsOwnRulesNeverTheRoots(t *testing.T) {
	e, proj, rootLed := project(t, docsRule)
	other := e.Project()
	e.GitInit(other)
	e.WriteFile(other, "docs/seed.md", "seed\n")
	e.WriteFile(other, "lib/seed.md", "seed\n")
	e.CommitAll(other, "the other project")
	otherLed := filepath.Join(t.TempDir(), "other-ledger.jsonl")
	e.FileGuard(other, "libs", libsRule, map[string]string{"check.sh": recorder(otherLed)})
	e.CommitAll(other, "the other repository's own rule")
	plain := t.TempDir() // not a repository
	const sess = "s-003-66"

	commitIn := func(rel, content, msg string) string {
		return "mkdir -p " + other + "/" + filepath.Dir(rel) + " && printf '%s' '" + content + "' > " + other + "/" + rel +
			" && git -C " + other + " add -A && git -C " + other + " commit -q -m '" + msg + "'"
	}

	// Violates the ROOT's rule only; the other repository has no rule about docs.
	e.Run(proj, sess, "docs elsewhere", Turns("done",
		Bash("b1", commitIn("docs/x.md", "FORBIDDEN words", "docs in the other repository")),
		Bash("b2", "mkdir -p "+plain+"/docs && printf '%s' 'FORBIDDEN words' > "+plain+"/docs/y.md"),
	))
	if got := stopRefusals(e, proj, sess); got != "" {
		t.Fatalf("the root's docs rule followed the agent into a repository that has no such rule, or a plain folder was judged:\n%s", got)
	}
	for _, run := range ledger(t, rootLed) {
		if strings.Contains(strings.Join(paths(run.Files), " "), "x.md") {
			t.Fatalf("the root's rule was handed a file of the other repository: %v", paths(run.Files))
		}
	}
	blocks := stopBlocks(e, proj, sess)

	// Violates the OTHER repository's own rule.
	e.Run(proj, sess, "lib elsewhere", Turns("done",
		Bash("b3", commitIn("lib/z.md", "FORBIDDEN words", "lib in the other repository")),
	))
	got := newBlocks(e, proj, sess, blocks)
	// (A run from another repository stores no refusal, so the Stop says "not judged yet" for its own rule.)
	if !strings.Contains(got, `file-guard "libs"`) || !strings.Contains(got, other) || !strings.Contains(got, "lib/z.md") {
		t.Fatalf("the other repository's own rule did not refuse its violation, naming the folder:\n%s", got)
	}
	blocks = stopBlocks(e, proj, sess)

	e.Run(proj, sess, "fix it", Turns("fixed",
		Bash("b4", commitIn("lib/z.md", "clean words", "fix lib")),
	))
	if n := stopBlocks(e, proj, sess); n != blocks {
		t.Fatalf("the fixed repository was still refused:\n%s", newBlocks(e, proj, sess, blocks))
	}
}
