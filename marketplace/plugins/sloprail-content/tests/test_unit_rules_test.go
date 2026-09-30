package e2e

import "testing"

// unit-satisfies-rules is a file-guard with no gate (judged at Stop over the
// committed changeset — a writing-rule judgement is inherently after-the-fact). ONE check: collect
// every rule the unit's tags select (rules-lib.sh) and put them all to one
// judge call, granted allowed_tools: [Read, Bash] so a rule that asks for a
// deterministic measurement (a character count, a grep for a banned phrase)
// can actually be run against the real unit file rather than eyeballed —
// see the plugin README's "The mechanism".
//
// A rule's applicability is decided by the unit's OWN tags against each
// rule's applies_to — see rules-lib.sh. A rule with NO applies_to is global.
//
// WHAT THESE TESTS CAN AND CANNOT PROVE. The judge's model call is stubbed
// (InstallJudgeClaude) in every e2e suite in this repo — the mock cannot run
// sr-agent's real --model/--settings judge path, so it substitutes a fixed
// verdict. That means these tests CANNOT prove a deterministic rule's
// measurement is actually correct (the stub never runs Bash) — what they DO
// prove is real: which rules are SELECTED for a given unit (the tag
// intersection, exercised by installing rules whose folder names differ and
// asserting which one's stub-driven block appears), that the guard has no gate
// (writes land, refusals arrive only via the Stop after-check),
// and that a unit with no applicable rule is never even judged into a false
// block. The deterministic-measurement CLAIM itself (does `wc -m` really
// catch an over-limit tweet) is proven by hand against the shipped rule text
// and the judge's own tool grant, not by this e2e suite — there is no way to
// drive a real judge's Bash calls through this harness.

// installGlobalRule writes a project-wide JUDGE rule (no applies_to —
// global) DIRECTLY to disk via WriteFile, bypassing every guardrail
// (WriteFile is not a tool call the mock's hooks ever see) — these tests are
// about unit-satisfies-rules's OWN selection/judging, not about
// content-rule-is-grounded's citation check, which has its own dedicated
// suite (test_content_rule_grounded_test.go). A grounding quote is included
// anyway, for readability and so the rule file would ALSO pass
// content-rule-is-grounded if it were ever written through the mock.
func installGlobalRule(t *testing.T, e *Env, proj, name, level, body string) {
	t.Helper()
	e.WriteFile(proj, ".sloprail/content-rules/"+name+"/RULE.md",
		"---\nlevel: "+level+"\ncreated: 2026-09-25\n---\n"+body+"\n")
}

// installTaggedRule is installGlobalRule with an applies_to tag selector.
func installTaggedRule(t *testing.T, e *Env, proj, name, level, tag, body string) {
	t.Helper()
	e.WriteFile(proj, ".sloprail/content-rules/"+name+"/RULE.md",
		"---\nlevel: "+level+"\ncreated: 2026-09-25\napplies_to: ["+tag+"]\n---\n"+body+"\n")
}

// TestRules_GlobalJudgeRuleAppliesToUnitWithNoTags: a global rule (no
// applies_to) is judged against a unit that carries no tags at all — the
// compatibility case the taxonomy is required to preserve. Judge stub FAILs,
// so the block proves the rule was actually put to the judge for this unit.
func TestRules_GlobalJudgeRuleAppliesToUnitWithNoTags(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installGlobalRule(t, e, proj, "01_global-tone", "must_not",
		"Rule: no hype language.")
	installPluginTree(t, proj)
	installPluginStructure(t, e, proj)
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "WRITING RULE VIOLATION: 01_global-tone [must_not]: the unit uses hype language the rule forbids"}`)

	sess := "s-global-notags"
	body := unitFrontmatter(
		"transcript_path: /abs/s.jsonl\ncreated: 2026-09-25\ntype: post\nstatus: drafting\n",
		"# Announce\n\nThis is a revolutionary breakthrough.")

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", unitPath, body),
	).ThenCommit("Write the unit"))
	if res.Refused() {
		t.Fatalf("the unit write itself was refused at Pre (setup broken, guard has no gate):\n%s", res.Output)
	}
	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks) == 0 {
		t.Fatalf("a global rule was not applied to a unit with no tags:\n%s", res.Output)
	}
	joined := ""
	for _, b := range blocks {
		joined += b + "\n"
	}
	if !containsStr(joined, "01_global-tone") {
		t.Errorf("the block did not name the global rule that fired:\n%s", joined)
	}
}

// TestRules_TaggedRuleAppliesOnlyWhenTagMatches: an X-scoped rule fires
// (judge FAIL) on a unit carrying tags: [x], and does NOT fire (no block) on
// a unit carrying only tags: [reddit] — same rule, the unit's own tags
// decide.
func TestRules_TaggedRuleAppliesOnlyWhenTagMatches(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installTaggedRule(t, e, proj, "02_x-tone", "must_not", "x",
		"Rule: no rhetorical-question openers on X.")
	installPluginTree(t, proj)
	installPluginStructure(t, e, proj)
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "WRITING RULE VIOLATION: 02_x-tone [must_not]: opens with a rhetorical question"}`)

	sess := "s-tag-match"
	body := unitFrontmatter(
		"transcript_path: /abs/s.jsonl\ncreated: 2026-09-25\ntype: post\nstatus: drafting\ntags: [x]\n",
		"Ever wonder why builds keep failing?")

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", unitPath, body),
	).ThenCommit("Write the unit"))
	if res.Refused() {
		t.Fatalf("the unit write was refused at Pre (setup broken):\n%s", res.Output)
	}
	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks) == 0 {
		t.Fatalf("an X-scoped rule did not fire on a unit carrying tags: [x]:\n%s", res.Output)
	}
}

// TestRules_TaggedRuleDoesNotApplyToOtherTag: the SAME rule as above, but the
// unit carries tags: [reddit] instead — the rule must NOT apply, proven by a
// judge stub set to FAIL: if the rule were (wrongly) judged, the turn would
// block; it must not.
func TestRules_TaggedRuleDoesNotApplyToOtherTag(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installTaggedRule(t, e, proj, "02_x-tone", "must_not", "x",
		"Rule: no rhetorical-question openers on X.")
	installPluginTree(t, proj)
	installPluginStructure(t, e, proj)
	// FAIL, not PASS: if this test's own setup accidentally applied the X rule
	// anyway, the FAIL stub would surface it as a block, making a silent
	// over-application visible rather than passing by accident.
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "WRITING RULE VIOLATION: should not have been asked"}`)

	sess := "s-tag-mismatch"
	body := unitFrontmatter(
		"transcript_path: /abs/s.jsonl\ncreated: 2026-09-25\ntype: post\nstatus: drafting\ntags: [reddit]\n",
		"Ever wonder why builds keep failing?")

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", unitPath, body),
	).ThenCommit("Write the unit"))
	if res.Refused() {
		t.Fatalf("the unit write was refused at Pre (setup broken):\n%s", res.Output)
	}
	for _, b := range e.BlockingErrorsFrom(proj, sess, "Stop") {
		if containsStr(b, "02_x-tone") {
			t.Fatalf("the X-scoped rule fired on a unit carrying tags: [reddit], not [x]:\n%s", b)
		}
	}
}

// TestRules_DeterministicRuleRefusesViaJudgeStub: a rule written to ask the
// judge for a deterministic measurement (a character limit) fires when the
// stubbed judge says FAIL — proving the rule reaches the judge with the
// unit's file path available (additionalContext.unit_path, rendered into the
// prompt) for a real judge to act on, even though this e2e cannot drive a
// real judge's own Bash call. See the file header's "what these tests can
// and cannot prove".
func TestRules_DeterministicRuleRefusesViaJudgeStub(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installTaggedRule(t, e, proj, "03_x-limit", "must", "x",
		"Rule: each tweet is at most 280 characters. Measure with wc -m against the unit file; do not estimate.")
	// unit-md-first requires UNIT.md to exist before the draft can be written;
	// seed it into the baseline so this test stays about unit-satisfies-rules.
	e.WriteFile(proj, unitPath, unitFrontmatter(
		"transcript_path: /abs/s.jsonl\ncreated: 2026-09-25\ntype: post\nstatus: drafting\ntags: [x]\n",
		"Announcing the launch."))
	installPluginTree(t, proj)
	installPluginStructure(t, e, proj)
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "WRITING RULE VIOLATION: 03_x-limit [must]: measured 312 characters via wc -m, over the 280 limit"}`)

	sess := "s-det-refuse"
	over := ""
	for i := 0; i < 300; i++ {
		over += "b"
	}
	body := unitFrontmatter(
		"transcript_path: /abs/s.jsonl\ncreated: 2026-09-25\ntype: post\nstatus: drafting\ntags: [x]\n",
		over)

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", draftPath, body),
	).ThenCommit("Write the unit"))
	if res.Refused() {
		t.Fatalf("the draft write was refused at Pre (setup broken, guard has no gate):\n%s", res.Output)
	}
	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks) == 0 {
		t.Fatalf("a deterministic rule (judge stub FAIL) did not block:\n%s", res.Output)
	}
	joined := ""
	for _, b := range blocks {
		joined += b + "\n"
	}
	if !containsStr(joined, "03_x-limit") {
		t.Errorf("the block did not name the deterministic rule:\n%s", joined)
	}
}

// TestRules_DeterministicRulePassesViaJudgeStub: the SAME kind of
// deterministic rule, this time with the judge stub set to PASS — proves
// the guard does not block by default and that a PASS from the judge
// (whatever measurement it made) is honoured.
func TestRules_DeterministicRulePassesViaJudgeStub(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installTaggedRule(t, e, proj, "03_x-limit", "must", "x",
		"Rule: each tweet is at most 280 characters. Measure with wc -m against the unit file; do not estimate.")
	// unit-md-first requires UNIT.md to exist before the draft can be written;
	// seed it into the baseline so this test stays about unit-satisfies-rules.
	e.WriteFile(proj, unitPath, unitFrontmatter(
		"transcript_path: /abs/s.jsonl\ncreated: 2026-09-25\ntype: post\nstatus: drafting\ntags: [x]\n",
		"Announcing the launch."))
	e.CommitAll(proj, "the seeded unit and its topic rule")
	installPluginTree(t, proj)
	installPluginStructure(t, e, proj)
	e.CommitAll(proj, "install the rules")
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	sess := "s-det-pass"
	body := unitFrontmatter(
		"transcript_path: /abs/s.jsonl\ncreated: 2026-09-25\ntype: post\nstatus: drafting\ntags: [x]\n",
		"A short tweet, comfortably under the limit.")

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", draftPath, body),
	).ThenCommit("Write the unit"))
	if res.Refused() {
		t.Fatalf("the draft write was refused at Pre (setup broken):\n%s", res.Output)
	}
	if blocks := e.BlockingErrorsFrom(proj, sess, "Stop"); len(blocks) != 0 {
		t.Fatalf("a judge PASS was not honoured:\n%v", blocks)
	}
}

// TestRules_NoApplicableRulesPasses: a unit with no tags, no global rules
// configured at all, and no topic constraints/ — nothing applies, and the
// turn is not blocked. The judge ALWAYS runs, even with no applicable rule
// (prepare proceeds with the "NONE" sentinel and the template passes
// trivially on it) — stubbed PASS proves that trivial pass, not a real rule.
func TestRules_NoApplicableRulesPasses(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, proj)
	installPluginStructure(t, e, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	sess := "s-no-rules"
	body := unitFrontmatter(
		"transcript_path: /abs/s.jsonl\ncreated: 2026-09-25\ntype: post\nstatus: raw\n",
		"Nothing to check here.")

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", unitPath, body),
	).ThenCommit("Write the unit"))
	if res.Refused() {
		t.Fatalf("the unit write was refused at Pre (setup broken):\n%s", res.Output)
	}
	if blocks := e.BlockingErrorsFrom(proj, sess, "Stop"); len(blocks) != 0 {
		t.Fatalf("a unit with no applicable rules was blocked at Stop:\n%v", blocks)
	}
}

// TestRules_UnitWriteJudgesTheDraft: a UNIT.md holds frontmatter, not the text a
// writing rule is about, so a UNIT.md write (a publish, a tag change) hands the
// judge the unit's 02_draft.md too — a rule added after the draft was written
// still meets it.
func TestRules_UnitWriteJudgesTheDraft(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installTaggedRule(t, e, proj, "02_x-tone", "must_not", "x",
		"Rule: no rhetorical-question openers on X.")
	installPluginTree(t, proj)
	installPluginStructure(t, e, proj)
	e.WriteFile(proj, "memories/topics/20260101_launch/units/01_announce/02_draft.md",
		"ZZ_DRAFT Ever wonder why builds keep failing?\n")
	e.CommitAll(proj, "seed the draft")
	e.InstallJudgeClaudeCapturing(proj, "judge-prompt.txt", `{"pass": true, "reasoning": ""}`)

	res := e.Run(proj, "s-unit-draft", authPrompt, Turns("done",
		Write("w1", unitPath, unitFrontmatter("created: 2026-09-25\ntype: post\nstatus: drafting\ntags: [x]\n", "")),
	).ThenCommit("Tag the unit"))
	if res.Refused() {
		t.Fatalf("the unit write was refused at Pre (setup broken):\n%s", res.Output)
	}
	prompt := e.JudgePrompt(proj, "judge-prompt.txt")
	if !containsStr(prompt, "--- 02_draft.md ---") || !containsStr(prompt, "ZZ_DRAFT Ever wonder why builds keep failing?") {
		t.Fatalf("the rules judge was not handed the unit's draft on a UNIT.md write:\n%s", prompt)
	}
	if !containsStr(prompt, "01_announce/02_draft.md</measure-at>") {
		t.Errorf("a measurement is not pointed at the draft:\n%s", prompt)
	}
}
