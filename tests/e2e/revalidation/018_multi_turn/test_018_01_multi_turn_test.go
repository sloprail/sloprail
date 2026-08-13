package e2e

import (
	"fmt"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// Revalidation across MANY cycles, which is where a session actually lives.
//
// Every other test in this tree spans one or two cycles. A real session spans
// dozens, and the failures that hurt most are the ones that need distance to
// appear: state that survives cycle 2 and is lost by cycle 5, a refusal that
// decays, a record that follows a changing session id into an empty directory
// halfway through the afternoon's work. None of those are visible in a
// two-cycle test — a store that dropped everything after N cycles, or on the
// first re-fork, passes every short test in this package and fails a user.
//
// # What a "cycle" is here, and why the count is real
//
// Each e.Run is one invocation of the agent against the same conversation: a
// fresh process, a fresh hook process per event, and a store that must be
// reopened and read back. Nothing is carried in memory between them. So a claim
// that state written in cycle 1 is readable in cycle 5 is a claim about the
// durable record, made against four intervening process boundaries.
//
// The scenario ids matter for this and are not cosmetic. The mock re-runs a
// scenario script after each tool result and each turn skips itself if its
// marker is already in the conversation — so a second Run against one session
// reads the first Run's markers out of the transcript. Turn ids are therefore
// unique per cycle throughout this file; reusing one silently emits nothing and
// the test observes an empty cycle it would read as "the hook did not run".
//
// # Every test here is paired with something that can fail
//
// A multi-turn skip test is the most vacuous-prone shape in the tree: "the hook
// did not run in cycles 2..5" is satisfied by a hook that never ran at all, by
// a store that never opened, and by a scenario that emitted nothing. So each
// test asserts BOTH directions — what must be skipped and, in the same run,
// something that must NOT be — so that a build where nothing happens fails
// rather than passes.

const judgeDecl = `---
hooks:
  PreFileCreate:
    - hooks:
        - type: command
          command: ./judge.sh
  PreFileUpdate:
    - hooks:
        - type: command
          command: ./judge.sh
---

# Refuses content holding a secret, and records every time it is asked.
`

// judgeScript records that it was asked, and what about, before deciding.
const judgeScript = `#!/bin/sh
payload=$(cat)
path=$(printf '%s' "$payload" | sed -n 's/.*"path":"\([^"]*\)".*/\1/p')
ws=$(printf '%s' "$payload" | sed -n 's|.*"guardrailDir":"\(.*\)/\.sloprail/guardrails/.*|\1|p')
echo "asked path=[$path]" >> "$PWD/log"
body=$(printf '%s' "$payload" | grep -o '"content":"[^"]*"' || true)
if [ -z "$body" ]; then
  body=$(cat "$ws/$path" 2>/dev/null || true)
fi
case "$body" in
  *SECRET*) echo "content holds a secret" >&2; exit 2 ;;
esac
exit 0
`

// watcherDecl and watcherScript are a rule with no opinion: it records every
// event it is asked about and permits it. What a test wants when the question
// is "was this rule invoked", with no refusal to complicate the count.
const watcherDecl = judgeDecl

const watcherScript = `#!/bin/sh
cat >/dev/null
echo "asked" >> "$PWD/log"
exit 0
`

// settle offers a create of the given content and then removes the file, so the
// verdict recorded is a create's — keyed on the PENDING bytes — and the next
// offer of the same content is a create too.
//
// Every multi-cycle comparison in this file is built out of creates for that
// reason. An update's subject cannot be the bytes it would leave (PreFileUpdate
// carries a path and no content), so a cross-cycle test built on updates would
// be comparing something other than what it claims.
func settle(id, path, content string) []harness.Turn {
	return []harness.Turn{
		Write(id, path, content),
		Bash(id+"-rm", "rm -f "+path),
	}
}

// T018_01: a verdict recorded in cycle 1 is still in force in cycle 5.
//
// The plain distance test. The same content is offered in five successive
// cycles of one conversation. The first offer is judged; the other four are the
// identical subject with a real pass on record, and must be skipped.
//
// A store that opened correctly but wrote nothing durable fails at cycle 2. A
// store that expired verdicts, or that keyed them on anything that drifts per
// process — a pid, a temp path, a clock — fails at whichever cycle the drift
// first bites, which is exactly the failure a two-cycle test cannot see.
//
// The negative half is in the same run: cycle 5 also offers DIFFERENT content,
// which must be judged. Without it, "asked once in five cycles" would also be
// the reading of a build where the guardrail stopped firing after cycle 1.
func TestT018_01_AVerdictFromCycleOneStillHoldsInCycleFive(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "judge", judgeDecl, map[string]string{"judge.sh": judgeScript})

	const sess = "s-018-01"
	const content = "content settled in the first cycle"

	for cycle := 1; cycle <= 5; cycle++ {
		id := fmt.Sprintf("c%d", cycle)
		e.Run(proj, sess, fmt.Sprintf("cycle %d", cycle),
			Turns("done", settle(id, "notes.md", content)...))

		lines := e.Ledger(proj, "judge", "log")
		if n := len(lines); n != 1 {
			t.Fatalf("after cycle %d the guardrail had been asked %d time(s), want 1 — a verdict "+
				"recorded in the first cycle stays in force for as long as the content does. A "+
				"second invocation means the record did not survive the process boundary between "+
				"cycles: expired, keyed on something that drifts per process, or never written at "+
				"all. Ledger: %v", cycle, n, lines)
		}
	}

	// The negative half, in the same conversation and the same run of the
	// suite: content this guardrail has NOT passed must still be judged, in the
	// fifth cycle, after four skips. Without this the loop above is equally the
	// signature of a guardrail that quietly stopped firing.
	got := e.Run(proj, sess, "cycle 6: something new", Turns("done",
		Write("c6", "notes.md", "SECRET=hunter2"),
	))
	lines := e.Ledger(proj, "judge", "log")
	if n := len(lines); n != 2 {
		t.Fatalf("the guardrail was asked %d time(s) in total, want 2 — four cycles of skipping "+
			"must not be a guardrail that stopped firing, and new content in the sixth cycle is "+
			"still judged. Ledger: %v\n%s", n, lines, got.Output)
	}
	if !got.Saw("content holds a secret") {
		t.Fatalf("the new content was not refused in the sixth cycle, so the rule was asked but "+
			"no longer governs:\n%s", got.Output)
	}
}

// T018_02: a refusal is retained across ten cycles, and lifts the moment the
// content is fixed.
//
// A refusal that decays stops being enforced, and it does so silently — the
// cycle where it is dropped looks identical to every other, because a missing
// row and a failing row both answer "do not skip" to the only reader there is.
// What distinguishes them is what happens NEXT: with the refusal gone the
// content is unjudged rather than refused, and the first verdict recorded for it
// is believed.
//
// So this drives the sequence a stuck agent actually produces: the same bad
// content offered in ten successive cycles, refused every time, and then fixed
// — after which the fix passes and the fixed content settles. Ten because the
// failure mode being ruled out is decay, and decay needs distance; two cycles
// would be satisfied by a store that remembered exactly one.
//
// Both halves fail loudly. Ten refusals is the retention; the eleventh cycle
// passing is the proof that the guardrail was still deciding rather than stuck
// refusing everything, which a refusal-only test could not tell apart.
func TestT018_02_ARefusalIsRetainedForTenCyclesAndLiftsWhenFixed(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "judge", judgeDecl, map[string]string{"judge.sh": judgeScript})

	const sess = "s-018-02"
	const cycles = 10

	for cycle := 1; cycle <= cycles; cycle++ {
		got := e.Run(proj, sess, fmt.Sprintf("cycle %d: offer the bad content", cycle),
			Turns("done", Write(fmt.Sprintf("bad%d", cycle), "notes.md", "SECRET=hunter2")))

		if !got.Saw("content holds a secret") {
			t.Fatalf("cycle %d: the refusal did not travel back. A refusal is retained, so the "+
				"same failing content is judged again on every offer until it changes — and a "+
				"cycle where it goes quiet is a violation that has stopped being enforced with "+
				"nothing to notice:\n%s", cycle, got.Output)
		}
		if n := len(e.Ledger(proj, "judge", "log")); n != cycle {
			t.Fatalf("after cycle %d the guardrail had been asked %d time(s), want %d — one per "+
				"offer. A skipped offer here means the stored refusal was read as settled work",
				cycle, n, cycle)
		}
	}

	// The agent finally fixes it. The fix must PASS — which is what shows the
	// ten refusals above were a rule deciding rather than a rule stuck.
	fixed := e.Run(proj, sess, "cycle 11: fix it", Turns("done",
		Write("fix", "notes.md", "benign at last"),
	))
	if fixed.Refused() {
		t.Fatalf("the fixed content was refused too, so the ten refusals above are not evidence "+
			"of a retained verdict — they are evidence of a rule refusing everything:\n%s",
			fixed.Output)
	}
	if !fixed.Saw("File written successfully") {
		t.Fatalf("the fix never landed:\n%s", fixed.Output)
	}
	if n := len(e.Ledger(proj, "judge", "log")); n != cycles+1 {
		t.Fatalf("the guardrail was asked %d time(s) through the fix, want %d", n, cycles+1)
	}
}

// T018_03: a re-fork in the middle of a long conversation keeps everything
// recorded before it.
//
// 016 forks after one cycle. This forks after four, and continues for two more,
// which is the shape the invariant is really about: a harness changes the id it
// reports for a conversation that is still going — on a retried request, after
// compacting, on a restart — and the state at risk is not one verdict but an
// afternoon's worth.
//
// Three files are settled across cycles 1-3 under the first id. The fork
// happens. Cycles 4-5 run under the NEW id and re-offer all three. All three
// must still be exempt: a store that followed the fork opens an empty database
// and re-judges every one of them, silently, mid-session.
//
// The negative half is a fourth file, never seen before the fork, offered after
// it. It must be judged — otherwise "nothing ran after the fork" would satisfy
// the test just as well as "everything was correctly skipped".
func TestT018_03_AForkMidConversationKeepsEveryVerdictRecordedBeforeIt(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "watcher", watcherDecl, map[string]string{"judge.sh": watcherScript})

	const before = "s-018-03-before"
	const after = "s-018-03-after"

	files := []string{"alpha.md", "beta.md", "gamma.md"}

	// Cycles 1-3 under the first reported id: one file settled per cycle.
	for i, f := range files {
		e.Run(proj, before, fmt.Sprintf("settle %s", f),
			Turns("done", settle(fmt.Sprintf("s%d", i), f, "settled "+f)...))
	}
	if n := len(e.Ledger(proj, "watcher", "log")); n != len(files) {
		t.Fatalf("the guardrail was asked %d time(s) before the fork, want %d — one per file "+
			"settled, or there is nothing for the fork to preserve", n, len(files))
	}

	// The harness re-forks: same conversation, new reported id.
	e.Fork(proj, before, after)

	// Cycles 4-5 under the NEW id, re-offering everything settled before it.
	for i, f := range files {
		e.Run(proj, after, fmt.Sprintf("re-offer %s after the fork", f),
			Turns("done", settle(fmt.Sprintf("r%d", i), f, "settled "+f)...))
	}

	if n := len(e.Ledger(proj, "watcher", "log")); n != len(files) {
		t.Fatalf("the guardrail was asked %d time(s) in total, want %d — every verdict recorded "+
			"across the cycles BEFORE the re-fork must still be in force after it. More "+
			"invocations means the engine keyed its state on the id the harness reports, "+
			"followed the change into an empty database, and abandoned an afternoon's worth of "+
			"settled work mid-conversation", n, len(files))
	}

	// The negative half: a file this conversation has never judged, offered
	// after the fork. It must be asked about — or the silence above is a
	// guardrail that stopped firing rather than a record that survived.
	e.Run(proj, after, "a file never seen before the fork",
		Turns("done", settle("new", "delta.md", "brand new content")...))

	if n := len(e.Ledger(proj, "watcher", "log")); n != len(files)+1 {
		t.Fatalf("the guardrail was asked %d time(s) in total, want %d — unjudged content after "+
			"a re-fork is still judged. If it was not, the exemptions observed above are a hook "+
			"that stopped running rather than a record that survived", n, len(files)+1)
	}
}

// T018_04: a guardrail added mid-session inherits no verdict, including
// verdicts recorded many cycles before it existed.
//
// 015 states this across two cycles. The multi-turn version is the one that
// matters operationally: a rule is written and dropped into a session that has
// been running for a while, and the files it most needs to judge are precisely
// the ones the older rules settled long ago.
//
// Four files are settled under "alpha" across four cycles. "beta" is then
// installed and every one of those files is offered again. beta must be asked
// about all four; alpha must be asked about none of them, which is what shows
// something is genuinely being skipped and beta's invocations are not just "no
// exemption exists on this build".
func TestT018_04_AGuardrailAddedLateJudgesEverythingSettledBeforeIt(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "alpha", watcherDecl, map[string]string{"judge.sh": watcherScript})

	const sess = "s-018-04"
	files := []string{"one.md", "two.md", "three.md", "four.md"}

	for i, f := range files {
		e.Run(proj, sess, fmt.Sprintf("settle %s", f),
			Turns("done", settle(fmt.Sprintf("s%d", i), f, "settled "+f)...))
	}
	if n := len(e.Ledger(proj, "alpha", "log")); n != len(files) {
		t.Fatalf("alpha was asked %d time(s), want %d — every file has to be settled under alpha "+
			"alone for this test to mean anything", n, len(files))
	}

	// The new rule arrives after four cycles of settled work. It has judged
	// nothing, and none of the verdicts on record are its.
	e.Guardrail(proj, "beta", watcherDecl, map[string]string{"judge.sh": watcherScript})

	for i, f := range files {
		e.Run(proj, sess, fmt.Sprintf("re-offer %s", f),
			Turns("done", settle(fmt.Sprintf("r%d", i), f, "settled "+f)...))
	}

	if n := len(e.Ledger(proj, "beta", "log")); n != len(files) {
		t.Fatalf("beta was asked %d time(s), want %d — a guardrail added to a running session "+
			"must judge every file the older rules had already settled. Anything less and a new "+
			"rule is installed, bound, enabled, and inert on exactly the work it was written for",
			n, len(files))
	}
	if n := len(e.Ledger(proj, "alpha", "log")); n != len(files) {
		t.Fatalf("alpha was asked %d time(s) in total, want %d — it had already passed this exact "+
			"content, so its own exemptions should hold across the re-offers. If alpha ran again, "+
			"nothing is being skipped on this build and beta's invocations say nothing about "+
			"verdicts belonging to the rule that reached them", n, len(files))
	}
}

// T018_05: a guardrail removed mid-session stops being asked, and the rules
// that remain keep their own verdicts.
//
// The other half of a changing rule set. Removing a rule must do two things and
// not a third: the removed rule stops firing (obviously), the surviving rule
// keeps its exemptions (less obviously — a record keyed loosely enough to be
// disturbed by a rule disappearing would re-judge everything), and the removed
// rule's verdicts must not start answering for anyone else.
//
// # What "removed" means here, and the trap that is not one
//
// The whole folder goes. Deleting only GUARDRAIL.md is NOT a removal: the
// engine then sees a folder the project keeps as a guardrail which it cannot
// parse, and refuses every action while that is true — deliberately, because an
// unreadable rule must not be read as approval merely for being unreadable.
// Writing this test the other way produced exactly that, with every subsequent
// write in the session denied by a rule that no longer existed. That is correct
// behaviour (pinned by pre_tool/013_broken_declaration_is_not_silent) and the
// wrong thing to exercise here, so harness.RemoveGuardrail removes the folder
// and hands back the ledger before it goes.
func TestT018_05_AGuardrailRemovedMidSessionStopsFiringAndDisturbsNothing(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "keeper", watcherDecl, map[string]string{"judge.sh": watcherScript})
	e.Guardrail(proj, "goner", watcherDecl, map[string]string{"judge.sh": watcherScript})

	const sess = "s-018-05"

	e.Run(proj, sess, "settle under both rules",
		Turns("done", settle("s1", "notes.md", "settled content")...))

	for _, name := range []string{"keeper", "goner"} {
		if n := len(e.Ledger(proj, name, "log")); n != 1 {
			t.Fatalf("%s was asked %d time(s), want 1 — both rules have to judge the file for "+
				"the removal below to be removing something that was live", name, n)
		}
	}

	// The rule is removed the way a user removes one. Its ledger stood at one
	// line; with the folder gone, a rule that somehow still ran would recreate
	// the file, so an absent ledger afterwards is the answer that nothing did.
	if n := len(e.RemoveGuardrail(proj, "goner", "log")); n != 1 {
		t.Fatalf("the removed rule's ledger stood at %d line(s) at removal, want 1", n)
	}

	e.Run(proj, sess, "offer the settled content again",
		Turns("done", settle("s2", "notes.md", "settled content")...))

	if lines := e.Ledger(proj, "goner", "log"); len(lines) != 0 {
		t.Fatalf("the removed guardrail wrote %d line(s) after its folder was deleted (%v) — a "+
			"rule a project no longer declares must stop being asked", len(lines), lines)
	}
	if n := len(e.Ledger(proj, "keeper", "log")); n != 1 {
		t.Fatalf("keeper was asked %d time(s) in total, want 1 — its own pass on this exact "+
			"content still holds. A rule disappearing from the session must not disturb the "+
			"verdicts of the rules that remain", n)
	}

	// And keeper still governs: new content is judged. Without this, the two
	// assertions above are equally satisfied by a session in which no guardrail
	// runs at all after a rule is deleted — which would be a far worse bug than
	// the one being ruled out.
	e.Run(proj, sess, "offer new content",
		Turns("done", settle("s3", "notes.md", "brand new content")...))

	if n := len(e.Ledger(proj, "keeper", "log")); n != 2 {
		t.Fatalf("keeper was asked %d time(s) in total, want 2 — removing one rule must not stop "+
			"the others from firing. If keeper went quiet, deleting a rule disables the whole "+
			"session's enforcement rather than one rule's", n)
	}
	if lines := e.Ledger(proj, "goner", "log"); len(lines) != 0 {
		t.Fatalf("the removed guardrail wrote %d line(s) (%v)", len(lines), lines)
	}
}

// T018_05b: a guardrail DISABLED mid-session stops being asked, and everything
// else is undisturbed.
//
// The other way a user turns a rule off, and the one that can be observed more
// sharply than removal. Here the folder, the scripts and the ledger all stay in
// place — so "the disabled rule did not run" is a claim about a file that
// exists and did not grow, rather than about a file that is absent. A rule that
// kept firing appends to it.
//
// Turning a rule off is a declaration rather than an absence (`enabled: false`
// in the frontmatter), which means the engine reads the rule and then declines
// to run it. That is a different code path from not finding it at all, and it
// is the one a user takes when silencing a rule temporarily — which is exactly
// when the rules around it must keep working.
func TestT018_05b_AGuardrailDisabledMidSessionStopsFiringAndDisturbsNothing(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "keeper", watcherDecl, map[string]string{"judge.sh": watcherScript})
	e.Guardrail(proj, "muted", watcherDecl, map[string]string{"judge.sh": watcherScript})

	const sess = "s-018-05b"

	e.Run(proj, sess, "settle under both rules",
		Turns("done", settle("s1", "notes.md", "settled content")...))

	for _, name := range []string{"keeper", "muted"} {
		if n := len(e.Ledger(proj, name, "log")); n != 1 {
			t.Fatalf("%s was asked %d time(s), want 1 — both rules have to be live before one "+
				"of them is turned off", name, n)
		}
	}

	e.DisableGuardrail(proj, "muted", watcherDecl)

	// NEW content, not a repeat. A repeat would be legitimately skipped for
	// muted anyway — it holds its own pass on the settled bytes — so the
	// silence would prove nothing about the disabling. Content nobody has
	// judged is the case where a live rule certainly WOULD run.
	e.Run(proj, sess, "offer content neither rule has judged",
		Turns("done", settle("s2", "notes.md", "content nobody has judged")...))

	if n := len(e.Ledger(proj, "muted", "log")); n != 1 {
		t.Fatalf("the disabled guardrail was asked %d time(s) in total, want 1 — a rule declaring "+
			"itself off must not run, and this offer is content it has never judged, so an "+
			"exemption cannot account for the silence", n)
	}
	if n := len(e.Ledger(proj, "keeper", "log")); n != 2 {
		t.Fatalf("keeper was asked %d time(s) in total, want 2 — disabling one rule must not "+
			"silence the others, and this is content keeper has not judged either", n)
	}
}

// T018_06: verdicts for many files coexist across many cycles.
//
// Distance in the other dimension. The tests above hold one or two files across
// many cycles; this holds many files across many cycles and interleaves them, so
// that a record which kept only the most recent verdict — per session, per
// guardrail, or in a fixed-size cache — is caught.
//
// Six files are settled in cycle 1. Cycles 2-4 re-offer them in a DIFFERENT
// order each time. Every re-offer must be skipped: six verdicts, all live, none
// evicting another. An engine holding one row per session passes cycle 1 and
// fails immediately; one holding a small cache fails at whichever size it has.
//
// The negative half is a seventh file introduced in the last cycle, which must
// be judged.
func TestT018_06_ManyFilesVerdictsCoexistAcrossManyCycles(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "watcher", watcherDecl, map[string]string{"judge.sh": watcherScript})

	const sess = "s-018-06"
	files := []string{"a.md", "b.md", "c.md", "d.md", "e.md", "f.md"}

	// Cycle 1: settle all six, one turn each.
	var turns []harness.Turn
	for i, f := range files {
		turns = append(turns, settle(fmt.Sprintf("s%d", i), f, "settled "+f)...)
	}
	e.Run(proj, sess, "settle six files", Turns("done", turns...))

	if n := len(e.Ledger(proj, "watcher", "log")); n != len(files) {
		t.Fatalf("the guardrail was asked %d time(s) in the first cycle, want %d — one per file",
			n, len(files))
	}

	// Cycles 2-4: the same six, each cycle in a different order. Order is
	// rotated rather than shuffled so the test is deterministic while still
	// exercising a different sequence each time — a record that survived only
	// because the access order matched the write order would be caught.
	for cycle := 2; cycle <= 4; cycle++ {
		var rotated []harness.Turn
		for i := range files {
			f := files[(i+cycle)%len(files)]
			rotated = append(rotated, settle(fmt.Sprintf("c%d-%d", cycle, i), f, "settled "+f)...)
		}
		e.Run(proj, sess, fmt.Sprintf("cycle %d", cycle), Turns("done", rotated...))

		if n := len(e.Ledger(proj, "watcher", "log")); n != len(files) {
			t.Fatalf("after cycle %d the guardrail had been asked %d time(s), want %d — six "+
				"verdicts have to be live at once, across cycles and in any order. A record "+
				"keeping one verdict per session, or a cache smaller than the working set, "+
				"shows up here as re-judged files", cycle, n, len(files))
		}
	}

	// The negative half: a seventh file, in the last cycle, after three cycles
	// of complete silence. It must be judged.
	e.Run(proj, sess, "a seventh file", Turns("done", settle("new", "g.md", "settled g.md")...))
	if n := len(e.Ledger(proj, "watcher", "log")); n != len(files)+1 {
		t.Fatalf("the guardrail was asked %d time(s) in total, want %d — after three silent "+
			"cycles a new file must still be judged, or the silence was a hook that stopped "+
			"firing rather than verdicts being honoured", n, len(files)+1)
	}
}

// T018_07: a refusal recorded before a re-fork is still refusing after it.
//
// The two multi-turn mechanisms crossed. 016 forks around a PASS, which is
// observed as a hook not running — the vacuity-prone direction. This forks
// around a REFUSAL, which is observed as a refusal travelling back, and cannot
// pass by anything failing to happen.
//
// It is also the case with the worst failure mode. A pass lost to a fork costs
// re-judging. A refusal lost to a fork costs enforcement: the content is
// unjudged rather than refused on the other side, and the first rule to record
// a pass for it is believed.
func TestT018_07_ARefusalSurvivesAReforkAndKeepsRefusing(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "judge", judgeDecl, map[string]string{"judge.sh": judgeScript})

	const before = "s-018-07-before"
	const after = "s-018-07-after"

	first := e.Run(proj, before, "offer bad content", Turns("done",
		Write("b1", "notes.md", "SECRET=hunter2"),
	))
	if !first.Saw("content holds a secret") {
		t.Fatalf("the content was not refused before the fork, so there is no refusal to "+
			"survive it:\n%s", first.Output)
	}

	e.Fork(proj, before, after)

	// The same content, under the new reported id. Still refused.
	second := e.Run(proj, after, "offer it again after the fork", Turns("done",
		Write("b2", "notes.md", "SECRET=hunter2"),
	))
	if !second.Saw("content holds a secret") {
		t.Fatalf("the refused content was not refused after the re-fork. A refusal that does not "+
			"survive a change of reported session id leaves the content unjudged rather than "+
			"refused on the other side, and the first verdict recorded for it is believed:\n%s",
			second.Output)
	}

	// And the fix lands after the fork, so the conversation is still deciding
	// rather than stuck refusing. This is what distinguishes "the refusal
	// survived" from "everything after a fork is refused".
	fixed := e.Run(proj, after, "fix it after the fork", Turns("done",
		Write("f1", "notes.md", "benign"),
	))
	if fixed.Refused() {
		t.Fatalf("benign content was refused after the fork, so the refusal above is not "+
			"evidence of a retained verdict:\n%s", fixed.Output)
	}

	// Three judgements so far: the refusal before the fork, the refusal after,
	// and the fix. A count short of that is an offer that was skipped.
	lines := e.Ledger(proj, "judge", "log")
	if n := len(lines); n != 3 {
		t.Fatalf("the guardrail was asked %d time(s), want 3 — one per offer, since a refusal "+
			"never licenses a skip and the fix is content nobody had judged. Ledger: %v", n, lines)
	}

	// # What this test does and does NOT pin, measured rather than assumed
	//
	// Every assertion above is satisfied by an engine that ABANDONED the record
	// at the fork. A refusal that was forgotten is unjudged rather than settled,
	// so it is judged again and refused again; the fix is judged either way.
	// Re-judging produces exactly the observations asserted here.
	//
	// Verified by mutation: making the identity walk stop at the forked
	// transcript's own root (internal/transcript/identity.go, the
	// LogicalParentUUID hop) leaves this test green while T018_03 goes red.
	//
	// That is not a defect in this test so much as a property of refusals — the
	// same one 014 documents at length. A retained refusal and a discarded one
	// are indistinguishable to the only production reader of a stored verdict
	// (sessionstate.Skippable answers false for both), so NO refusal-based
	// observation can pin state survival. Only an EXEMPTION can, because only a
	// pass has a consequence that a lost record would not reproduce — which is
	// what T018_03 asserts, with the same fork, across three files.
	//
	// So the claim this test makes is the narrower true one: a re-fork does not
	// cause a violation to go quiet, and does not leave the conversation stuck
	// refusing after the work is corrected. Stated here rather than dressed up
	// as more, so that nobody reads it as the survival claim T018_03 owns.
}

// T018_08: the same content in two conversations, held apart across many
// cycles.
//
// The negative control for everything above, at multi-turn scale. 016 states it
// for one cycle: an unrelated session inherits nothing. The failure this rules
// out is a store that resolves every session in a project to one identity —
// under which every skip test in this tree would pass while every session read
// every other session's verdicts, and two agents working the same repository
// would exempt each other's unjudged work.
//
// Two conversations run alternately for three cycles each against the same
// path and the same content. Each must judge it exactly once — its own first
// offer — and skip its own repeats, which is both halves at once: shared
// identity shows up as one conversation skipping on the other's verdict, and no
// identity at all shows up as six judgements instead of two.
func TestT018_08_TwoConversationsHoldTheSameContentApartAcrossCycles(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	proj := e.Project()
	// A rule per conversation would confuse the count, so there is one rule and
	// the ledger records the path. What tells the two apart is the arithmetic:
	// each conversation contributes exactly one judgement of the same content.
	e.Guardrail(proj, "watcher", watcherDecl, map[string]string{"judge.sh": watcherScript})

	const left = "s-018-08-left"
	const right = "s-018-08-right"
	const content = "content both conversations write"

	for cycle := 1; cycle <= 3; cycle++ {
		e.Run(proj, left, fmt.Sprintf("left cycle %d", cycle),
			Turns("done", settle(fmt.Sprintf("l%d", cycle), "notes.md", content)...))
		e.Run(proj, right, fmt.Sprintf("right cycle %d", cycle),
			Turns("done", settle(fmt.Sprintf("r%d", cycle), "notes.md", content)...))
	}

	// Two judgements, not one and not six.
	//
	//   6 — no exemption is working at all, and every skip test in this tree is
	//       passing for a reason unrelated to what it claims.
	//   1 — the two conversations resolved to one identity, so one skipped on
	//       the other's verdict. Two agents in one repository would then exempt
	//       each other's unjudged work, which is the worst reading of the two.
	lines := e.Ledger(proj, "watcher", "log")
	if n := len(lines); n != 2 {
		want := "one judgement per conversation: each judges the content on its first offer and " +
			"skips its own repeats"
		switch {
		case n == 1:
			t.Fatalf("the guardrail was asked once across two conversations, want 2 — %s. One "+
				"means both resolved to the SAME identity, so one conversation skipped on the "+
				"other's verdict. State is keyed per session precisely because a session may "+
				"hold its own tree, and one session's pass must never stand in for another's. "+
				"Ledger: %v", want, lines)
		case n >= 6:
			t.Fatalf("the guardrail was asked %d time(s), want 2 — %s. Six is one per offer, "+
				"which means nothing was ever skipped and no exemption is working on this build. "+
				"Ledger: %v", n, want, lines)
		default:
			t.Fatalf("the guardrail was asked %d time(s), want 2 — %s. Ledger: %v", n, want, lines)
		}
	}
}

// T018_09: guardrail STATE — not verdicts — written early is readable late.
//
// The other thing a session remembers, and the one a rule spanning cycles is
// built on. A verdict is the engine's own bookkeeping; `session state` is what a
// guardrail writes for itself, and a rule that counts something, or remembers
// that it warned once, depends on it holding across the whole conversation
// rather than the next cycle.
//
// A counter is incremented once per cycle for six cycles, each in a fresh
// process. Reading back 1,2,3,4,5,6 in order is a claim that cannot pass by
// accident: a store that never opened gives an error, one keyed per process
// gives 1 every time, one that lost the record mid-way restarts the sequence.
//
// This is also the control T018_01 rests on, stated for a longer run than
// harness.SessionStoreOpens covers — that one establishes a single round trip
// inside one cycle, and this establishes it across six.
func TestT018_09_GuardrailStateWrittenEarlyIsReadableManyCyclesLater(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "counter", watcherDecl, map[string]string{"judge.sh": counterScript})

	const sess = "s-018-09"
	const cycles = 6

	for cycle := 1; cycle <= cycles; cycle++ {
		// A different path each cycle, so no invocation can be legitimately
		// exempted by an earlier one — the counter must actually be reached
		// every cycle for the sequence below to be a measurement.
		e.Run(proj, sess, fmt.Sprintf("cycle %d", cycle), Turns("done",
			Write(fmt.Sprintf("c%d", cycle), fmt.Sprintf("f%d.md", cycle), "content"),
		))
	}

	lines := e.Ledger(proj, "counter", "log")
	if n := len(lines); n != cycles {
		t.Fatalf("the counting hook ran %d time(s), want %d — one per cycle, or the sequence "+
			"below is not a measurement of anything. Ledger: %v", n, cycles, lines)
	}
	for i, l := range lines {
		want := fmt.Sprintf("count=[%d]", i+1)
		if !strings.Contains(l, want) {
			t.Fatalf("cycle %d recorded %q, want %s — a guardrail's own state must accumulate "+
				"across the whole conversation. A count that restarts at 1 is a record keyed per "+
				"process; a count that stops advancing is a record that stopped being written; "+
				"an error is a store that never opened. Ledger: %v", i+1, l, want, lines)
		}
	}
}

// counterScript reads its own count, increments it, and records what it read.
//
// The arithmetic is done in the hook rather than by the test, because what is
// under test is that the VALUE travels: a hook that could not read its
// predecessor's write would record an empty count and the sequence would break
// at the first cycle rather than passing with a plausible-looking number.
const counterScript = `#!/bin/sh
cat >/dev/null
prev=$(sloprail session state get count 2>/dev/null || true)
case "$prev" in
  ''|*[!0-9]*) prev=0 ;;
esac
next=$((prev + 1))
echo "count=[$next]" >> "$PWD/log"
sloprail session state set count "$next" >/dev/null 2>&1
exit 0
`
