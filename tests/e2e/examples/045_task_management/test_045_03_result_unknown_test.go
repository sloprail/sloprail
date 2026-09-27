package e2e

import "testing"

// T045_11: a command-derived edit to ASK.md whose resulting bytes this engine
// cannot predict.
//
// `ask-is-human-authored` is PREVENTIVE, and for a preventive guard the engine
// refuses an underivable Pre write before its checks run — but evaluates `require`
// first (nature_fileguard.go's isUnderivablePreWrite branch), because an unmet
// prerequisite names the actual fix. `sed -i` carries no citation, so the refusal
// is the citation requirement's, naming ASK.md and the sr-file form. sed's
// transformation is not modeled, so filemod emits PreFileUpdate with
// resultKnown:false; ASK.md must already exist (sed -i edits).
func TestT045_11_CommandDerivedEditRefusedAsUnderivable(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, askPath, authPrompt+"\n")
	installExampleTree(t, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	res := e.Run(proj, "s-045-11", authPrompt, Turns("done",
		Bash("b1", "sed -i.bak s/migrate/MIGRATE/ "+askPath),
	))

	if !res.Refused() {
		t.Fatalf("a command-derived (resultKnown:false) edit to ASK.md was not refused:\n%s", res.Output)
	}
	if !res.Saw("must be grounded in a citation") || !res.Saw(askPath) || !res.Saw("sr-file edit") {
		t.Errorf("the refusal is not the citation requirement's, naming ASK.md and the sr-file form:\n%s", res.Output)
	}
}
