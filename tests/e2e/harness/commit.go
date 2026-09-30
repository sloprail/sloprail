package harness

import (
	"strconv"
	"strings"
)

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

// CommitSeedThenRules commits the project's own files first and its .sloprail rules in a
// second commit when any are uncommitted. A rule's range starts at the parent of the
// commit that added it, so seed files committed together with the rule would sit inside
// the first range as additions instead of being the baseline. Returns the seed commit.
func (e *Env) CommitSeedThenRules(dir, msg string) string {
	e.t.Helper()
	sha := e.CommitAllExcept(dir, msg, ".sloprail")
	if e.Git(dir, "status", "--porcelain", "--", ".sloprail") != "" {
		e.Git(dir, "add", "-A", "--", ".sloprail")
		e.Git(dir, "commit", "-m", "install the rules")
	}
	return sha
}

// commitCmd is the shell line of an agent's commit: everything staged, the subject,
// and the trailers as their own paragraph of the message. An empty commit is allowed,
// so a commit turn after a write a gate refused (which left nothing) is harmless.
func commitCmd(subject string, trailers ...string) string {
	return commitStagedCmd("git add -A", subject, trailers...)
}

// commitStagedCmd is commitCmd with the staging step given: `git add -A` commits
// everything, `git add -- <paths>` only the named paths.
func commitStagedCmd(stage, subject string, trailers ...string) string {
	cmd := stage + " && git commit -q --allow-empty -m " + shQuote(subject)
	if len(trailers) > 0 {
		cmd += " -m " + shQuote(strings.Join(trailers, "\n"))
	}
	return cmd
}

// AmendLast is the turn where the agent rewrites its LAST commit's message to carry
// the given trailers — the remedy a refusal for a missing citation gives ("amend that
// commit"): a citation grounds the files its own commit changed, so the trailer has to
// ride the commit that changed them.
func AmendLast(id, subject string, trailers ...string) Turn {
	cmd := "git commit -q --amend --allow-empty -m " + shQuote(subject)
	if len(trailers) > 0 {
		cmd += " -m " + shQuote(strings.Join(trailers, "\n"))
	}
	return Bash(id, cmd)
}

// SquashLast is the turn where the agent folds its last n commits into one carrying
// the given trailers, for a refusal whose uncited files were changed by several commits.
func SquashLast(id string, n int, subject string, trailers ...string) Turn {
	return Bash(id, "git reset -q --soft HEAD~"+strconv.Itoa(n)+" && "+commitStagedCmd("true", subject, trailers...))
}

// CommitFile is a scenario turn in which the AGENT writes a file and commits it —
// the way a file-guard's range comes to hold work. Bash rather than Write, so it
// also works inside a sub-agent (whose Write the mock does not apply). trailers
// are `Key: value` lines (CitesUser, CitesTool, ...) carried in the message.
func CommitFile(id, path, content, subject string, trailers ...string) Turn {
	return Bash(id, "mkdir -p \"$(dirname "+shQuote(path)+")\" && printf '%s' "+shQuote(content)+" > "+shQuote(path)+
		" && "+commitCmd(subject, trailers...))
}
