package e2e

import (
	"encoding/json"
	"testing"
)

// refusal_outlives_baseline: a file whose most recent check has passed=false is
// reported again on every cycle until a hook passes it, whatever the point the
// difference is measured from.
//
// The spec's reasoning: "The measuring point moves when history moves, and a
// refusal must not move with it. Were the two tied together, switching branches
// would drop an unfixed violation out of view and the file would be left broken
// with nothing left to say so."
//
// The shape of the test is therefore: get a file refused, then move the
// measuring point out from under it, then show the file still arrives. The move
// has to be one that would genuinely remove the file from a
// baseline-derived difference — otherwise the file arrives because it is still
// in the diff, and the refusal's own contribution is unproven.

// refuseNamed refuses any file whose path contains "bad", and records every
// file it was handed.
//
// It records BEFORE deciding, so the ledger shows arrival independently of the
// verdict — which is the whole observation this directory needs. A guardrail
// that only refused would leave "was this file put in front of me again?"
// answerable solely by the refusal travelling back, and a refusal at an
// after-the-fact point does not stop anything, so it is a weaker signal.
const refuseNamed = `---
hooks:
  PostFileCreate:
    - hooks:
        - type: command
          command: ./judge.sh
  PostFileUpdate:
    - hooks:
        - type: command
          command: ./judge.sh
  PostFileDelete:
    - hooks:
        - type: command
          command: ./judge.sh
---

# Refuses any file whose path contains "bad"
`

// judgeScript records the payload, then refuses when the path contains "bad".
//
// Exit 2 is the refusal channel. The path is read out of the payload with a
// grep rather than a JSON parser because a hook is an ordinary shell script and
// this keeps the fixture free of dependencies.
const judgeScript = `#!/bin/sh
payload="$(cat)"
printf '%s\n' "$payload" >> "$PWD/seen"
case "$payload" in
  *bad*) echo "this file is not acceptable" >&2; exit 2 ;;
esac
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

func countPath(got []observed, path string) int {
	n := 0
	for _, o := range got {
		if o.Path == path {
			n++
		}
	}
	return n
}

// T015_01: a refused file is put in front of the rule again on the next cycle.
//
// The base case, before any baseline movement is involved. The file is refused
// in the first cycle and not fixed; the second cycle must show it again. An
// engine that reported only what changed SINCE THE LAST CYCLE — rather than what
// is unfixed — shows it once and never again, and the violation is lost while
// the file is still broken.
//
// Two cycles in one session are driven as two Run calls with the same session
// id, which is what makes the second one a later cycle of the same session
// rather than a fresh one with a fresh baseline.
func TestT015_01_ARefusedFileIsReportedAgainOnTheNextCycle(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Guardrail(proj, "watcher", refuseNamed, map[string]string{"judge.sh": judgeScript})

	const sess = "s-015-01"
	e.Run(proj, sess, "write a bad file", Turns("done",
		Write("w1", "bad-file.md", "violates\n"),
	))

	first := observedFiles(t, e.Ledger(proj, "watcher", "seen"))
	if countPath(first, "bad-file.md") == 0 {
		t.Fatalf("the offending file never reached the rule in the first cycle: %v — "+
			"nothing was refused, so there is no surviving refusal to test", first)
	}

	// A second cycle in the same session that does NOT touch the offending
	// file. Its only change is elsewhere, so a difference-only engine has
	// nothing to say about bad-file.md.
	e.Run(proj, sess, "do something else", Turns("done",
		Write("w2", "unrelated.md", "fine\n"),
	))

	after := observedFiles(t, e.Ledger(proj, "watcher", "seen"))
	if countPath(after, "bad-file.md") <= countPath(first, "bad-file.md") {
		t.Fatalf("an unfixed refusal was not re-reported on the next cycle: saw it %d times "+
			"after the first cycle and %d times after the second (%v) — the file is still broken "+
			"and nothing is left to say so", countPath(first, "bad-file.md"), countPath(after, "bad-file.md"), after)
	}
}

// T015_02: the refusal survives the measuring point moving.
//
// The invariant proper. The offending file is refused while the session is on
// main; then the agent switches to a branch cut from the root, which re-takes
// the measuring point onto a line of history where that file's content is not a
// difference at all. A refusal tied to the baseline disappears at exactly this
// moment — which is the failure the spec describes, and the file is left broken
// with nothing to report it.
//
// The offending file is committed on main before the switch and re-created after
// it, so it is present in the tree at the end of the second cycle without being
// part of the second cycle's diff against the re-taken point.
func TestT015_02_ARefusalSurvivesTheMeasuringPointMoving(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Guardrail(proj, "watcher", refuseNamed, map[string]string{"judge.sh": judgeScript})

	// The rule is committed first, so it exists on both lines of history.
	// Without this the checkout below deletes .sloprail/ along with everything
	// else the other branch does not hold, the project then loads no rules, and
	// the ledger stops growing for a reason that has nothing to do with
	// refusals surviving — which is exactly the false pass this test is about.
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "the guardrail, on every line of history")
	root := e.Git(proj, "rev-parse", "HEAD")

	// A branch that diverges from that point, prepared before the session so the
	// switch during it is a real change of history.
	e.Git(proj, "checkout", "-b", "feature", root)
	e.Git(proj, "commit", "--allow-empty", "-m", "on feature")
	e.Git(proj, "checkout", "main")
	e.Git(proj, "commit", "--allow-empty", "-m", "on main, after the split")

	if e.Git(proj, "cat-file", "-t", "feature:.sloprail/guardrails/watcher/GUARDRAIL.md") != "blob" {
		t.Fatalf("the guardrail is not present on the branch the agent switches to, so the " +
			"rule cannot fire there and a ledger that stops growing would prove nothing")
	}

	const sess = "s-015-02"
	e.Run(proj, sess, "write a bad file", Turns("done",
		Write("w1", "bad-file.md", "violates\n"),
		// Committed, so the file is part of history rather than outstanding
		// work — the switch below then carries it, and it is not in the second
		// cycle's diff.
		//
		// This path only, not `git add -A`. The harness writes its own
		// .scenario.sh into the project, and committing that makes the later
		// `git checkout` abort with "your local changes would be overwritten"
		// when the next cycle rewrites it — the branch switch then never
		// happens and the test measures nothing.
		Bash("b1", "git add bad-file.md && git commit -m 'the bad file'"),
	))

	first := observedFiles(t, e.Ledger(proj, "watcher", "seen"))
	if countPath(first, "bad-file.md") == 0 {
		t.Fatalf("the offending file never reached the rule in the first cycle: %v — "+
			"nothing was refused, so there is no surviving refusal to test", first)
	}

	e.Run(proj, sess, "switch branches", Turns("done",
		Bash("b2", "git checkout feature"),
		// Re-created after the switch, which the doc comment above describes and
		// which the arrangement genuinely needs: `feature` was cut from the root,
		// so checking it out DELETES bad-file.md from the tree. Without writing
		// it back the file is not merely absent from the diff — it is absent from
		// the project, and "still broken with nothing to report it" would be a
		// claim about a file that no longer exists.
		Write("w2", "bad-file.md", "violates\n"),
		Write("w3", "unrelated.md", "fine\n"),
	))

	if got := e.Git(proj, "rev-parse", "--abbrev-ref", "HEAD"); got != "feature" {
		t.Fatalf("the agent did not actually switch branches (on %q), so the measuring point "+
			"never moved and this proves nothing", got)
	}

	// The rule must be asked about the offending file AGAIN, after the point it
	// was measured from has moved.
	//
	// Counted over the entries this cycle added rather than over the ledger as a
	// whole. A refusal blocks the turn and the agent is driven round again, so
	// one unfixed violation writes many lines in a single cycle — a comparison
	// of cumulative totals measures how many times the mock retried, not whether
	// the refusal outlived the branch switch.
	after := observedFiles(t, e.Ledger(proj, "watcher", "seen"))
	if len(after) <= len(first) {
		t.Fatalf("the second cycle observed nothing at all (%d entries, was %d), so there is "+
			"no evidence either way about the refusal surviving: %v", len(after), len(first), after)
	}
	if countPath(after[len(first):], "bad-file.md") == 0 {
		t.Fatalf("an unfixed refusal was dropped when the measuring point moved: the file was not "+
			"reported again after the branch switch (%v) — the refusal was tied to the baseline, "+
			"so switching branches left a broken file with nothing to report it", after[len(first):])
	}
}

// T015_03: a file that gets fixed stops being reported.
//
// The negative half, and the one that stops T015_01 and T015_02 from being
// satisfied by an engine that simply reports every file it has ever seen
// forever. "Until a hook passes it" is a real boundary: once the content passes,
// the file must fall out.
//
// The fix is a rename of the content, not of the path — the same path now holds
// content the rule accepts.
func TestT015_03_AFixedFileStopsBeingReported(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	// Refuses on CONTENT here, so the same path can be fixed in place. The
	// path-based fixture above cannot express a fix without a rename.
	const refuseContent = `---
hooks:
  PostFileCreate:
    - hooks:
        - type: command
          command: ./judge.sh
  PostFileUpdate:
    - hooks:
        - type: command
          command: ./judge.sh
---

# Refuses while the file holds the forbidden word
`
	// Reads the file off disk rather than the payload: the after-the-fact kinds
	// carry the path, and the content is already on disk by then.
	//
	// The project root is derived from guardrailDir, which is the one field in
	// the payload that names an absolute location. There is no `cwd` in a
	// guardrail hook payload (it carries `event` and `guardrailDir`, nothing
	// else) and the mock sets no CLAUDE_PROJECT_DIR — verified against this
	// worktree. A prefix that resolved to empty would make every grep miss, the
	// rule would refuse nothing, and this test would pass while testing
	// nothing. The refusal ledger checked below is what makes that failure
	// visible instead.
	const judgeContentScript = `#!/bin/sh
payload="$(cat)"
printf '%s\n' "$payload" >> "$PWD/seen"
path="$(printf '%s' "$payload" | sed -n 's/.*"path":"\([^"]*\)".*/\1/p')"
gdir="$(printf '%s' "$payload" | sed -n 's/.*"guardrailDir":"\([^"]*\)".*/\1/p')"
root="${gdir%/.sloprail/guardrails/*}"
if [ -n "$path" ] && [ -f "$root/$path" ] && grep -q FORBIDDEN "$root/$path"; then
  echo "$path" >> "$PWD/refused"
  echo "still contains the forbidden word" >&2; exit 2
fi
exit 0
`
	e.Guardrail(proj, "watcher", refuseContent, map[string]string{"judge.sh": judgeContentScript})

	const sess = "s-015-03"
	e.Run(proj, sess, "write then fix", Turns("done",
		Write("w1", "subject.md", "FORBIDDEN content\n"),
	))
	first := observedFiles(t, e.Ledger(proj, "watcher", "seen"))
	if countPath(first, "subject.md") == 0 {
		t.Fatalf("the file never reached the rule in the first cycle: %v", first)
	}
	// The rule must have actually REFUSED, not merely been handed the file.
	//
	// This fixture locates the file from the payload's own fields; if it
	// resolved neither the path nor the project root it would exit 0 on
	// everything, leaving a "fixed" file indistinguishable from one that was
	// never broken and the comparison below trivially satisfied.
	//
	// Read from the guardrail's own folder rather than from the run's output. A
	// refusal at an after-the-fact point does not travel back through a tool
	// result — there is no pending call to deny — so it does not appear in the
	// mock's stream at all, and asserting on the stream here would fail for
	// every build including a correct one.
	if len(e.Ledger(proj, "watcher", "refused")) == 0 {
		t.Fatalf("the rule never refused the offending file, so nothing here was ever unfixed " +
			"and the comparison below cannot fail")
	}

	// Fixed, then a further cycle that touches something else entirely.
	e.Run(proj, sess, "fix it", Turns("done",
		Write("w2", "subject.md", "acceptable content\n"),
	))
	fixed := observedFiles(t, e.Ledger(proj, "watcher", "seen"))

	e.Run(proj, sess, "unrelated work", Turns("done",
		Write("w3", "elsewhere.md", "fine\n"),
	))
	after := observedFiles(t, e.Ledger(proj, "watcher", "seen"))

	if countPath(after, "subject.md") != countPath(fixed, "subject.md") {
		t.Fatalf("a file that has been fixed and passed was reported again on a later cycle: "+
			"seen %d times when it passed, %d times after an unrelated cycle (%v) — "+
			"a passing verdict must end the re-reporting, or every file ever refused accumulates forever",
			countPath(fixed, "subject.md"), countPath(after, "subject.md"), after)
	}
}

// T015_04: a refused file that is NO LONGER A DIFFERENCE is still reported.
//
// The invariant's real claim, and the one T015_02 above cannot make. There the
// offending file is re-created after the switch, so it is outstanding work in
// the tree and arrives in the ordinary difference — the refusal contributes
// nothing to its arrival, and the test passes identically on an engine that
// discards refusals outright. Measured: dropping every failing verdict in
// revalidation.Record leaves T015_01, T015_02 and T015_03 all green.
//
// Here the branch the agent switches to ALREADY HOLDS the offending file,
// committed and identical. After the switch the file is on disk and broken,
// the measuring point has been re-taken onto that line, and the file is not a
// difference against it by any reading of the tree. Only the retained refusal
// still knows. So this fails on an engine that keeps refusals but never reads
// them, which is what the engine did until readdOutstanding existed.
func TestT015_04_ARefusedFileOutsideTheDifferenceIsStillReported(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Guardrail(proj, "watcher", refuseNamed, map[string]string{"judge.sh": judgeScript})
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "the guardrail, on every line of history")
	root := e.Git(proj, "rev-parse", "HEAD")

	// The branch already carries the offending file, so switching to it leaves
	// the file on disk WITHOUT putting it in the difference.
	e.Git(proj, "checkout", "-b", "feature", root)
	writeFile(t, proj, "bad-file.md", "violates\n")
	e.Git(proj, "add", "bad-file.md")
	e.Git(proj, "commit", "-m", "the bad file, already on this line")
	e.Git(proj, "checkout", "main")
	e.Git(proj, "commit", "--allow-empty", "-m", "on main, after the split")

	const sess = "s-015-04"
	e.Run(proj, sess, "write a bad file", Turns("done",
		Write("w1", "bad-file.md", "violates\n"),
		Bash("b1", "git add bad-file.md && git commit -m 'the bad file'"),
	))
	first := observedFiles(t, e.Ledger(proj, "watcher", "seen"))
	if countPath(first, "bad-file.md") == 0 {
		t.Fatalf("the offending file never reached the rule in the first cycle: %v — "+
			"nothing was refused, so there is no surviving refusal to test", first)
	}

	e.Run(proj, sess, "switch branches", Turns("done",
		Bash("b2", "git checkout feature"),
		Write("w3", "unrelated.md", "fine\n"),
	))

	if got := e.Git(proj, "rev-parse", "--abbrev-ref", "HEAD"); got != "feature" {
		t.Fatalf("the agent did not actually switch branches (on %q), so the measuring point "+
			"never moved and this proves nothing", got)
	}
	// The premise: the file is genuinely still broken on disk.
	if !e.Wrote(proj, "bad-file.md") {
		t.Fatalf("the offending file is not in the tree, so there is nothing left unfixed " +
			"and its absence from the report would be correct")
	}

	after := observedFiles(t, e.Ledger(proj, "watcher", "seen"))
	if len(after) <= len(first) {
		t.Fatalf("the second cycle observed nothing at all (%d entries, was %d), so there is "+
			"no evidence either way: %v", len(after), len(first), after)
	}
	// The control: this cycle's ordinary difference did arrive, so the assertion
	// below is about the refused file rather than about a dead ledger.
	second := after[len(first):]
	if !sawPathIn(second, "unrelated.md") {
		t.Fatalf("the cycle's own work is missing from %v — nothing was dispatched, so the "+
			"claim below would be vacuous", second)
	}
	if countPath(second, "bad-file.md") == 0 {
		t.Fatalf("an unfixed refusal was dropped once it left the difference: the file is still "+
			"broken on disk and was not reported after the branch switch (%v) — the refusal was "+
			"tied to the measuring point after all", second)
	}

	// And it arrives as an UPDATE, not a create.
	//
	// The re-added file is not this cycle's work: it was present at the point
	// being measured from, which is exactly why the difference is silent about
	// it. Reporting it as a creation would tell every rule bound to
	// PostFileCreate about a file the cycle did not create — a rule that only
	// fires on new files would object to one that has been there all along.
	//
	// Asserted because nothing else can catch it. The path is what the earlier
	// assertion reads, and the kind is chosen by readdOutstanding alone — a
	// re-add that claimed the file was absent at the baseline produces the same
	// path in the same ledger, and every other test in this tree stays green.
	for _, o := range second {
		if o.Path == "bad-file.md" && o.Kind != "PostFileUpdate" {
			t.Fatalf("a re-reported unfixed file arrived as %q, want PostFileUpdate — it was "+
				"present at the point being measured from, so calling it a creation puts it in "+
				"front of every rule bound to new files", o.Kind)
		}
	}
}

// sawPathIn reports whether a path appears at all, for the control assertions.
func sawPathIn(got []observed, path string) bool { return countPath(got, path) > 0 }
