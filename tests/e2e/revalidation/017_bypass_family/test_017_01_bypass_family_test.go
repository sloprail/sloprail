package e2e

import (
	"fmt"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The refusal bypass, and its whole family.
//
// # The bug this package is built around
//
// A create records its pass on the PENDING bytes — the only place a creation's
// content can be seen, since the file is not there yet. On the OLD Pre dispatch the
// next update took its subject from DISK, which by then held exactly those bytes.
// So the two derivations coincided the instant the create landed, and the first
// update after any successful create matched the create's stored pass and was
// skipped REGARDLESS OF WHAT IT CONTAINED.
//
// The fix is stated as a rule about what a subject IS: it exists only where the
// fingerprint names what the action WOULD LEAVE. PreFileCreate has one (pending
// bytes on the event); PreFileUpdate has none (a path and no content); a delete has
// none.
//
// # RE-VEHICLED onto the NEW file-guard nature (was old GUARDRAIL.md hooks)
//
// The invariant this family defends is that a verdict is keyed on CONTENT AT A
// PATH, so stale content never licenses new content and a refusal is never lost to
// a pass for other bytes. The file-guard's after-check drives the SAME
// (path, guardrail, fingerprint) store (nature_fileguard.go's runFileGuardsPost),
// so every claim here is reached identically. What changes is HOW the bytes reach
// the subject: on the after-check the subject is ALWAYS the settled file on disk,
// for a create and an update alike, so the specific Pre-path coincidence that
// produced the bug cannot arise — the disk genuinely moves between two successive
// writes of different content, and the second is correctly re-judged. The family
// therefore reads here as "different content is judged on its own account; a
// refusal survives a pass for other bytes or another path" — the invariant, reached
// through the after-check rather than the Pre subject. The exact transformation is
// in tests/e2e/REVEHICLE-PATTERN.md.
//
// Two consequences for the shape of the fixtures:
//
//   - Successive offers against ONE path are driven across CYCLES, not within one
//     cycle: two writes to a path in a single cycle collapse to one net Post event,
//     so the second offer is a later cycle's write, which is also the durable path
//     the bug rode (a verdict written to the store and read back by a later
//     process).
//   - A refusal is a Stop block whose reason names the path (read with
//     e.BlockingErrorsFrom(…, "Stop")); a not-fine cycle is a permanent block, so
//     SetStopBlockCap(1) bounds the mock's Stop retries and keeps re-fire counts one
//     per cycle. The check records to `.sloprail/file-guard/<name>/log` via
//     $SR_GUARDRAIL_DIR, read with e.FileGuardLedgerLines. `match: "**/*.md"`
//     selects the written files and never a guard's own `log`.

// judgeGuard is a REAL judge: a `.md` file is not fine if its settled content holds
// SECRET. It records the path it was asked about (T017_06 turns on two files being
// judged separately) and refuses with a per-path reason.
const judgeGuard = `match: "**/*.md"
checks:
  - script: ./judge.sh
`

const judgeScript = `#!/bin/sh
payload=$(cat)
path=$(printf '%s' "$payload" | sed -n 's/.*"path":"\([^"]*\)".*/\1/p')
case "$path" in
  .sloprail/*) exit 0 ;;
esac
echo "asked path=[$path]" >> "$SR_GUARDRAIL_DIR/log"
root="${SR_GUARDRAIL_DIR%/.sloprail/file-guard/*}"
if [ -n "$path" ] && [ -f "$root/$path" ] && grep -q SECRET "$root/$path"; then
  printf '{"reason":"content of %s holds a secret"}\n' "$path"
  exit 1
fi
exit 0
`

// permissiveScript records the path and permits everything — for the rule that
// must NOT have an opinion, so a refusal observed in T017_04 can only have come
// from the rule that does.
const permissiveScript = `#!/bin/sh
payload=$(cat)
path=$(printf '%s' "$payload" | sed -n 's/.*"path":"\([^"]*\)".*/\1/p')
case "$path" in
  .sloprail/*) exit 0 ;;
esac
echo "asked path=[$path]" >> "$SR_GUARDRAIL_DIR/log"
exit 0
`

func asks(lines []string, path string) int {
	n := 0
	for _, l := range lines {
		if strings.Contains(l, "path=["+path+"]") {
			n++
		}
	}
	return n
}

func refusedPath(t *testing.T, e *harness.Env, proj, sess, path string) bool {
	t.Helper()
	for _, b := range e.BlockingErrorsFrom(proj, sess, "Stop") {
		if strings.Contains(b, "content of "+path+" holds a secret") {
			return true
		}
	}
	return false
}

// T017_01: a pass for one content does not exempt DIFFERENT content at the same
// path.
//
// The original bug's invariant. On the old Pre dispatch a create's pass was keyed
// on the pending bytes and the next update's subject came from disk holding exactly
// those bytes, so the update rode the pass whatever it contained. On the after-check
// the subject is the settled file, which genuinely moves between the two writes — so
// the second content is correctly re-judged, and this pins that a stale pass
// licenses nothing.
//
// Two cycles, two different bodies against one path. The second must be judged.
func TestT017_01_APassDoesNotExemptDifferentContentAtTheSamePath(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "judge", judgeGuard, map[string]string{"judge.sh": judgeScript})

	const sess = "s-017-01"

	e.Run(proj, sess, "write v1", Turns("done", Write("w1", "notes.md", "v1 benign")))
	if n := asks(e.FileGuardLedgerLines(proj, "judge", "log"), "notes.md"); n != 1 {
		t.Fatalf("the first content was judged %d time(s), want 1 — its pass has to be on record", n)
	}

	seen := len(e.FileGuardLedgerLines(proj, "judge", "log"))
	e.Run(proj, sess, "change it", Turns("done", Write("w2", "notes.md", "v2 quite different")))
	if n := asks(e.FileGuardLedgerLines(proj, "judge", "log")[seen:], "notes.md"); n == 0 {
		t.Fatalf("the changed content was not re-judged — a stored pass keyed on the previous bytes " +
			"licenses nothing here, or an edit inherits the verdict its predecessor earned")
	}
}

// T017_02: EVERY change is judged, not just the first after a settle.
//
// The bug's tail. On the old dispatch a fix that exempted an update only once would
// pass a create-then-update case and fail here, because the coincidence recurs. On
// the after-check the disk advances with each write, so three distinct bodies across
// three cycles are three distinct subjects and three judgements.
func TestT017_02_EveryChangeIsJudgedNotJustTheFirst(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "judge", judgeGuard, map[string]string{"judge.sh": judgeScript})

	const sess = "s-017-02"
	bodies := []string{"v1 benign", "v2 also benign, different", "v3 benign again, different still"}

	seen := 0
	for i, body := range bodies {
		e.Run(proj, sess, fmt.Sprintf("write %d", i+1), Turns("done",
			Write(fmt.Sprintf("w%d", i), "notes.md", body)))
		lines := e.FileGuardLedgerLines(proj, "judge", "log")
		if n := asks(lines[seen:], "notes.md"); n == 0 {
			t.Fatalf("write %d (%q) was not judged (delta %v) — every change is judged on its own "+
				"content, not skipped on a verdict earned by content the file no longer holds",
				i+1, body, lines[seen:])
		}
		seen = len(lines)
	}
}

// T017_03: a settle in one cycle does not exempt a change in the next.
//
// The claim across a CYCLE boundary, where the verdict travels the durable path: a
// pass written to the database in cycle 1 and read back by a fresh process in cycle
// 2. A verdict written to the store and read back is exactly what the bug rode.
func TestT017_03_ASettleInOneCycleDoesNotExemptAChangeInTheNext(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "judge", judgeGuard, map[string]string{"judge.sh": judgeScript})

	const sess = "s-017-03"

	e.Run(proj, sess, "settle in cycle 1", Turns("done", Write("w1", "notes.md", "v1 benign")))
	if n := asks(e.FileGuardLedgerLines(proj, "judge", "log"), "notes.md"); n != 1 {
		t.Fatalf("the file was judged %d time(s) in the first cycle, want 1 — its pass has to be on "+
			"record for the second cycle to have something to wrongly inherit", n)
	}

	seen := len(e.FileGuardLedgerLines(proj, "judge", "log"))
	e.Run(proj, sess, "change it in cycle 2", Turns("done", Write("w2", "notes.md", "v2 quite different")))
	if n := asks(e.FileGuardLedgerLines(proj, "judge", "log")[seen:], "notes.md"); n == 0 {
		t.Fatalf("a change arriving a cycle later was not judged (delta) — the pass was written to " +
			"the session's record in one cycle and read back in the next, which is the durable path " +
			"the bypass rode; a later change is still judged on its own content")
	}
}

// T017_04: two guardrails, one of which passed the content and one of which never
// saw it.
//
// The bypass reached through the guardrail key of the record. A verdict is stored
// per (path, guardrail, content); pool it per (path, content) and a rule that has
// never judged a file inherits whatever another rule concluded. That is what makes
// adding a guardrail dangerous — installed, bound, enabled, inert on everything the
// older rules had already passed.
//
// blind settles benign content in cycle 1. picky arrives in cycle 2 and the SAME
// benign content is re-written (picky must judge it — it holds no verdict), then
// SECRET content (picky must refuse it; blind does not). The load-bearing assertion
// is the refusal: the new rule's judgement actually governs.
func TestT017_04_AGuardrailThatNeverSawTheContentDoesNotInheritAnothersPass(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	// picky refuses the SECRET write — a permanent block — so bound the retries.
	e.SetStopBlockCap(1)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "blind", judgeGuard, map[string]string{"judge.sh": permissiveScript})

	const sess = "s-017-04"

	e.Run(proj, sess, "settle under one rule", Turns("done",
		Write("w1", "notes.md", "benign settled content")))
	if n := asks(e.FileGuardLedgerLines(proj, "blind", "log"), "notes.md"); n != 1 {
		t.Fatalf("blind was asked %d time(s) in the first cycle about notes.md, want 1 — the "+
			"content has to be settled under blind alone for this test to mean anything", n)
	}

	// picky arrives mid-session, after the verdict was recorded. It has judged nothing.
	e.FileGuard(proj, "picky", judgeGuard, map[string]string{"judge.sh": judgeScript})

	// The settled benign content, re-written. picky holds no verdict on it and must
	// judge it.
	seenPicky := 0
	e.Run(proj, sess, "re-offer the settled content", Turns("done",
		Write("w2", "notes.md", "benign settled content")))
	pickyLines := e.FileGuardLedgerLines(proj, "picky", "log")
	if n := asks(pickyLines[seenPicky:], "notes.md"); n == 0 {
		t.Fatalf("picky was not asked about content blind had settled — a rule that has never judged " +
			"a file cannot be exempted by another rule's verdict on it. An exemption here is picky " +
			"inheriting blind's pass, under which any guardrail added to a running session is inert " +
			"on every file the older rules already permitted")
	}
	seenPicky = len(pickyLines)

	// Now SECRET content. picky refuses it; blind does not.
	e.Run(proj, sess, "offer a secret", Turns("done",
		Write("w3", "secret.md", "SECRET=hunter2")))
	if n := asks(e.FileGuardLedgerLines(proj, "picky", "log"), "secret.md"); n == 0 {
		t.Fatalf("picky did not judge the secret content")
	}
	// picky's judgement governs: the secret is refused, by the rule that has an
	// opinion about it. This is the half a count cannot reach.
	if !refusedPath(t, e, proj, sess, "secret.md") {
		t.Fatalf("the secret-bearing write was never refused, though picky was asked about it — " +
			"being asked is not the same as governing, and the whole value of a new rule is that its " +
			"verdict decides")
	}
}

// T017_05: content edited back to a previously-refused body is refused again.
//
// The bypass run backwards. Forwards, a stale pass licenses new content; backwards,
// a fresh pass would erase an older refusal for content that is coming back:
//
//	cycle 1  SECRET=hunter2 -> refused, a fail recorded on those bytes.
//	cycle 2  benign         -> passes.
//	cycle 3  SECRET=hunter2 -> the ORIGINAL refused content, offered again.
//
// The third cycle must be refused. An engine keying its verdict on the PATH rather
// than the content holds one row per file, the benign pass is that row, and the
// return to the failing content rides it.
func TestT017_05_ContentEditedBackToARefusedBodyIsRefusedAgain(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	e.SetStopBlockCap(1)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "judge", judgeGuard, map[string]string{"judge.sh": judgeScript})

	const sess = "s-017-05"

	e.Run(proj, sess, "bad", Turns("done", Write("w1", "notes.md", "SECRET=hunter2")))
	if !refusedPath(t, e, proj, sess, "notes.md") {
		t.Fatalf("the first offer was not refused, so there is no refusal to be run backwards")
	}

	e.Run(proj, sess, "good", Turns("done", Write("w2", "notes.md", "benign")))

	seen := len(e.FileGuardLedgerLines(proj, "judge", "log"))
	e.Run(proj, sess, "bad again", Turns("done", Write("w3", "notes.md", "SECRET=hunter2")))
	if n := asks(e.FileGuardLedgerLines(proj, "judge", "log")[seen:], "notes.md"); n == 0 {
		t.Fatalf("the return to the refused content was not judged — content refused, fixed, and " +
			"then restored must be judged in its own right")
	}
	if !refusedPath(t, e, proj, sess, "notes.md") {
		t.Fatalf("content that was refused, fixed, and then restored was not refused again — the " +
			"pass earned by the benign content in between was read as the FILE's verdict rather than " +
			"as that content's")
	}
}

// T017_06: a file whose content is identical to another file's is judged on its own
// account.
//
// A subject is a (path, content) pair, and this is the test for the path half. Key
// an exemption on the fingerprint alone and the second file rides the first's
// verdict, making any two files interchangeable the moment their bodies agree.
//
// The same bytes at three DIFFERENT paths, in one cycle — three distinct paths do
// not collapse. Each must be judged.
func TestT017_06_TwoFilesWithIdenticalContentAreJudgedSeparately(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "judge", judgeGuard, map[string]string{"judge.sh": judgeScript})

	e.Run(proj, "s-017-06", "the same bytes at three paths", Turns("done",
		Write("w1", "one.md", "identical content"),
		Write("w2", "two.md", "identical content"),
		Write("w3", "three.md", "identical content"),
	))

	lines := e.FileGuardLedgerLines(proj, "judge", "log")
	for _, p := range []string{"one.md", "two.md", "three.md"} {
		if n := asks(lines, p); n == 0 {
			t.Fatalf("%s was not judged (%v) — a verdict is about content AT A PATH, not about "+
				"content anywhere. Keyed on the fingerprint alone, the second and third files would "+
				"ride the first's pass, and any two files would become interchangeable the moment "+
				"their bodies agreed", p, lines)
		}
	}
}

// T017_07: identical bytes REFUSED at one path are still refused at another.
//
// The mirror of T017_06, where the cost is a lost refusal rather than a lost check.
// The same secret at two paths; both must be refused. This also rules out a store
// that recorded refusals per CONTENT — reasonable-sounding, and wrong: T017_06
// proves the key is not content-only, and this proves the refusal is not lost by the
// path being new.
func TestT017_07_ARefusalAtOnePathDoesNotSilenceJudgementAtAnother(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	e.SetStopBlockCap(1)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "judge", judgeGuard, map[string]string{"judge.sh": judgeScript})

	const sess = "s-017-07"
	e.Run(proj, sess, "the same secret at two paths", Turns("done",
		Write("w1", "one.md", "SECRET=hunter2"),
		Write("w2", "two.md", "SECRET=hunter2"),
	))

	lines := e.FileGuardLedgerLines(proj, "judge", "log")
	for _, p := range []string{"one.md", "two.md"} {
		if n := asks(lines, p); n == 0 {
			t.Fatalf("%s was not judged (%v) — the second file has no verdict of its own and must be "+
				"judged", p, lines)
		}
		if !refusedPath(t, e, proj, sess, p) {
			t.Fatalf("%s was not refused — a refusal recorded against one path is about that path, "+
				"and the same bytes arriving somewhere else are unjudged rather than settled", p)
		}
	}
}

// T017_08: a delete records no pass, so the create that follows it is judged.
//
// The rule says a subject exists only where the fingerprint names what the action
// WOULD LEAVE, and a delete leaves nothing. The dangerous version of getting this
// wrong is fingerprinting the DOOMED file and keying an exemption on exactly the
// content the action exists to remove.
//
// create benign (passes), delete it, create DIFFERENT content that holds a secret.
// Nothing about the removal can license bytes nobody has seen, so the secret create
// must be judged and refused.
func TestT017_08_ADeleteLicensesNothingForTheCreateThatFollows(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	e.SetStopBlockCap(1)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "judge", judgeGuard, map[string]string{"judge.sh": judgeScript})

	const sess = "s-017-08"
	// The removal is done through Bash, and the create afterwards carries DIFFERENT
	// bytes holding a secret — so no legitimate exemption can cover it and the failure
	// is a refusal that went missing.
	e.Run(proj, sess, "create, remove, create something else", Turns("done",
		Write("w1", "notes.md", "benign original"),
		Bash("b1", "rm -f notes.md"),
		Write("w2", "notes.md", "SECRET=hunter2"),
	))

	if n := asks(e.FileGuardLedgerLines(proj, "judge", "log"), "notes.md"); n == 0 {
		t.Fatalf("the file was never judged — a removal leaves no bytes, so nothing about it can " +
			"license the content of a later write")
	}
	if !refusedPath(t, e, proj, sess, "notes.md") {
		t.Fatalf("the secret-bearing create after a removal was never refused. Nothing a delete does " +
			"may stand in for a judgement of the bytes a later create leaves behind")
	}
}
