package commandmod

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// OnlyCalls reports whether EXECUTING raw can do nothing but run calls that
// allowed accepts — the precondition for running an agent's command line
// ahead of time to learn what it would do.
//
// It is an allowlist over the parsed syntax tree, never a string test. A line
// passes only when every statement is a plain call (or an &&/|| chain of them)
// whose leading literal words allowed accepts, and nothing anywhere in it can
// run code, touch a file, or mean something else to the shell that later runs
// it for real:
//
//   - no expansion but a leading `~/` — no command or process substitution (`$(...)`, backticks, `<(...)` run code during EXPANSION,
//     before any allowed program starts), and no parameter or arithmetic
//     expansion either: `${X:=a[$(id)]}` then `$((X))`, `${!X}` and `${X@P}`
//     all run code in bash, and even a plain `$X` is read from an environment
//     the ahead-of-time run does not share with the real one (`$ZSH_VERSION`
//     is set in one shell and not the other), so what it predicts is not what
//     runs. A word opening with `=` is refused for the same reason: zsh
//     expands it to a command's path;
//   - no unquoted glob or brace character anywhere in a word (`*`, `?`, `[`,
//     `{`, and zsh's extended `^`, `#`, `~`): the ahead-of-time run is bash,
//     the real one is the user's shell, and the two expand a pattern
//     differently — zsh reads `memories/**/a.md` recursively where bash leaves
//     it as text, and brace, extglob and EXTENDED_GLOB rules differ too — so
//     the file the dry run judged would not be the file the real run changes.
//     Quoted (`'notes-*.md'`) or escaped (`notes-\*.md`), the character is
//     text to every shell and the word stays pure. `~` is admitted only as a
//     bare leading `~` or `~/`, which is $HOME to both; `~name` is a user's
//     home to bash and may be a named directory to zsh;
//   - no redirection but here-documents/here-strings on stdin and descriptor
//     duplication to a NUMBER (`2>&1`) — `>&out.txt` writes a file in bash;
//   - no pipes, subshells, groups, conditionals, loops, functions, background
//     jobs or coprocesses;
//   - no assignments, not even as a call's prefix: `PATH=. prog` picks which
//     program runs, and any variable the allowed program itself reads (a mode
//     switch, a file to trust) would be the line's to set.
//
// allowed receives each call's leading LITERAL words (the program name first,
// then any literal words up to the first expansion), so it can check a
// program and a subcommand without being handed text an expansion produced.
// No word it is handed can expand to anything but itself (or a leading `~/`
// to $HOME). An unparseable or empty line is not pure.
func OnlyCalls(raw string, allowed func(literals []string) bool) bool {
	f, err := syntax.NewParser().Parse(strings.NewReader(raw), "")
	if err != nil || len(f.Stmts) == 0 {
		return false
	}
	for _, st := range f.Stmts {
		if !pureStmt(st, allowed) {
			return false
		}
	}
	return true
}

func pureStmt(st *syntax.Stmt, allowed func([]string) bool) bool {
	if st == nil || st.Background || st.Coprocess {
		return false
	}
	for _, r := range st.Redirs {
		if !pureRedir(r) {
			return false
		}
	}
	switch c := st.Cmd.(type) {
	case *syntax.CallExpr:
		return pureCall(c, allowed)
	case *syntax.BinaryCmd:
		if c.Op != syntax.AndStmt && c.Op != syntax.OrStmt {
			return false
		}
		return pureStmt(c.X, allowed) && pureStmt(c.Y, allowed)
	}
	return false
}

func pureCall(c *syntax.CallExpr, allowed func([]string) bool) bool {
	if len(c.Args) == 0 || len(c.Assigns) > 0 {
		return false
	}
	var literals []string
	for _, w := range c.Args {
		if !pureWord(w) || equalsExpansion(w) || shellPattern(w) {
			return false
		}
		if lit, ok := plainWord(w); ok && len(literals) == indexOf(c.Args, w) {
			literals = append(literals, lit)
		}
	}
	if len(literals) == 0 {
		return false
	}
	return allowed(literals)
}

func indexOf(ws []*syntax.Word, w *syntax.Word) int {
	for i, x := range ws {
		if x == w {
			return i
		}
	}
	return -1
}

// pureRedir admits only redirections that neither run code nor name a file.
func pureRedir(r *syntax.Redirect) bool {
	switch r.Op {
	case syntax.Hdoc, syntax.DashHdoc:
		return r.Hdoc == nil || pureWord(r.Hdoc)
	case syntax.WordHdoc:
		return pureWord(r.Word)
	case syntax.DplOut, syntax.DplIn:
		lit, ok := plainWord(r.Word)
		if !ok {
			return false
		}
		if lit == "-" {
			return true
		}
		for _, ch := range lit {
			if ch < '0' || ch > '9' {
				return false
			}
		}
		return lit != ""
	}
	return false
}

// pureWord reports whether expanding w runs no code and reads no variable:
// no substitution and no parameter or arithmetic expansion anywhere inside it,
// however deeply nested. `$"..."` is refused as locale-dependent, and `$'...'`
// only when it spells a `\u`/`\U` escape, which bash 3.2 — the one a Mac runs
// ahead of time — leaves as text where a newer shell makes a character.
func pureWord(w *syntax.Word) bool {
	pure := true
	syntax.Walk(w, func(n syntax.Node) bool {
		switch n := n.(type) {
		case *syntax.CmdSubst, *syntax.ProcSubst, *syntax.ParamExp, *syntax.ArithmExp, *syntax.ExtGlob:
			pure = false
		case *syntax.DblQuoted:
			pure = !n.Dollar
		case *syntax.SglQuoted:
			pure = !n.Dollar || !strings.Contains(n.Value, `\u`) && !strings.Contains(n.Value, `\U`)
		}
		return pure
	})
	return pure
}

// equalsExpansion reports whether w opens with a bare `=`, which zsh expands to
// the path of the command it names (`=ls` is /bin/ls) and bash leaves as text.
func equalsExpansion(w *syntax.Word) bool {
	if len(w.Parts) == 0 {
		return false
	}
	lit, ok := w.Parts[0].(*syntax.Lit)
	return ok && strings.HasPrefix(lit.Value, "=")
}

// shellPattern reports whether w holds, outside any quotes, a character some
// shell expands against the file tree or into several words: a glob (`*`, `?`,
// `[`), a brace (`{`), zsh's EXTENDED_GLOB operators (`^`, `#`, `~` — options
// an interactive zsh commonly sets), or a `~` that is not a bare leading `~` or
// `~/`. A backslash-escaped character is text to every shell and is skipped.
//
// Refused rather than interpreted: bash (which runs the line ahead of time)
// and zsh (which a Mac runs it in for real) disagree on `**`, on a pattern
// that matches nothing, on brace ranges and on extended globs, and a dry run
// that expanded differently from the real run would have judged another file.
func shellPattern(w *syntax.Word) bool {
	for i, p := range w.Parts {
		lit, ok := p.(*syntax.Lit)
		if !ok {
			continue
		}
		v := lit.Value
		for j := 0; j < len(v); j++ {
			switch v[j] {
			case '\\':
				j++ // the next byte is escaped
			case '*', '?', '[', '{', '^', '#':
				return true
			case '~':
				leading := i == 0 && j == 0
				if !leading || (len(v) > 1 && v[1] != '/') || (len(v) == 1 && len(w.Parts) > 1) {
					return true
				}
			}
		}
	}
	return false
}

// plainWord is w's value when it contains no expansion at all — plain text,
// single quotes, or double quotes around plain text.
func plainWord(w *syntax.Word) (string, bool) {
	var b strings.Builder
	for _, p := range w.Parts {
		switch p := p.(type) {
		case *syntax.Lit:
			b.WriteString(p.Value)
		case *syntax.SglQuoted:
			if p.Dollar {
				return "", false
			}
			b.WriteString(p.Value)
		case *syntax.DblQuoted:
			for _, q := range p.Parts {
				lit, ok := q.(*syntax.Lit)
				if !ok {
					return "", false
				}
				b.WriteString(lit.Value)
			}
		default:
			return "", false
		}
	}
	return b.String(), true
}
