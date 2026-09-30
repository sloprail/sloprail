package harness

import (
	"os/exec"
	"strings"
	"testing"
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

// stageArgs is the `git add` that stages everything, or — with pathspecs — only those
// paths. The one place a commit's staging is spelled, for the test's own hand (Env) and
// the agent's (a scenario turn's shell line) alike.
func stageArgs(pathspecs ...string) []string {
	if len(pathspecs) == 0 {
		return []string{"add", "-A"}
	}
	return append([]string{"add", "-A", "--"}, pathspecs...)
}

// excludeSpecs is the pathspecs that stage everything BUT the given paths.
func excludeSpecs(paths []string) []string {
	specs := []string{"."}
	for _, p := range paths {
		specs = append(specs, ":(exclude)"+p)
	}
	return specs
}

// commitArgs is the `git commit` of a subject and its trailers (their own paragraph of
// the message). An empty commit is allowed: a commit after a write a gate refused, which
// left nothing, is harmless.
func commitArgs(subject string, trailers ...string) []string {
	args := []string{"commit", "-q", "--allow-empty", "-m", subject}
	if len(trailers) > 0 {
		args = append(args, "-m", strings.Join(trailers, "\n"))
	}
	return args
}

// commitCmd is the shell line of an agent's commit: stage (see stageArgs), then commit.
func commitCmd(stage []string, subject string, trailers ...string) string {
	quote := func(args []string) string {
		q := make([]string, len(args))
		for i, a := range args {
			q[i] = shQuote(a)
		}
		return "git " + strings.Join(q, " ")
	}
	return quote(stage) + " && " + quote(commitArgs(subject, trailers...))
}

// gitIn runs git in dir and returns its trimmed output, failing the test on an error.
// A package function so the installers that have no Env — they are handed a directory —
// use the same runner as Env.Git.
func gitIn(t testing.TB, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("harness: git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// commitIn stages (see stageArgs), commits, and returns the new HEAD.
func commitIn(t testing.TB, dir string, stage []string, msg string, trailers ...string) string {
	t.Helper()
	gitIn(t, dir, stage...)
	gitIn(t, dir, commitArgs(msg, trailers...)...)
	return gitIn(t, dir, "rev-parse", "HEAD")
}

// CommitAll stages everything in dir and commits it with msg, carrying the given
// trailers (CitesUser, CitesTool, or any `Key: value` line), and returns the new
// HEAD. Everything, because the sandbox's own files — rules, scripts — belong in
// the history too and a test that forgot one would be judging a different tree.
func (e *Env) CommitAll(dir, msg string, trailers ...string) string {
	e.t.Helper()
	return commitIn(e.t, dir, stageArgs(), msg, trailers...)
}

// CommitAllExcept is CommitAll but leaves the given paths (relative to dir) out:
// they stay in the working tree, uncommitted. For a test whose point is that a
// rule's own folder was never committed, or that the seed comes before the rules.
func (e *Env) CommitAllExcept(dir, msg string, exclude ...string) string {
	e.t.Helper()
	return commitIn(e.t, dir, stageArgs(excludeSpecs(exclude)...), msg)
}

// CommitSeedThenRules commits the project's own files first and its .sloprail rules in a
// second commit when any are uncommitted. A rule's range starts at the parent of the
// commit that added it, so seed files committed together with the rule would sit inside
// the first range as additions instead of being the baseline. Returns the seed commit.
func (e *Env) CommitSeedThenRules(dir, msg string) string {
	e.t.Helper()
	sha := e.CommitAllExcept(dir, msg, ".sloprail")
	if e.Git(dir, "status", "--porcelain", "--", ".sloprail") != "" {
		e.CommitAll(dir, "install the rules")
	}
	return sha
}

// CommitInstalled commits everything in dir so a freshly installed guardrail tree is
// part of the session's history rather than a change waiting to be committed: an
// uncommitted rule folder is itself a guarded path (the sloprail plugin's
// authoring-slop selects every hook script), so the first Stop would ask for a commit
// of it. A no-op when dir is not a git repository. A package function, not an Env
// method, for the installers that have no Env — they are handed a directory.
func CommitInstalled(t testing.TB, dir string) {
	t.Helper()
	if exec.Command("git", "-C", dir, "rev-parse", "--is-inside-work-tree").Run() != nil {
		return
	}
	commitIn(t, dir, stageArgs(), "install the guardrail tree")
}

// CommitFile is a scenario turn in which the AGENT writes a file and commits it —
// the way a file-guard's range comes to hold work. Bash rather than Write, so it
// also works inside a sub-agent (whose Write the mock does not apply). trailers
// are `Key: value` lines (CitesUser, CitesTool, ...) carried in the message.
func CommitFile(id, path, content, subject string, trailers ...string) Turn {
	return Bash(id, "mkdir -p \"$(dirname "+shQuote(path)+")\" && printf '%s' "+shQuote(content)+" > "+shQuote(path)+
		" && "+commitCmd(stageArgs(), subject, trailers...))
}
