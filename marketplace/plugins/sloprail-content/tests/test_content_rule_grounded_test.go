package e2e

import "testing"

// content-rule-is-grounded is a PREVENTIVE file-guard over RULE.md/
// CONSTRAINT.md, reusing sloprail-tasks's task-body-is-human-authored
// pattern exactly, for a rule's body instead of a task's:
//
//   1. SCRIPT (check-rule.sh): the BODY must carry a [quote](jsonl) link
//      whose quote GROUNDS via cite to the user's own words. No citation, or
//      one that does not ground, is refused HERE — deterministically, before
//      the judge.
//   2. PREPARE + JUDGE (resolve-cited-rule-quotes.sh + judge-rule-body.md.j2):
//      the rule must correspond to the cited words and hold THAT AND NOTHING
//      ELSE. The judge is stubbed.
//
// An earlier draft grounded via a frontmatter transcript_paths list and also
// checked that a script: rule named a script capable of refusing — both
// gone (see rule.cue and the plugin README's migration note): grounding
// moved to the body, and there is no more script-rule mechanism at all.
//
// These prove: a rule with no citation in its body at all is refused by the
// deterministic script, before the judge; a citation that does not ground
// (a fabricated quote) is refused; a grounded body the judge accepts (PASS)
// passes and lands; and a grounded body the judge rejects as adding
// untraceable scope (FAIL) is refused, with the judge's reasoning reaching
// the agent.

// TestRuleGrounded_NoCitationRefusedByScript: a RULE.md with no citation
// link in its body at all is refused by the deterministic script stage,
// before the judge. The stub is set to PASS: if the engine ever reached the
// judge here (it must not — the script refuses first), the write would go
// through anyway on this stub, so this proves the SCRIPT, not the judge,
// refused.
func TestRuleGrounded_NoCitationRefusedByScript(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, proj)
	installPluginStructure(t, e, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	sess := "s-rule-nocite"
	rule := "---\nlevel: must_not\ncreated: 2026-09-25\n---\nRule: no hype language, an agent could have written this with no citation.\n"

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", ".sloprail/content-rules/01_tone/RULE.md", rule),
	))
	if !res.Refused() {
		t.Fatalf("a rule with no citation was not refused:\n%s", res.Output)
	}
	if e.Exists(proj, ".sloprail/content-rules/01_tone/RULE.md") {
		t.Errorf("the preventive guard let a citation-less rule land on disk")
	}
	if !res.Saw("carries no citation of a user message") {
		t.Errorf("the refusal was not the script tier's missing-citation reason:\n%s", res.Output)
	}
}

// TestRuleGrounded_UngroundedCitationRefused: the body carries a citation
// LINK (the right shape), but the quote is fabricated — it does not resolve
// to any real user message. Refused, proving the quote must actually ground,
// not merely be link-shaped.
func TestRuleGrounded_UngroundedCitationRefused(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, proj)
	installPluginStructure(t, e, proj)

	sess := "s-rule-ungrounded"
	tp := e.TranscriptPath(proj, sess)
	rule := "---\nlevel: must_not\ncreated: 2026-09-25\n---\nRule: no hype language. The user said " +
		cite("nobody ever actually said this", tp, 1) + ".\n"

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", ".sloprail/content-rules/01_tone/RULE.md", rule),
	))
	if !res.Refused() {
		t.Fatalf("a rule citing a fabricated quote was not refused:\n%s", res.Output)
	}
	if e.Exists(proj, ".sloprail/content-rules/01_tone/RULE.md") {
		t.Errorf("the preventive guard let an ungrounded rule land on disk")
	}
}

// TestRuleGrounded_HumanAuthoredPasses: a body that quotes and links the
// human's own real prompt (grounded) and that the judge accepts (pass:true)
// admits, and the file lands. The happy path and the control for the
// refusal below.
func TestRuleGrounded_HumanAuthoredPasses(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, proj)
	installPluginStructure(t, e, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	sess := "s-rule-ok"
	tp := e.TranscriptPath(proj, sess)
	rule := "---\nlevel: must_not\ncreated: 2026-09-25\n---\nRule: no hype language. The user said " +
		cite("Approve writing rules and publishing", tp, 1) + ".\n"

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", ".sloprail/content-rules/01_tone/RULE.md", rule),
	))
	if res.Refused() {
		t.Fatalf("a human-authored, judge-accepted rule body was refused:\n%s", res.Output)
	}
	if !e.Exists(proj, ".sloprail/content-rules/01_tone/RULE.md") {
		t.Errorf("an admitted rule write did not land on disk")
	}
}

// TestRuleGrounded_SlopBodyRefusedByJudge: a body carrying a grounded
// citation but ALSO agent-authored elaboration the human never asked for —
// the "and nothing else" violation — passes stage 1 (the citation grounds)
// and is then REFUSED by the judge (stub pass:false), with the judge's
// reasoning reaching the agent.
func TestRuleGrounded_SlopBodyRefusedByJudge(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, proj)
	installPluginStructure(t, e, proj)
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "RULE BODY: the body adds a threshold and an exception the user never stated"}`)

	sess := "s-rule-slop"
	tp := e.TranscriptPath(proj, sess)
	rule := "---\nlevel: must_not\ncreated: 2026-09-25\n---\nRule: no hype language. The user said " +
		cite("Approve writing rules and publishing", tp, 1) +
		". This applies to every unit except drafts under 50 words, which may use up to two hype phrases.\n"

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", ".sloprail/content-rules/01_tone/RULE.md", rule),
	))
	if !res.Refused() {
		t.Fatalf("a slop rule body the judge rejected was not refused:\n%s", res.Output)
	}
	if e.Exists(proj, ".sloprail/content-rules/01_tone/RULE.md") {
		t.Errorf("the preventive guard let a judge-rejected rule land on disk")
	}
	if !res.Saw("threshold and an exception the user never stated") {
		t.Errorf("the judge's reasoning did not reach the agent:\n%s", res.Output)
	}
}
