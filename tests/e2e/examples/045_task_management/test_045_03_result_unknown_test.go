package e2e

import "testing"

// T045_11: a command-derived edit to ASK.md whose resulting bytes this engine
// cannot predict.
//
// `ask-is-human-authored` is a gate; the engine evaluates `require` before any
// check, because an unmet prerequisite names the actual fix (the gate's own
// require-known-result.sh check would otherwise refuse an underivable result). `sed -i` carries no citation, so the refusal
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
	).ThenCommit("write the files"))

	if !res.Refused() {
		t.Fatalf("a command-derived (resultKnown:false) edit to ASK.md was not refused:\n%s", res.Output)
	}
	if !res.Saw("must cite the user's own words") || !res.Saw(askPath) || !res.Saw("sr-file edit") {
		t.Errorf("the refusal is not the citation requirement's, naming ASK.md and the sr-file form:\n%s", res.Output)
	}
}

// T045_12: a cited sr-file write followed by another command on the same line has a result the
// engine cannot work out, and the refusal says the fix plainly: sr-file alone in its own Bash
// call, nothing after it. Agents read "cannot be checked" as "this edit is not allowed".
// Run alone, the same write lands.
func TestT045_12_SrFileBesideAnotherCommandSaysToRunItAlone(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	write := "sr-file write " + askPath + " --cite:user 'migrate the auth module' <<'EOF'\n" + authPrompt + "\nEOF"
	res := e.Run(proj, "s-045-12", authPrompt, Turns("done",
		Bash("b1", write+"\ncat "+askPath),
	))
	if e.Exists(proj, askPath) {
		t.Fatalf("the write beside another command landed:\n%s", res.Output)
	}
	if !res.Saw("cannot be checked") || !res.Saw("Run sr-file alone in its own Bash call, with nothing before or after it on the line") {
		t.Fatalf("the refusal does not say to run sr-file alone in its own call:\n%s", res.Output)
	}

	e.Run(proj, "s-045-12", "again", Turns("done", Bash("b2", write)))
	if !e.Exists(proj, askPath) {
		t.Fatalf("the same write, run alone, did not land")
	}
}
