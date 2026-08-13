package commandmod

import (
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// walk parses a command line and returns every invocation it performs, in the
// order the programs appear in the text.
//
// The recover is not defensive habit. A panic here would exit the guardrail
// non-zero, and a harness reads a non-zero guardrail as a refusal — so a
// parser bug would not degrade the rule, it would block the agent's work with
// a reason nobody can act on. Whatever was collected before the panic is kept:
// half a flattened list still lets a rule fire, where none lets it silently
// pass.
func walk(raw string) (invs []Invocation) {
	defer func() {
		_ = recover()
	}()

	f, err := syntax.NewParser().Parse(strings.NewReader(raw), "")
	if err != nil {
		// An unparseable line yields no invocations rather than a guess. The
		// raw text still rides on the event, so a rule may still match it.
		return nil
	}

	cfg := newConfig()

	// syntax.Walk, not a hand-rolled switch over statement types. `time cmd`
	// is a TimeClause, `! cmd` a negated Stmt, `{ cmd; }` a Block, `cmd &` a
	// backgrounded Stmt, and a coproc a CoprocClause — a traversal that
	// matched only CallExpr at the top would see through none of them. Walk
	// descends into every one, so the CallExpr inside arrives here regardless
	// of what wraps it, including a command substitution's own statements.
	syntax.Walk(f, func(n syntax.Node) bool {
		call, ok := n.(*syntax.CallExpr)
		if !ok {
			return true
		}
		invs = append(invs, resolve(cfg, call)...)
		return true
	})
	return invs
}

// newConfig builds the expansion used for a walk.
//
// A variable rather than a direct call so a test can substitute an expansion
// that panics. The panic this guards against is a bug inside the parser, which
// cannot be provoked from a command line once the ProcSubst handler is in
// place — so without a seam the recover above is unreachable, and an
// unreachable guard is one nobody can prove still works. The production path
// never reassigns this.
var newConfig = safeConfig

// safeConfig is an expansion that resolves what it can and refuses to resolve
// anything it cannot resolve without side effects.
//
// Expansion is not optional. A raw literal keeps its backslashes and drops its
// quotes: `n\pm publish` reads as `n\pm`, and `n""pm publish` reads as a bare
// ` publish` with the program gone entirely. A rule matching npm misses both.
// Only expansion turns them back into `npm`.
//
// What it must not do is act. The three deliberate omissions below are each
// load-bearing.
func safeConfig() *expand.Config {
	return &expand.Config{
		// An empty environment, not the process's. A variable read off the
		// real environment would make extraction depend on where it ran, so
		// the same command line would resolve differently for two agents.
		Env: expand.ListEnviron(),

		// CmdSubst is deliberately nil. With no handler, `$(touch /tmp/x)`
		// returns an UnexpectedCommandError instead of running touch — the
		// substitution's own statements are still walked as AST, so the
		// program inside is reported without ever being executed.
		//
		// ReadDir and ReadDir2 are deliberately nil too, which disables
		// globbing: `rm *.go` keeps `*.go` as written, and the filesystem is
		// never read. A guardrail that stats the tree to decide what a command
		// means is a guardrail whose verdict depends on the tree.

		// ProcSubst is the one handler that must exist. In v3.13.1 expansion
		// nil-dereferences on a process substitution without it, so `diff
		// <(ls a)` panics — and a panic in a guardrail is read as a refusal.
		// The handler resolves it to the path a shell would have substituted,
		// without opening anything. The statements inside are still walked, so
		// `ls` is reported.
		ProcSubst: func(*syntax.ProcSubst) (string, error) { return procSubstPath, nil },
	}
}

// procSubstPath is what a process substitution resolves to. A shell would hand
// the program a file descriptor path; nothing is opened here, and the value
// exists only so the argument vector keeps its shape.
const procSubstPath = "/dev/fd/63"
