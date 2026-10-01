package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// How a commit message carries its citations, and what a quote may be matched
// against. A citation is a `Sloprail-Cites-*:` line anywhere in the message — git
// parses only the last paragraph as trailers, and an agent that writes
// `-m 'Sloprail-Cites-User: …' -m 'Co-Authored-By: …'` puts its citation in front
// of another paragraph. And a quote matches the tool's output, not the agent's
// own commit message read back by `git log`.

const coAuthor = " -m 'Co-Authored-By: Someone <someone@example.invalid>'"

// commitOwnParagraph is the agent's commit whose citation is a paragraph of its own,
// in front of another: the form git does not read as trailers.
func commitOwnParagraph(id, subject, cite string) harness.Turn {
	return Bash(id, "git add -A && git commit -q --allow-empty -m '"+subject+"' -m '"+cite+"'"+coAuthor)
}

// amendOwnParagraph is commitOwnParagraph rewriting the last commit's message.
func amendOwnParagraph(id, subject, cite string) harness.Turn {
	return Bash(id, "git commit -q --amend --allow-empty -m '"+subject+"' -m '"+cite+"'"+coAuthor)
}

// citationRefusals counts the Stops of the session refused for want of a
// citation. The record keeps every refusal, so a test asks whether a run added
// one, not whether any exists.
func citationRefusals(e *harness.Env, proj, sess string) int {
	n := 0
	for _, m := range e.AllBlockingErrorsFrom(proj, sess, "Stop") {
		if strings.Contains(m, noCitation) {
			n++
		}
	}
	return n
}

// T041_50: a citation in a paragraph of its own is read. A quote nobody said, in
// that form, is refused; the user's words, in that form, ground the change.
func TestT041_50_ACitationInAnyParagraphIsRead(t *testing.T) {
	e, proj := guarded(t, afterCitationGuard)
	e.Run(proj, "s-041-50", prompt, Turns("done",
		Write("w1", "memories/a.md", "# a\n"),
		commitOwnParagraph("c1", "note the decision", "Sloprail-Cites-User: words nobody said"),
	))
	refused := citationRefusals(e, proj, "s-041-50")
	if refused == 0 {
		t.Fatalf("a citation that resolves nowhere passed at Stop:\n%q", e.AllBlockingErrorsFrom(proj, "s-041-50", "Stop"))
	}
	e.Run(proj, "s-041-50", "fix it", Turns("done",
		amendOwnParagraph("c2", "note the decision", "Sloprail-Cites-User: adopt a decision log"),
	))
	if got := citationRefusals(e, proj, "s-041-50"); got != refused {
		t.Fatalf("a citation in a paragraph of its own was not read (%d refusals, had %d):\n%q", got, refused, e.AllBlockingErrorsFrom(proj, "s-041-50", "Stop"))
	}
}

// T041_51: the agent's own `git log` prints its commit message, quote included, and
// so does a sloprail query tool. Those echoes are not a second match: one genuine
// output plus its echoes is one source. Two genuine outputs of the same words stay
// ambiguous.
func TestT041_51_AnEchoOfTheCommitIsNotASecondMatch(t *testing.T) {
	e, proj := guarded(t, toolResultGuard)
	e.Run(proj, "s-041-51", prompt, Turns("done",
		Write("w1", "memories/a.md", "# a\n"),
		Bash("t1", "echo 'DUPLICATE-5521 passed'"),
		Bash("t2", "echo 'DUPLICATE-5521 passed'"),
	).ThenCommit("note the result", harness.CitesTool("DUPLICATE-5521 passed")))
	refused := citationRefusals(e, proj, "s-041-51")
	if refused == 0 {
		t.Fatalf("a quote of two genuine outputs passed at Stop:\n%q", e.AllBlockingErrorsFrom(proj, "s-041-51", "Stop"))
	}
	e.Run(proj, "s-041-51", "cite the one", Turns("done",
		Bash("t3", "echo 'SINGLE-5522 passed'"),
		Bash("l1", "git log -1"),
		Bash("l2", "git -C . show --stat HEAD"),
		Bash("l3", "sr-session trajectory cite 'SINGLE-5522 passed'"),
		harness.AmendLast("a1", "note the result", harness.CitesTool("SINGLE-5522 passed")),
		Bash("l4", "git log -1"),
	))
	if got := citationRefusals(e, proj, "s-041-51"); got != refused {
		t.Fatalf("a genuine output plus its echoes was ambiguous (%d refusals, had %d):\n%q", got, refused, e.AllBlockingErrorsFrom(proj, "s-041-51", "Stop"))
	}
}

// T041_52: the user's words in a `Sloprail-Cites-Tool:` trailer, and the agent's own
// `git log` output as the only place a quote sits, ground nothing. A genuine output
// of the same words does.
func TestT041_52_TheUsersWordsAndAGitLogGroundNoToolCitation(t *testing.T) {
	e, proj := guarded(t, toolResultGuard)
	e.Run(proj, "s-041-52", prompt, Turns("done",
		Write("w1", "memories/a.md", "# a\n"),
	).ThenCommit("note the decision", harness.CitesTool("adopt a decision log")))
	refused := citationRefusals(e, proj, "s-041-52")
	if refused == 0 {
		t.Fatalf("the user's words, cited as tool output, passed at Stop:\n%q", e.AllBlockingErrorsFrom(proj, "s-041-52", "Stop"))
	}
	e.Run(proj, "s-041-52", "again", Turns("done",
		Bash("l1", "git log -1"),
		harness.AmendLast("a1", "GITLOGONLY-6601 all green", harness.CitesTool("GITLOGONLY-6601 all green")),
		Bash("l2", "git log -1"),
	))
	again := citationRefusals(e, proj, "s-041-52")
	if again == refused {
		t.Fatalf("the agent's own git log output grounded a citation:\n%q", e.AllBlockingErrorsFrom(proj, "s-041-52", "Stop"))
	}
	e.Run(proj, "s-041-52", "run it", Turns("done",
		Bash("t1", "echo 'GITLOGONLY-6601 all green'"),
		Bash("l3", "git log -1"),
	))
	if got := citationRefusals(e, proj, "s-041-52"); got != again {
		t.Fatalf("a genuine output beside the git log did not ground it (%d refusals, had %d):\n%q", got, again, e.AllBlockingErrorsFrom(proj, "s-041-52", "Stop"))
	}
}
