package e2e

import (
	"reflect"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// A rule's life inside one session: it can be taken away, switched off, or
// arrive late, and the evaluation follows the tree as it is at each Stop.

// strictRule is a second file-guard that never changes during these tests: it
// refuses any `strict/**` file containing NOPE.
const strictRule = "match: \"strict/**\"\nchecks:\n  - script: ./check.sh\n"

const strictCheck = `#!/bin/sh
payload="$(cat)"
if printf '%s' "$payload" | jq -e 'any(.changeset.files[]; .newContent | contains("NOPE"))' >/dev/null; then
  echo '{"reason":"STRICT-SAYS-NO: NOPE in the changeset"}'
  exit 1
fi
exit 0
`

// twoRuleProject is the docs rule (recording to a ledger) plus the strict rule,
// both committed.
func twoRuleProject(t *testing.T) (*Env, string, string) {
	t.Helper()
	e, proj, led := project(t, docsRule)
	e.FileGuard(proj, "strict", strictRule, map[string]string{"check.sh": strictCheck})
	e.CommitAll(proj, "the second rule")
	return e, proj, led
}

// T003_18: a rule taken away, or switched off in the project's config, mid-session
// stops firing — and the rule beside it keeps refusing. The docs rule refuses a
// forbidden file first (the control: it fires); the agent then retires it and
// commits forbidden content under both rules; only the strict rule speaks.
func TestT003_18_ARuleRemovedOrDisabledStopsFiring(t *testing.T) {
	retire := map[string]string{
		"removed":  "git rm -rq .sloprail/file-guard/docs && git commit -qm 'remove the docs rule'",
		"disabled": "printf '  - file-guard/docs\\n' >> .sloprail/config.yaml && git add -A && git commit -qm 'disable the docs rule'",
	}
	for name, cmd := range retire {
		t.Run(name, func(t *testing.T) {
			e, proj, led := twoRuleProject(t)
			sess := "s-003-18-" + name

			e.Run(proj, sess, "write a doc", Turns("done",
				harness.CommitFile("c1", "docs/a.md", "FORBIDDEN words", "add a"),
			))
			if !strings.Contains(strings.Join(e.BlockingErrorsFrom(proj, sess, "Stop"), "\n"), "FORBIDDEN text in the changeset") {
				t.Fatalf("premise: the docs rule did not fire before it was retired: %q", e.BlockingErrors(proj, sess))
			}
			seen, ran := stopBlocks(e, proj, sess), len(ledger(t, led))

			e.Run(proj, sess, "retire the rule and go on", Turns("done",
				Bash("r1", cmd),
				harness.CommitFile("c2", "docs/b.md", "FORBIDDEN words again", "add b"),
				harness.CommitFile("c3", "strict/x.md", "NOPE", "add x"),
			))
			blocks := newBlocks(e, proj, sess, seen)
			if !strings.Contains(blocks, "STRICT-SAYS-NO") {
				t.Fatalf("the rule beside the retired one stopped refusing: %q", blocks)
			}
			if strings.Contains(blocks, "FORBIDDEN text in the changeset") {
				t.Fatalf("the %s docs rule still refused:\n%s", name, blocks)
			}
			if n := len(ledger(t, led)); n != ran {
				t.Fatalf("the %s docs rule's check ran %d more time(s)", name, n-ran)
			}

			// Fixed, the session passes, though the docs are still "forbidden".
			seen = stopBlocks(e, proj, sess)
			e.Run(proj, sess, "fix it", Turns("done",
				harness.CommitFile("c4", "strict/x.md", "fine", "fix x"),
			))
			if n := stopBlocks(e, proj, sess); n != seen {
				t.Fatalf("the fixed session was still refused:\n%s", newBlocks(e, proj, sess, seen))
			}
		})
	}
}

// T003_19: a rule added mid-session judges from the EARLIER of its own commit's parent
// and the session's start, so the session's work committed before the rule existed (a
// forbidden doc) is judged too, and history from before the session is not.
func TestT003_19_ARuleAddedMidSessionJudgesFromItsOwnCommit(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "docs/seed.md", "seed\n")
	e.DisableShippedFileGuards(proj)
	sessionStart := e.CommitAll(proj, "the project")

	// The rule is prepared outside the tree and arrives in a commit of its own.
	led := t.TempDir() + "/ledger.jsonl"
	stage := e.Project()
	e.FileGuard(stage, "docs", docsRule, map[string]string{"check.sh": recorder(led)})

	const sess = "s-003-19"
	e.Run(proj, sess, "write a doc", Turns("done",
		harness.CommitFile("c1", "docs/old.md", "FORBIDDEN words, from before the rule", "add old"),
	))
	if n := stopBlocks(e, proj, sess); n != 0 {
		t.Fatalf("premise: a session with no rule was refused (%d blocks)", n)
	}

	e.Run(proj, sess, "add the rule, then a doc", Turns("done",
		Bash("r1", "mkdir -p .sloprail/file-guard && cp -R '"+stage+"/.sloprail/file-guard/docs' .sloprail/file-guard/"),
		harness.Commit("r2", "add the docs rule"),
		harness.CommitFile("c2", "docs/new.md", "FORBIDDEN words, after the rule", "add new"),
	))
	if !strings.Contains(newBlocks(e, proj, sess, 0), "FORBIDDEN text in the changeset") {
		t.Fatalf("the rule did not judge the work committed after it arrived: %q", e.BlockingErrors(proj, sess))
	}
	for _, r := range ledger(t, led) {
		if r.Base != sessionStart {
			t.Fatalf("the rule was judged from %s, want the session's start %s (earlier than its own commit's parent)", r.Base, sessionStart)
		}
		if !reflect.DeepEqual(paths(r.Files), []string{"docs/new.md", "docs/old.md"}) && !reflect.DeepEqual(paths(r.Files), []string{"docs/old.md", "docs/new.md"}) {
			t.Fatalf("the rule judged %v; the session's work before the rule and after it are both its business, and the seed is neither", paths(r.Files))
		}
	}
	if len(ledger(t, led)) == 0 {
		t.Fatal("the check never ran")
	}

	// Both docs are the rule's business now; fixed, the range passes.
	seen := stopBlocks(e, proj, sess)
	e.Run(proj, sess, "fix it", Turns("done",
		Bash("f1", "printf 'clean words' > docs/old.md && printf 'clean words' > docs/new.md && git add -A && git commit -q -m 'fix both'"),
	))
	if n := stopBlocks(e, proj, sess); n != seen {
		t.Fatalf("the fixed work was still refused:\n%s", newBlocks(e, proj, sess, seen))
	}
}
