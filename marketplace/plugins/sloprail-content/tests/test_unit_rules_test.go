package e2e

import (
	"strings"
	"testing"
)

// unit-satisfies-rules is a file-guard, NOT preventive (a Stop after-check —
// the same binding unit-satisfies-constraints used, and for the same reason:
// a writing-rule judgement is inherently after-the-fact). ONE check: collect
// every rule the unit's tags select (rules-lib.sh) and put them all to one
// judge call, granted allowed_tools: [Read, Bash] so a rule that asks for a
// deterministic measurement (a character count, a grep for a banned phrase)
// can actually be run against the real unit file rather than eyeballed —
// see the plugin README's "The mechanism".
//
// A rule's applicability is decided by the unit's OWN tags against each
// rule's applies_to — see rules-lib.sh. A rule with NO applies_to is global.
//
// TAGS ALWAYS COME FROM UNIT.md, AND THE DRAFT IS WHAT GETS JUDGED — a fix
// to a real bug: 02_draft.md carries no frontmatter of its own, so reading
// tags off whichever file was written meant a draft write always resolved
// to zero tags (rule set NONE, passing trivially), while a UNIT.md-only
// write got the full tag-selected set applied to the pitch, not the copy
// that ships. TestRules_DraftGetsUnitsTags below is the regression test for
// exactly that: a draft-only write (UNIT.md already on disk with tags, only
// 02_draft.md written this turn) must still pick up UNIT.md's tags.
//
// WHAT THESE TESTS CAN AND CANNOT PROVE. The judge's model call is stubbed
// (InstallJudgeClaude) in every e2e suite in this repo — the mock cannot run
// sr-agent's real --model/--settings judge path, so it substitutes a fixed
// verdict. That means these tests CANNOT prove a deterministic rule's
// measurement is actually correct (the stub never runs Bash) — what they DO
// prove is real: which rules are SELECTED for a given unit (the tag
// intersection, exercised by installing rules whose folder names differ and
// asserting which one's stub-driven block appears), that the guard is not
// preventive (writes land, refusals arrive only via the Stop after-check),
// and that a unit with no applicable rule is never even judged into a false
// block. The deterministic-measurement CLAIM itself (does `wc -m` really
// catch an over-limit tweet) is proven by hand against the shipped rule text
// and the judge's own tool grant, not by this e2e suite — there is no way to
// drive a real judge's Bash calls through this harness. The PRE-COMPUTED
// FACTS wiring (does prepare-judge-rules.sh's compute-draft-facts.sh output
// actually reach the judge's prompt) IS provable, though, the same way
// TestRubricReachesRuleJudgePrompt proves the rubric-array wiring elsewhere
// in this repo: capture the rendered prompt (InstallJudgeClaudeCapturing)
// and assert a computed number appears in it — see
// TestRules_PrecomputedFactsReachJudgePrompt.

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
	))
	if res.Refused() {
		t.Fatalf("the unit write itself was refused at Pre (setup broken, guard is not preventive):\n%s", res.Output)
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
	))
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
	))
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
// unit's file path available (additionalContext.judged_path, rendered into
// the prompt) for a real judge to act on, even though this e2e cannot drive
// a real judge's own Bash call. See the file header's "what these tests can
// and cannot prove".
//
// UNIT.md is written FIRST, carrying tags: [x] — tags are always resolved
// from UNIT.md (see the file header), so a draft-only write with no UNIT.md
// on disk cannot pick up the x-scoped rule at all. This mirrors how a real
// unit is authored: the pitch (UNIT.md) exists before its draft.
func TestRules_DeterministicRuleRefusesViaJudgeStub(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installTaggedRule(t, e, proj, "03_x-limit", "must", "x",
		"Rule: each tweet is at most 280 characters. Measure with wc -m against the unit file; do not estimate.")
	installPluginTree(t, proj)
	installPluginStructure(t, e, proj)
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "WRITING RULE VIOLATION: 03_x-limit [must]: measured 312 characters via wc -m, over the 280 limit"}`)

	sess := "s-det-refuse"
	unitBody := unitFrontmatter(
		"transcript_path: /abs/s.jsonl\ncreated: 2026-09-25\ntype: post\nstatus: drafting\ntags: [x]\n",
		"The pitch.")
	over := ""
	for i := 0; i < 300; i++ {
		over += "b"
	}

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", unitPath, unitBody),
		Write("w2", draftPath, over),
	))
	if res.Refused() {
		t.Fatalf("a write was refused at Pre (setup broken, guard is not preventive):\n%s", res.Output)
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
// (whatever measurement it made) is honoured. UNIT.md is written first, same
// as the refuse case above.
func TestRules_DeterministicRulePassesViaJudgeStub(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installTaggedRule(t, e, proj, "03_x-limit", "must", "x",
		"Rule: each tweet is at most 280 characters. Measure with wc -m against the unit file; do not estimate.")
	installPluginTree(t, proj)
	installPluginStructure(t, e, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	sess := "s-det-pass"
	unitBody := unitFrontmatter(
		"transcript_path: /abs/s.jsonl\ncreated: 2026-09-25\ntype: post\nstatus: drafting\ntags: [x]\n",
		"The pitch.")

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", unitPath, unitBody),
		Write("w2", draftPath, "A short tweet, comfortably under the limit."),
	))
	if res.Refused() {
		t.Fatalf("a write was refused at Pre (setup broken):\n%s", res.Output)
	}
	if blocks := e.BlockingErrorsFrom(proj, sess, "Stop"); len(blocks) != 0 {
		t.Fatalf("a judge PASS was not honoured:\n%v", blocks)
	}
}

// TestRules_DraftGetsUnitsTags: the regression test for the core bug. UNIT.md
// is written first with tags: [x] and committed to the tree (so it is ON
// DISK, not just in this turn's own diff) — then a LATER, SEPARATE write
// touches ONLY 02_draft.md, which carries no frontmatter of its own. Before
// the fix, prepare-judge-rules.sh read tags off the file being written
// (02_draft.md), found no frontmatter, and resolved zero tags — so the
// x-scoped rule below would NEVER have fired on a draft-only write, no
// matter what the draft said. The judge stub is set to FAIL: a block here
// proves the x-scoped rule was selected using UNIT.md's tags, not the
// draft's (nonexistent) ones.
func TestRules_DraftGetsUnitsTags(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installTaggedRule(t, e, proj, "04_x-only", "must_not", "x",
		"Rule: no rhetorical-question openers on X.")
	installPluginTree(t, proj)
	installPluginStructure(t, e, proj)

	// UNIT.md lands and is judged (rule set NONE for this pitch's own prose,
	// no violation) in an earlier session, standing in for "the unit was
	// already pitched before this turn started".
	unitBody := unitFrontmatter(
		"transcript_path: /abs/s.jsonl\ncreated: 2026-09-25\ntype: post\nstatus: drafting\ntags: [x]\n",
		"The pitch: announce the launch.")
	e.WriteFile(proj, unitPath, unitBody)

	e.InstallJudgeClaude(`{"pass": false, "reasoning": "WRITING RULE VIOLATION: 04_x-only [must_not]: opens with a rhetorical question"}`)

	sess := "s-draft-tags"
	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", draftPath, "Ever wonder why builds keep failing?"),
	))
	if res.Refused() {
		t.Fatalf("the draft write was refused at Pre (setup broken, guard is not preventive):\n%s", res.Output)
	}
	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks) == 0 {
		t.Fatalf("a draft-only write did not pick up its unit's tags — the x-scoped rule never fired:\n%s", res.Output)
	}
	joined := ""
	for _, b := range blocks {
		joined += b + "\n"
	}
	if !containsStr(joined, "04_x-only") {
		t.Errorf("the block did not name the x-scoped rule, so the tag was not resolved from UNIT.md:\n%s", joined)
	}
}

// TestRules_UnitMdOnlyWithNoDraftSelectsNoRule: a UNIT.md write with no
// 02_draft.md on disk yet — even though an x-scoped rule is configured and
// the unit carries tags: [x] — resolves to the NONE sentinel rather than
// judging the pitch paragraph as if it were the unit's shipped copy (see
// prepare-judge-rules.sh's "NO DRAFT YET"). A stubbed judge cannot itself
// prove NONE was passed rather than the rule text (the mock returns
// whatever verdict it was configured with regardless of the prompt it was
// sent — see TestRules_PrecomputedFactsReachJudgePrompt's pattern for why
// the prompt itself must be captured to prove this), so this captures the
// rendered prompt and asserts it carries the NONE sentinel and NOT the
// configured x-scoped rule's own text.
func TestRules_UnitMdOnlyWithNoDraftSelectsNoRule(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installTaggedRule(t, e, proj, "05_x-only", "must_not", "x",
		"Rule: no rhetorical-question openers on X.")
	installPluginTree(t, proj)
	installPluginStructure(t, e, proj)
	e.InstallJudgeClaudeCapturing(proj, "judge-prompt.txt", `{"pass": true, "reasoning": ""}`)

	sess := "s-unit-only-no-draft"
	unitBody := unitFrontmatter(
		"transcript_path: /abs/s.jsonl\ncreated: 2026-09-25\ntype: post\nstatus: drafting\ntags: [x]\n",
		"Ever wonder why builds keep failing?")

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", unitPath, unitBody),
	))
	if res.Refused() {
		t.Fatalf("the unit write was refused at Pre (setup broken):\n%s", res.Output)
	}
	if blocks := e.BlockingErrorsFrom(proj, sess, "Stop"); len(blocks) != 0 {
		t.Fatalf("a UNIT.md-only write with no draft yet was blocked:\n%v", blocks)
	}

	prompt := e.JudgePrompt(proj, "judge-prompt.txt")
	if prompt == "" {
		t.Fatalf("unit-satisfies-rules's judge never ran — no prompt captured")
	}
	if strings.Contains(prompt, "no rhetorical-question openers") {
		t.Fatalf("the x-scoped rule reached the judge prompt even though no draft exists yet — the pitch was judged as if it were the shipped copy:\n%s", prompt)
	}
	if !strings.Contains(prompt, "NONE") {
		t.Fatalf("the NONE sentinel did not reach the judge prompt for a UNIT.md-only write with no draft:\n%s", prompt)
	}
}

// TestRules_PrecomputedFactsReachJudgePrompt: prepare-judge-rules.sh computes
// deterministic facts about the draft (compute-draft-facts.sh) and hands
// them to the judge as additionalContext.facts_* — this proves that wiring
// actually reaches the rendered prompt, the same way
// TestRubricReachesRuleJudgePrompt (engine_repo_judges) proves the
// meta-rules array reaches authoring-slop's prompt: a stubbed verdict alone
// cannot tell you whether prepare's output was ever rendered in, since an
// absent key renders empty rather than erroring. Capture the prompt and
// assert the EXACT precomputed numbers for this draft appear in it.
func TestRules_PrecomputedFactsReachJudgePrompt(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installTaggedRule(t, e, proj, "06_x-limit", "must", "x",
		"Rule: the title line must be at most 40 characters. Use the pre-computed title_chars fact; do not remeasure.")
	installPluginTree(t, proj)
	installPluginStructure(t, e, proj)
	e.InstallJudgeClaudeCapturing(proj, "judge-prompt.txt", `{"pass": true, "reasoning": ""}`)

	unitBody := unitFrontmatter(
		"transcript_path: /abs/s.jsonl\ncreated: 2026-09-25\ntype: post\nstatus: drafting\ntags: [x]\n",
		"The pitch.")
	e.WriteFile(proj, unitPath, unitBody)

	// A short, known title line, chosen so its wc -m character count is easy
	// to assert against without duplicating compute-draft-facts.sh's own
	// logic: "Short Title" is 11 characters.
	draftBody := "Short Title\n\nSome body words here for the count."

	sess := "s-facts-reach-prompt"
	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", draftPath, draftBody),
	))
	if res.Refused() {
		t.Fatalf("the draft write was refused at Pre (setup broken):\n%s", res.Output)
	}

	prompt := e.JudgePrompt(proj, "judge-prompt.txt")
	if prompt == "" {
		t.Fatalf("unit-satisfies-rules's judge never ran — no prompt captured")
	}
	if !strings.Contains(prompt, "Short Title") {
		t.Fatalf("the computed title_line fact did not reach the judge prompt:\n%s", prompt)
	}
	if !strings.Contains(prompt, "title_chars: 11") {
		t.Fatalf("the computed title_chars fact (11) did not reach the judge prompt:\n%s", prompt)
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
	))
	if res.Refused() {
		t.Fatalf("the unit write was refused at Pre (setup broken):\n%s", res.Output)
	}
	if blocks := e.BlockingErrorsFrom(proj, sess, "Stop"); len(blocks) != 0 {
		t.Fatalf("a unit with no applicable rules was blocked at Stop:\n%v", blocks)
	}
}
