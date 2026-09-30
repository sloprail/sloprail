package e2e

import (
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// TODO(D3): drive the verdict via a10n-claude-mock once a10n-cli#470's mock grows
// sr-agent's claude-flag surface for this path; today the proven InstallJudgeClaude
// stub supplies the model verdict (the same substitution T034_09/10 make).
//
// content-de-layering is a file-guard over files under updates/ or branding/. Its
// one check is a judge (no prepare, no script tier). The judge renders the settled
// file's own content and rules on whether it restates a fact that lives elsewhere.
//
// The scenarios prove: a duplicated-fact file BLOCKS at Stop and the judge's
// reasoning reaches the agent (violation); a clean referencing file ADMITS (happy);
// a file OUTSIDE updates//branding/ is never judged, even holding the same content
// (does-not-fire control, incl. the near-miss the match deliberately excludes);
// the settled file's content and path reach the rendered template (the
// event -> template wiring, this example's stand-in for prepare -> template since
// it has no prepare); and — the file-guard property — a not-fine file RE-FIRES
// each cycle until it is FIXED.

// duplicatedUpdate is an update file that spells out a person's standing detail
// inline instead of linking to their file — the exact "one fact, two homes"
// failure this rule catches. The fact string is distinctive so a test can find it
// in the rendered prompt.
const duplicatedUpdate = `# Weekly update

Reminder: Dana Per is the Head of Platform and prefers async standups.
We shipped the ingest cutover this week.
`

// cleanUpdate references the person file instead of restating the standing detail
// — the shape the rule admits.
const cleanUpdate = `# Weekly update

Progress from [Dana Per](../people/dana-per.md) on the ingest cutover this week.
`

// T043_01: a file that duplicates a fact BLOCKS at Stop, and the judge's reasoning
// reaches the agent.
//
// The write LANDS (a file-guard's Stop after-check cannot undo it), but
// the turn is blocked so the agent is sent round again with the judge's words. The
// stub returns pass:false with the reasoning the rule would give; the verify
// script refuses, sr-agent exits non-zero, the guard blocks at Stop.
func TestT043_01_DuplicatedFactBlocks(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj)
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "Dana Per's role is restated inline here; its home is the person file people/dana-per.md"}`)

	e.Run(proj, "s-043-01", "write a weekly update", Turns("done",
		Write("w1", "memories/updates/2026-08-18.md", duplicatedUpdate),
	))

	blocks := e.BlockingErrorsFrom(proj, "s-043-01", "Stop")
	if len(blocks) == 0 {
		t.Fatalf("a file duplicating a fact did not block the turn at Stop")
	}
	joined := ""
	for _, b := range blocks {
		joined += b + "\n"
	}
	if !containsStr(joined, "restated inline") {
		t.Errorf("the judge's reasoning did not reach the agent:\n%s", joined)
	}
	// The write landed — this is an after-check, not a pre-write refusal.
	if !e.Exists(proj, "memories/updates/2026-08-18.md") {
		t.Errorf("the after-check undid the write; a file-guard must not")
	}
}

// T043_02: a clean referencing file ADMITS — the turn ends, no block.
//
// The pass control for T043_01: without it a guard that blocked every update would
// pass T043_01 while being broken. Same path, same rule, opposite verdict; the only
// difference is the file's content, which is what the judge reads.
func TestT043_02_CleanReferenceAdmits(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	e.Run(proj, "s-043-02", "write a clean update", Turns("done",
		Write("w1", "memories/updates/2026-08-18.md", cleanUpdate),
	))

	if blocks := e.BlockingErrorsFrom(proj, "s-043-02", "Stop"); len(blocks) != 0 {
		t.Errorf("a clean referencing update was blocked anyway:\n%v", blocks)
	}
}

// T043_03: a file OUTSIDE updates//branding/ is never judged, even holding the very
// content the rule refuses.
//
// The does-not-fire control, and the one that matters most: the judge is stubbed to
// FAIL, so if the guard fired on this path the turn would block. It does not,
// because the path is neither under updates/ nor branding/. The near-miss
// `myupdates/…` — which the match's `contains "/updates/"`/`startsWith "updates/"`
// deliberately excludes — is covered too, so a guard that matched on a bare
// substring would be caught.
func TestT043_03_OutsideMatchIsNeverJudged(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj)
	// FAIL verdict: any firing would block.
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "would block if it fired"}`)

	e.Run(proj, "s-043-03", "write outside the guarded areas", Turns("done",
		// A plain memory, not under updates/ or branding/.
		Write("w1", "memories/notes/scratch.md", duplicatedUpdate),
		// A near-miss the match must NOT treat as an updates/ segment.
		Write("w2", "myupdates/note.md", duplicatedUpdate),
	))

	if blocks := e.BlockingErrorsFrom(proj, "s-043-03", "Stop"); len(blocks) != 0 {
		t.Errorf("the guard fired on a path outside updates//branding/ (or on the myupdates/ near-miss):\n%v", blocks)
	}
	// The writes landed — asserting only "no block" would also hold if the agent
	// never wrote.
	if !e.Exists(proj, "memories/notes/scratch.md") || !e.Exists(proj, "myupdates/note.md") {
		t.Errorf("the unguarded writes did not land at all")
	}
}

// T043_04: the settled file's content and path reach the rendered template — the
// event -> template wiring, proven directly and shown to change with the file.
//
// This example has no prepare; its template reads event.newContent and event.path
// straight off the file event. A stubbed verdict cannot show the file reached the
// template (undefined renders empty), so the capturing shim records the prompt and
// the test asserts the file's OWN distinctive fact and its path appear in it — and
// that a DIFFERENT file renders a DIFFERENT prompt, so the render is following the
// event, not a fixed string.
func TestT043_04_FileContentReachesTemplate(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj)
	e.InstallJudgeClaudeCapturing(proj, "judge-prompt.txt", `{"pass": false, "reasoning": "dup"}`)

	e.Run(proj, "s-043-04a", "write a branding doc with strategy in it", Turns("done",
		Write("w1", "memories/branding/voice.md", "# Voice\n\nWe price at a premium because the segment is underserved — a strategy call.\n"),
	))

	prompt := e.JudgePrompt(proj, "judge-prompt.txt")
	if prompt == "" {
		t.Fatalf("the judge never ran, so nothing about the wiring can be concluded")
	}
	// The file's own content reached the template.
	if !containsStr(prompt, "price at a premium because the segment is underserved") {
		t.Errorf("event.newContent did not reach the template:\n%s", prompt)
	}
	// And its path.
	if !containsStr(prompt, "memories/branding/voice.md") {
		t.Errorf("event.path did not reach the template:\n%s", prompt)
	}

	// A DIFFERENT file, fresh session: the prompt must follow it.
	e2 := New(t)
	proj2 := e2.Project()
	e2.GitInit(proj2)
	installExampleTree(t, proj2)
	e2.InstallJudgeClaudeCapturing(proj2, "judge-prompt.txt", `{"pass": false, "reasoning": "dup"}`)

	e2.Run(proj2, "s-043-04b", "write an update duplicating a role", Turns("done",
		Write("w1", "memories/updates/2026-08-18.md", duplicatedUpdate),
	))

	prompt2 := e2.JudgePrompt(proj2, "judge-prompt.txt")
	if prompt2 == "" {
		t.Fatalf("the judge never ran for the update file")
	}
	if !containsStr(prompt2, "Dana Per is the Head of Platform") {
		t.Errorf("the update file's own content did not reach the template:\n%s", prompt2)
	}
	// The other run's content must not leak into this one.
	if containsStr(prompt2, "price at a premium") {
		t.Errorf("the template carried a previous run's file content — the render is not following the event:\n%s", prompt2)
	}
}

// T043_05: a not-fine file RE-FIRES each cycle until it is FIXED — the file-guard's
// defining property.
//
// The scenario spans four cycles of ONE conversation (four Run calls, one session
// id). Because every cycle appends to the same transcript, BlockingErrorsFrom
// returns the blocks of ALL cycles so far, de-duplicated by text — so each cycle's
// verdict reasoning is made DISTINCT, and a cycle's re-fire is proven by its own
// reasoning text APPEARING, a cleared file by a later reasoning text NOT appearing.
//
//   - Cycle 1 writes the duplicated-fact file; the judge refuses with reason R1.
//     R1 must appear.
//   - Cycle 2 does work ENTIRELY OUTSIDE updates//branding/ (nothing the guard
//     matches), with the judge set to refuse with reason R2. Any block this cycle
//     can ONLY come from re-judging the still-outstanding bad file — so R2
//     appearing proves the re-fire re-judged a file this cycle never touched.
//   - Cycle 3 FIXES the bad file (rewrites it to a clean reference); the judge is
//     set to pass, and the file clears.
//   - Cycle 4 again does work outside the match, with the judge set to refuse with
//     a fresh reason R4. R4 must NOT appear: the fixed file is no longer
//     outstanding, so nothing re-fires, so no new block is produced. This is what
//     tells "fixed and cleared" apart from "still re-firing".
func TestT043_05_NotFineFileReFiresUntilFixed(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj)
	sess := "s-043-05"

	// Cycle 1: the bad file. The judge refuses with a distinct reason.
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "R1 Dana Per's role duplicated inline"}`)
	e.Run(proj, sess, "write a weekly update", Turns("done",
		Write("w1", "memories/updates/2026-08-18.md", duplicatedUpdate),
	))
	if !hasReason(e, proj, sess, "R1") {
		t.Fatalf("the duplicated-fact file did not block in the first cycle")
	}

	// Cycle 2: work OUTSIDE the match. A block here can only be the re-fired bad
	// file, judged afresh with reason R2.
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "R2 the duplicated update is still outstanding"}`)
	e.Run(proj, sess, "do unrelated non-matching work", Turns("done",
		Write("w2", "memories/notes/unrelated.md", "a plain note the guard does not match"),
	))
	if !hasReason(e, proj, sess, "R2") {
		t.Fatalf("an unfixed not-fine file was NOT re-judged on a cycle that never touched it — the re-fire did not happen")
	}

	// Cycle 3: FIX the file. Judge passes; the outstanding refusal clears.
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)
	e.Run(proj, sess, "fix the update", Turns("done",
		Write("w3", "memories/updates/2026-08-18.md", cleanUpdate),
	))

	// Cycle 4: work OUTSIDE the match again, judge armed to refuse with a NEW
	// reason. If the fix cleared the outstanding file, nothing re-fires and R4 never
	// appears.
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "R4 must not appear if the fix cleared the file"}`)
	e.Run(proj, sess, "more unrelated non-matching work", Turns("done",
		Write("w4", "memories/notes/another.md", "another plain unmatched note"),
	))
	if hasReason(e, proj, sess, "R4") {
		t.Errorf("a FIXED file kept re-firing: cycle 4 touched nothing the guard matches, yet a fresh block appeared")
	}
}

// hasReason reports whether any Stop block recorded for the session carries the
// given reasoning marker. Used to tell cycles apart across a shared transcript.
func hasReason(e *harness.Env, proj, sess, marker string) bool {
	for _, b := range e.BlockingErrorsFrom(proj, sess, "Stop") {
		if containsStr(b, marker) {
			return true
		}
	}
	return false
}
