package harness

import "strings"

// Committing the sandbox's work, and citing in the commit.
//
// A file-guard judges commits, so a test that wants one judged has to commit
// what its agent wrote. One helper for that, and one pair of trailer builders
// for the grounding a commit carries — never `git add`/`git commit` lines pasted
// into each test.

// CitesUser is a `Sloprail-Cites-User:` trailer: the commit says the user asked
// for this, in these words. Resolved against the transcripts like
// `sr-file --cite:user`.
func CitesUser(quote string) string { return "Sloprail-Cites-User: " + quote }

// CitesTool is a `Sloprail-Cites-Tool:` trailer: the commit is grounded in a
// tool's output rather than the user's words.
func CitesTool(quote string) string { return "Sloprail-Cites-Tool: " + quote }

// CommitAll stages everything in dir and commits it with msg, carrying the given
// trailers (CitesUser, CitesTool, or any `Key: value` line), and returns the new
// HEAD. Everything, because the sandbox's own files — rules, scripts — belong in
// the history too and a test that forgot one would be judging a different tree.
func (e *Env) CommitAll(dir, msg string, trailers ...string) string {
	e.t.Helper()
	if len(trailers) > 0 {
		msg += "\n\n" + strings.Join(trailers, "\n")
	}
	e.Git(dir, "add", "-A")
	e.Git(dir, "commit", "-m", msg)
	return e.Git(dir, "rev-parse", "HEAD")
}

// CommitAllExcept is CommitAll but leaves the given paths (relative to dir) out:
// they stay in the working tree, uncommitted. For a test whose point is that a
// rule's own folder was never committed.
func (e *Env) CommitAllExcept(dir, msg string, exclude ...string) string {
	e.t.Helper()
	e.Git(dir, "add", "-A")
	for _, p := range exclude {
		e.Git(dir, "reset", "-q", "--", p)
	}
	e.Git(dir, "commit", "-m", msg)
	return e.Git(dir, "rev-parse", "HEAD")
}

// commitCmd is the shell line of an agent's commit: everything staged, the subject,
// and the trailers as their own paragraph of the message. An empty commit is allowed,
// so a commit turn after a write a gate refused (which left nothing) is harmless.
func commitCmd(subject string, trailers ...string) string {
	cmd := "git add -A && git commit -q --allow-empty -m " + shQuote(subject)
	if len(trailers) > 0 {
		cmd += " -m " + shQuote(strings.Join(trailers, "\n"))
	}
	return cmd
}

// CommitFile is a scenario turn in which the AGENT writes a file and commits it —
// the way a file-guard's range comes to hold work. Bash rather than Write, so it
// also works inside a sub-agent (whose Write the mock does not apply). trailers
// are `Key: value` lines (CitesUser, CitesTool, ...) carried in the message.
func CommitFile(id, path, content, subject string, trailers ...string) Turn {
	return Bash(id, "mkdir -p \"$(dirname "+shQuote(path)+")\" && printf '%s' "+shQuote(content)+" > "+shQuote(path)+
		" && "+commitCmd(subject, trailers...))
}
