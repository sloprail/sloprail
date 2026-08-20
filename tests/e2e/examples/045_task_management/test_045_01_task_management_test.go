package e2e

import (
	"testing"
)

// task-management is a PREVENTIVE file-guard over `**/tasks/*/*/ASK.md` with two
// checks in order: a cheap script (must carry a reference to a human message at
// all) and, only once a reference exists, a prepare + judge (resolve the referenced
// message and rule that the content is TRUE to it and holds THAT AND NOTHING ELSE).
// Being preventive, a not-fine write is refused at PRE-tool, before it lands.
//
// The scenarios prove: content with NO reference is refused by the SCRIPT tier
// before any model is asked (deterministic first cut); content whose reference
// resolves but whose text the judge rejects BLOCKS at pre-tool and the judge's
// reasoning reaches the agent (the judge tier); a reference that does NOT resolve
// is refused with the template's unresolved-reference branch; a faithful ASK.md
// ADMITS and the write lands (happy); the resolved human-message text reaches the
// rendered template and changes with the reference (prepare -> template wiring); a
// write the match does not select (a RESULT.md, or a non-ASK path) is never judged;
// and the preventive guard keeps a not-fine write OFF DISK.
//
// The reference form used for the resolvable cases is `jsonl:1-1` — line 1 of the
// session record is the human's own prompt (the seeded root), which the prepare
// resolves via `sed`+`jq` cleanly. (The `message_id=<uuid>` form the example also
// supports crashes the prepare on a plain-string user message — the same
// string-content jq fragility action-proof hit — so these tests use the jsonl form
// that resolves; see the report.)

const askPath = "memories/tasks/auth/001/ASK.md"

// authPrompt is the human message the session starts from; jsonl:1-1 resolves to
// it. Distinctive so a test can find the resolved text in the rendered prompt.
const authPrompt = "Please migrate the auth module to the new token format."

// T045_01: ASK.md content carrying NO reference is refused by the SCRIPT tier,
// before the judge — the deterministic first cut.
//
// The write is preventive, so this refuses at pre-tool and the file never lands.
// The stub is set to PASS: if the engine ever reached the judge here (it must not —
// the script refuses first), or admitted, the write would go through. It does not.
func TestT045_01_NoReferenceRefusedByScript(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	res := e.Run(proj, "s-045-01", authPrompt, Turns("done",
		Write("w1", askPath, "Migrate the auth module. (no reference to any human message)"),
	))

	if !res.Refused() {
		t.Fatalf("ASK.md content with no human-message reference was not refused:\n%s", res.Output)
	}
	if e.Exists(proj, askPath) {
		t.Errorf("the preventive guard let a reference-less ASK.md land on disk")
	}
	// The refusal is the SCRIPT tier's own reason (naming the missing reference),
	// not a judge verdict.
	if !res.Saw("must carry a reference to the human message") {
		t.Errorf("the refusal was not the script tier's missing-reference reason:\n%s", res.Output)
	}
}

// T045_02: ASK.md whose reference RESOLVES but whose content the judge rejects
// BLOCKS at pre-tool, and the judge's reasoning reaches the agent — the judge tier.
//
// The content carries a valid jsonl:1-1 reference, so the script tier passes and
// the prepare resolves the human message; the judge (stub pass:false) then refuses
// with the reasoning the rule would give ("and nothing else" violated). The write
// is preventive, so it is denied before it lands.
func TestT045_02_ResolvedButRejectedByJudgeBlocks(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj)
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "the ASK wraps the reference in agent-authored scope the human never asked for"}`)

	res := e.Run(proj, "s-045-02", authPrompt, Turns("done",
		Write("w1", askPath, "jsonl:1-1\nMigrate the auth module — and also refactor logging, add metrics, and write docs."),
	))

	if !res.Refused() {
		t.Fatalf("an ASK.md the judge rejected was not refused at pre-tool:\n%s", res.Output)
	}
	if e.Exists(proj, askPath) {
		t.Errorf("the preventive guard let a judge-rejected ASK.md land on disk")
	}
	if !res.Saw("agent-authored scope the human never asked for") {
		t.Errorf("the judge's reasoning did not reach the agent:\n%s", res.Output)
	}
}

// T045_03: a reference that does NOT resolve is refused with the template's
// unresolved-reference branch.
//
// The content carries a well-formed jsonl reference (so the script tier passes),
// but names a line range the record has no user message at — jsonl:999-999. The
// prepare reports resolved:false, the template takes its "The reference did not
// resolve — treat this as a fail" branch, and the judge (stub pass:false) refuses.
// This is what proves the prepare's resolved/unresolved distinction reaches the
// template, not merely that some reference existed.
func TestT045_03_UnresolvableReferenceBlocks(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj)
	e.InstallJudgeClaudeCapturing(proj, "judge-prompt.txt", `{"pass": false, "reasoning": "the reference resolves to no message"}`)

	res := e.Run(proj, "s-045-03", authPrompt, Turns("done",
		Write("w1", askPath, "jsonl:999-999\nMigrate the auth module."),
	))

	if !res.Refused() {
		t.Fatalf("an ASK.md whose reference does not resolve was not refused:\n%s", res.Output)
	}
	prompt := e.JudgePrompt(proj, "judge-prompt.txt")
	if prompt == "" {
		t.Fatalf("the judge never ran, so the unresolved-reference branch was not exercised")
	}
	// The template rendered its unresolved branch — the prepare's resolved:false
	// reached it.
	if !containsStr(prompt, "reference did not resolve") {
		t.Errorf("the template did not render its unresolved-reference branch on a non-resolving reference:\n%s", prompt)
	}
}

// T045_04: a faithful ASK.md ADMITS and the write LANDS — the happy path and the
// control for every block above.
//
// A valid jsonl:1-1 reference (script passes), a resolvable human message (prepare
// resolves it), and a judge that accepts (stub pass:true). The preventive guard
// admits the write, so the file lands. Without this a guard that refused every
// ASK.md would pass the block tests while being broken.
func TestT045_04_FaithfulAskAdmitsAndLands(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	res := e.Run(proj, "s-045-04", authPrompt, Turns("done",
		Write("w1", askPath, "jsonl:1-1\n"+authPrompt),
	))

	if res.Refused() {
		t.Fatalf("a faithful ASK.md was refused:\n%s", res.Output)
	}
	if !e.Exists(proj, askPath) {
		t.Errorf("an admitted ASK.md write did not land on disk")
	}
}

// T045_05: the resolved human-message text reaches the rendered template — the
// prepare -> template wiring, proven directly and shown to follow the transcript.
//
// The prepare resolves jsonl:1-1 to the session's own first user message and hands
// it to the template as additionalContext.referenced_message. A stubbed verdict
// cannot show this (undefined renders empty), so the capturing shim records the
// prompt and the test asserts the ACTUAL human prompt text appears in it — and that
// a session started from a DIFFERENT prompt renders that different text, so the
// prepare is resolving from the real record, not echoing a fixed string.
func TestT045_05_ResolvedMessageReachesTemplate(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj)
	e.InstallJudgeClaudeCapturing(proj, "judge-prompt.txt", `{"pass": false, "reasoning": "x"}`)

	e.Run(proj, "s-045-05a", authPrompt, Turns("done",
		Write("w1", askPath, "jsonl:1-1\n"+authPrompt),
	))

	prompt := e.JudgePrompt(proj, "judge-prompt.txt")
	if prompt == "" {
		t.Fatalf("the judge never ran, so nothing about the wiring can be concluded")
	}
	// The resolved human message (pulled from line 1 of the record) reached the
	// template.
	if !containsStr(prompt, "migrate the auth module to the new token format") {
		t.Errorf("the resolved human message did not reach the template:\n%s", prompt)
	}

	// A DIFFERENT starting prompt, fresh session: the resolved text must follow it.
	other := "Add rate limiting to the public API gateway."
	e2 := New(t)
	proj2 := e2.Project()
	e2.GitInit(proj2)
	installExampleTree(t, proj2)
	e2.InstallJudgeClaudeCapturing(proj2, "judge-prompt.txt", `{"pass": false, "reasoning": "x"}`)

	e2.Run(proj2, "s-045-05b", other, Turns("done",
		Write("w1", askPath, "jsonl:1-1\n"+other),
	))

	prompt2 := e2.JudgePrompt(proj2, "judge-prompt.txt")
	if prompt2 == "" {
		t.Fatalf("the judge never ran for the second session")
	}
	if !containsStr(prompt2, "rate limiting to the public API gateway") {
		t.Errorf("the second session's resolved message did not reach the template:\n%s", prompt2)
	}
	if containsStr(prompt2, "migrate the auth module") {
		t.Errorf("the template carried the previous session's resolved message — the prepare is not resolving from the real record:\n%s", prompt2)
	}
}

// T045_06: a write the match does not select is never judged — the does-not-fire
// control.
//
// The guard binds `**/tasks/*/*/ASK.md` specifically; the unit is explicit that the
// RESULT is not guarded, only the ask. So a RESULT.md in the same task folder, and
// an ordinary file outside tasks/, must be left alone — even carrying no reference
// (which would fail the script tier) and with the judge stubbed to FAIL. Any
// firing would block; neither does.
func TestT045_06_NonAskWritesAreNeverJudged(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj)
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "would block if it fired"}`)

	res := e.Run(proj, "s-045-06", authPrompt, Turns("done",
		// The RESULT of the same task — an agent MAY author this; not guarded.
		Write("w1", "memories/tasks/auth/001/RESULT.md", "Implemented the token migration. (no reference, agent-authored)"),
		// A plain file outside tasks/.
		Write("w2", "memories/notes/scratch.md", "a scratch note with no reference at all"),
	))

	if res.Refused() {
		t.Fatalf("a non-ASK write was refused by the ask-only guard:\n%s", res.Output)
	}
	if !e.Exists(proj, "memories/tasks/auth/001/RESULT.md") || !e.Exists(proj, "memories/notes/scratch.md") {
		t.Errorf("the non-ASK writes did not land at all")
	}
}
