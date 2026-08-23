package e2e

import (
	"fmt"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// exemption_needs_pass: a file is exempt from a guardrail's check only when a
// check exists whose fingerprint equals the file's current one AND whose
// verdict was a pass.
//
// Both halves are load-bearing, and each is tested by removing one of them:
//
//   - T013_01 keeps the content identical and makes the verdict a REFUSAL. The
//     content half holds, the verdict half does not, so the check must run
//     again — a refusal read as settled work is a violation gone quiet.
//   - T013_02 keeps the verdict a pass and CHANGES the content. The verdict half
//     holds, the content half does not, so the check must run again — an edited
//     file inheriting its predecessor's verdict is content nobody judged.
//   - T013_03 is the positive case, and it is what makes the two above mean
//     anything: identical content that really did pass IS skipped. Without it,
//     both could pass on a build where the exemption never fires at all.
//   - T013_05 and T013_06 are the refusal bypass, reached where content is
//     genuinely on the settled file.
//
// # RE-VEHICLED onto the NEW file-guard nature (was old GUARDRAIL.md hooks)
//
// The exemption is the SHARED revalidation store: Skippable answers true only for a
// (path, guardrail, fingerprint) row whose verdict was a pass, and the file-guard's
// after-check drives that exact store (nature_fileguard.go's runFileGuardsPost calls
// the same rev.Subject / rev.Skip / rev.Record the old dispatch did). So the two
// halves of the exemption — content matches AND verdict was a pass — are observed
// here through the after-check. The exact transformation is in
// tests/e2e/REVEHICLE-PATTERN.md.
//
// On the after-check the subject is ALWAYS the settled file's fingerprint (for a
// create and an update alike), so successive offers against one path are driven
// across CYCLES rather than within one cycle (two writes to a path in a single
// cycle collapse to one net Post event). "the check was asked" is each guard's own
// ledger (e.FileGuardLedgerLines, written via $SR_GUARDRAIL_DIR); "the refusal
// reached the agent" is e.BlockingErrorsFrom(…, "Stop") with a per-path reason; a
// not-fine cycle is a permanent block, so SetStopBlockCap(1) bounds the mock's Stop
// retries. `match: "**/*.md"` selects the written files without matching a guard's
// own `log`.
//
// # T013_04 has no after-check equivalent, and why that is not a coverage loss
//
// The old T013_04 pinned the REFUSAL BYPASS on the PreFileUpdate path directly: it
// seeded a file, made it READ-ONLY so two successive writes both FAILED on their own
// (leaving the disk still between them), and asserted the Pre-tool hook was asked
// twice about the unchanged disk — the shape in which PreFileUpdate carried a path
// and NO pending content, so its subject came from a disk that did not move and the
// second pending payload rode the first's stored pass.
//
// That observation is OLD-DISPATCH-ONLY and cannot be re-vehicled onto the
// file-guard's after-check: a write that never lands produces no Post file event, so
// the after-check never fires on it — there is nothing to seed read-only, nothing to
// count. And the mechanism it isolated does not exist on the after-check at all: the
// subject there is always the settled file on disk, which for two successive writes
// of different content genuinely moves, so a stale pass can never license new
// content by the coincidence the old bug turned on.
//
// The INVARIANT T013_04 defended — a second, different pending payload against a
// file is judged on its own account and never rides an earlier pass — is preserved
// and reached through the after-check by T013_05 and T013_06 below (and, at the
// family level, by tests/e2e/revalidation/017_bypass_family). So the coverage
// survives; only the Pre-path-specific fixture, which measured a mechanism the new
// dispatch does not have, does not.

// judgeGuard is a REAL judge: a `.md` file is not fine if its settled content holds
// SECRET. It records the path it was asked about and refuses with a per-path reason.
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

// asks counts, over a slice of ledger lines, how many name the given path.
func asks(lines []string, path string) int {
	n := 0
	for _, l := range lines {
		if strings.Contains(l, "path=["+path+"]") {
			n++
		}
	}
	return n
}

// refusedPath reports whether a Stop-blocking refusal naming the path reached the
// conversation.
func refusedPath(t *testing.T, e *harness.Env, proj, sess, path string) bool {
	t.Helper()
	for _, b := range e.BlockingErrorsFrom(proj, sess, "Stop") {
		if strings.Contains(b, "content of "+path+" holds a secret") {
			return true
		}
	}
	return false
}

// T013_01: content matches a stored check, but that check was a REFUSAL.
//
// The fingerprint half of the exemption is satisfied — the content is byte-identical
// across cycles — so a store applying only that half would skip, and the violation
// would go quiet. It must not: the verdict half fails, so the not-fine file re-fires
// on every cycle it is not fixed.
//
// Cycle 1 writes the failing content (refused). A later cycle of unrelated work must
// re-judge the still-not-fine file — the fingerprint is unchanged, but the stored
// verdict is a refusal, which never licenses a skip.
func TestT013_01_MatchingContentWithARefusedVerdictIsNotExempt(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	e.SetStopBlockCap(1)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "judge", judgeGuard, map[string]string{"judge.sh": judgeScript})

	const sess = "s-013-01"

	seen := 0
	e.Run(proj, sess, "write a secret", Turns("done", Write("w1", "notes.md", "SECRET=hunter2")))
	first := e.FileGuardLedgerLines(proj, "judge", "log")
	if asks(first[seen:], "notes.md") == 0 {
		t.Fatalf("the failing content was never judged in the first cycle: %v", first)
	}
	seen = len(first)
	if !refusedPath(t, e, proj, sess, "notes.md") {
		t.Fatalf("the failing content was not refused, so there is no stored refusal to test")
	}

	// A later cycle of unrelated work. The still-not-fine file has an UNCHANGED
	// fingerprint but a REFUSED verdict, so it must be judged again.
	e.Run(proj, sess, "unrelated work", Turns("done", Write("u2", "unrelated.md", "fine")))
	after := e.FileGuardLedgerLines(proj, "judge", "log")
	if n := asks(after[seen:], "notes.md"); n == 0 {
		t.Fatalf("identical content whose stored verdict was a REFUSAL was skipped (delta %v) — the "+
			"content half of the exemption is satisfied, but the verdict half is not, so the check "+
			"must run again or the violation goes quiet", after[seen:])
	}
	if !refusedPath(t, e, proj, sess, "notes.md") {
		t.Fatalf("the re-fired failing content was not refused again")
	}
}

// T013_02: the verdict was a pass, but the content has CHANGED.
//
// The first write passes and is recorded. The second write offers different content
// against the same path. The verdict half of the exemption is satisfied — this
// guardrail did pass this file — so a store applying only that half would skip, and
// the new content would inherit its predecessor's verdict.
//
// It must not. On the after-check the subject is the settled disk, which now holds
// the new bytes, so the fingerprint differs and the check runs on the new content.
func TestT013_02_APassingVerdictOnDifferentContentIsNotExempt(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "judge", judgeGuard, map[string]string{"judge.sh": judgeScript})

	const sess = "s-013-02"

	e.Run(proj, sess, "write, then change it", Turns("done", Write("w1", "notes.md", "benign one")))
	if n := asks(e.FileGuardLedgerLines(proj, "judge", "log"), "notes.md"); n != 1 {
		t.Fatalf("the first content was judged %d time(s), want 1 — its pass has to be on record", n)
	}

	seen := len(e.FileGuardLedgerLines(proj, "judge", "log"))
	e.Run(proj, sess, "change the content", Turns("done",
		Write("w2", "notes.md", "benign two, quite different")))
	if n := asks(e.FileGuardLedgerLines(proj, "judge", "log")[seen:], "notes.md"); n == 0 {
		t.Fatalf("a file whose content changed was not judged again (delta) — an edit must not " +
			"inherit the verdict its predecessor earned")
	}
}

// T013_03: content that passed and did not change IS exempt.
//
// The positive half, and the one that makes this file's negatives mean something.
// Without it T013_01 and T013_02 would both pass on a build where nothing is ever
// skipped.
//
// The same fine bytes are written in cycle 1 (judged, passes) and rewritten
// unchanged in later cycles: the settled fingerprint matches the recorded pass, so
// the check must not be asked again. The unrelated file each later cycle is the
// control — it IS judged, so the silence about the settled file is a real skip
// rather than a check that stopped firing.
func TestT013_03_UnchangedPassingContentIsExempt(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "judge", judgeGuard, map[string]string{"judge.sh": judgeScript})

	const sess = "s-013-03"

	seen := 0
	e.Run(proj, sess, "write fine content", Turns("done",
		Write("w1", "settled.md", "benign and unchanging")))
	first := e.FileGuardLedgerLines(proj, "judge", "log")
	if asks(first[seen:], "settled.md") == 0 {
		t.Fatalf("the file was never judged in the first cycle: %v — there is no pass for the skip "+
			"to be about", first)
	}
	seen = len(first)

	for cycle := 2; cycle <= 3; cycle++ {
		moving := fmt.Sprintf("moving%d.md", cycle)
		e.Run(proj, sess, "rewrite unchanged, plus unrelated", Turns("done",
			Write(fmt.Sprintf("s%d", cycle), "settled.md", "benign and unchanging"),
			Write(fmt.Sprintf("u%d", cycle), moving, "cycle content"),
		))
		lines := e.FileGuardLedgerLines(proj, "judge", "log")
		delta := lines[seen:]
		// The control: this cycle judged the file it actually changed.
		if !strings.Contains(strings.Join(delta, "\n"), "path=["+moving+"]") {
			t.Fatalf("cycle %d judged nothing it changed (delta %v) — the silence about the settled "+
				"file below would then prove nothing", cycle, delta)
		}
		if n := asks(delta, "settled.md"); n > 0 {
			t.Fatalf("cycle %d re-judged content this guardrail had already permitted, unchanged "+
				"(%d times, delta %v) — a passing verdict on unchanged content must end the "+
				"re-judging, or a second look can block work the agent has already done", cycle, n, delta)
		}
		seen = len(lines)
	}
}

// T013_05: a second, different payload is refused, not riding the first's pass.
//
// The refusal bypass reached where content is genuinely on the settled file. A
// benign create settles in cycle 1; a later cycle writes SECRET content to the same
// path. The subject the engine keys on differs between them — the settled disk moved
// — so the second must be judged and refused. An engine keying the subject on
// anything that did not move between the two offers would skip it, and the secret
// would land unjudged.
//
// This is the assertion that would have caught the reported bug, and the one that
// fails loudly rather than counting.
func TestT013_05_ASecondDifferentPayloadIsRefusedNotRidingTheFirstsPass(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	e.SetStopBlockCap(1)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "judge", judgeGuard, map[string]string{"judge.sh": judgeScript})

	const sess = "s-013-05"

	e.Run(proj, sess, "settle benign content", Turns("done", Write("w1", "notes.md", "benign")))
	if n := asks(e.FileGuardLedgerLines(proj, "judge", "log"), "notes.md"); n != 1 {
		t.Fatalf("the benign content was judged %d time(s), want 1 — its pass has to be on record", n)
	}

	seen := len(e.FileGuardLedgerLines(proj, "judge", "log"))
	e.Run(proj, sess, "then a secret", Turns("done", Write("w2", "notes.md", "SECRET=hunter2")))
	if n := asks(e.FileGuardLedgerLines(proj, "judge", "log")[seen:], "notes.md"); n == 0 {
		t.Fatalf("the second, different content was not judged (delta) — it must be judged in its " +
			"own right, not skipped on the pass recorded for the benign content before it")
	}
	if !refusedPath(t, e, proj, sess, "notes.md") {
		t.Fatalf("the secret-bearing write was never refused — the payload reached the file " +
			"unjudged because its subject matched the pass stored for the benign content that " +
			"preceded it")
	}
}

// T013_06: a settle's pass does not exempt the change that follows it.
//
// The bypass reached by the sequence a session actually produces: benign content
// settled, then different content written. On the old Pre dispatch the create's pass
// was keyed on the pending bytes and the following update took its subject from a
// disk holding exactly those bytes, so the update rode the pass whatever it
// contained. On the after-check the subject is the settled disk, which moves to the
// new bytes — so the change is correctly judged on its own account.
//
// Two cycles, two different bodies against one path; the second must be judged.
func TestT013_06_ASettlesPassDoesNotExemptTheChangeThatFollows(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "judge", judgeGuard, map[string]string{"judge.sh": judgeScript})

	const sess = "s-013-06"

	e.Run(proj, sess, "settle v1", Turns("done", Write("w1", "notes.md", "v1 benign")))
	if n := asks(e.FileGuardLedgerLines(proj, "judge", "log"), "notes.md"); n != 1 {
		t.Fatalf("the settle was judged %d time(s), want 1 — its pass has to be on record when the "+
			"change arrives", n)
	}

	seen := len(e.FileGuardLedgerLines(proj, "judge", "log"))
	e.Run(proj, sess, "change it", Turns("done", Write("w2", "notes.md", "v2 quite different")))
	if n := asks(e.FileGuardLedgerLines(proj, "judge", "log")[seen:], "notes.md"); n == 0 {
		t.Fatalf("the change following a settle was not judged (delta) — a pass earned by the old " +
			"content must not license the new")
	}
}
