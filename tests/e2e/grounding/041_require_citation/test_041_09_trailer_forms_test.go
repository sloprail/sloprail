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

// citationRefused reports whether the session's range is refused, as it stands, for want
// of a citation. A quote is resolved by `sr-checks run`, where the transcript is; the Stop
// only verifies what that run stored (it trusts the trailers), so the refusal is read from
// the run over the session's range, the same call the agent makes before it stops.
func citationRefused(e *harness.Env, proj, sess string) bool {
	return strings.Contains(strings.Join(e.CheckRun(proj, sess), "\n"), noCitation)
}

// T041_50: a citation in a paragraph of its own is read. A quote nobody said, in
// that form, is refused; the user's words, in that form, ground the change.
func TestT041_50_ACitationInAnyParagraphIsRead(t *testing.T) {
	e, proj := guardedUncited(t, afterCitationGuard)
	e.Run(proj, "s-041-50", prompt, Turns("done",
		Write("w1", "memories/a.md", "# a\n"),
		commitOwnParagraph("c1", "note the decision", "Sloprail-Cites-User: words nobody said"),
	))
	// The precondition that makes this a test of OUR parsing: git itself reads no
	// trailer from this message, because the citation is not in its last paragraph.
	if got := e.Git(proj, "log", "-1", "--format=%(trailers:key=Sloprail-Cites-User)"); got != "" {
		t.Fatalf("git parsed the citation as a trailer, so this would not test the paragraph rule: %q", got)
	}
	if !citationRefused(e, proj, "s-041-50") {
		t.Fatalf("a citation that resolves nowhere passed the range's check")
	}
	e.Run(proj, "s-041-50", "fix it", Turns("done",
		amendOwnParagraph("c2", "note the decision", "Sloprail-Cites-User: adopt a decision log"),
	))
	if citationRefused(e, proj, "s-041-50") {
		t.Fatalf("a citation in a paragraph of its own was not read:\n%q", e.CheckRun(proj, "s-041-50"))
	}
}

// T041_51: the agent's own `git log` prints its commit message, quote included, and
// so does a sloprail query tool. Those echoes are not a second match: one genuine
// output plus its echoes is one source. Two genuine outputs of the same words stay
// ambiguous.
func TestT041_51_AnEchoOfTheCommitIsNotASecondMatch(t *testing.T) {
	e, proj := guardedUncited(t, toolResultGuard)
	e.Run(proj, "s-041-51", prompt, Turns("done",
		Write("w1", "memories/a.md", "# a\n"),
		Bash("t1", "echo 'DUPLICATE-5521 passed'"),
		Bash("t2", "echo 'DUPLICATE-5521 passed'"),
	).ThenCommit("note the result", harness.CitesTool("DUPLICATE-5521 passed")))
	if !citationRefused(e, proj, "s-041-51") {
		t.Fatalf("a quote of two genuine outputs passed the range's check")
	}
	e.Run(proj, "s-041-51", "cite the one", Turns("done",
		Bash("t3", "echo 'SINGLE-5522 passed'"),
		Bash("l1", "git log -1"),
		Bash("l2", "git -C . show --stat HEAD"),
		Bash("l3", "sr-session trajectory cite 'SINGLE-5522 passed'"),
		harness.AmendLast("a1", "note the result", harness.CitesTool("SINGLE-5522 passed")),
		Bash("l4", "git log -1"),
	))
	if citationRefused(e, proj, "s-041-51") {
		t.Fatalf("a genuine output plus its echoes was ambiguous:\n%q", e.CheckRun(proj, "s-041-51"))
	}
}

// T041_52: the user's words in a `Sloprail-Cites-Tool:` trailer, and the agent's own
// `git log` output as the only place a quote sits, ground nothing. A genuine output
// of the same words does.
func TestT041_52_TheUsersWordsAndAGitLogGroundNoToolCitation(t *testing.T) {
	e, proj := guardedUncited(t, toolResultGuard)
	e.Run(proj, "s-041-52", prompt, Turns("done",
		Write("w1", "memories/a.md", "# a\n"),
	).ThenCommit("note the decision", harness.CitesTool("adopt a decision log")))
	if !citationRefused(e, proj, "s-041-52") {
		t.Fatalf("the user's words, cited as tool output, passed the range's check")
	}
	e.Run(proj, "s-041-52", "again", Turns("done",
		Bash("l1", "git log -1"),
		harness.AmendLast("a1", "GITLOGONLY-6601 all green", harness.CitesTool("GITLOGONLY-6601 all green")),
		Bash("l2", "git log -1"),
	))
	if !citationRefused(e, proj, "s-041-52") {
		t.Fatalf("the agent's own git log output grounded a citation")
	}
	e.Run(proj, "s-041-52", "run it", Turns("done",
		Bash("t1", "echo 'GITLOGONLY-6601 all green'"),
		Bash("l3", "git log -1"),
	))
	if citationRefused(e, proj, "s-041-52") {
		t.Fatalf("a genuine output beside the git log did not ground it:\n%q", e.CheckRun(proj, "s-041-52"))
	}
}

// echoRefusedThenPassed: a quote whose only home is the agent's own commit message, printed
// back by echoCmd, grounds nothing; once a tool genuinely printed it, the echo beside it
// does no harm.
func echoRefusedThenPassed(t *testing.T, sess, quote, echoCmd string) {
	t.Helper()
	e, proj := guardedUncited(t, toolResultGuard)
	e.Run(proj, sess, prompt, Turns("done",
		Write("w1", "memories/a.md", "# a\n"),
	).ThenCommit(quote, harness.CitesTool(quote)))
	if !citationRefused(e, proj, sess) {
		t.Fatalf("a quote nothing printed passed the range's check")
	}
	e.Run(proj, sess, "print it", Turns("done", Bash("l2", echoCmd)))
	if !citationRefused(e, proj, sess) {
		t.Fatalf("%s printing the commit message grounded a citation", echoCmd)
	}
	e.Run(proj, sess, "run it", Turns("done",
		Bash("t1", "echo '"+quote+"'"),
		Bash("l3", echoCmd),
	))
	if citationRefused(e, proj, sess) {
		t.Fatalf("a genuine output beside %s did not ground it:\n%q", echoCmd, e.CheckRun(proj, sess))
	}
}

// T041_56: a wrapped `git log` is as much an echo as a bare one.
func TestT041_56_AWrappedGitLogIsAnEcho(t *testing.T) {
	echoRefusedThenPassed(t, "s-041-56", "WRAPPED-7701 all green", "sh -c 'git log -1'")
}

// T041_57: so is a ref listing that prints the messages.
func TestT041_57_AForEachRefListingIsAnEcho(t *testing.T) {
	echoRefusedThenPassed(t, "s-041-57", "FOREACH-7702 all green", "git for-each-ref --format='%(contents)' refs/heads")
}
