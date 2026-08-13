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

	cfg := safeConfig()

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

// expandPerWord expands each word on its own, keeping those that resolve and
// dropping those that do not.
//
// This is the honest floor of static resolution. A word that cannot be
// resolved without running something is not guessed at and not replaced with a
// placeholder — a placeholder in argv[0] would be a program name no rule
// should match, and one further along would be a value the command never
// receives. It is simply absent, and the vector is what remains.
func expandPerWord(cfg *expand.Config, words []*syntax.Word) []string {
	var fields []string
	for _, w := range words {
		got, err := expand.Fields(cfg, w)
		if err != nil {
			continue
		}
		fields = append(fields, got...)
	}
	return fields
}

// procSubstPath is what a process substitution resolves to. A shell would hand
// the program a file descriptor path; nothing is opened here, and the value
// exists only so the argument vector keeps its shape.
const procSubstPath = "/dev/fd/63"

// resolve turns one parsed call into the invocations it performs.
//
// Assigns are not consulted: mvdan/sh already separates an environment prefix
// from the argument vector, so `FOO=1 npm publish` arrives with Args holding
// npm and publish alone. That separation is the whole reason for parsing
// rather than matching the string — a regex cannot tell an assignment from a
// program.
func resolve(cfg *expand.Config, call *syntax.CallExpr) []Invocation {
	if len(call.Args) == 0 {
		// A bare assignment — `FOO=1` on its own — parses as a call with no
		// arguments. It runs no program, so it is not an invocation.
		return nil
	}

	fields, err := expand.Fields(cfg, call.Args...)
	if err != nil {
		// Expansion refused the vector as a whole, which is the intended
		// outcome for a command substitution — `echo $(npm publish)` cannot
		// resolve, because resolving it would mean running npm.
		//
		// Refusing the whole vector over one word would lose echo, which is
		// genuinely about to run. So the words are expanded one at a time and
		// the ones that resolve are kept. That is strictly what can be seen:
		// npm is still reported, from walking the substitution's own
		// statements, and the argument echo would have received is omitted
		// rather than invented.
		fields = expandPerWord(cfg, call.Args)
	}
	if len(fields) == 0 {
		// Every word expanded to nothing, or none could be resolved. Nothing
		// can honestly be said to be about to run.
		return nil
	}

	return fromArgv(fields)
}
