package commandmod

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// AndChain returns the calls of raw, in the order they run, when raw is ONE
// statement made only of plain calls joined by && — `a && b && c` — each as
// its exact argument vector. In such a line a call that fails stops every call
// after it, which is what lets a caller that knows a call will fail refuse the
// whole line and lose nothing that ran before it, if nothing before it had an
// effect.
//
// The calls are returned only up to the first one whose words are not exact
// literals — a bare word or a single-quoted one, with nothing the shell would
// expand, glob or unescape — since from there on what runs is not what the line
// spells. ok is false for anything else: several statements (`a; b` runs b
// whatever a did), `||`, pipes, redirections other than a here-document,
// assignments, a background job, or a line that does not parse.
func AndChain(raw string) (calls [][]string, ok bool) {
	f, err := syntax.NewParser().Parse(strings.NewReader(raw), "")
	if err != nil || len(f.Stmts) != 1 {
		return nil, false
	}
	var stmts []*syntax.Stmt
	var flatten func(st *syntax.Stmt) bool
	flatten = func(st *syntax.Stmt) bool {
		if st == nil || st.Background || st.Coprocess || st.Negated {
			return false
		}
		switch c := st.Cmd.(type) {
		case *syntax.BinaryCmd:
			if c.Op != syntax.AndStmt || len(st.Redirs) > 0 {
				return false
			}
			return flatten(c.X) && flatten(c.Y)
		case *syntax.CallExpr:
			stmts = append(stmts, st)
			return true
		}
		return false
	}
	if !flatten(f.Stmts[0]) {
		return nil, false
	}
	for _, st := range stmts {
		for _, r := range st.Redirs {
			if r.Op != syntax.Hdoc && r.Op != syntax.DashHdoc {
				return nil, false
			}
		}
		c := st.Cmd.(*syntax.CallExpr)
		if len(c.Assigns) > 0 || len(c.Args) == 0 {
			return nil, false
		}
		argv := make([]string, 0, len(c.Args))
		for _, w := range c.Args {
			lit, exact := exactWord(w)
			if !exact {
				return calls, true
			}
			argv = append(argv, lit)
		}
		calls = append(calls, argv)
	}
	return calls, true
}

// exactWord is w's value when the shell would pass it through unchanged: a
// bare literal with nothing to expand, glob or unescape, or a single-quoted
// string.
func exactWord(w *syntax.Word) (string, bool) {
	var b strings.Builder
	for _, p := range w.Parts {
		switch p := p.(type) {
		case *syntax.Lit:
			if strings.ContainsAny(p.Value, "\\*?[]{}~") {
				return "", false
			}
			b.WriteString(p.Value)
		case *syntax.SglQuoted:
			if p.Dollar {
				return "", false
			}
			b.WriteString(p.Value)
		default:
			return "", false
		}
	}
	return b.String(), true
}
