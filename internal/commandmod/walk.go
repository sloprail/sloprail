package commandmod

import (
	"path"
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
func walk(raw string) []Invocation {
	return walkAt(raw, 0)
}

// walkAt is walk at a known interpreter-payload depth.
//
// The depth is threaded through the parse rather than kept in a package
// variable, because two payloads in one line are independent: `sh -c 'a' && sh
// -c 'b'` is one level deep twice, not two levels deep, and a shared counter
// would make the second one's budget depend on the first one's.
//
// Its own recover, so a panic parsing a PAYLOAD cannot take the outer line's
// invocations with it. A payload is text an agent chose and this is the one
// place the module feeds such text back into the parser, so if any input can
// find a parser bug it is this one — and losing the outer `sh` on top of the
// payload would turn a partial answer into no answer.
// sr:invariant events/command-nesting-flattened
// sr:invariant events/command-undecidable-not-guessed
func walkAt(raw string, depth int) (invs []Invocation) {
	defer func() {
		_ = recover()
	}()

	f, err := syntax.NewParser().Parse(strings.NewReader(raw), "")
	if err != nil {
		// An unparseable line yields no invocations rather than a guess. The
		// raw text still rides on the event, so a rule may still match it.
		//
		// A payload reaches this too, and the same answer is right: `sh -c
		// 'if ['` is a line whose payload is not a shell script, and inventing
		// programs out of it would be worse than reporting sh alone.
		return nil
	}

	cfg := newConfig()

	// syntax.Walk, not a hand-rolled switch over statement types. `time cmd`
	// is a TimeClause, `! cmd` a negated Stmt, `{ cmd; }` a Block, `cmd &` a
	// backgrounded Stmt, and a coproc a CoprocClause — a traversal that
	// matched only CallExpr at the top would see through none of them. Walk
	// descends into every one, so the CallExpr inside arrives here regardless
	// of what wraps it, including a command substitution's own statements.
	//
	// The directory each statement runs in, threaded through the `cd`s ahead of
	// it — the same traversal FileTargets uses (see cwd.go), so a relative path
	// a program names means the same file to both. `stmt` tracks the statement
	// a CallExpr belongs to, exactly as fileTargetsAt does.
	cwds := cwdFor(f)
	var stmt *syntax.Stmt
	syntax.Walk(f, func(n syntax.Node) bool {
		switch node := n.(type) {
		case *syntax.Stmt:
			stmt = node
		case *syntax.CallExpr:
			at := stmtAt(cwds, stmt)
			// What this call's programs inherit from the line: the scope's
			// exports under the call's own prefix.
			scope := withEnv(at.env, assignsOf(cfg, node))
			stdin := stdinOf(stmt)
			// Words are read against the variables the line assigned before this
			// statement, and a reference to any other is unresolvable.
			cfgAt := cfgWith(cfg, at.vars)
			for _, inv := range resolve(cfgAt, node, depth) {
				inv.Env = underlay(inv.Env, scope)
				if inv.Stdin == nil {
					inv.Stdin = stdin
				}
				// Every invocation resolve returns carries a Cwd relative to
				// where ITS line started: "." for the call itself, and for an
				// interpreter payload's programs whatever the payload's own
				// walk resolved. Composing onto this statement's directory is
				// what makes `cd /x && sh -c 'cd y && cat z'` report cat at
				// /x/y.
				inv.Cwd = composeCwd(at, inv.Cwd)
				invs = append(invs, inv)
			}
			// A literal eval payload runs these programs in this shell,
			// exactly as `sh -c` runs its payload in a child: re-parsed at
			// depth+1 against the same bound. eval itself is reported above.
			if text, isEval, ok := evalPayloadText(cfgAt, node); isEval && ok && depth < maxUnwrapDepth {
				for _, inv := range walkAt(text, depth+1) {
					inv.Env = underlay(inv.Env, scope)
					inv.Cwd = composeCwd(at, inv.Cwd)
					invs = append(invs, inv)
				}
			}
		}
		return true
	})
	return invs
}

// composeCwd places a directory relative to where a (sub-)line started onto
// the directory that line itself runs in, in Invocation.Cwd's wire spelling.
//
// An unknown on either side is unknown, except that an absolute inner
// directory does not depend on where it was reached from. A relative inner one
// under the start of the outer sequence stays relative — this package never
// learns the harness's working directory, and does not guess it.
func composeCwd(at cwd, inner string) string {
	switch {
	case inner == "":
		return ""
	case path.IsAbs(inner):
		return inner
	case at.unknown, at.opaque:
		// An opaque eval may have moved the shell: a program's directory is
		// not claimed (see cwd.opaque — file targets resolve on regardless).
		return ""
	case at.dir == "":
		return inner
	case inner == ".":
		return at.dir
	default:
		return path.Clean(path.Join(at.dir, inner))
	}
}

// chdirFlags are the wrappers that run their program in another directory, and
// the flag that names it: the short spelling (also attached, `-C/x`) and the
// long one (also `--chdir=/x`).
var chdirFlags = map[string][2]string{
	"env":  {"-C", "--chdir"},
	"sudo": {"-D", "--chdir"},
}

// wrapperChdir reads the directory a wrapper runs its program in, from the
// wrapper's own words (its argv up to the program). moves is false when the
// wrapper does not change directory; known is false when it does, to a
// directory that is not a literal word (`env -C "$D" …`) or starts with `~`,
// which this package never expands. The last flag wins, as for the programs.
func wrapperChdir(own []word, gapAtEnd bool) (dir string, known, moves bool) {
	flags, ok := chdirFlags[basename(own[0].value)]
	// A word that was dropped as unresolvable among the wrapper's own words, or between it and the
	// program (`env -C "$D" cat f`, `nohup $X cat f`), may be the directory or a wrapper of its own
	// (`$X` = `env -C /y`): what follows moves, to somewhere unknown, unless a later literal option
	// sets the directory after it.
	gapSince := gapAtEnd
	if !ok {
		for _, w := range own[1:] {
			gapSince = gapSince || w.gapBefore || w.gapAfter
		}
		if gapSince {
			return "", false, true
		}
		return "", false, false
	}
	short, long := flags[0], flags[1]
	set := func(w word) {
		dir, moves = w.value, true
		known = w.literal && w.value != "" && !strings.HasPrefix(w.value, "~")
		gapSince = false
	}
	// gapSince is only the gaps AFTER the last directory option, so the walk is in order.
	gapSince = false
	for i := 1; i < len(own); i++ {
		a := own[i]
		if a.gapBefore || a.gapAfter {
			gapSince = true
		}
		switch {
		case (a.value == short || a.value == long) && !a.literal:
			// `-C"$D"` expanded to a bare `-C`: an attached value this
			// package cannot see (see unwrap). The directory is unknown, and
			// the next word is the program, not the value.
			dir, known, moves = "", false, true
			gapSince = false
		case a.value == short || a.value == long:
			if i+1 < len(own) {
				i++
				if own[i].gapBefore {
					// a word was lost between the option and its value
					dir, known, moves = "", false, true
					gapSince = false
				} else {
					set(own[i])
				}
			}
		case strings.HasPrefix(a.value, long+"="):
			set(word{value: strings.TrimPrefix(a.value, long+"="), literal: a.literal})
		case strings.HasPrefix(a.value, short) && !strings.HasPrefix(a.value, "--"):
			set(word{value: strings.TrimPrefix(a.value, short), literal: a.literal})
		}
	}
	if gapAtEnd {
		gapSince = true
	}
	if gapSince {
		return "", false, true
	}
	return dir, known, moves
}

// chdirCwd places an invocation's Cwd (relative to where its wrapper started)
// under the directory the wrapper moved to, in Invocation.Cwd's wire spelling:
// unknown when the directory is.
func chdirCwd(dir string, known bool, inner string) string {
	if !known {
		return ""
	}
	return composeCwd(cwd{dir: path.Clean(dir)}, inner)
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

// stdinOf is the literal text a statement's own redirects feed its command's standard
// input: the last stdin redirect decides, and only a heredoc (a quoted delimiter, or an
// unquoted one whose body has no expansion or escape) or a literal here-string yields
// text. Any other stdin redirect (`< file`) makes it unknown, nil.
func stdinOf(stmt *syntax.Stmt) *string {
	if stmt == nil {
		return nil
	}
	var out *string
	for _, r := range stmt.Redirs {
		if r.N != nil && r.N.Value != "0" {
			continue
		}
		switch r.Op {
		case syntax.Hdoc, syntax.DashHdoc:
			out = nil
			if r.Hdoc == nil {
				continue
			}
			var b strings.Builder
			ok := true
			for _, part := range r.Hdoc.Parts {
				l, isLit := part.(*syntax.Lit)
				if !isLit || (isPlainLit(r.Word) && strings.Contains(l.Value, `\`)) {
					ok = false
					break
				}
				b.WriteString(l.Value)
			}
			if ok {
				s := b.String()
				if r.Op == syntax.DashHdoc {
					lines := strings.Split(s, "\n")
					for i, l := range lines {
						lines[i] = strings.TrimLeft(l, "\t")
					}
					s = strings.Join(lines, "\n")
				}
				out = &s
			}
		case syntax.WordHdoc:
			out = nil
			if r.Word == nil || !isLiteral(r.Word) {
				continue
			}
			if v, err := expand.Literal(newConfig(), r.Word); err == nil {
				v += "\n"
				out = &v
			}
		case syntax.RdrIn, syntax.RdrInOut:
			out = nil
		}
	}
	return out
}

// isPlainLit reports a word with no quoting at all: a heredoc delimiter written so, its
// body is subject to expansion and escapes.
func isPlainLit(w *syntax.Word) bool {
	if w == nil {
		return true
	}
	for _, p := range w.Parts {
		l, ok := p.(*syntax.Lit)
		if !ok || strings.Contains(l.Value, `\`) {
			return false
		}
	}
	return true
}
