package e2e

import "testing"

// content-rule-is-grounded is a PREVENTIVE file-guard over RULE.md/
// CONSTRAINT.md. Two independent checks:
//
//   1. At least one transcript_paths entry must GROUND to a real user
//      message (via cite --source-types user, over the LINE itself as the
//      quote).
//   2. A SCRIPT rule's named script must contain a real conditional refusal
//      path — an unconditional exit 0 is refused as a trivial no-op.
//
// These prove: a rule with no transcript_paths at all is refused; a script
// rule naming an unconditionally-permitting script is refused; and a
// grounded judge rule (citing the session's own real line-1 user prompt)
// with a real script (this plugin's own shipped char-limit.sh, which
// genuinely refuses) passes.

// TestRuleGrounded_NoTranscriptPathsRefused: a RULE.md with no
// transcript_paths at all is refused before it lands.
func TestRuleGrounded_NoTranscriptPathsRefused(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, proj)

	sess := "s-rule-noorigin"
	rule := "---\nlevel: must_not\ncreated: 2026-09-25\n---\nRule: no hype language.\n"

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", ".sloprail/content-rules/01_tone/RULE.md", rule),
	))
	if !res.Refused() {
		t.Fatalf("a rule with no transcript_paths was not refused:\n%s", res.Output)
	}
	if e.Exists(proj, ".sloprail/content-rules/01_tone/RULE.md") {
		t.Errorf("the preventive guard let an ungrounded rule land on disk")
	}
	if !res.Saw("RULE NOT GROUNDED") {
		t.Errorf("the refusal was not the grounding check's own reason:\n%s", res.Output)
	}
}

// TestRuleGrounded_UngroundedTranscriptPathRefused: transcript_paths names a
// real line in the session transcript, but that line is NOT a user message
// (it is the harness's own auth-prompt line cited at a line number that does
// not correspond to any real entry — the fabricated-path shape) — refused,
// proving the entry must actually resolve, not merely be present.
func TestRuleGrounded_UngroundedTranscriptPathRefused(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, proj)

	sess := "s-rule-badorigin"
	rule := "---\nlevel: must_not\ncreated: 2026-09-25\ntranscript_paths: [\"/nonexistent/session.jsonl:1\"]\n---\nRule: no hype language.\n"

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", ".sloprail/content-rules/01_tone/RULE.md", rule),
	))
	if !res.Refused() {
		t.Fatalf("a rule citing a nonexistent transcript was not refused:\n%s", res.Output)
	}
	if e.Exists(proj, ".sloprail/content-rules/01_tone/RULE.md") {
		t.Errorf("the preventive guard let a rule with an unresolvable citation land on disk")
	}
}

// TestRuleGrounded_TrivialScriptRefused: a script rule names a script that
// unconditionally exits 0 (no refuse() call, no conditional non-zero exit
// anywhere) — refused as incapable of ever refusing, even though its
// transcript_paths grounds for real.
func TestRuleGrounded_TrivialScriptRefused(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, proj)
	e.WriteExecutable(proj, ".sloprail/content-rules/scripts/always-permit.sh", "#!/usr/bin/env bash\ncat >/dev/null\nexit 0\n")

	sess := "s-rule-trivialscript"
	tp := e.TranscriptPath(proj, sess)
	// Run once first so the transcript exists on disk with the real root
	// prompt before this rule write cites it (the mock's preamble means the
	// root prompt is NOT physical line 1 — see RootMessageLine).
	e.Run(proj, sess, authPrompt, Turns("done"))
	line := e.RootMessageLine(sess)
	rule := "---\nlevel: must\ncreated: 2026-09-25\ntranscript_paths: [\"" + tp + ":" + itoa(line) + "\"]\nscript:\n  name: always-permit\n---\nTrivial no-op.\n"

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", ".sloprail/content-rules/02_trivial/RULE.md", rule),
	))
	if !res.Refused() {
		t.Fatalf("a script rule naming an unconditionally-permitting script was not refused:\n%s", res.Output)
	}
	if e.Exists(proj, ".sloprail/content-rules/02_trivial/RULE.md") {
		t.Errorf("the preventive guard let a trivial script rule land on disk")
	}
	if !res.Saw("TRIVIAL SCRIPT RULE") {
		t.Errorf("the refusal was not the script-triviality check's own reason:\n%s", res.Output)
	}
}

// TestRuleGrounded_GroundedRuleWithRealScriptPasses: transcript_paths cites
// line 1 of the session's OWN transcript, which the harness seeds as the
// real user prompt (authPrompt) — grounds for real via cite, over the line's
// own text — and script.name points at this plugin's own char-limit.sh,
// which genuinely contains a conditional refusal path. The write lands.
func TestRuleGrounded_GroundedRuleWithRealScriptPasses(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, proj)

	sess := "s-rule-ok"
	tp := e.TranscriptPath(proj, sess)
	// Run once first so the transcript exists on disk with the real root
	// prompt before this rule write cites it — mirrors the tasks suite's own
	// two-run shape for citing something that must already exist. The mock's
	// preamble means the root prompt is NOT physical line 1 (RootMessageLine).
	e.Run(proj, sess, authPrompt, Turns("done"))
	line := e.RootMessageLine(sess)

	rule := "---\nlevel: must\ncreated: 2026-09-25\ntranscript_paths: [\"" + tp + ":" + itoa(line) + "\"]\napplies_to:\n  channels: [x]\nscript:\n  name: char-limit\n  args: [\"280\"]\n---\nX character limit.\n"

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", ".sloprail/content-rules/03_x-limit/RULE.md", rule),
	))
	if res.Refused() {
		t.Fatalf("a grounded rule with a real (non-trivial) script was refused:\n%s", res.Output)
	}
	if !e.Exists(proj, ".sloprail/content-rules/03_x-limit/RULE.md") {
		t.Errorf("an admitted rule write did not land on disk")
	}
}
