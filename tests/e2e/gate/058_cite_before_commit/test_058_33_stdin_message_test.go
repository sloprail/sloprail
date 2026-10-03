package e2e

import "testing"

// T058_33: `git commit -F - <<'EOF'` carries its message in the heredoc; a resolving trailer in it passes.
func TestT058_33_HeredocMessageWithResolvingTrailerPasses(t *testing.T) {
	e, proj := project(t)
	res := e.Run(proj, "s-058-33", prompt, Turns("done",
		stage("a", "docs/a.md", "a"),
		Bash("c", "git commit -q -F - <<'EOF'\nadd a\n\nSloprail-Cites-User: "+quote+"\n\nCo-Authored-By: X <x@y.z>\nEOF\n"),
	))
	if res.Refused() {
		t.Fatalf("a heredoc message with a resolving trailer was refused:\n%s", res.Output)
	}
	has(t, subjects(e, proj), "add a")
}

// T058_34: the same heredoc with a quote that does not resolve is refused.
func TestT058_34_HeredocMessageWithUnresolvingTrailerIsRefused(t *testing.T) {
	e, proj := project(t)
	res := e.Run(proj, "s-058-34", prompt, Turns("done",
		stage("a", "docs/a.md", "a"),
		Bash("c", "git commit -q -F - <<EOF\nadd a\n\nSloprail-Cites-User: nobody ever said this\nEOF\n"),
	))
	if !res.Refused() {
		t.Fatalf("a heredoc message with an unresolving trailer was not refused:\n%s", res.Output)
	}
	has(t, res.Output, "did not resolve")
}

// T058_35: `--file=-` and a here-string read the message the same way.
func TestT058_35_FileEqualsDashAndHereString(t *testing.T) {
	e, proj := project(t)
	res := e.Run(proj, "s-058-35", prompt, Turns("done",
		stage("a", "docs/a.md", "a"),
		Bash("c", "git commit -q --file=- <<<$'add a\\n\\nSloprail-Cites-User: "+quote+"'"),
	))
	if res.Refused() {
		t.Fatalf("a commit with --file=- and a here-string trailer was refused:\n%s", res.Output)
	}
	has(t, subjects(e, proj), "add a")
}

// T058_36: a message from a pipe is unknowable: let through (the file-guards check at Stop and in CI).
func TestT058_36_PipedMessageIsLetThrough(t *testing.T) {
	e, proj := project(t)
	res := e.Run(proj, "s-058-36", prompt, Turns("done",
		stage("a", "docs/a.md", "a"),
		Bash("c", "printf 'add a\\n' | git commit -q -F -"),
	))
	if res.Refused() {
		t.Fatalf("a commit whose message comes from a pipe was refused at the gate:\n%s", res.Output)
	}
}

// T058_37: a quote wrapped onto an unindented second line: the first line alone resolves, so it counts.
func TestT058_37_WrappedQuoteFirstLineCounts(t *testing.T) {
	e, proj := project(t)
	res := e.Run(proj, "s-058-37", prompt, Turns("done",
		stage("a", "docs/a.md", "a"),
		Bash("c", "git commit -q -F - <<'EOF'\nadd a\n\nSloprail-Cites-User: adopt a\ndecision log\n\nCo-Authored-By: X <x@y.z>\nEOF\n"),
	))
	if res.Refused() {
		t.Fatalf("a wrapped quote whose first line resolves was refused:\n%s", res.Output)
	}
}
