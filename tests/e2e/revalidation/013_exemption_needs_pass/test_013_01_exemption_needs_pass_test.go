package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// exemption_needs_pass: a file is exempt from a guardrail's hook only when a
// check exists whose fingerprint equals the file's current one AND whose
// verdict was a pass.
//
// Both halves are load-bearing, and each is tested by removing one of them:
//
//   - T013_01 keeps the content identical and makes the verdict a REFUSAL. The
//     content half holds, the verdict half does not, so the hook must run
//     again — a refusal read as settled work is a violation gone quiet.
//   - T013_02 keeps the verdict a pass and CHANGES the content. The verdict
//     half holds, the content half does not, so the hook must run again — an
//     edited file inheriting its predecessor's verdict is content nobody
//     judged.
//   - T013_03 is the positive case, and it is what makes the two above mean
//     anything: identical content that really did pass IS skipped. Without it,
//     both could pass on a build where the exemption never fires at all.
//   - T013_04 is the refusal bypass, and is the reason this file exists.
//
// Every test here observes the hook's own ledger rather than the refusal
// travelling back, because "did not run" leaves no trace in the stream: a hook
// that stayed silent and a hook that never ran look identical from outside. A
// line per invocation is the one channel that tells them apart.

// judgeDecl binds a judging hook to the two pre-file kinds a write goes
// through: the first write to a path creates, every later one updates.
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

// judgeScript is the rule under test, and it is a REAL judge: it reads what it
// is handed and decides. It records that it was asked BEFORE it decides, so a
// refusal and a pass are both visible as invocations — a refusal that left no
// ledger line would be indistinguishable from a hook that never ran.
//
// It judges the pending content when the event carries it, which for a creation
// is the bytes that would land. On an update the kind carries a path and no
// content, so it reads the file — which is what an ordinary rule would do, and
// is the situation the bypass arises in. The workspace comes off guardrailDir
// in the payload rather than from an environment variable: the payload is the
// one channel that carries it on every branch here, and a rule reaching for a
// variable that is not set would silently judge nothing and pass everything,
// which would make T013_04 green for the wrong reason.
//
// It also records WHAT it judged, not merely that it was asked. A count alone
// cannot tell "asked twice about two different bodies" from "asked twice about
// the same one", and T013_04 turns on the file being unchanged between the two
// writes — a premise that has to be observed rather than assumed.
const judgeScript = `#!/bin/sh
payload=$(cat)
path=$(printf '%s' "$payload" | sed -n 's/.*"path":"\([^"]*\)".*/\1/p')
ws=$(printf '%s' "$payload" | sed -n 's|.*"guardrailDir":"\(.*\)/\.sloprail/guardrails/.*|\1|p')
echo "asked disk=[$(cat "$ws/$path" 2>/dev/null)]" >> "$PWD/log"
body=$(printf '%s' "$payload" | grep -o '"newContent":"[^"]*"' || true)
if [ -z "$body" ]; then
  body=$(cat "$ws/$path" 2>/dev/null || true)
fi
case "$body" in
  *SECRET*) echo "content holds a secret" >&2; exit 2 ;;
esac
exit 0
`

// asks counts what the ledger recorded: one line per time the guardrail's hook
// was actually invoked.
func asks(t *testing.T, lines []string) int {
	t.Helper()
	return len(lines)
}

// T013_01: content matches a stored check, but that check was a REFUSAL.
//
// The file is written with failing content, refused, and then the agent offers
// the SAME failing content again. The fingerprint half of the exemption is
// satisfied — the content is byte-identical — so a store applying only that
// half would skip, and the violation would go quiet after one cycle.
//
// It must not. The verdict half fails, the hook runs again, and the refusal
// stands.
func TestT013_01_MatchingContentWithARefusedVerdictIsNotExempt(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "judge", judgeDecl, map[string]string{"judge.sh": judgeScript})

	// Both writes offer the same failing bytes. The first is refused, so the
	// file never lands; the second offers it again.
	got := e.Run(proj, "s-013-01", "write a secret twice", Turns("done",
		Write("w1", "notes.md", "SECRET=hunter2"),
		Write("w2", "notes.md", "SECRET=hunter2"),
	))

	lines := e.Ledger(proj, "judge", "log")
	if n := asks(t, lines); n != 2 {
		t.Fatalf("the guardrail was asked %d time(s), want 2 — identical content whose stored "+
			"verdict was a REFUSAL must not be exempt, or the violation goes quiet after one "+
			"cycle. Ledger: %v", n, lines)
	}
	// And the refusal really did travel back both times, so what was counted
	// above was two judgements and not two silent passes.
	if n := strings.Count(got.Output, "content holds a secret"); n != 2 {
		t.Fatalf("the refusal reached the agent %d time(s), want 2:\n%s", n, got.Output)
	}
}

// T013_02: the verdict was a pass, but the content has CHANGED.
//
// The first write passes and is recorded. The second write offers different
// content against the same path. The verdict half of the exemption is
// satisfied — this guardrail did pass this file — so a store applying only that
// half would skip, and the new content would inherit its predecessor's verdict.
//
// It must not. The content half fails, and the hook runs on the new bytes.
func TestT013_02_APassingVerdictOnDifferentContentIsNotExempt(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "judge", judgeDecl, map[string]string{"judge.sh": judgeScript})

	got := e.Run(proj, "s-013-02", "write, then change it", Turns("done",
		Write("w1", "notes.md", "benign one"),
		Write("w2", "notes.md", "benign two, quite different"),
	))

	lines := e.Ledger(proj, "judge", "log")
	if n := asks(t, lines); n != 2 {
		t.Fatalf("the guardrail was asked %d time(s), want 2 — a file whose content changed "+
			"must be judged again, or an edit inherits the verdict its predecessor earned. "+
			"Ledger: %v", n, lines)
	}
	// Both writes landed. The premise is that the file's content really changed
	// between the two offers — a refusal would have stopped the first from
	// landing, the second would have been judged against different bytes than
	// intended, and the count above would be right for the wrong reason.
	if n := strings.Count(got.Output, "File written successfully"); n != 2 {
		t.Fatalf("%d write(s) landed, want 2 — both offers must go through for the content to "+
			"have actually changed between them:\n%s", n, got.Output)
	}
}

// T013_03: content that passed and did not change IS exempt.
//
// The positive half, and the one that makes this file's negatives mean
// something. Without it T013_01 and T013_02 would both pass on a build where
// nothing is ever skipped — which is precisely the state this branch is in, and
// precisely why the control gates them.
//
// The same bytes are offered twice. The first offer is judged and passes; the
// second offer is the same content this guardrail has already permitted, so it
// must not be asked again. Re-asking is not merely waste: a judge hook is a
// model call rather than a function, so a second look can refuse work the agent
// has already done and can no longer reach.
//
// A creation is what is repeated, because PreFileCreate is the kind that
// carries the pending bytes on the event — which is what lets the engine
// fingerprint what the write would LEAVE. See T013_04 for why an update
// cannot.
func TestT013_03_UnchangedPassingContentIsExempt(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "judge", judgeDecl, map[string]string{"judge.sh": judgeScript})

	// Two creations of the same content at two different paths would be two
	// different subjects. So: create, delete, create the same bytes again —
	// both are PreFileCreate, both carry the same pending content, and the
	// path is the same, so the second is the identical subject the first
	// passed.
	got := e.Run(proj, "s-013-03", "create, remove, create the same again", Turns("done",
		Write("w1", "notes.md", "benign and unchanging"),
		Bash("b1", "rm -f notes.md"),
		Write("w2", "notes.md", "benign and unchanging"),
	))

	// The first offer landed, or there was no pass to be exempt from and the
	// count below would be 1 because the second offer was never judged either.
	// Exactly one write lands: the second is the one under test and must be
	// skipped, but skipping the HOOK does not stop the write, so both land.
	if n := strings.Count(got.Output, "File written successfully"); n != 2 {
		t.Fatalf("%d write(s) landed, want 2 — both offers must go through, or the second was "+
			"not the repeat of a passed creation that this test needs:\n%s", n, got.Output)
	}
	lines := e.Ledger(proj, "judge", "log")
	if n := asks(t, lines); n != 1 {
		t.Fatalf("the guardrail was asked %d time(s), want 1 — content this guardrail has "+
			"already permitted, unchanged, must not be re-judged; a judge is a model call and "+
			"a second look can block work the agent already fixed. Ledger: %v", n, lines)
	}
}

// T013_04: two different pending payloads against one unchanged file must BOTH
// be judged. The refusal bypass.
//
// This is the bug a review found in this feature, and the reason an e2e for it
// could not be left deleted:
//
//	write "benign"          (disk holds v1) -> judged, passes, pass recorded
//	write "SECRET=hunter2"  (disk holds v1) -> subject matches the stored pass
//	                                        -> SKIPPED, never judged
//
// On PreFileUpdate the kind carries a path and no content — an Edit supplies a
// patch, not a body. An engine that fingerprints what is ON DISK is therefore
// fingerprinting the content the write would REPLACE, not what it would leave,
// and that does not move between two successive offers against an unchanged
// file. Two different pending payloads produce one identical subject, the
// second matches the first's stored pass, and the malicious payload is skipped
// without ever being judged. runs=1, no deny.
//
// The test drives that sequence with real successive writes. What it asserts is
// the thing that actually distinguishes the two designs:
//
//   - the guardrail is asked about BOTH payloads, and
//   - the second one is REFUSED.
//
// The refusal is the load-bearing half. A count alone could be satisfied by an
// engine that asked twice and let the secret through anyway, and the whole
// point is that the payload is judged, not merely seen.
//
// Note what makes the setup honest: the file must already EXIST before the two
// writes, so both of them are updates rather than a create followed by an
// update. A create carries its content and has never had this bug — running
// the sequence that way would assert nothing and pass on the buggy build.
//
// Which means the SEEDING command must not itself produce a create carrying
// content. That is a real constraint now rather than a free one: the engine
// derives a redirection's resulting bytes wherever the line determines them,
// so `printf 'v1' > notes.md` is a content-carrying create and defeats the
// setup. See the comment at the Bash turn for the spelling that seeds without
// one, and why.
func TestT013_04_TwoPendingPayloadsAgainstAnUnchangedFileAreBothJudged(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "judge", judgeDecl, map[string]string{"judge.sh": judgeScript})

	// The file must not move between the two writes. If it does, their subjects
	// differ for a reason that has nothing to do with the invariant, no skip
	// occurs, and the test passes on the buggy build while proving nothing.
	//
	// What holds it still is the FILESYSTEM: b0 seeds the file and makes it
	// read-only, so both writes are attempted and both fail on their own. That
	// matters more than convenience —
	//
	//   - A refusal from the guardrail under test cannot do it: a stored
	//     refusal fails the passing half of the exemption by itself, so that
	//     rule would correctly re-run and the skip could never fire.
	//   - A refusal from a SECOND guardrail cannot do it either. The dispatch
	//     returns at the first refusal (session_pre_tool.go: `return deny(...)`
	//     exits the whole loop), so whichever rule refuses first, the other is
	//     never asked on that event at all. Which rule that is depends on load
	//     order, and cross-guardrail order is not something the spec promises —
	//     `order_within_binding` is about hooks inside ONE binding.
	//     internal/guardrail/store.go sorts declarations by name today, but a
	//     test resting on that rests on an implementation detail.
	//
	// So no guardrail refuses here. One rule, which passes every time, and a
	// file the writes cannot change. It records a pass against the disk's
	// fingerprint on the first write; on the second — whose pending body is
	// entirely different — that same unchanged disk yields the same subject,
	// and the pass is read as a licence to skip.
	//
	// b0 seeds without producing a PreFileCreate that carries content, so both
	// writes are updates. A create carries its content and has never had this
	// bug, so seeding that way would assert nothing and pass on the buggy build.
	//
	// The command substitution is load-bearing and is not decoration. The
	// engine now derives the resulting content of a redirection whenever the
	// LINE determines it, so the obvious spelling — `printf 'v1' > notes.md` —
	// produces a create carrying "v1", the hook runs before the file exists,
	// and the first invocation records `disk=[]` instead of `disk=[v1]`. That
	// is the engine working as intended; it is this test's premise that has to
	// move.
	//
	// `"$(echo v1)"` is not a literal word, so the content is genuinely
	// underivable — commandmod declines to resolve it rather than assuming an
	// environment, which is the same rule that refuses `> $OUT`. Measured:
	// FileTargets returns PayloadNone for this line and PayloadLiteral for the
	// bare `printf 'v1'`. The shell still writes exactly `v1`, so what the
	// writes are held against is unchanged.
	got := e.Run(proj, "s-013-04", "two payloads against one unchanged file", Turns("done",
		Bash("b0", `printf '%s' "$(echo v1)" > notes.md && chmod 444 notes.md`),
		Write("w1", "notes.md", "first pending body"),
		Write("w2", "notes.md", "second pending body, quite different"),
	))

	// The setup really did what it claims. Both writes were attempted and both
	// failed, so the file still holds what b0 put there — without this the test
	// could be counting invocations against a disk that moved underneath them,
	// which is the one thing that would make the count meaningless.
	if n := strings.Count(got.Output, "permission denied"); n != 2 {
		t.Fatalf("want both writes to have failed on the read-only file, keeping its content "+
			"still between them; %d did:\n%s", n, got.Output)
	}

	lines := e.Ledger(proj, "judge", "log")
	// Every line records what the hook saw on disk. They must all agree, or the
	// file moved after all and the premise of the test is gone.
	for i, l := range lines {
		if !strings.Contains(l, "disk=[v1]") {
			t.Fatalf("invocation %d judged %q, want the unchanged v1 — the file moved between "+
				"the writes, so their subjects differed for a reason unrelated to the "+
				"invariant: %v", i, l, lines)
		}
	}
	if n := asks(t, lines); n != 2 {
		t.Fatalf("the guardrail was asked %d time(s), want 2. REFUSAL BYPASS: on "+
			"PreFileUpdate the subject is fingerprinted from what is ON DISK rather than from "+
			"what the write would leave, so two different pending payloads against an "+
			"unchanged file produce one identical subject — and the second rides the pass the "+
			"first recorded, without ever being judged. Ledger: %v\n%s", n, lines, got.Output)
	}
}

// T013_06: a create's pass must not exempt the update that follows it.
//
// The same bypass reached by the sequence a session actually produces, and the
// one that fires without needing a refusal to hold the disk still:
//
//	Write "v1"  -> a CREATE. Its subject is the PENDING bytes, judged, pass
//	               recorded under fingerprint(v1). The write lands.
//	Write "v2"  -> an UPDATE. Its subject is taken from DISK, which now holds
//	               v1 — the very content the create's pass was recorded under.
//	               The row matches, the verdict was a pass, and v2 is skipped.
//
// So the first update after any successful create is always exempt, whatever it
// contains. The two derivations coincide the instant the create lands, and the
// pass earned by the old content licenses the new.
//
// The assertion is the count, because on this engine an update-bound hook
// cannot see the payload to refuse it (see T013_05). Two writes, two different
// bodies, two judgements — and the second one is the one that goes missing.
func TestT013_06_ACreatesPassDoesNotExemptTheUpdateThatFollows(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "judge", judgeDecl, map[string]string{"judge.sh": judgeScript})

	got := e.Run(proj, "s-013-06", "create then change", Turns("done",
		Write("w1", "notes.md", "v1 benign"),
		Write("w2", "notes.md", "v2 quite different"),
	))

	lines := e.Ledger(proj, "judge", "log")
	if n := asks(t, lines); n != 2 {
		t.Fatalf("the guardrail was asked %d time(s), want 2. The create recorded its pass "+
			"under the fingerprint of the PENDING bytes; the update that followed took its "+
			"subject from DISK, which by then held exactly those bytes — so the update matched "+
			"the create's pass and was skipped without its own content ever being judged. "+
			"Ledger: %v\n%s", n, lines, got.Output)
	}
	// The create landed, or there was no pass for the update to inherit and the
	// count above would mean something else entirely.
	if n := strings.Count(got.Output, "File written successfully"); n != 2 {
		t.Fatalf("%d write(s) landed, want 2 — the create must go through for its pass to be "+
			"on record when the update arrives:\n%s", n, got.Output)
	}
}

// T013_05: the same bypass, asserted on the half that a guardrail can actually
// see — and therefore the half where "judged" means judged rather than merely
// asked.
//
// T013_04 counts invocations, which is the most an update can be held to on
// this engine: PreFileUpdate declares a path and NO content (see
// filemod.Kinds), so a hook bound to it cannot read the payload it is being
// asked to permit, and reading the file gets the bytes the write would
// REPLACE. A guardrail on the update path is therefore structurally unable to
// refuse a pending payload no matter how the skip behaves — reported, not
// fixed here.
//
// So this states the same claim where content IS on the event. Two creations of
// the same path, the second carrying a secret, with the file removed in between
// so both are creations. The subject the engine keys on differs between them —
// it is the pending bytes — so the second must be judged and refused. An engine
// keying the subject on anything that did not move between the two offers (the
// path alone, the empty disk, the pass just recorded) would skip it, and the
// secret would land unjudged.
//
// This is the assertion that would have caught the reported bug had the update
// path been able to carry it, and it is the one that fails loudly rather than
// counting.
func TestT013_05_ASecondDifferentPayloadIsRefusedNotRidingTheFirstsPass(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "judge", judgeDecl, map[string]string{"judge.sh": judgeScript})

	got := e.Run(proj, "s-013-05", "benign, remove, then payload", Turns("done",
		Write("w1", "notes.md", "benign"),
		Bash("b1", "rm -f notes.md"),
		Write("w2", "notes.md", "SECRET=hunter2"),
	))

	lines := e.Ledger(proj, "judge", "log")
	if n := asks(t, lines); n != 2 {
		t.Fatalf("the guardrail was asked %d time(s), want 2 — the second offer is different "+
			"content and must be judged in its own right. Ledger: %v", n, lines)
	}
	if !got.Saw("content holds a secret") {
		t.Fatalf("the secret-bearing write was never refused, though the guardrail was asked "+
			"%d time(s). The payload reached the file unjudged because its subject matched the "+
			"pass stored for the benign content that preceded it.\nLedger: %v\n%s",
			asks(t, lines), lines, got.Output)
	}
}
