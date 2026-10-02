package e2e

import (
	"strings"
	"testing"
)

// content-rule-is-grounded is a PreFileWrite gate plus a file-guard (the judge, at Stop over the committed changeset) over RULE.md/
// CONSTRAINT.md. Every change must be grounded in the user's own words,
// cited on the ACTION (`sr-file write|edit ... --cite:user '<quote>'`), never
// stored in the rule:
//
//  0. REQUIRE (citation, user pool): a change carrying no citation that
//     resolves — the Write tool, or a quote the user never said — is refused
//     before any check runs, and the refusal names sr-file.
//  1. SCRIPT (check-rule.sh): the frontmatter satisfies rule.cue and the body
//     is not empty.
//  2. PREPARE + JUDGE (resolve-cited-rule-quotes.sh + judge-rule-body.md.j2):
//     the judge is handed the cited quotes off changeset.citations (the commit's Sloprail-Cites-User trailers) and decides
//     whether the rule holds THAT AND NOTHING ELSE. The verdict is stubbed.
//
// An earlier version required a `[quote](jsonl)` link in the rule's body and
// grounded it in the script; that is gone (a transcript path does not resolve
// on another machine). Old rules that still carry such a link are neither
// required nor refused — TestRuleGrounded_LegacyLinkRuleEditPasses.

// rulePrompt is the user's own message in every rule scenario; ruleQuote is
// the words of it a grounded change cites.
const (
	rulePrompt = "Add a writing rule for every unit: no hype language."
	ruleQuote  = "no hype language"
	rulePath   = ".sloprail/content-rules/01_tone/RULE.md"
	ruleBody   = "---\nlevel: must_not\ncreated: 2026-09-25\n---\nRule: no hype language.\n"
)

// installRuleProject stands up a project with the plugin installed. seed, when
// non-empty, is a rule already on disk at rulePath, committed into the
// session baseline with the plugin tree.
func installRuleProject(t *testing.T, seed string) (*Env, string) {
	t.Helper()
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	if seed != "" {
		e.WriteFile(proj, rulePath, seed)
		// History before the rules exist (see installPublishProject).
		e.CommitAll(proj, "the seeded rule")
	}
	installPluginTree(t, proj)
	installPluginStructure(t, e, proj)
	e.CommitAll(proj, "install the rules")
	return e, proj
}

// TestRuleGrounded_UncitedWriteRefused: a rule written with the Write tool
// carries no citation, so require refuses it before any check runs — and the
// refusal tells the agent the grounded way (sr-file ... --cite:user). The
// judge stub is PASS: reaching it would have let the write through, so a
// refusal proves the require, not the judge, decided.
func TestRuleGrounded_UncitedWriteRefused(t *testing.T) {
	e, proj := installRuleProject(t, "")
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	res := e.Run(proj, "s-rule-uncited", rulePrompt, Turns("done",
		Write("w1", rulePath, ruleBody),
	))
	if !res.Refused() {
		t.Fatalf("an uncited rule write was not refused:\n%s", res.Output)
	}
	if e.Exists(proj, rulePath) {
		t.Errorf("the gate let an uncited rule land on disk")
	}
	if !res.Saw("sr-file") || !res.Saw("--cite:user") {
		t.Errorf("the refusal does not tell the agent to ground the change with sr-file --cite:user:\n%s", res.Output)
	}
}

// TestRuleGrounded_UnresolvedQuoteRefused: sr-file is used, but the quote is
// words the user never said — it resolves to nothing, so the change carries
// no citation and is refused. The quote must actually be the user's.
func TestRuleGrounded_UnresolvedQuoteRefused(t *testing.T) {
	e, proj := installRuleProject(t, "")
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	res := e.Run(proj, "s-rule-unresolved", rulePrompt, Turns("done",
		Bash("b1", srFileWrite(rulePath, ruleBody, "nobody ever actually said this")),
	))
	if !res.Refused() {
		t.Fatalf("a rule citing words the user never said was not refused:\n%s", res.Output)
	}
	if e.Exists(proj, rulePath) {
		t.Errorf("the gate let an ungrounded rule land on disk")
	}
}

// TestRuleGrounded_CitedWritePasses: a rule written with sr-file citing the
// user's own words, which the judge accepts, lands — and the judge was handed
// the cited quote as written (off changeset.citations), not a link parsed out of the body. The rule file
// itself carries no transcript link.
func TestRuleGrounded_CitedWritePasses(t *testing.T) {
	e, proj := installRuleProject(t, "")
	e.InstallJudgeClaudeCapturing(proj, judgePromptFile, `{"pass": true, "reasoning": ""}`)

	sess := "s-rule-ok"
	res := e.Run(proj, sess, rulePrompt, Turns("done",
		Bash("b1", srFileWrite(rulePath, ruleBody, ruleQuote)),
	).ThenCommit("Add the rule", CitesUser(ruleQuote)))
	if res.Refused() {
		t.Fatalf("a cited, judge-accepted rule was refused:\n%s", res.Output)
	}
	if !e.Exists(proj, rulePath) {
		t.Fatalf("an admitted rule write did not land on disk:\n%s", res.Output)
	}
	prompt := e.JudgePrompt(proj, judgePromptFile)
	if prompt == "" {
		t.Fatalf("the judge never ran on a cited rule write:\n%s", res.Output)
	}
	if !strings.Contains(prompt, "<citations>") || !strings.Contains(prompt, "<quote>"+ruleQuote) {
		t.Errorf("the judge was not handed the cited quote %q:\n%s", ruleQuote, prompt)
	}
	if strings.Contains(prompt, ".jsonl") {
		t.Errorf("the judge was handed a session-record path; it judges committed bytes and the quote as written:\n%s", prompt)
	}
	if body := readProj(t, proj, rulePath); strings.Contains(body, ".jsonl") {
		t.Errorf("the rule file carries a transcript link; it should hold derived text only:\n%s", body)
	}
}

// TestRuleGrounded_SlopBodyBlockedByJudgeAtStop: the change is cited, but the body
// ALSO carries agent-authored elaboration the user never asked for — the "and
// nothing else" violation. The gate (citation + script, no model) admits and the
// rule lands; the judge (stub pass:false) lives in the file-guard and blocks the
// turn at Stop, and its reasoning reaches the agent.
func TestRuleGrounded_SlopBodyBlockedByJudgeAtStop(t *testing.T) {
	e, proj := installRuleProject(t, "")
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "RULE BODY: the body adds a threshold and an exception the user never stated"}`)

	slop := "---\nlevel: must_not\ncreated: 2026-09-25\n---\nRule: no hype language. This applies to every unit except drafts under 50 words, which may use up to two hype phrases.\n"
	res := e.Run(proj, "s-rule-slop", rulePrompt, Turns("done",
		Bash("b1", srFileWrite(rulePath, slop, ruleQuote)),
	).ThenCommit("Add the rule", CitesUser(ruleQuote)))
	if res.Refused() {
		t.Fatalf("the gate (no model) refused a cited, well-formed rule:\n%s", res.Output)
	}
	if !e.Exists(proj, rulePath) {
		t.Errorf("the cited rule did not land: a gate holds no judge")
	}
	blocks := strings.Join(e.BlockingErrorsFrom(proj, "s-rule-slop", "Stop"), "\n")
	if !strings.Contains(blocks, "threshold and an exception the user never stated") {
		t.Errorf("the judge's reasoning did not block the turn at Stop:\n%s", blocks)
	}
}

// TestRuleGrounded_InvalidFrontmatterRefusedByScript: a cited rule whose
// frontmatter violates rule.cue (a level the schema does not allow) is
// refused by the script stage, before the judge (stub PASS).
func TestRuleGrounded_InvalidFrontmatterRefusedByScript(t *testing.T) {
	e, proj := installRuleProject(t, "")
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	bad := "---\nlevel: should\ncreated: 2026-09-25\n---\nRule: no hype language.\n"
	res := e.Run(proj, "s-rule-badfm", rulePrompt, Turns("done",
		Bash("b1", srFileWrite(rulePath, bad, ruleQuote)),
	))
	if !res.Refused() {
		t.Fatalf("a rule with invalid frontmatter was not refused:\n%s", res.Output)
	}
	if !res.Saw("RULE FRONTMATTER INVALID") {
		t.Errorf("the refusal was not the script's frontmatter reason:\n%s", res.Output)
	}
}

// TestRuleGrounded_LegacyLinkRuleEditPasses: a rule written before the
// migration still carries a transcript link in its body. Editing it with a
// cited sr-file edit is permitted — the old link is neither required nor
// refused — and the judge is handed the change's diff, so it weighs what the
// edit added rather than the carried-over text.
func TestRuleGrounded_LegacyLinkRuleEditPasses(t *testing.T) {
	legacy := "---\nlevel: must_not\ncreated: 2026-01-01\n---\nRule: no hype. The user said [no hype](/Users/someone/.claude/projects/x/abc.jsonl:42).\n"
	e, proj := installRuleProject(t, legacy)
	e.InstallJudgeClaudeCapturing(proj, judgePromptFile, `{"pass": true, "reasoning": ""}`)

	res := e.Run(proj, "s-rule-legacy", rulePrompt, Turns("done",
		Bash("b1", srFileEdit(rulePath, "Rule: no hype.", "Rule: no hype language.", ruleQuote)),
	).ThenCommit("Reword the rule", CitesUser(ruleQuote)))
	if res.Refused() {
		t.Fatalf("a cited edit of a rule carrying a legacy link was refused:\n%s", res.Output)
	}
	if body := readProj(t, proj, rulePath); !strings.Contains(body, "Rule: no hype language.") {
		t.Fatalf("the cited edit did not apply:\n%s", body)
	}
	prompt := e.JudgePrompt(proj, judgePromptFile)
	if !strings.Contains(prompt, "-Rule: no hype. The user said") || !strings.Contains(prompt, "+Rule: no hype language.") || !strings.Contains(prompt, ruleQuote) {
		t.Errorf("the judge of an update was not handed the change and the cited quote:\n%s", prompt)
	}
}
