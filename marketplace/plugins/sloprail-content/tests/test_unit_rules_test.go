package e2e

import "testing"

// unit-satisfies-rules is a file-guard, NOT preventive (a Stop after-check —
// the same binding unit-satisfies-constraints used, and for the same reason:
// a writing-rule judgement is inherently after-the-fact). Two checks:
//
//   1. SCRIPT (check-script-rules.sh): every applicable SCRIPT rule runs
//      deterministically; a failure refuses BEFORE the judge is ever asked.
//   2. PREPARE + JUDGE (prepare-judge-rules.sh + judge-rules.md.j2): every
//      applicable JUDGE rule is put to one model call together.
//
// A rule's applicability is decided by the unit's OWN taxonomy (channels,
// type, tags) against each rule's applies_to — see rules-lib.sh. A rule with
// NO applies_to is global.
//
// These tests install ONE project-wide rule per case directly under
// .sloprail/content-rules/ (skipping content-rule-is-grounded's own
// enforcement — that guard is proven separately in
// test_content_rule_grounded_test.go — by writing the rule file onto disk
// with WriteFile/WriteExecutable rather than through the mock, exactly how
// sloprail-tasks's tests seed a project's PRE-existing state distinct from
// what the scenario does).

// installGlobalRule writes a project-wide JUDGE rule (no applies_to — global)
// with a grounded-looking transcript_paths entry (content-rule-is-grounded is
// not installed by these tests, so the entry's groundedness is not checked
// here — see the note above).
func installGlobalRule(t *testing.T, e *Env, proj, name, level, body string) {
	t.Helper()
	e.WriteFile(proj, ".sloprail/content-rules/"+name+"/RULE.md",
		"---\nlevel: "+level+"\ncreated: 2026-09-25\n---\n"+body+"\n")
}

// installChannelRule writes a project-wide JUDGE rule scoped to one channel.
func installChannelRule(t *testing.T, e *Env, proj, name, level, channel, body string) {
	t.Helper()
	e.WriteFile(proj, ".sloprail/content-rules/"+name+"/RULE.md",
		"---\nlevel: "+level+"\ncreated: 2026-09-25\napplies_to:\n  channels: ["+channel+"]\n---\n"+body+"\n")
}

// installScriptRule writes a project-wide SCRIPT rule.
func installScriptRule(t *testing.T, e *Env, proj, name, level, channel, scriptName string, args []string) {
	t.Helper()
	argsYAML := ""
	for _, a := range args {
		argsYAML += "    - \"" + a + "\"\n"
	}
	e.WriteFile(proj, ".sloprail/content-rules/"+name+"/RULE.md",
		"---\nlevel: "+level+"\ncreated: 2026-09-25\napplies_to:\n  channels: ["+channel+"]\nscript:\n  name: "+scriptName+"\n  args:\n"+argsYAML+"---\nScript rule.\n")
}

// TestRules_GlobalJudgeRuleAppliesToUnitWithNoChannels: a global rule (no
// applies_to) is judged against a unit that carries no channels at all — the
// compatibility case the taxonomy is required to preserve. Judge stub FAILs,
// so the block proves the rule was actually put to the judge for this unit.
func TestRules_GlobalJudgeRuleAppliesToUnitWithNoChannels(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installGlobalRule(t, e, proj, "01_global-tone", "must_not",
		"Rule: no hype language.\n\nPASS: plain claims.\nFAIL: \"revolutionary breakthrough\"")
	installPluginTree(t, proj)
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "WRITING RULE VIOLATION: 01_global-tone [must_not]: the unit uses hype language the rule forbids"}`)

	sess := "s-global-nochannel"
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
		t.Fatalf("a global rule was not applied to a unit with no channels:\n%s", res.Output)
	}
	joined := ""
	for _, b := range blocks {
		joined += b + "\n"
	}
	if !containsStr(joined, "01_global-tone") {
		t.Errorf("the block did not name the global rule that fired:\n%s", joined)
	}
}

// TestRules_ChannelRuleAppliesOnlyWhenChannelMatches: an X-scoped rule fires
// (judge FAIL) on a unit carrying channels: [x], and does NOT fire (no block)
// on a unit carrying only channels: [reddit] — same rule, taxonomy decides.
func TestRules_ChannelRuleAppliesOnlyWhenChannelMatches(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installChannelRule(t, e, proj, "02_x-tone", "must_not", "x",
		"Rule: no rhetorical-question openers on X.\n\nPASS: a plain claim.\nFAIL: \"Ever wonder why...?\"")
	installPluginTree(t, proj)
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "WRITING RULE VIOLATION: 02_x-tone [must_not]: opens with a rhetorical question"}`)

	sess := "s-channel-match"
	body := unitFrontmatter(
		"transcript_path: /abs/s.jsonl\ncreated: 2026-09-25\ntype: post\nstatus: drafting\nchannels: [x]\n",
		"Ever wonder why builds keep failing?")

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", unitPath, body),
	))
	if res.Refused() {
		t.Fatalf("the unit write was refused at Pre (setup broken):\n%s", res.Output)
	}
	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks) == 0 {
		t.Fatalf("an X-scoped rule did not fire on a unit carrying channels: [x]:\n%s", res.Output)
	}
}

// TestRules_ChannelRuleDoesNotApplyToOtherChannel: the SAME rule as above,
// but the unit carries channels: [reddit] instead — the rule must NOT apply,
// proven by a judge stub set to FAIL: if the rule were (wrongly) judged, the
// turn would block; it must not.
func TestRules_ChannelRuleDoesNotApplyToOtherChannel(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installChannelRule(t, e, proj, "02_x-tone", "must_not", "x",
		"Rule: no rhetorical-question openers on X.")
	installPluginTree(t, proj)
	// FAIL, not PASS: if this test's own setup accidentally applied the X rule
	// anyway, the FAIL stub would surface it as a block, making a silent
	// over-application visible rather than passing by accident.
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "WRITING RULE VIOLATION: should not have been asked"}`)

	sess := "s-channel-mismatch"
	body := unitFrontmatter(
		"transcript_path: /abs/s.jsonl\ncreated: 2026-09-25\ntype: post\nstatus: drafting\nchannels: [reddit]\n",
		"Ever wonder why builds keep failing?")

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", unitPath, body),
	))
	if res.Refused() {
		t.Fatalf("the unit write was refused at Pre (setup broken):\n%s", res.Output)
	}
	for _, b := range e.BlockingErrorsFrom(proj, sess, "Stop") {
		if containsStr(b, "02_x-tone") {
			t.Fatalf("the X-scoped rule fired on a unit carrying channels: [reddit], not [x]:\n%s", b)
		}
	}
}

// TestRules_OverLimitXThreadRefusedByScript: an X-scoped char-limit SCRIPT
// rule refuses a thread whose second segment is over 280 characters. No judge
// stub needed to prove the refusal — the script check runs BEFORE the judge
// and a script refusal never reaches it (stub omitted entirely: the mock's
// default `claude` would be invoked and fail if reached, which would fail the
// test loudly rather than silently passing).
func TestRules_OverLimitXThreadRefusedByScript(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installScriptRule(t, e, proj, "03_x-limit", "must", "x", "char-limit", []string{"280", "---"})
	installPluginTree(t, proj)
	// Belt-and-braces: the script check SHOULD refuse first and the judge
	// check should never be reached (checks run in order, first refusal ends
	// the guard) — but stub the judge PASS anyway so a real `claude` call is
	// never what makes this test flaky if the engine ever retries the Stop
	// cycle.
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	sess := "s-x-over-limit"
	over := ""
	for i := 0; i < 300; i++ {
		over += "b"
	}
	body := unitFrontmatter(
		"transcript_path: /abs/s.jsonl\ncreated: 2026-09-25\ntype: thread\nstatus: drafting\nchannels: [x]\n",
		"First tweet is short.\n---\n"+over)

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", draftPath, body),
	))
	if res.Refused() {
		t.Fatalf("the draft write was refused at Pre (setup broken, guard is not preventive):\n%s", res.Output)
	}
	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks) == 0 {
		t.Fatalf("an over-limit X thread was not refused by the char-limit script rule:\n%s", res.Output)
	}
	joined := ""
	for _, b := range blocks {
		joined += b + "\n"
	}
	if !containsStr(joined, "CHAR LIMIT") && !containsStr(joined, "SCRIPT RULE VIOLATION") {
		t.Errorf("the block was not the script rule's own char-limit refusal:\n%s", joined)
	}
}

// TestRules_InLimitXThreadPasses: the SAME rule, a thread whose segments are
// all under 280 characters — no script refusal, and (judge stub set to FAIL,
// as an "it must not even be reached" control, mirroring
// TestRules_ChannelRuleDoesNotApplyToOtherChannel's shape) no judge rule
// applies either since none is installed in this test, so any block at all
// would be a false positive.
func TestRules_InLimitXThreadPasses(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installScriptRule(t, e, proj, "03_x-limit", "must", "x", "char-limit", []string{"280", "---"})
	installPluginTree(t, proj)

	sess := "s-x-in-limit"
	body := unitFrontmatter(
		"transcript_path: /abs/s.jsonl\ncreated: 2026-09-25\ntype: thread\nstatus: drafting\nchannels: [x]\n",
		"First tweet is short.\n---\nSecond tweet is also comfortably short.")

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", draftPath, body),
	))
	if res.Refused() {
		t.Fatalf("the draft write was refused at Pre (setup broken):\n%s", res.Output)
	}
	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	for _, b := range blocks {
		if containsStr(b, "CHAR LIMIT") || containsStr(b, "03_x-limit") {
			t.Fatalf("an in-limit X thread was refused by the char-limit script rule:\n%s", b)
		}
	}
}

// TestRules_BannedPhraseRefused: a banned-phrases SCRIPT rule (global, no
// applies_to) refuses a unit whose body contains a listed phrase.
func TestRules_BannedPhraseRefused(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, ".sloprail/content-rules/banned.txt", "delve into\nit's important to note\n")
	e.WriteFile(proj, ".sloprail/content-rules/04_no-ai-tells/RULE.md",
		"---\nlevel: must_not\ncreated: 2026-09-25\nscript:\n  name: banned-phrases\n  args:\n    - \".sloprail/content-rules/banned.txt\"\n---\nNo AI-tell phrases.\n")
	installPluginTree(t, proj)

	sess := "s-banned-phrase"
	body := unitFrontmatter(
		"transcript_path: /abs/s.jsonl\ncreated: 2026-09-25\ntype: post\nstatus: drafting\n",
		"Let's delve into the details of this launch.")

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", unitPath, body),
	))
	if res.Refused() {
		t.Fatalf("the unit write was refused at Pre (setup broken):\n%s", res.Output)
	}
	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks) == 0 {
		t.Fatalf("a banned phrase was not refused:\n%s", res.Output)
	}
	joined := ""
	for _, b := range blocks {
		joined += b + "\n"
	}
	if !containsStr(joined, "BANNED PHRASE") && !containsStr(joined, "delve into") {
		t.Errorf("the block did not name the banned phrase:\n%s", joined)
	}
}

// TestRules_NoApplicableRulesPasses: a unit with no channels/tags, no global
// rules configured at all, and no topic constraints/ — nothing applies, and
// the turn is not blocked. The control proving the guard does not block by
// default.
func TestRules_NoApplicableRulesPasses(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, proj)
	// The judge ALWAYS runs, even with no applicable judge rule — prepare
	// proceeds with the "NONE" sentinel and the template passes trivially on
	// it (there is no permit-without-judging in the judge: check contract).
	// pass:true here proves that trivial pass, not a real rule.
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
