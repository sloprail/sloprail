package e2e

// TODO(D3): drive verdict via a10n-claude-mock once a10n-cli#470 lands + new mock
// on PATH; today InstallJudgeClaude supplies the verdict (though the judge is not
// reached in this test — the script refuses on the ambiguous cite first).
//
// The AMBIGUOUS-ASK branch (cite exit code 2). The shipped
// removal-has-a-grounded-ask.sh branches `sr-session trajectory cite` rc 0/1/2/other
// with distinct reasons: rc0 admits (T049_08), rc1 refuses "resolves to nothing"
// (T049_10), and rc2 refuses "matches SEVERAL user messages … ambiguous". This
// closes rc2: a quote that appears in TWO separate user messages makes cite return
// two matches, and the script must refuse with the ambiguity reason.
//
// cite counts one match per USER ENTRY that contains the quote (internal/transcript
// Cite iterates user entries by line), so two matches require two user entries. The
// harness seeds ONE root user entry from the prompt; this test pre-seeds the session
// transcript with TWO user entries carrying the same quoted text before Run (which
// leaves an existing transcript untouched), so cite resolves the sr:asked quote to
// both and returns 2.

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// seedTwoUserMessages writes the session transcript with a parentless ROOT user
// entry (the identity walk needs it) plus a SECOND user entry, both carrying
// `text`. Run's own seed skips an existing transcript, so this survives.
func seedTwoUserMessages(t *testing.T, e *env, proj, sess, text string) {
	t.Helper()
	path := e.TranscriptPath(proj, sess)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("seed two user messages: mkdir: %v", err)
	}
	root := fmt.Sprintf(
		`{"type":"user","uuid":%q,"parentUuid":null,"cwd":%q,"message":{"role":"user","content":%q}}`,
		"e2e-root-"+sess, proj, text)
	second := fmt.Sprintf(
		`{"type":"user","uuid":%q,"parentUuid":%q,"cwd":%q,"message":{"role":"user","content":%q}}`,
		"e2e-user2-"+sess, "e2e-root-"+sess, proj, text)
	if err := os.WriteFile(path, []byte(root+"\n"+second+"\n"), 0o644); err != nil {
		t.Fatalf("seed two user messages: %v", err)
	}
}

// T049_13: an AMBIGUOUS grounded ask (cite rc2) is refused with the ambiguity
// reason. The sr:asked quote is a phrase the user said in TWO separate messages,
// so cite returns two matches; the script refuses "matches SEVERAL user messages
// … ambiguous", and that reason reaches the agent. This exercises the rc2 branch
// the admit (rc0, T049_08) and no-match (rc1, T049_10) tests do not.
func TestT049_13_AmbiguousAskBlocksViaScript(t *testing.T) {
	e := newEnv(t)
	proj := nudProject(t, e)

	// The shared phrase appears in two user messages -> cite returns 2.
	const shared = "please remove the second line"
	sess := "s-049-13"
	seedTwoUserMessages(t, e, proj, sess, shared)

	e.WriteFile(proj, "memories/topic.md", "keep this line\nremove the second line\n")
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "irrelevant — the script refuses on the ambiguous cite"}`)

	res := e.Run(proj, sess, shared, Turns("done",
		Write("w1", "memories/topic.md",
			"---\n# sr:asked \"please remove the second line\"\n---\nkeep this line\n"),
	))

	if !res.Refused() {
		t.Fatalf("an ambiguous grounded ask (cite rc2) was NOT refused:\n%s", res.Output)
	}
	if !res.Saw("matches SEVERAL user messages") {
		t.Fatalf("the ambiguity (cite rc2) reason did not reach the agent:\n%s", res.Output)
	}
}
