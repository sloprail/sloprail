package commandmod

import (
	"slices"
	"strings"

	"github.com/sloprail/sloprail/internal/transcript"
)

// Which commands print the agent's own text back. A quote an agent cites as a tool's
// output has to match exactly one result, and a command that reads commit messages
// (`git log`) or runs one of sloprail's own tools (`sr-session trajectory cite 'q'`)
// prints the quote the agent wrote. That is an echo of the citation, never a source.
//
// Decided on the parsed command line (ExtractCommand, ReadTargets), not on its text, so
// `sh -c 'git log'`, `/usr/bin/git log` and `git -C "a b" log` are the invocation they
// resolve to. A line that resolves to no invocation at all (an unparseable one) is not
// vouched for: it is an echo too. A false alarm only withholds a result from citation;
// it never admits one.

// echoVerbs are the git subcommands that print commit or tag messages, or a ref's.
var echoVerbs = []string{
	"log", "show", "reflog", "cat-file", "shortlog", "format-patch", "whatchanged",
	"notes", "rev-list", "for-each-ref", "tag", "stash", "blame", "describe", "var", "show-branch",
}

// EchoesRecord reports whether the output of the shell command raw is the agent's own
// text read back: one of sloprail's binaries (sr, sr-session, sr-file, sr-mark,
// sr-checks, sr-agent, ...), git with a message-printing subcommand, or anything that
// reads a path inside .git/ (COMMIT_EDITMSG, logs/HEAD, ...).
func EchoesRecord(raw string) bool {
	invs := ExtractCommand(raw).Invocations
	if len(invs) == 0 {
		return strings.TrimSpace(raw) != ""
	}
	for _, inv := range invs {
		if inv.Bin == "sr" || strings.HasPrefix(inv.Bin, "sr-") {
			return true
		}
		if inv.Bin == "git" && slices.Contains(echoVerbs, gitSubcommand(inv.Argv)) {
			return true
		}
		if slices.ContainsFunc(inv.Argv, namesDotGit) {
			return true
		}
	}
	return slices.ContainsFunc(ReadTargets(raw), func(t ReadTarget) bool { return namesDotGit(t.Path) })
}

// gitSubcommand is the first operand of a git argv once git's own options, and their
// values, are set aside.
func gitSubcommand(argv []string) string {
	for i := 1; i < len(argv); i++ {
		a := argv[i]
		switch {
		case a == "-C" || a == "-c" || a == "--git-dir" || a == "--work-tree" ||
			a == "--namespace" || a == "--config-env" || a == "--super-prefix" || a == "--exec-path":
			i++
		case strings.HasPrefix(a, "-"):
		default:
			return a
		}
	}
	return ""
}

// namesDotGit reports a path that goes into a .git directory, as an argument or as an
// option's value (`--file=.git/x`).
func namesDotGit(arg string) bool {
	if v, ok := strings.CutPrefix(arg, "-"); ok {
		if _, val, has := strings.Cut(v, "="); has {
			arg = val
		}
	}
	arg = strings.ReplaceAll(arg, `\`, "/")
	return strings.HasPrefix(arg, ".git/") || strings.Contains(arg, "/.git/") || strings.HasSuffix(arg, "/.git") || arg == ".git"
}

func init() { transcript.CommandEchoes = EchoesRecord }
