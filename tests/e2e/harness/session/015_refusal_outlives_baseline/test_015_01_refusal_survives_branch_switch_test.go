package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
	"github.com/sloprail/sloprail/tests/e2e/harness/session/changesetkit"
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
//
//
// This directory tests what a refusal does across a branch switch: a file-guard
// judges the committed changeset at Stop, and an unfixed refusal survives because
// the range does not move until a passing Stop (the next Stop's changeset still
// holds the file).
//
// The observation channel is a file-guard's own ledger under
// `.sloprail/file-guard/watcher/seen` (written via $SR_GUARDRAIL_DIR), read with
// e.FileGuardLedgerLines and parsed back into the FLAT CheckPayload (`.changeset.files[]`,
// path and status A/M/D) the new format hands a check, decoded by changesetkit.

// refuseNamedGuard is a NEW-FORMAT file-guard that refuses any file whose path
// contains "bad", and records every file it was handed.
//
// After-check (a file-guard judges the committed changeset at Stop): the refusal
// stands while the file stays in the changeset, which is exactly the point where refusal-survival is measured — a
// pre-block would stop the write and there would be nothing on disk to re-report.
//
// `match: "**/*.md"` selects the same files the old path-based hook saw: `**/`
// compiles to an OPTIONAL leading directory (`(?:.*/)?`), so it matches
// `bad-file.md` at the repo root AND `sub/x.md` at any depth. Crucially it does
// NOT match the guard's own ledger (`seen`, no `.md`), its `file-guard.yaml`, or
// its `judge.sh` — so the guard
// cannot re-observe its own bookkeeping and there is no ledger-doubling blowup.
//
// The check records BEFORE deciding, so the ledger shows arrival independently of
// the verdict — which is the whole observation this directory needs. A guardrail
// that only refused would leave "was this file put in front of me again?"
// answerable solely by the refusal travelling back, and an after-check refusal
// does not stop anything, so it is a weaker signal.
const refuseNamedGuard = `match: "**/*.md"
checks:
  - script: ./judge.sh
`

// T015_06: a refusal is one session's state. Two sessions in one tree hold their own: session
// one's unfixed refusal is not session two's — session two's first Stop verifies the range ITS
// folder tracks (its range holds the bad file session one committed: the branch is shared, so it
// arrives as an addition), is handed the file again and refused by that verdict — while session
// one's record of refusals is untouched by anything session two did.
func TestT015_06_TwoSessionsInOneTreeHoldTheirRefusalsApart(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	led := e.NewLedger("seen")
	e.FileGuard(proj, "watcher", refuseNamedGuard, map[string]string{"judge.sh": judgeScript(led)})
	e.CommitAll(proj, "the guardrail before the sessions")

	e.Run(proj, "s-015-06-one", "write a bad file", Turns("done",
		Write("w1", "bad-file.md", "violates\n"),
	).ThenCommit("the bad file"))
	oneBefore := len(e.StopContinuations(proj, "s-015-06-one"))
	if oneBefore == 0 {
		t.Fatalf("premise: session one's bad file was not refused")
	}
	first := changesetkit.Files(t, led.Lines())

	e.Run(proj, "s-015-06-two", "write something else", Turns("done",
		Write("w2", "unrelated.md", "fine\n"),
	).ThenCommit("unrelated work"))

	after := changesetkit.Files(t, led.Lines())
	second := after[len(first):]
	if len(second) == 0 {
		t.Fatalf("session two's range was never judged: session one's state stood in for it")
	}
	if !changesetkit.Saw(second, "unrelated.md") {
		t.Fatalf("session two was not handed its own work: %v", second)
	}
	if got := changesetkit.Statuses(second, "bad-file.md"); len(got) == 0 || got[0] != "A" {
		t.Fatalf("session two's range did not hold the file session one was refused for (as an addition): %v", second)
	}
	if len(e.StopContinuations(proj, "s-015-06-two")) == 0 {
		t.Fatalf("session two, whose range holds the bad file, was not refused at its own Stop")
	}
	if n := len(e.StopContinuations(proj, "s-015-06-one")); n != oneBefore {
		t.Fatalf("session one's refusals changed from %d to %d because of session two", oneBefore, n)
	}
}

// judgeScript records the FLAT CheckPayload, then refuses when the path contains
// "bad".
//
// New-format refusal contract: exit non-zero refuses, and a `{"reason": "..."}` on
// stdout is the reason the agent is told (scriptRefusalReason prefers structured
// stdout). Exit 0 permits. This replaces the old exit-2-with-stderr channel.
//
// The path is read out of the payload with a sed rather than a JSON parser because
// a check is an ordinary shell script and this keeps the fixture free of
// dependencies. The flat wire form still carries `"path":"…"` (the event's fields
// spread directly under `event`, so `.event.path` rather than `.event.fields.path`),
// so the same sed matches.
//
// The ledger is written to $SR_GUARDRAIL_DIR/seen — the folder the engine sets for
// a file-guard check (`.sloprail/file-guard/watcher/`), the new-format ledger idiom
// (cf. the fileguard e2e's checkForbidSecret). A defensive `.sloprail/*` skip is
// kept from the old fixture: it is no longer load-bearing (the ledger has no `.md`
// suffix, so `**/*.md` never matches it and no self-observation can occur), but it
// costs nothing and keeps the intent — this rule judges the agent's files, not the
// engine's own bookkeeping — legible.
func judgeScript(led *harness.Ledger) string {
	return `#!/bin/sh
payload="$(cat)"
# The changeset's FILES are the matched ones; the rule's own files (added in the
# commit that installed it, which the range covers) sit in .changeset.others.
paths="$(printf '%s' "$payload" | jq -r '.changeset.files[].path')"
[ -n "$paths" ] || exit 0
printf '%s\n' "$payload" >> ` + led.Sh() + `
for path in $paths; do
  case "$path" in
    bad*) echo '{"reason":"this file is not acceptable"}'; exit 1 ;;
  esac
done
exit 0
`
}

// countPath is how many recorded entries name the path.
func countPath(got []changesetkit.Observed, path string) int {
	return len(changesetkit.Statuses(got, path))
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
	led := e.NewLedger("seen")
	e.FileGuard(proj, "watcher", refuseNamedGuard, map[string]string{"judge.sh": judgeScript(led)})
	e.CommitAll(proj, "the guardrail before the session")

	const sess = "s-015-01"
	e.Run(proj, sess, "write a bad file", Turns("done",
		Write("w1", "bad-file.md", "violates\n"),
	).ThenCommit("the bad file"))

	first := changesetkit.Files(t, led.Lines())
	if countPath(first, "bad-file.md") == 0 {
		t.Fatalf("the offending file never reached the rule in the first cycle: %v — "+
			"nothing was refused, so there is no surviving refusal to test", first)
	}

	// A second cycle in the same session that does NOT touch the offending
	// file. Its only change is elsewhere, so a difference-only engine has
	// nothing to say about bad-file.md.
	e.Run(proj, sess, "do something else", Turns("done",
		Write("w2", "unrelated.md", "fine\n"),
	).ThenCommit("unrelated work"))

	after := changesetkit.Files(t, led.Lines())
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
	led := e.NewLedger("seen")
	e.FileGuard(proj, "watcher", refuseNamedGuard, map[string]string{"judge.sh": judgeScript(led)})

	// The rule is committed first, so it exists on both lines of history.
	// Without this the checkout below deletes .sloprail/ along with everything
	// else the other branch does not hold, the project then loads no rules, and
	// the ledger stops growing for a reason that has nothing to do with
	// refusals surviving — which is exactly the false pass this test is about.
	// The harness's own scenario script is kept out of every commit: a tracked
	// copy rewritten by the next cycle would abort the branch switch below.
	writeFile(t, proj, ".gitignore", ".scenario.sh\n")
	e.CommitAll(proj, "the guardrail, on every line of history")
	root := e.Git(proj, "rev-parse", "HEAD")

	// A branch that diverges from that point, prepared before the session so the
	// switch during it is a real change of history.
	e.Git(proj, "checkout", "-b", "feature", root)
	e.Git(proj, "commit", "--allow-empty", "-m", "on feature")
	e.Git(proj, "checkout", "main")
	e.Git(proj, "commit", "--allow-empty", "-m", "on main, after the split")

	if e.Git(proj, "cat-file", "-t", "feature:.sloprail/file-guard/watcher/file-guard.yaml") != "blob" {
		t.Fatalf("the file-guard is not present on the branch the agent switches to, so the " +
			"rule cannot fire there and a ledger that stops growing would prove nothing")
	}

	const sess = "s-015-02"
	e.Run(proj, sess, "write a bad file", Turns("done",
		Write("w1", "bad-file.md", "violates\n"),
	).ThenCommit("the bad file"))

	first := changesetkit.Files(t, led.Lines())
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
	).ThenCommit("recreate the bad file"))

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
	after := changesetkit.Files(t, led.Lines())
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

// T015_03: a file that gets fixed stops being refused.
//
// The negative half, and the one that stops T015_01 and T015_02 from being
// satisfied by an engine that simply refuses every file it has ever refused
// forever. "Until a hook passes it" is a real boundary: once the content passes,
// the refusal must end.
//
// The fix is a rename of the content, not of the path — the same path now holds
// content the rule accepts.
func TestT015_03_AFixedFileStopsBeingReported(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	// Refuses on CONTENT here, so the same path can be fixed in place. The
	// path-based fixture above cannot express a fix without a rename. Same
	// new-format shape (match + a script check), after-check.
	const refuseContentGuard = `match: "**/*.md"
checks:
  - script: ./judge.sh
`
	// Reads the file off disk rather than the payload: the after-the-fact kinds
	// carry the path, and the content is already on disk by then.
	//
	// The project root is derived from $SR_GUARDRAIL_DIR, the one thing the engine
	// hands a file-guard check that names an absolute location
	// (`.sloprail/file-guard/watcher`, so trimming `/.sloprail/file-guard/*` yields
	// the project root). The new-format CheckPayload has no `guardrailDir` field the
	// old payload carried — SR_GUARDRAIL_DIR is the replacement. A prefix that
	// resolved to empty would make every grep miss, the rule would refuse nothing,
	// and this test would pass while testing nothing. The refusal ledger checked
	// below is what makes that failure visible instead.
	//
	// The guardrail's OWN files are skipped. With a `**/*.md` match this is
	// defensive rather than load-bearing (the seen/refused ledgers have no `.md`
	// suffix and cannot match), but it keeps the rule about the agent's files, not
	// the engine's own bookkeeping.
	// The ledgers are harness ledgers, outside the rule's folder: a rule's hash covers its
	// whole folder, so a ledger written there would change the hash on every run
	// and void the watermark this test is about (a pass advancing the range).
	seen, refusedLed := e.NewLedger("seen"), e.NewLedger("refused")
	judgeContentScript := `#!/bin/sh
payload="$(cat)"
paths="$(printf '%s' "$payload" | jq -r '.changeset.files[].path')"
[ -n "$paths" ] || exit 0
root="${SR_GUARDRAIL_DIR%/.sloprail/file-guard/*}"
printf '%s\n' "$payload" >> ` + seen.Sh() + `
refused=0
for path in $paths; do
  if [ -f "$root/$path" ] && grep -q FORBIDDEN "$root/$path"; then
    echo "$path" >> ` + refusedLed.Sh() + `
    refused=1
  fi
done
if [ "$refused" = 1 ]; then
  echo '{"reason":"still contains the forbidden word"}'; exit 1
fi
exit 0
`
	e.FileGuard(proj, "watcher", refuseContentGuard, map[string]string{"judge.sh": judgeContentScript})
	e.CommitAll(proj, "the guardrail before the session")

	const sess = "s-015-03"
	e.Run(proj, sess, "write then fix", Turns("done",
		Write("w1", "subject.md", "FORBIDDEN content\n"),
	).ThenCommit("write subject"))
	first := changesetkit.Files(t, seen.Lines())
	if countPath(first, "subject.md") == 0 {
		t.Fatalf("the file never reached the rule in the first cycle: %v", first)
	}
	// The rule must have actually REFUSED, not merely been handed the file.
	//
	// This fixture locates the file from $SR_GUARDRAIL_DIR; if it resolved neither
	// the path nor the project root it would exit 0 on everything, leaving a
	// "fixed" file indistinguishable from one that was never broken and the
	// comparison below trivially satisfied.
	//
	// Read from the guard's own folder rather than from the run's output. A refusal
	// at an after-the-fact point does not travel back through a tool result — there
	// is no pending call to deny — so it does not appear in the mock's stream at
	// all, and asserting on the stream here would fail for every build including a
	// correct one.
	if len(refusedLed.Lines()) == 0 {
		t.Fatalf("the rule never refused the offending file, so nothing here was ever unfixed " +
			"and the comparison below cannot fail")
	}

	// Fixed: the same range, now holding acceptable content, passes. What used to
	// be asserted here too — that a later unrelated cycle is not handed the fixed
	// file again — was the per-session watermark, which is gone: the caller states
	// the range, and a range that holds the file hands it to the rule.
	e.Run(proj, sess, "fix it", Turns("done",
		Write("w2", "subject.md", "acceptable content\n"),
	).ThenCommit("fix subject"))
	if refusals := e.CheckRun(proj, sess); len(refusals) != 0 {
		t.Fatalf("a file that has been fixed is still refused: %q — a passing verdict must "+
			"end the refusal, or every file ever refused accumulates forever", refusals)
	}
}

// T015_04: a refusal outlives the branch the agent leaves it on.
//
// The refused file is committed on main; the agent then checks out another branch,
// where the file does not exist at all, and does unrelated work there. The range of
// main is still one the session answers for (a tracked range is per branch), so the
// Stop still verifies it: the rule is put the file again and the Stop refuses it,
// naming main (the stored verdict of the unchanged range is replayed, not re-asked). A
// refusal tied to the branch the agent happens to be on would drop the broken file out of
// view at exactly this moment.
//
// There is no re-creation of the file after the switch (unlike T015_02): the file is
// absent from the tree and from feature's range, so the only way it can be reported
// is through main's range.
func TestT015_04_ARefusedFileOnAnotherBranchIsStillReported(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	led := e.NewLedger("seen")
	e.FileGuard(proj, "watcher", refuseNamedGuard, map[string]string{"judge.sh": judgeScript(led)})
	writeFile(t, proj, ".gitignore", ".scenario.sh\n")
	e.CommitAll(proj, "the guardrail, on every line of history")
	root := e.Git(proj, "rev-parse", "HEAD")

	e.Git(proj, "checkout", "-b", "feature", root)
	e.Git(proj, "commit", "--allow-empty", "-m", "on feature")
	e.Git(proj, "checkout", "main")

	const sess = "s-015-04"
	e.Run(proj, sess, "write a bad file", Turns("done",
		Write("w1", "bad-file.md", "violates\n"),
	).ThenCommit("the bad file"))
	first := changesetkit.Files(t, led.Lines())
	if countPath(first, "bad-file.md") == 0 {
		t.Fatalf("the offending file never reached the rule in the first cycle: %v — "+
			"nothing was refused, so there is no surviving refusal to test", first)
	}
	stops := len(e.AllBlockingErrorsFrom(proj, sess, "Stop"))

	e.Run(proj, sess, "switch branches", Turns("done",
		Bash("b2", "git checkout feature"),
		Write("w3", "unrelated.md", "fine\n"),
	).ThenCommit("unrelated work"))

	if got := e.Git(proj, "rev-parse", "--abbrev-ref", "HEAD"); got != "feature" {
		t.Fatalf("the agent did not actually switch branches (on %q), so nothing moved and this proves nothing", got)
	}
	if e.Wrote(proj, "bad-file.md") {
		t.Fatalf("the offending file is still in the tree on feature, so it is in the branch's own " +
			"range and its report proves nothing about the other branch's")
	}
	second := changesetkit.Files(t, led.Lines())[len(first):]
	if !changesetkit.Saw(second, "unrelated.md") {
		t.Fatalf("the cycle's own work is missing from %v — the claim below would be vacuous", second)
	}
	// The rule here is a SCRIPT check. A stored pass is a hit and runs nothing: the passing
	// unrelated.md reaches the script exactly once, however many times the cycle verifies its
	// range. A stored script refusal is asked again: main's refused bad-file.md reaches it once.
	if n := countPath(second, "unrelated.md"); n != 1 {
		t.Fatalf("the passing unrelated.md reached the script %d times (%v): a hit must run nothing", n, second)
	}
	if n := countPath(second, "bad-file.md"); n != 1 {
		t.Fatalf("the refused bad-file.md reached the script %d times (%v): a script refusal is asked again, once", n, second)
	}
	later := e.AllBlockingErrorsFrom(proj, sess, "Stop")[stops:]
	if got := strings.Join(later, "\n"); !strings.Contains(got, "this file is not acceptable") || !strings.Contains(got, "(main") {
		t.Fatalf("the Stop after the switch did not refuse main's unfixed range:\n%s", got)
	}
}
