package e2e

import (
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// state_survives_refork: the checks a session holds are found again under the
// same conversation after the harness changes the session id it reports.
//
// A harness may change that id for a conversation that is still going — on a
// retried request, after compacting, on a restart. State found by the reported
// id would follow the change into an empty directory and abandon every verdict
// recorded so far: silently, mid-session, leaving the agent re-judged on work
// it had already fixed.
//
// # What a fork is, and why it has to be built here
//
// The transcript is written by harness.Fork, in the restart shape (a new file
// opening on its own record that names one in the old file as its logical
// parent); the fork shapes current Claude Code leaves are driven through the
// mock in session/054_identity_continuations. Nothing about
// its shape is invented for convenience — it is what the identity walk looks
// for, and what a10n measured across real transcripts:
//
//   - the new transcript's root record is parentless, exactly like any other
//     transcript's first record;
//   - it carries logicalParentUuid naming a record in the OLD transcript, which
//     is what marks it a continuation rather than a new conversation;
//   - the record it names is really in the old file, which Fork asserts.
//
// So the conversation's identity is the OLD root's uuid, reached in one hop,
// while the id the harness reports is the new one. A store keyed on the
// reported id opens an empty database; one keyed on the origin finds the
// verdicts already recorded. That difference is the whole invariant.
//
// # RE-VEHICLED onto the NEW file-guard nature (was old GUARDRAIL.md hooks)
//
// The identity the verdict store is keyed by is SHARED machinery: openRevalidation
// resolves the CONVERSATION's id (the way `session id` does, not the harness's
// reported id), and the file-guard's after-check opens that same store through the
// same path. So a verdict recorded before a re-fork is found after it, or not, by
// the same code the old dispatch used — re-vehicling onto e.FileGuard observes the
// SAME survival through the NEW dispatch. The exact transformation is in
// tests/e2e/REVEHICLE-PATTERN.md.
//
// The after-check fingerprints the settled file on disk, so "settle then re-offer
// the same content" is just writing the same bytes twice: the second write's
// fingerprint matches the pass recorded before the fork, and the check is skipped.
// The ledger moves to `.sloprail/file-guard/<name>/log` (read with
// e.FileGuardLedgerLines) and each guard writes it via $SR_GUARDRAIL_DIR.
//
// # Why the negative control matters more here than anywhere else
//
// "State survived" is observed as a check NOT running — and on a broken build
// the check runs, which is the same thing an un-forked second cycle looks like
// if nothing was ever recorded. So T016_01 is paired with T016_02, which forks
// to an UNRELATED conversation and requires the check to run again. Without the
// pair, a build that never skips anything would pass the first test by
// accident, and a build that skips everything would pass it for the wrong
// reason.

const watcherGuard = `match: "**/*.md"
checks:
  - script: ./watch.sh
`

const watcherScript = `#!/bin/sh
cat >/dev/null
echo "asked" >> "$SR_GUARDRAIL_DIR/log"
exit 0
`

// T016_01: a verdict recorded before a re-fork is still in force after it.
//
// The file is settled under one session id. The harness then re-forks: a new
// id, a new transcript, and that transcript continues the first. The same
// content is offered again under the NEW id.
//
// It must be skipped. The conversation is the same one, its record is where it
// was, and the verdict recorded before the fork still applies. A check running
// here is the engine having followed the fork into an empty directory.
func TestT016_01_AVerdictSurvivesTheHarnessChangingTheSessionID(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "watcher", watcherGuard, map[string]string{"watch.sh": watcherScript})

	const before = "s-016-01-before"
	const after = "s-016-01-after"

	// Settle the content. On the after-check the subject is the settled file's
	// fingerprint, so re-writing the same bytes after the fork yields the same
	// subject and the pass recorded before it exempts the write.
	e.Run(proj, before, "settle the file", Turns("done",
		Write("w1", "notes.md", "settled content"),
	))
	if n := len(e.FileGuardLedgerLines(proj, "watcher", "log")); n != 1 {
		t.Fatalf("the guardrail was asked %d time(s) before the fork, want 1 — the content has "+
			"to be settled for this test to mean anything", n)
	}

	// The harness re-forks: same conversation, new reported id.
	e.Fork(proj, before, after)

	e.Run(proj, after, "rewrite the same content", Turns("done",
		Write("w2", "notes.md", "settled content"),
	))

	if n := len(e.FileGuardLedgerLines(proj, "watcher", "log")); n != 1 {
		t.Fatalf("the guardrail was asked %d time(s) in total, want 1 — the verdict recorded "+
			"before the re-fork must still be in force after it. A second invocation means the "+
			"engine keyed its state on the id the harness reports, followed the fork into an "+
			"empty database, and abandoned every verdict recorded so far mid-conversation", n)
	}
}

// T016_02: the negative control. An UNRELATED conversation does not inherit
// this one's verdicts.
//
// Same content, same path, same project — but a session whose transcript is a
// root in its own right, continuing nothing. Its identity is its own, so it has
// no record here and the check must run.
//
// This is what makes T016_01 mean "the fork was followed" rather than "nothing
// is ever judged twice". Together they pin the identity to the CONVERSATION:
// one changes the reported id and keeps the state, the other changes the
// conversation and loses it. A build that resolved every session to the same
// identity would pass T016_01 and fail here.
func TestT016_02_AnUnrelatedSessionDoesNotInheritTheVerdicts(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "watcher", watcherGuard, map[string]string{"watch.sh": watcherScript})

	e.Run(proj, "s-016-02-first", "settle the file", Turns("done",
		Write("w1", "notes.md", "settled content"),
	))
	if n := len(e.FileGuardLedgerLines(proj, "watcher", "log")); n != 1 {
		t.Fatalf("the guardrail was asked %d time(s) in the first session, want 1", n)
	}

	// No Fork: a plain second session, seeded as its own root by Run. It
	// continues nothing, so it is a different conversation.
	e.Run(proj, "s-016-02-second", "rewrite the same content", Turns("done",
		Write("w2", "notes.md", "settled content"),
	))

	if n := len(e.FileGuardLedgerLines(proj, "watcher", "log")); n != 2 {
		t.Fatalf("the guardrail was asked %d time(s) in total, want 2 — a session that "+
			"continues nothing is a different conversation and holds no verdicts from this "+
			"one. State keyed per session is the whole reason for that: a session may hold its "+
			"own worktree, so one session's passing check must not stand in for another's", n)
	}
}

// T016_03: the fork really is a fork, and not merely a second session that
// happens to work.
//
// T016_01 would pass on a build that resolved every session in a project to one
// identity — the state would survive, but for the wrong reason, and T016_02 is
// what rules that out. This pins the remaining assumption from the other side:
// that the transcript Fork wrote is genuinely resolved by the identity walk to
// the ORIGIN of the conversation it continues, rather than to itself.
//
// Asked of the engine, through the same command every hook uses. If the walk
// stopped at the forked file's own root, the two ids would differ and the
// verdicts would not have been reachable in T016_01 at all.
//
// This test installs no guardrail — it reads the engine's own identity resolution
// (SessionIdentity), which is format-neutral — so it is unchanged by the
// re-vehicling.
func TestT016_03_TheForkedTranscriptResolvesToTheOriginalConversation(t *testing.T) {
	e := New(t)
	proj := e.Project()

	const before = "s-016-03-before"
	const after = "s-016-03-after"

	e.Run(proj, before, "start the conversation", Turns("done",
		Write("w1", "notes.md", "anything"),
	))
	e.Fork(proj, before, after)

	original := e.SessionIdentity(proj, before)
	forked := e.SessionIdentity(proj, after)

	if original == "" {
		t.Fatalf("the original session resolved to no identity at all")
	}
	if forked != original {
		t.Fatalf("the forked transcript resolves to %q, want the conversation's own origin "+
			"(%q) — a fork that resolved to itself would key its state in a new, empty place, "+
			"which is the silent mid-session loss this invariant exists to prevent",
			forked, original)
	}

	// And the identity really was REACHED ACROSS a hop, rather than the forked
	// transcript happening to have the same root record written into it. The
	// fork's own root is a distinct record — Fork names it "e2e-fork-<id>" —
	// so an engine resolving the forked file to its own root would answer that
	// instead, and the equality above would have failed. Asserting the answer
	// is the ORIGINAL file's root, by name, is what distinguishes "crossed the
	// fork" from "the two files are identical".
	//
	// A constant-vs-constant check on the two session ids would not do this
	// job: they differ by construction and such a check could never fire.
	if want := e.OriginRecord(proj, before); original != want || want == "" {
		t.Fatalf("the original session resolves to %q, want its transcript's origin record %q — if the "+
			"origin is not the record this test thinks it is, the equality above compared two "+
			"values that could coincide for reasons unrelated to the walk", original, want)
	}
}
