package e2e

import "testing"

// T045_09/10: the `transcript_path=<path>` reference form — the third of the
// "three reference forms" the README and has-message-reference.sh's own regex
// advertise (transcript_path, message_id, jsonl:), alongside message_id (T045_07/
// 08) and jsonl: (T045_01/03). resolve-referenced-message.sh's case statement had
// NO arm for transcript_path=* at all: has-message-reference.sh's regex matches
// it and lets the write through to the judge tier, but the prepare script's `*)`
// fallback silently resolved every transcript_path= reference to "" — regardless
// of whether the named transcript, or a message in it, genuinely existed. Fixed
// to resolve the FIRST user message in the named transcript file (the "cite a
// whole other transcript, not a range within it" shape jsonl:N alone already has
// for a single line).
//
// A transcript_path= reference names a DIFFERENT session's record — cited here by
// running a first turn under its own session id (sessionA) to seed a real,
// distinct human message on disk, then referencing that session's transcript path
// from a second turn's ASK.md.

const priorSessionPrompt = "Rotate the signing keys before the certificate expires."

// T045_09: transcript_path= naming a REAL prior transcript resolves to that
// transcript's own human message and admits.
func TestT045_09_TranscriptPathReferenceResolvesAndAdmits(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj)

	// First turn: an unrelated session, seeding a real transcript with a
	// distinct human message this test can find resolved in the judge prompt.
	priorSess := "s-045-09-prior"
	e.Run(proj, priorSess, priorSessionPrompt, Turns("done"))
	priorTranscript := e.TranscriptPath(proj, priorSess)

	e.InstallJudgeClaudeCapturing(proj, "judge-prompt.txt", `{"pass": true, "reasoning": ""}`)

	sess := "s-045-09"
	ref := "transcript_path=" + priorTranscript
	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", askPath, ref+"\n"+priorSessionPrompt),
	))

	if res.Refused() {
		t.Fatalf("a faithful ASK.md citing a real prior transcript by transcript_path was refused:\n%s", res.Output)
	}
	if !e.Exists(proj, askPath) {
		t.Errorf("an admitted transcript_path-referenced ASK.md write did not land on disk")
	}
	prompt := e.JudgePrompt(proj, "judge-prompt.txt")
	if prompt == "" {
		t.Fatalf("the judge never ran, so transcript_path resolution was not exercised")
	}
	if !containsStr(prompt, "Rotate the signing keys") {
		t.Errorf("the transcript_path reference did not resolve to the prior session's human message in the template:\n%s", prompt)
	}
	if containsStr(prompt, "reference did not resolve") {
		t.Errorf("the transcript_path reference was treated as unresolvable despite naming a real transcript:\n%s", prompt)
	}
}

// T045_10: transcript_path= naming a transcript file that does not exist is
// refused with the template's unresolved-reference branch — the transcript_path
// counterpart to T045_08's unknown message_id.
func TestT045_10_UnknownTranscriptPathBlocks(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj)
	e.InstallJudgeClaudeCapturing(proj, "judge-prompt.txt", `{"pass": false, "reasoning": "the transcript_path names no real record"}`)

	res := e.Run(proj, "s-045-10", authPrompt, Turns("done",
		Write("w1", askPath, "transcript_path=/nonexistent/nowhere.jsonl\nRotate the signing keys."),
	))

	if !res.Refused() {
		t.Fatalf("an ASK.md whose transcript_path names no real file was not refused:\n%s", res.Output)
	}
	if e.Exists(proj, askPath) {
		t.Errorf("the preventive guard let an unresolvable-transcript_path ASK.md land on disk")
	}
	prompt := e.JudgePrompt(proj, "judge-prompt.txt")
	if prompt == "" {
		t.Fatalf("the judge never ran, so the unresolved-transcript_path branch was not exercised")
	}
	if !containsStr(prompt, "reference did not resolve") {
		t.Errorf("the template did not render its unresolved-reference branch on an unknown transcript_path:\n%s", prompt)
	}
}
