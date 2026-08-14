package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The refusal bypass, and its whole family.
//
// # The bug this package is built around
//
// A create records its pass on the PENDING bytes — the only place a creation's
// content can be seen, since the file is not there yet. The next update takes
// its subject from DISK, which by then holds exactly those bytes. So the two
// derivations coincide the instant the create lands, and the first update after
// any successful create matched the create's stored pass and was skipped
// REGARDLESS OF WHAT IT CONTAINED.
//
// It shipped. Review caught it, not tests — which is the reason this package
// exists rather than one more case being appended to 013.
//
// The fix is stated as a rule about what a subject IS: it exists only where the
// fingerprint names what the action WOULD LEAVE. PreFileCreate has one (the
// pending bytes are on the event). PreFileUpdate has none (the kind carries a
// path and no content, so the bytes the write leaves are unavailable rather
// than merely unread). Deletes have none (there are no resulting bytes at all).
//
// # What that rule predicts, and what each test here holds it to
//
// A rule is only worth having if it decides cases nobody enumerated. Each test
// below is a case the rule decides, arrived at by asking what ELSE coincides:
//
//   - T017_01 create-then-update: the original bug, one cycle.
//   - T017_02 create, update, update: the bug's tail. A design that fixed only
//     the FIRST update would pass T017_01 and fail here.
//   - T017_03 create in cycle 1, update in cycle 2: the same coincidence across
//     a turn boundary, where the pass is read back from the store rather than
//     held in one process's memory.
//   - T017_04 two guardrails, one of which passed the content and one of which
//     never saw it: pooling by content rather than by rule would let the blind
//     one inherit.
//   - T017_05 a file edited back to previously-refused content: the bypass run
//     backwards. The refusal must not be lost to the pass in between.
//   - T017_06 two files whose content is identical: a subject is a (path,
//     content) pair, and keying on content alone would exempt the second file
//     on the first file's verdict.
//   - T017_07 delete: an action that leaves no bytes may not record a pass, or
//     the delete's "verdict" would license the create that follows it.
//
// # Why the ledger, and why it records what it SAW
//
// "The hook did not run" leaves no trace in the stream — a hook that stayed
// silent and a hook that never ran are identical from outside. So every hook
// here appends a line, and the line carries what the hook was shown. A count
// alone cannot tell "asked twice about two different bodies" from "asked twice
// about the same one", and several tests here turn on exactly that distinction.

// judgeDecl binds a judging hook to both pre-file kinds a write goes through:
// the first write to a path creates, every later one updates.
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

// judgeScript is a REAL judge: it reads what it is handed and decides.
//
// It records that it was asked BEFORE deciding, so a refusal and a pass are
// both visible as invocations — a refusal that left no ledger line would be
// indistinguishable from a hook that never ran.
//
// It records the PATH it was asked about as well as the disk it saw. T017_06
// turns on two different files being judged separately, which a bare count
// cannot express.
//
// The workspace comes off guardrailDir in the payload rather than from an
// environment variable: the payload is the one channel that carries it on every
// branch here, and a rule reaching for an unset variable would silently judge
// nothing and pass everything — which would make the refusal assertions green
// for the wrong reason.
const judgeScript = `#!/bin/sh
payload=$(cat)
path=$(printf '%s' "$payload" | sed -n 's/.*"path":"\([^"]*\)".*/\1/p')
ws=$(printf '%s' "$payload" | sed -n 's|.*"guardrailDir":"\(.*\)/\.sloprail/guardrails/.*|\1|p')
echo "asked path=[$path] disk=[$(cat "$ws/$path" 2>/dev/null)]" >> "$PWD/log"
body=$(printf '%s' "$payload" | grep -o '"content":"[^"]*"' || true)
if [ -z "$body" ]; then
  body=$(cat "$ws/$path" 2>/dev/null || true)
fi
case "$body" in
  *SECRET*) echo "content holds a secret" >&2; exit 2 ;;
esac
exit 0
`

// T017_01: a create's pass does not exempt the update that follows it.
//
// The bug as it shipped, in its shortest form:
//
//	Write "v1" -> a CREATE. Subject is the PENDING bytes. Judged, passes, a
//	              pass is recorded under fingerprint(v1). The write lands, so
//	              disk now holds v1.
//	Write "v2" -> an UPDATE. Under the broken design its subject came from
//	              DISK — which holds v1, the exact content the create's pass
//	              was recorded under. Row matches, verdict was a pass, SKIPPED.
//
// So the first update after any successful create was exempt whatever it
// contained. Two writes, two different bodies, two judgements — and the second
// is the one that went missing.
//
// The assertion is the count. On this engine an update-bound hook cannot see
// the pending payload to refuse it (PreFileUpdate carries no content), so
// "judged" is observable here only as "asked". T017_04 states the refusing half
// where the content IS on the event.
func TestT017_01_ACreatesPassDoesNotExemptTheUpdateThatFollows(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "judge", judgeDecl, map[string]string{"judge.sh": judgeScript})

	got := e.Run(proj, "s-017-01", "create then change", Turns("done",
		Write("w1", "notes.md", "v1 benign"),
		Write("w2", "notes.md", "v2 quite different"),
	))

	// The create landed, or there is no pass on record for the update to
	// inherit and the count below would mean something else entirely.
	if n := strings.Count(got.Output, "File written successfully"); n != 2 {
		t.Fatalf("%d write(s) landed, want 2 — the create must go through for its pass to be on "+
			"record when the update arrives:\n%s", n, got.Output)
	}

	lines := e.Ledger(proj, "judge", "log")
	if n := len(lines); n != 2 {
		t.Fatalf("the guardrail was asked %d time(s), want 2. REFUSAL BYPASS: the create recorded "+
			"its pass under the fingerprint of the PENDING bytes; the update that followed took "+
			"its subject from DISK, which by then held exactly those bytes — so the update "+
			"matched the create's pass and was skipped without its own content ever being "+
			"judged. Ledger: %v\n%s", n, lines, got.Output)
	}
}

// T017_02: the bypass does not merely shift by one — a SECOND update is judged
// too.
//
// The bug's tail, and the case that separates a real fix from a patch. Suppose
// an engine were "fixed" by exempting an update only once, or by clearing the
// create's row after the first update consumed it. T017_01 would go green while
// the mechanism stayed wrong, because the coincidence between the pending bytes
// and the disk recurs: after the first update lands, disk holds v2, and an
// engine still deriving an update's subject from disk records a pass on v2 and
// hands it to the offer of v3.
//
// Three writes, three distinct bodies, three judgements. Anything less means
// some update rode a verdict earned by content that is no longer what it holds.
func TestT017_02_EveryUpdateAfterACreateIsJudgedNotJustTheFirst(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "judge", judgeDecl, map[string]string{"judge.sh": judgeScript})

	got := e.Run(proj, "s-017-02", "create then change twice", Turns("done",
		Write("w1", "notes.md", "v1 benign"),
		Write("w2", "notes.md", "v2 also benign, different"),
		Write("w3", "notes.md", "v3 benign again, different still"),
	))

	// All three landed, so each offer really did face a disk holding its
	// predecessor's bytes — the arrangement the bypass needs.
	if n := strings.Count(got.Output, "File written successfully"); n != 3 {
		t.Fatalf("%d write(s) landed, want 3 — every offer must go through for the next one to "+
			"meet a disk holding the previous body:\n%s", n, got.Output)
	}

	lines := e.Ledger(proj, "judge", "log")
	if n := len(lines); n != 3 {
		t.Fatalf("the guardrail was asked %d time(s), want 3 — the bypass recurs on every update, "+
			"not only the first after a create: each landed write makes the disk match the pass "+
			"just recorded, so an engine keying an update's subject on disk skips them all. A fix "+
			"that exempted only one update would pass the create-then-update case and fail here. "+
			"Ledger: %v\n%s", n, lines, got.Output)
	}

	// And the three offers really were three different bodies. The disk each
	// hook saw is what the previous write left, so the recorded disks must
	// advance rather than repeat — if they did not, the file never moved and
	// the count above is right for a reason unrelated to the invariant.
	if len(lines) == 3 {
		if !strings.Contains(lines[1], "disk=[v1 benign]") {
			t.Fatalf("the second invocation saw %q, want the first write's bytes on disk — the "+
				"file did not move between offers, so the subjects did not coincide the way the "+
				"bypass needs and this test proves nothing: %v", lines[1], lines)
		}
		if !strings.Contains(lines[2], "disk=[v2 also benign, different]") {
			t.Fatalf("the third invocation saw %q, want the second write's bytes on disk: %v",
				lines[2], lines)
		}
	}
}

// T017_03: the same coincidence across a CYCLE boundary.
//
// T017_01 and T017_02 run inside one invocation of the agent, so a store that
// happened to be right only because a single process held its own decisions in
// memory would satisfy them. This spans two Runs of the SAME conversation: the
// create is judged and recorded in cycle 1, the process exits, and the update
// arrives in cycle 2 to a store that has to be reopened and read back.
//
// The claim is unchanged — an update is judged on its own account — but the
// path the verdict travels is the durable one. A pass written to the database
// and read back by a later process is exactly what the bypass rode.
func TestT017_03_ACreateInOneCycleDoesNotExemptAnUpdateInTheNext(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "judge", judgeDecl, map[string]string{"judge.sh": judgeScript})

	const sess = "s-017-03"

	// Cycle 1: the create alone. Its pass is recorded and the file lands.
	first := e.Run(proj, sess, "create the file", Turns("done",
		Write("w1", "notes.md", "v1 benign"),
	))
	if !first.Saw("File written successfully") {
		t.Fatalf("the create did not land, so no pass was recorded and the second cycle has "+
			"nothing to wrongly inherit:\n%s", first.Output)
	}
	if n := len(e.Ledger(proj, "judge", "log")); n != 1 {
		t.Fatalf("the guardrail was asked %d time(s) in the first cycle, want 1 — the create has "+
			"to be judged for its pass to be on record", n)
	}

	// Cycle 2: same conversation, a fresh process, and an update whose body the
	// earlier pass never covered.
	second := e.Run(proj, sess, "change it in the next cycle", Turns("done",
		Write("w2", "notes.md", "v2 quite different"),
	))

	lines := e.Ledger(proj, "judge", "log")
	if n := len(lines); n != 2 {
		t.Fatalf("the guardrail was asked %d time(s) in total, want 2 — the create's pass was "+
			"written to the session's record in one cycle and read back in the next, which is the "+
			"durable path the bypass rode. An update arriving a cycle later is still an update, "+
			"and is still judged on its own content. Ledger: %v\n%s", n, lines, second.Output)
	}
}

// T017_04: two guardrails, one of which passed the content and one of which
// never saw it.
//
// The bypass reached through the OTHER key of the record. A verdict is stored
// per (path, guardrail, content); pool it per (path, content) instead and a
// rule that has never judged a file inherits whatever another rule concluded
// about it. That is the failure that makes adding a guardrail dangerous — it
// would be installed, bound, enabled, and inert on everything the existing
// rules had already passed.
//
// Stated where a refusal is observable, which is what makes this the sharp
// version of the family:
//
//	cycle 1: only "blind" is installed. It judges a CREATE of benign content
//	         and passes. Nothing else has seen this file.
//	         The file is then removed, so the next offer is a create too.
//	cycle 2: "picky" is installed alongside it, and the SAME benign content is
//	         offered again — content picky would pass — followed by a create of
//	         SECRET content, which picky refuses and blind does not.
//
// The load-bearing assertion is the refusal. A count could be satisfied by an
// engine that asked picky and let the secret through anyway; the point is that
// the new rule's judgement actually governs.
func TestT017_04_AGuardrailThatNeverSawTheContentDoesNotInheritAnothersPass(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	proj := e.Project()
	// blind permits everything and records that it was asked.
	e.Guardrail(proj, "blind", judgeDecl, map[string]string{"judge.sh": permissiveScript})

	const sess = "s-017-04"

	// Cycle 1: blind alone settles the benign content. Removed afterwards so
	// the repeat offer is a create — an update's subject comes from disk and
	// would make cycle 2 a different comparison than intended.
	e.Run(proj, sess, "settle the content under one rule", Turns("done",
		Write("w1", "notes.md", "benign settled content"),
		Bash("b1", "rm -f notes.md"),
	))
	if n := len(e.Ledger(proj, "blind", "log")); n != 1 {
		t.Fatalf("blind was asked %d time(s) in the first cycle, want 1 — the content has to be "+
			"settled under blind alone for this test to mean anything", n)
	}

	// picky arrives mid-session, after the verdict was recorded. It has judged
	// nothing.
	e.Guardrail(proj, "picky", judgeDecl, map[string]string{"judge.sh": judgeScript})

	got := e.Run(proj, sess, "offer the settled content, then a secret", Turns("done",
		Write("w2", "notes.md", "benign settled content"),
		Bash("b2", "rm -f notes.md"),
		Write("w3", "notes.md", "SECRET=hunter2"),
	))

	// picky was asked about BOTH offers. It holds no verdict of its own on
	// either, so neither may be exempt — the pass on record belongs to blind.
	pickyLines := e.Ledger(proj, "picky", "log")
	if n := len(pickyLines); n != 2 {
		t.Fatalf("picky was asked %d time(s), want 2 — a rule that has never judged a file cannot "+
			"be exempted by another rule's verdict on it. An exemption here is picky inheriting "+
			"blind's pass, under which any guardrail added to a running session is inert on every "+
			"file the older rules already permitted. Ledger: %v\n%s", n, pickyLines, got.Output)
	}

	// And picky's judgement governs: the secret is refused, by the rule that
	// has an opinion about it. This is the half a count cannot reach.
	if !got.Saw("content holds a secret") {
		t.Fatalf("the secret-bearing write was never refused, though picky was asked %d time(s) — "+
			"being asked is not the same as governing, and the whole value of a new rule is that "+
			"its verdict decides.\nLedger: %v\n%s", len(pickyLines), pickyLines, got.Output)
	}

	// blind stays exempt on its own pass for the repeat of the benign content.
	// Without this the test would also pass on a build where nothing is ever
	// skipped, and picky running would say nothing about the per-rule grain.
	//
	// It is asked twice in total: once in cycle 1, and once for the SECRET
	// content in cycle 2, which it has never seen. The repeat of the benign
	// content is the one it must skip.
	blindLines := e.Ledger(proj, "blind", "log")
	if n := len(blindLines); n != 2 {
		t.Fatalf("blind was asked %d time(s) in total, want 2 — one for the content it settled in "+
			"cycle 1, and one for the SECRET content it has never seen. The repeat of its own "+
			"settled content must be skipped; if it was not, nothing is being skipped here at all "+
			"and picky running proves nothing about verdicts belonging to the rule that reached "+
			"them. Ledger: %v", n, blindLines)
	}
}

// T017_05: a file edited back to previously-refused content is refused again.
//
// The bypass run backwards. Forwards, a stale pass licenses new content;
// backwards, a fresh pass would erase an older refusal for content that is
// coming back:
//
//	SECRET=hunter2 -> refused. Nothing lands.
//	benign         -> judged, passes.
//	SECRET=hunter2 -> the ORIGINAL refused content, offered again.
//
// An engine keying its verdict on the PATH rather than on the content holds one
// row per file, the benign pass is that row, and the return to the failing
// content rides it. Same shape as the shipped bug, reached from the other
// direction.
//
// Every offer is a creation — the benign write that lands is removed — because
// a refused write never lands and a create is the kind that carries its pending
// bytes where the hook can see them. That is what makes the REFUSAL, not merely
// the invocation, observable.
func TestT017_05_ContentEditedBackToARefusedBodyIsRefusedAgain(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "judge", judgeDecl, map[string]string{"judge.sh": judgeScript})

	got := e.Run(proj, "s-017-05", "bad, good, bad again", Turns("done",
		Write("w1", "notes.md", "SECRET=hunter2"),
		Write("w2", "notes.md", "benign"),
		Bash("b1", "rm -f notes.md"),
		Write("w3", "notes.md", "SECRET=hunter2"),
	))

	lines := e.Ledger(proj, "judge", "log")
	if n := len(lines); n != 3 {
		t.Fatalf("the guardrail was asked %d time(s), want 3 — the first offer is refused, the "+
			"second passes, and the return to the refused content must be judged in its own "+
			"right. Ledger: %v\n%s", n, lines, got.Output)
	}

	// Two refusals: the first offer and the return to it. One refusal means the
	// third offer rode the benign pass — a verdict keyed on the path rather
	// than on the content, and the violation came back unnoticed.
	if n := strings.Count(got.Output, "content holds a secret"); n != 2 {
		t.Fatalf("the guardrail refused %d time(s), want 2 — content that was refused, fixed, and "+
			"then restored must be refused again. One refusal here means the pass earned by the "+
			"benign content in between was read as the FILE's verdict rather than as that "+
			"content's.\nLedger: %v\n%s", n, lines, got.Output)
	}

	// The benign write really passed, or the two refusals above could be the
	// first two offers with the third never judged at all.
	if !got.Saw("File written successfully") {
		t.Fatalf("no write ever landed, so the benign offer never passed and the sequence under "+
			"test did not happen:\n%s", got.Output)
	}
}

// T017_06: a file whose content is identical to another file's is judged on its
// own account.
//
// A subject is a (path, content) pair, and this is the test for the path half.
// Key an exemption on the fingerprint alone — a plausible simplification, since
// the fingerprint is the thing that "identifies the content" — and the second
// file rides the first file's verdict. Two files are then interchangeable to
// every rule the moment their bodies agree, which is common: a template copied
// into two places, an empty file, a generated header.
//
// Stated where it is sharp: the first file's content passes, and the same bytes
// are offered at a DIFFERENT path. That second offer must be judged. Then the
// reverse case — identical bytes that were REFUSED at one path must not silence
// the judgement at another, which is the same claim with the verdict flipped.
func TestT017_06_TwoFilesWithIdenticalContentAreJudgedSeparately(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "judge", judgeDecl, map[string]string{"judge.sh": judgeScript})

	got := e.Run(proj, "s-017-06", "the same bytes at three paths", Turns("done",
		Write("w1", "one.md", "identical content"),
		Write("w2", "two.md", "identical content"),
		Write("w3", "three.md", "identical content"),
	))

	lines := e.Ledger(proj, "judge", "log")
	if n := len(lines); n != 3 {
		t.Fatalf("the guardrail was asked %d time(s), want 3 — a verdict is about a file's "+
			"content AT A PATH, not about content anywhere. Keyed on the fingerprint alone, the "+
			"second and third files would ride the first's pass, and any two files would become "+
			"interchangeable to every rule the moment their bodies agreed. Ledger: %v\n%s",
			n, lines, got.Output)
	}

	// Each invocation names a different path, so the three really were three
	// distinct subjects rather than one file offered three times.
	for _, want := range []string{"path=[one.md]", "path=[two.md]", "path=[three.md]"} {
		found := false
		for _, l := range lines {
			if strings.Contains(l, want) {
				found = true
			}
		}
		if !found {
			t.Fatalf("no invocation was asked about %s — the three offers were not the three "+
				"distinct paths this test needs. Ledger: %v", want, lines)
		}
	}
}

// T017_07: identical bytes REFUSED at one path are still refused at another.
//
// The mirror of T017_06, and the one where the cost is a lost refusal rather
// than a lost check. The same secret is written to two paths. Both must be
// refused: the refusal recorded for the first names that path, and the second
// file has no verdict at all.
//
// This also rules out a subtler shape than fingerprint-only keying — a store
// that recorded refusals per CONTENT (reasonable-sounding: "these bytes are
// bad, wherever they are") would refuse both and pass this test, so what makes
// it non-vacuous is T017_06 alongside it: the pass case proves the key is not
// content-only, and this proves the refusal is not lost by the path being new.
func TestT017_07_ARefusalAtOnePathDoesNotSilenceJudgementAtAnother(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "judge", judgeDecl, map[string]string{"judge.sh": judgeScript})

	got := e.Run(proj, "s-017-07", "the same secret at two paths", Turns("done",
		Write("w1", "one.md", "SECRET=hunter2"),
		Write("w2", "two.md", "SECRET=hunter2"),
	))

	lines := e.Ledger(proj, "judge", "log")
	if n := len(lines); n != 2 {
		t.Fatalf("the guardrail was asked %d time(s), want 2 — the second file has no verdict of "+
			"its own and must be judged. Ledger: %v\n%s", n, lines, got.Output)
	}
	if n := strings.Count(got.Output, "content holds a secret"); n != 2 {
		t.Fatalf("the guardrail refused %d time(s), want 2 — a refusal recorded against one path "+
			"is about that path, and the same bytes arriving somewhere else are unjudged rather "+
			"than settled.\nLedger: %v\n%s", n, lines, got.Output)
	}
}

// T017_08: a delete records no pass, so the create that follows it is judged.
//
// The rule says a subject exists only where the fingerprint names what the
// action WOULD LEAVE, and a delete leaves nothing. The dangerous version of
// getting this wrong is specific: fingerprint the DOOMED file on the Pre side
// and the exemption is keyed on exactly the content the action exists to
// remove. Then
//
//	create "benign"  -> passes. Pass recorded on fingerprint(benign).
//	delete           -> if this recorded a pass keyed on the disk it is about
//	                    to destroy, that is a SECOND pass on fingerprint(benign)
//	                    — or worse, the delete's own pass becomes the row.
//	create "benign"  -> matches, skipped.
//
// The last step is legitimately skipped here (same path, same content, a real
// earlier pass), so the delete cannot be caught that way. What catches it is
// asking whether the guardrail was invoked for the DELETE at all, and whether a
// create of DIFFERENT content after a delete is judged — which it must be,
// since nothing about a removal can license bytes nobody has seen.
//
// This is the neighbour the bypass rule decides without anyone having enumerated
// it, which is the test of whether the rule is a rule or a patch.
func TestT017_08_ADeleteLicensesNothingForTheCreateThatFollows(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "judge", judgeDecl, map[string]string{"judge.sh": judgeScript})

	// The removal is done through Bash rather than a file tool, because what is
	// under test is that nothing recorded around a removal licenses the write
	// that follows it. The create afterwards carries DIFFERENT bytes from the
	// one before, so no legitimate exemption can cover it — and it carries a
	// secret, so the failure is a refusal that went missing rather than a count
	// that came up short.
	got := e.Run(proj, "s-017-08", "create, remove, create something else", Turns("done",
		Write("w1", "notes.md", "benign original"),
		Bash("b1", "rm -f notes.md"),
		Write("w2", "notes.md", "SECRET=hunter2"),
	))

	lines := e.Ledger(proj, "judge", "log")
	if n := len(lines); n != 2 {
		t.Fatalf("the guardrail was asked %d time(s), want 2 — a removal leaves no bytes, so "+
			"nothing about it can license the content of a later write. Ledger: %v\n%s",
			n, lines, got.Output)
	}
	if !got.Saw("content holds a secret") {
		t.Fatalf("the secret-bearing create after a removal was never refused. Nothing a delete "+
			"does may stand in for a judgement of the bytes a later create leaves behind.\n"+
			"Ledger: %v\n%s", lines, got.Output)
	}
}

// permissiveScript records that it was asked and permits everything.
//
// Used for the rule that must NOT have an opinion, so that a refusal observed
// in T017_04 can only have come from the rule that does.
const permissiveScript = `#!/bin/sh
payload=$(cat)
path=$(printf '%s' "$payload" | sed -n 's/.*"path":"\([^"]*\)".*/\1/p')
echo "asked path=[$path]" >> "$PWD/log"
exit 0
`
