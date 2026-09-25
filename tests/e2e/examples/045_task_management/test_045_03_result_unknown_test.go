package e2e

import "testing"

// T045_11: a command-derived edit to ASK.md whose resulting bytes this engine
// cannot predict.
//
// `ask-is-human-authored` is a PREVENTIVE file-guard, and for a preventive guard
// the ENGINE ITSELF refuses any underivable Pre write before dispatch ever
// reaches a guard's own checks — see nature_fileguard.go's isUnderivablePreWrite
// gate ahead of runner.Run. So has-message-reference.sh's own `resultKnown`
// branch (added alongside the has("newContent") dead-branch fix, since
// has("newContent") is always true on Pre* and so could never fire as written)
// can never actually run on THIS guard: the engine's universal preventive-guard
// safety net always wins the race. That branch is real defense in depth — correct
// to keep, since a check script must not trust its caller unconditionally — but
// this test proves the refusal an agent actually sees here is the engine's
// generic "could not verify this write before it lands" message, not the
// script's "could not predict" wording; both fixes described in the audit stay,
// but neither script's resultKnown branch is reachable through this preventive
// guard, only through direct invocation or a future non-preventive guard reusing
// these scripts.
//
// `sed -i` is the proven command-derived-update vehicle (see
// tests/e2e/pre_tool/021_command_changes_a_file's T021_08 and
// internal/filemod/extract_command_test.go's TestExtractCommand_SedInPlace...):
// sed's transformation is not modeled, so filemod emits PreFileUpdate with
// resultKnown:false rather than guess. ASK.md must already exist on disk (sed -i
// edits, it does not create) — this test is about the underivable-write path, not
// the missing-reference path T045_01 already covers.
func TestT045_11_CommandDerivedEditRefusedAsUnderivable(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	e.WriteFile(proj, askPath, "jsonl:1-1\n"+authPrompt)

	res := e.Run(proj, "s-045-11", authPrompt, Turns("done",
		Bash("b1", "sed -i.bak s/migrate/MIGRATE/ "+askPath),
	))

	if !res.Refused() {
		t.Fatalf("a command-derived (resultKnown:false) edit to ASK.md was not refused:\n%s", res.Output)
	}
	if !res.Saw("could not verify this write before it lands") {
		t.Errorf("the refusal was not the engine's preventive-guard underivable-write refusal:\n%s", res.Output)
	}
	if res.Saw("must carry a reference") {
		t.Errorf("the resultKnown:false write was refused for the WRONG reason (missing reference, as if newContent were read as empty real content) rather than for being underivable:\n%s", res.Output)
	}
}
