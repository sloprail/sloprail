package e2e

// The AMBIGUOUS-ASK case. A quote that appears in TWO separate user messages
// resolves to neither: sr-file's dry run fails "ambiguous", the change carries no
// citation, and the refusal quotes sr-file's reason so the agent knows to extend
// the quote. The harness seeds ONE root user entry from the prompt; this test
// pre-seeds the session transcript with TWO user entries carrying the same text
// before Run (which leaves an existing transcript untouched).

import (
	"fmt"
	"github.com/sloprail/sloprail/tests/e2e/harness"
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
// so it resolves to two entries; sr-file's dry run fails "ambiguous", and the
// refusal quotes that reason to the agent. This exercises the rc2 branch
// the admit (rc0, T049_08) and no-match (rc1, T049_10) tests do not.
func TestT049_13_AmbiguousAskBlocksViaScript(t *testing.T) {
	e := newEnv(t)
	proj := nudProject(t, e)

	// The shared phrase appears in two user messages -> cite returns 2.
	const shared = "please remove the second line"
	sess := "s-049-13"
	seedTwoUserMessages(t, e, proj, sess, shared)

	seedCommittedMemory(t, e, proj, "memories/topic.md", "keep this line\nremove the second line\n")
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "irrelevant — the ambiguous cite is refused first"}`)

	res := e.Run(proj, sess, shared, Turns("done",
		srWrite("w1", "memories/topic.md", "keep this line\n", "please remove the second line"),
	).ThenCommit("write the files", harness.CitesUser("please remove the second line")))

	if !res.Refused() {
		t.Fatalf("an ambiguous grounded ask (cite rc2) was NOT refused:\n%s", res.Output)
	}
	if !res.Saw("sr-file said") || !res.Saw("is ambiguous") {
		t.Fatalf("the ambiguity reason did not reach the agent:\n%s", res.Output)
	}
}
