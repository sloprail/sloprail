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
// run code or touch a file on its own:
//
//   - no command or process substitution — `$(...)`, backticks, `<(...)` run
//     code during EXPANSION, before any allowed program starts;
//   - no redirection but here-documents/here-strings on stdin and descriptor
//     duplication to a NUMBER (`2>&1`) — `>&out.txt` writes a file in bash;
//   - no pipes, subshells, groups, conditionals, loops, functions, background
//     jobs or coprocesses;
//   - assignments only as a call's prefix (`VAR=x prog`), plain and pure.
//
// allowed receives each call's leading LITERAL words (the program name first,
// then any literal words up to the first expansion), so it can check a
// program and a subcommand without being handed text an expansion produced.
// An unparseable or empty line is not pure.
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
	if len(c.Args) == 0 {
		return false
	}
	for _, a := range c.Assigns {
		if a.Append || a.Naked || a.Array != nil || a.Index != nil {
			return false
		}
		if a.Value != nil && !pureWord(a.Value) {
			return false
		}
	}
	var literals []string
	for _, w := range c.Args {
		if !pureWord(w) {
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

// pureWord reports whether expanding w can run no code: no command or process
// substitution anywhere inside it, however deeply nested in a parameter
// expansion's default or an arithmetic expression.
func pureWord(w *syntax.Word) bool {
	pure := true
	syntax.Walk(w, func(n syntax.Node) bool {
		switch n.(type) {
		case *syntax.CmdSubst, *syntax.ProcSubst:
			pure = false
		}
		return pure
	})
	return pure
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
