package e2e

import (
	"encoding/json"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The revalidation triple on the POST path, where a fingerprint comes off DISK.
//
// 013 and 017 cover this family on the PRE path, and they cannot cover it here.
// The two branches of revalidation.Subject are different code:
//
//   - PreFileCreate fingerprints the pending bytes carried ON THE EVENT, because
//     the file is not there yet and that is the only place a hook could see it.
//   - PostFileCreate and PostFileUpdate fingerprint the file ON DISK, via
//     fingerprint.OfFile — an observation of a cycle that has already settled.
//
// So every claim about what a Post verdict is keyed on has to be made against
// the disk-reading branch. That branch has a failure the Pre branch cannot have:
// it must RESOLVE the path first, and an unreadable or unresolvable path answers
// "no subject", which silently means no skip and — the half that matters — no
// record. A defect there does not surface as a wrong verdict; it surfaces as a
// session that never settles anything, forever.
//
// The three cases the spec's own reasoning names, each asserted here on the Post
// path:
//
//	same path, same fingerprint       -> skip        (exemption_needs_pass)
//	same path, different fingerprint  -> re-judge    (exemption_needs_pass)
//	different path, same content      -> judge       (identity_is_content)
//
// The third is the one a plausible simplification gets wrong: keying on the
// fingerprint alone is tempting, since the fingerprint is "what identifies the
// content", and it makes any two files interchangeable to every rule the moment
// their bodies agree.
//
// Each test drives several cycles under ONE session id, and every assertion
// reads the DELTA a cycle added to the ledger rather than a total. A total
// cannot tell "cycle two asked again" from "cycle one asked twice", which is
// exactly the confusion these tests exist to resolve.
//
// # RE-VEHICLED onto the NEW file-guard nature (was old GUARDRAIL.md hooks)
//
// The revalidation POST path is EXACTLY the file-guard's after-check: a
// non-preventive file-guard fires on the settled POST file event and records its
// verdict into the same revalidation store this directory measures — the same
// rev.Subject / rev.Skip / rev.Record the old Post dispatch drove (see
// services/sr-session/nature_fileguard.go's runFileGuardsPost). So the disk-read
// Subject branch, the (path, guardrail, fingerprint) skip key, and the
// judge-the-other-path rule are all reached identically; only the vehicle that
// installs the rule and the wire form its check reads have changed. The exact
// mechanical transformation is in tests/e2e/REVEHICLE-PATTERN.md.
//
// The three old Post kinds collapse into one after-check file-guard, which fires
// on whichever Post kind the change produced. `match: "**/*.md"` selects the same
// `.md` files the old per-kind bindings saw at any depth (the `**/` leading dir is
// optional) and — crucially — never matches the guard's own `seen` ledger (no
// `.md` suffix), so no self-observation doubles the ledger. The check records the
// FLAT CheckPayload the new format hands it (`.event.kind`, `.event.path`), never
// the old nested `.event.fields.*`, and its ledger is read with
// e.FileGuardLedgerLines from `.sloprail/file-guard/judge/seen`.

const bindPostFileEvents = `match: "**/*.md"
checks:
  - script: ./judge.sh
`

// judgeScript records and permits.
//
// Permitting is load-bearing: the exemption only applies to content a guardrail
// has judged AND passed, so a refusing fixture would keep every file eligible
// for re-judging and make every assertion below meaningless.
//
// The ledger is written to $SR_GUARDRAIL_DIR/seen — the folder the engine sets
// for a file-guard check (`.sloprail/file-guard/judge/`) — rather than the old
// hook's $PWD. Exit 0 permits, the same permit the old fixture gave; there is no
// refusal here, so nothing about the exit contract had to change.
const judgeScript = `#!/bin/sh
cat >> "$SR_GUARDRAIL_DIR/seen"
echo >> "$SR_GUARDRAIL_DIR/seen"
exit 0
`

type observed struct {
	Kind string
	Path string
}

// observedFiles parses the FLAT CheckPayload lines the check recorded. The
// event's own fields are spread directly under `event` (`.event.kind`,
// `.event.path`), NOT nested under an `event.fields` envelope the way the old
// format wrote them — so this reads Event.Kind and Event.Path directly.
func observedFiles(t *testing.T, lines []string) []observed {
	t.Helper()
	var got []observed
	for _, line := range lines {
		var p struct {
			Event struct {
				Kind string `json:"kind"`
				Path string `json:"path"`
			} `json:"event"`
		}
		if err := json.Unmarshal([]byte(line), &p); err != nil {
			t.Fatalf("hook was handed something that is not an event payload: %v\n%s", err, line)
		}
		got = append(got, observed{Kind: p.Event.Kind, Path: p.Event.Path})
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

func sawPath(got []observed, path string) bool { return countPath(got, path) > 0 }

// project is a repository with the judging rule committed, so the rule's own
// folder is part of the baseline rather than of every cycle's difference.
func project(t *testing.T) (*harness.Env, string) {
	t.Helper()
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "judge", bindPostFileEvents, map[string]string{"judge.sh": judgeScript})
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "the project before the session")
	return e, proj
}

// cycles drives a sequence of cycles under one session id and returns, per
// cycle, only the events THAT cycle added to the ledger.
func cycles(t *testing.T, e *harness.Env, proj, sess string, scenarios ...harness.Scenario) [][]observed {
	t.Helper()
	var out [][]observed
	seen := 0
	for i, s := range scenarios {
		e.Run(proj, sess, "cycle", s)
		lines := e.FileGuardLedgerLines(proj, "judge", "seen")
		if len(lines) < seen {
			t.Fatalf("cycle %d: the ledger shrank (%d lines, was %d)", i+1, len(lines), seen)
		}
		out = append(out, observedFiles(t, lines[seen:]))
		seen = len(lines)
	}
	return out
}

// T019_01: the three cases at once — skip, re-judge, and judge-the-other-path.
//
// One session, three cycles, and each cycle answers one of the three questions
// while the other two files hold still. Together in one test because the three
// only mean something as a set: an engine that never skips passes the second and
// third clauses alone, and one that keys on content alone passes the first two.
//
//	cycle 1  writes settled.md and twin-a.md          -> both judged
//	cycle 2  rewrites settled.md with NEW content     -> settled.md re-judged;
//	                                                     twin-a.md silent (skip)
//	cycle 3  writes twin-b.md with twin-a.md's BYTES  -> twin-b.md judged
//
// Every assertion is read from the slice of the cycle it is about, and each
// cycle's positive is asserted before its negative.
func TestT019_01_ThePostTripleSkipRejudgeAndTheOtherPath(t *testing.T) {
	e, proj := project(t)

	const twinBody = "bytes that will appear at two paths\n"

	got := cycles(t, e, proj, "s-019-01",
		Turns("done",
			Write("w1", "settled.md", "first version\n"),
			Write("w2", "twin-a.md", twinBody),
		),
		Turns("done", Write("w3", "settled.md", "second version\n")),
		Turns("done", Write("w4", "twin-b.md", twinBody)),
	)
	first, second, third := got[0], got[1], got[2]

	// Cycle one judged both, or nothing below has a verdict to rest on.
	if !sawPath(first, "settled.md") || !sawPath(first, "twin-a.md") {
		t.Fatalf("cycle one did not judge both files: %v — there is no stored pass for the "+
			"skip and re-judge cases to be about", first)
	}

	// SAME PATH, DIFFERENT FINGERPRINT -> re-judged. The positive for cycle two.
	if n := countPath(second, "settled.md"); n == 0 {
		t.Fatalf("content that CHANGED was not re-judged: %v\nthe stored pass was keyed on the "+
			"previous bytes, so it licenses nothing here — an exemption that survived an edit "+
			"would let a file be rewritten into a violation behind a verdict about other "+
			"content", second)
	}
	// SAME PATH, SAME FINGERPRINT -> skipped. Read from the same slice.
	if n := countPath(second, "twin-a.md"); n > 0 {
		t.Fatalf("content already judged and passed was asked about again (%d times) in a "+
			"later cycle: %v\nit is still in the difference — the session's baseline predates "+
			"it — so only the recorded verdict can keep it quiet, and re-asking is a fresh "+
			"model call about work the agent has moved on from", n, second)
	}

	// DIFFERENT PATH, SAME CONTENT -> judged. The positive for cycle three.
	if n := countPath(third, "twin-b.md"); n == 0 {
		t.Fatalf("the same bytes arriving at a NEW path were never judged there: %v\n"+
			"a verdict is about content AT A PATH; keyed on the fingerprint alone the new file "+
			"rides the old one's pass, and any two files become interchangeable to every rule "+
			"the moment their bodies agree", third)
	}
	// And the original twin stays quiet, so cycle three's judging is about the
	// new path rather than about the exemption having collapsed.
	if n := countPath(third, "twin-a.md"); n > 0 {
		t.Fatalf("the already-settled file was judged again (%d times) in the cycle that "+
			"introduced its twin: %v — the exemption is not holding, so the positive above "+
			"proves nothing about path-keying", n, third)
	}
}

// T019_02: a file edited back to bytes it was already passed on falls silent
// again.
//
// identity_is_content on the Post path, in the form that separates content from
// every other property a file has. The same path carries A, then B, then A
// again. The third cycle's bytes have already been judged and passed AT THIS
// PATH, so there is nothing new to ask.
//
// An engine deriving identity from a modification time, a revision counter, or
// "has it been written since we last looked" re-judges here, because all three
// of those did change. 020_identity_is_content makes this claim already; what
// is added here is the middle cycle as an explicit CONTROL in the same session —
// B must be judged — which is what stops the silence in cycle three from being
// an engine that stopped judging anything after cycle one.
func TestT019_02_ContentEditedAwayAndBackIsNotJudgedAgain(t *testing.T) {
	e, proj := project(t)

	const original = "the original bytes\n"

	got := cycles(t, e, proj, "s-019-02",
		Turns("done", Write("w1", "subject.md", original)),
		Turns("done", Write("w2", "subject.md", "different bytes\n")),
		Turns("done", Write("w3", "subject.md", original)),
	)
	first, second, third := got[0], got[1], got[2]

	if countPath(first, "subject.md") == 0 {
		t.Fatalf("the file was never judged at all: %v — nothing below can be a skip", first)
	}
	// The control: genuinely new content IS judged.
	if countPath(second, "subject.md") == 0 {
		t.Fatalf("changed content was not re-judged: %v — the hook is not running for new "+
			"content, so the silence asserted below would hold for the wrong reason", second)
	}
	if n := countPath(third, "subject.md"); n > 0 {
		t.Fatalf("content restored to bytes already judged and passed was judged again (%d "+
			"times): %v\nidentity is being derived from when the file was written rather than "+
			"from what it holds, so every revert re-opens a settled question", n, third)
	}
}

// T019_03: a file whose content is UNCHANGED but which was rewritten in place
// stays settled.
//
// The narrowest form of "and from nothing else about it". The agent writes the
// same bytes over the file — a formatter that changed nothing, a re-run of a
// generator — so the inode's modification time moves while the content does not.
// Nothing about the file that a verdict may depend on has changed, so the rule
// must not be asked again.
//
// This is distinct from T019_02, where the content genuinely differed in between.
// Here there is no intermediate state at all: an engine keyed on mtime, or on
// "the tool wrote to this path during the cycle", re-judges — and it does so on
// the most ordinary event in a long session.
//
// The control is a second file the same cycle really changes, read from the same
// slice.
func TestT019_03_RewritingAFileWithIdenticalBytesDoesNotReJudgeIt(t *testing.T) {
	e, proj := project(t)

	const body = "content that does not change\n"

	got := cycles(t, e, proj, "s-019-03",
		Turns("done", Write("w1", "steady.md", body)),
		Turns("done",
			// The same bytes written again, plus a genuine change elsewhere.
			Write("w2", "steady.md", body),
			Write("w3", "moving.md", "cycle two\n"),
		),
	)
	first, second := got[0], got[1]

	if countPath(first, "steady.md") == 0 {
		t.Fatalf("the file was never judged in cycle one: %v — nothing below can be a skip", first)
	}
	// The control: this cycle judged something.
	if !sawPath(second, "moving.md") {
		t.Fatalf("cycle two judged nothing at all: %v — the silence below proves nothing", second)
	}
	if n := countPath(second, "steady.md"); n > 0 {
		t.Fatalf("a file rewritten with byte-identical content was judged again (%d times): "+
			"%v\nthe fingerprint derives from content and from nothing else about the file, so "+
			"a write that changed no bytes changed no subject — re-asking here re-opens a "+
			"settled question on the most ordinary event in a long session", n, second)
	}
}
