package commandmod

import (
	"path"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// This file is what makes a relative file target mean the right file when the
// line that names it has already `cd`'d somewhere else.
//
// # The bug this fixes
//
// `cd /elsewhere && printf x > rel.txt` writes to `/elsewhere/rel.txt`. Before
// this file existed, FileTargets reported the bare word `rel.txt` — exactly what
// it reports for a line with no `cd` at all — and the caller (filemod's
// reportable) resolved that relative spelling against the SESSION's project
// root, because that is the only root it has ever been handed. So a command
// that named a file in a different tree entirely was reported, and refused or
// permitted, as though it named a file inside the project. Measured against the
// motivating case: a session rooted at one repo, a command line that `cd`s into
// a completely different one and writes a relative path there, refused as a
// write to the FIRST repo's own tree.
//
// # Why the fix lives here and not in filemod
//
// filemod does not see the command line's structure — it sees the flat
// FileTarget.Path this package already hands it, resolved or not. Teaching it
// about `cd` would mean re-parsing the command a second time, which is the
// exact duplication commandmod's own package doc argues against elsewhere
// ("two parses of one line are two answers free to disagree"). This package is
// the one holding the parsed AST, in the one place a `cd` and the redirection
// or operand it precedes are both visible.
//
// filemod's own contract is left completely alone. reportable() already knows
// how to answer "is this absolute path inside the project" (relative it to the
// root) and "is this absolute path outside" (leave it absolute, so no
// project-relative matcher admits it) — that is the whole of what an outside
// path needs to look like, and it already works for a tool call naming an
// absolute path outside the workspace. So the fix is to make a `cd`-affected
// relative target ARRIVE as an absolute path when the `cd` target was itself
// absolute (or resolves to one), which is exactly the spelling reportable
// already handles correctly. A `cd` into a path that is itself still relative
// to wherever the line started produces a target that is STILL relative,
// composed one level deeper — which is the ordinary case filemod has always
// resolved against the root, unchanged.
//
// # What "the session root" never enters into
//
// FileTargets remains a pure function of the command string alone. It has no
// root to resolve against and does not gain one here: a `cd` into a relative
// path composes onto wherever the sequence STARTED, which this package calls
// the empty effective directory, and a target under it is left exactly as
// relative as it always was. Only a `cd` whose own argument is absolute (or an
// earlier composition that became absolute) turns a later relative target
// absolute. That is deliberate: this package still does not know, and must not
// need to know, where the repository is checked out.

// cwd is the effective working directory at one point in a sequence of
// statements, threaded through however many `cd`s preceded it.
//
// Two states rather than a bare string, because a `cd` this package cannot
// resolve statically must not be silently treated as "no change" OR as "back
// at wherever we started" — both are guesses, and a target resolved against
// either could name a file the command never touches. See resolveTargetAt for
// what unknown does to a target reached under it.
type cwd struct {
	// unknown is true once a `cd` this package cannot resolve has been seen in
	// this scope: no argument, `cd -`, or an argument that did not expand to a
	// single certain word. It never clears itself back to known within the same
	// sequence — a shell that lost track of its own directory does not
	// spontaneously regain it — and it does not leak into an ENCLOSING scope: a
	// subshell that goes unknown internally leaves the statement after it
	// exactly where the top-level sequence was before the subshell opened,
	// because the subshell's `cd` never ran in the parent shell at all.
	unknown bool
	// opaque is true once an `eval` whose payload this package cannot read
	// (`eval "$(ssh-agent -s)"`, `eval "$X"`) has run in this scope. It MAY
	// have moved the shell, and almost never does.
	//
	// A separate bit from unknown because the two consumers of this state
	// fail in opposite directions. Invocation.Cwd reports it as unknown ("")
	// — a read credited against a guessed directory is a false credit. A file
	// target is still resolved as though the eval had not moved anything —
	// dropping it (what unknown does) would hide every relative write after
	// the eval from every file guard, and `eval "$(ssh-agent -s)" && echo x >
	// NOTES.md` is an ordinary line, not an evasion. See composeCwd and
	// resolveTargetAt.
	opaque bool
	// dir is the effective directory, meaningful only when !unknown.
	//
	// Empty means "wherever this sequence started" — the ordinary case with no
	// `cd` yet seen, and the only state a target resolves through UNCHANGED
	// (see resolveTargetAt). Otherwise it is a shell-style, "/"-joined path,
	// built by joining and cleaning `cd` arguments as they are seen: absolute
	// the moment any `cd` argument was itself absolute, and relative for as
	// long as every `cd` since the start of the sequence was relative — composed
	// onto each other, never onto a root this package does not have.
	dir string
	// env is what `export NAME=value` has set in this scope so far (value ""
	// when not literal). Immutable once built: a scope copies it on write, so
	// a subshell's copy and the parent's never alias.
	env map[string]string
	// vars is the shell variables the line has assigned so far in this scope
	// (`D=/x;`, `D=/x &&`, `export D=/x;`) whose value is a literal, name to
	// value. A variable absent here is UNKNOWN, not unset: the shell the
	// command runs in may hold it, so expansion treats a reference to it as
	// unresolvable (see cfgWith) rather than as empty. Immutable once built,
	// copy-on-write like env.
	vars map[string]string
	// noVars disables vars for this file: set when the line defines a shell
	// function, whose body can assign a variable anywhere it is called.
	noVars bool
}

// cfgWith is cfg expanding against what the line has assigned so far: exactly
// the variables in vars, and any other parameter an error (NoUnset), so a word
// that reads one is lost rather than read as empty.
func cfgWith(cfg *expand.Config, vars map[string]string) *expand.Config {
	c := *cfg
	list := make([]string, 0, len(vars))
	for k, v := range vars {
		list = append(list, k+"="+v)
	}
	c.Env = expand.ListEnviron(list...)
	c.NoUnset = true
	return &c
}

// setVar records name as holding value (known) or as unknown. Copy-on-write.
func (c *cwd) setVar(name, value string, known bool) {
	if c.noVars {
		return
	}
	if _, had := c.vars[name]; !had && !known {
		return
	}
	next := make(map[string]string, len(c.vars)+1)
	for k, v := range c.vars {
		next[k] = v
	}
	if known {
		next[name] = value
	} else {
		delete(next, name)
	}
	c.vars = next
}

// applyAssigns records the effect of NAME=value words, with each value read
// against the variables known before it. declare is true for a declaration
// clause, where a bare `NAME` assigns nothing.
func (c *cwd) applyAssigns(cfg *expand.Config, assigns []*syntax.Assign, declare bool) {
	for _, as := range assigns {
		if as.Name == nil {
			continue
		}
		if as.Value == nil && as.Naked && declare {
			continue
		}
		if as.Append || as.Array != nil || as.Index != nil {
			c.setVar(as.Name.Value, "", false)
			continue
		}
		v := ""
		if as.Value != nil {
			lit, err := expand.Literal(cfgWith(cfg, c.vars), as.Value)
			if err != nil || strings.Contains(lit, "~") || !paramsKnown(cfgWith(cfg, c.vars), as.Value) {
				c.setVar(as.Name.Value, "", false)
				continue
			}
			v = lit
		}
		c.setVar(as.Name.Value, v, true)
	}
}

// varWriters are the builtins that assign a variable by a route this package
// does not follow; seeing one forgets every known variable.
var varWriters = map[string]bool{
	"read": true, "mapfile": true, "readarray": true, "getopts": true, "unset": true, "let": true,
	"source": true, ".": true, "command": true, "builtin": true, "exec": true, "wait": true,
}

// startCwd is the effective directory nothing has yet moved out of: the empty,
// known state every top-level sequence begins in, and what a subshell's own
// scope is seeded with on entry (COPIED, not shared — see cwdForSequence).
var startCwd = cwd{}

// ptr returns a pointer to a copy of v.
//
// Used wherever a scope needs its OWN pointer to a value it will not write
// back through — a pipeline's two sides (cwdForStmt's BinaryCmd case), each
// of which gets a fresh copy of the cwd in effect so neither can leak a `cd`
// to the other or to whatever follows the pipeline. `&v` on a local variable
// would do the same thing at each call site; this exists so that intent —
// "a copy, not the original" — is named once rather than re-typed at every
// site that needs it.
func ptr[T any](v T) *T { return &v }

// advance returns the cwd after a `cd` invocation whose resolved, literal
// argument is target — or the unknown cwd, when isCdTarget could not offer one.
//
// A cd with no argument, `cd -`, or a non-literal argument is not resolved
// HERE; those are refused earlier, by cdTargetOf, which is what decides whether
// there is a target to advance by at all.
func (c cwd) advance(target string) cwd {
	if c.unknown {
		// Already lost. A further `cd`, however plainly spelled, does not
		// recover a directory this package gave up tracking — the shell's own
		// notion of where it is might not be what this package last knew
		// either, once one `cd` in the chain was unresolvable.
		return c
	}
	if path.IsAbs(target) {
		// An absolute cd names the directory outright, whatever an opaque
		// eval before it may have done.
		n := c
		n.dir, n.opaque = path.Clean(target), false
		return n
	}
	if c.dir == "" {
		// Relative, composed onto "wherever this sequence started" — which
		// stays exactly that: still relative, one level deeper. Not resolved
		// against any root, because this package has none.
		n := c
		n.dir = path.Clean(target)
		return n
	}
	n := c
	n.dir = path.Clean(path.Join(c.dir, target))
	return n
}

// resolveTargetAt turns a path as a command line spelled it into the path it
// actually names, given the effective directory at that point in the sequence.
//
// Three outcomes, and the second and third are both existing contracts this
// function reuses rather than invents:
//
//	at is unknown          decline, the same answer this package already gives
//	                        for a word it cannot resolve without guessing (a
//	                        non-literal argument, a glob, a lost word). See
//	                        targetsFor and literalWord for the convention:
//	                        emit what can be seen, drop what cannot.
//	rel resolves absolute   join and clean, so `cd /a && … > rel` reports
//	                        `/a/rel` — the same spelling filemod's reportable
//	                        already resolves correctly for a tool call naming an
//	                        absolute path, inside the project or outside it.
//	rel resolves relative   composed onto at.dir and left relative — the same
//	                        spelling filemod has always resolved against the
//	                        session root, so a line with no `cd` at all (at ==
//	                        startCwd) is completely unaffected.
//
// An already-ABSOLUTE path is untouched regardless of at: `cd /a && rm /b/c`
// removes /b/c, not something joined onto /a. This is also why callers must
// pass the ORIGINAL path here rather than something partway resolved — joining
// twice would be wrong for the relative case.
func resolveTargetAt(p string, at cwd) (string, bool) {
	if p == "" {
		return "", true
	}
	if path.IsAbs(p) {
		return p, true
	}
	if at.unknown {
		return "", false
	}
	if at.dir == "" {
		return p, true
	}
	return path.Clean(path.Join(at.dir, p)), true
}

// dirChangingBuiltins are the shell builtins this package knows change the
// CURRENT shell's directory, beyond plain `cd`.
//
// `pushd`/`popd` genuinely move the shell's cwd — that is their entire
// purpose — but resolving WHERE they move it to means modelling a whole
// directory STACK (pushd's argument, or none — meaning "swap with the top of
// the stack" — or popd, which needs to know what an EARLIER pushd on this
// same line already pushed), a much larger undertaking than the single
// current-directory cd tracks. So neither is resolved; both are recognised
// by name, here, ONLY so that seeing one can mark the scope unknown rather
// than silently doing nothing and leaving `current` exactly where it was —
// which would be the wrong-direction guess this package refuses everywhere
// else: `pushd /elsewhere && touch a` DOES move the shell, and reporting `a`
// as unaffected would be confidently wrong, not merely incomplete.
var dirChangingBuiltins = map[string]bool{"pushd": true, "popd": true}

// cdTargetOf reads a `cd`/`pushd`/`popd` invocation, and reports whether this
// call changes the current shell's directory at all.
//
// Only `cd`'s own shape is ever resolved to a concrete target — see the
// isCd-but-not-ok branches below. `pushd` and `popd` are recognised by name
// alone: isCd is true and ok is always false for them, which callers read as
// "this call changes the directory, but not to anywhere this package can
// name" — see dirChangingBuiltins for why resolving them is out of scope
// rather than merely skipped.
//
// For `cd` itself: only the shape this package can be sure of is resolved —
// exactly one operand, itself a single literal word that expanded to exactly
// one field. Everything else — no operand, `cd -`, more than one operand
// (which real cd also rejects, but a non-literal one is refused for the same
// reason literalWord refuses everywhere else in this package: guessing at an
// environment this does not have), or an operand that is not certain —
// reports ok=false, which callers read as "this cd could not be resolved"
// rather than "this is not a cd".
//
// cfg is the same expansion FileTargets already builds once per call and
// passes down; a `cd` argument is resolved exactly the way any other operand
// in this package is.
func cdTargetOf(cfg *expand.Config, call *syntax.CallExpr) (target string, isCd bool, ok bool) {
	if len(call.Args) == 0 || !isLiteral(call.Args[0]) {
		return "", false, false
	}
	got, err := expand.Fields(cfg, call.Args[0])
	if err != nil || len(got) != 1 {
		return "", false, false
	}
	if dirChangingBuiltins[got[0]] {
		// pushd/popd: recognised, never resolved. See dirChangingBuiltins.
		return "", true, false
	}
	if got[0] != "cd" {
		return "", false, false
	}
	isCd = true

	// Past this point the call IS a cd; whether it can be RESOLVED is a
	// separate question, answered by ok.
	if len(call.Args) != 2 {
		// No argument (`cd` alone, home) or more than one (a usage error in
		// real cd, or `cd -P dir`/`cd -L dir`, flags this does not parse). Both
		// are outside what this package resolves statically.
		return "", isCd, false
	}
	arg := call.Args[1]
	if !isLiteral(arg) && !plainKnown(cfg, arg) {
		// `cd "$SOME_VAR"`, `cd $(pwd)/x` — a target resolved out of an
		// environment this package does not have, exactly the case
		// literalWord refuses for a redirection's own path.
		return "", isCd, false
	}
	argFields, err := expand.Fields(cfg, arg)
	if err != nil || len(argFields) != 1 || argFields[0] == "" {
		return "", isCd, false
	}
	if argFields[0] == "-" {
		// `cd -`: the previous directory, which this package never recorded
		// even when it was known, because recording it would mean keeping a
		// SECOND cwd alongside the first for a spelling that is rare in
		// agent-written command lines. Refused rather than guessed at.
		return "", isCd, false
	}
	if hasGlob(argFields[0]) {
		// `cd /a/*` — the shell expands this against the FILESYSTEM into
		// whichever single directory matches (cd rejects more than one), and
		// safeConfig deliberately disables globbing so this package never
		// reads the tree to find out which. The same word passes every
		// certainty test up to here — isLiteral agrees a `*` is a plain
		// Lit — and looks resolved without being resolved to anything real,
		// exactly the hazard targetsFor's own hasGlob check exists to name
		// for an ordinary operand. Refused rather than guessed at.
		return "", isCd, false
	}
	if strings.HasPrefix(argFields[0], "~") {
		// `cd ~`, `cd ~/x`, `cd ~user` — tilde expansion needs a real $HOME
		// (or a real password database, for `~user`), neither of which this
		// package has. isLiteral treats `~` as a plain Lit (mvdan/sh does not
		// give it its own node type the way a parameter expansion gets one),
		// so expand.Fields under the empty environment returns it completely
		// UNEXPANDED — `cd ~/x` resolves to the literal three-character string
		// "~/x", not a real path. Composing that onto anything, or reporting
		// it as an absolute-looking target, would be exactly the
		// confidently-wrong answer this package refuses everywhere else: a
		// cwd that LOOKS resolved and names no real directory. Refused here
		// rather than guessed at, the same as `cd -` one branch above.
		return "", isCd, false
	}
	return argFields[0], isCd, true
}

// evalPayloadText reads an `eval` call's payload: its arguments joined by
// spaces, which is the text eval itself parses and runs in the current shell.
//
// isEval is false for any other call. ok is false when the payload cannot be
// read without guessing — an argument that is not literal (`eval "$(pyenv
// init -)"`, `eval "cd $D"`) — because eval joins its words, so one
// unknowable word makes the whole text a guess. A literal payload strictly
// shrinks with each nested `eval`, so re-parsing it has a bottom; the callers
// that re-parse it for programs and files also spend the interpreter depth
// budget on it, exactly as for `sh -c`.
func evalPayloadText(cfg *expand.Config, call *syntax.CallExpr) (payload string, isEval, ok bool) {
	if len(call.Args) == 0 || !isLiteral(call.Args[0]) {
		return "", false, false
	}
	got, err := expand.Fields(cfg, call.Args[0])
	if err != nil || len(got) != 1 || got[0] != "eval" {
		return "", false, false
	}
	words := make([]string, 0, len(call.Args)-1)
	for _, arg := range call.Args[1:] {
		if !isLiteral(arg) {
			return "", true, false
		}
		fields, err := expand.Fields(cfg, arg)
		if err != nil {
			return "", true, false
		}
		words = append(words, fields...)
	}
	return strings.Join(words, " "), true, true
}

// evalPayloadOf is evalPayloadText parsed: the statements eval would run.
// ok is also false for text that does not parse.
func evalPayloadOf(cfg *expand.Config, call *syntax.CallExpr) (payload []*syntax.Stmt, isEval, ok bool) {
	text, isEval, ok := evalPayloadText(cfg, call)
	if !isEval || !ok {
		return nil, isEval, false
	}
	f, err := syntax.NewParser().Parse(strings.NewReader(text), "")
	if err != nil {
		return nil, true, false
	}
	return f.Stmts, true, true
}

// cwdFor computes the effective directory belonging to each statement in a
// parsed file, keyed by pointer so fromRedirs and fromCall — which already
// walk the same tree via fileTargetsAt's syntax.Walk and already carry a
// *syntax.Stmt for the heredoc pairing — can look their own answer up rather
// than recomputing it.
//
// Built as its own traversal rather than folded into that syntax.Walk for one
// reason: syntax.Walk is pre-order with no exit callback, so nothing tells a
// visitor when a Subshell's statements are behind it and its cwd should revert
// for whatever follows at the OUTER level. Recursion gives that for free —
// cwdForSequence's own stack frame IS the scope, and it returns without
// touching the caller's cwd variable, so a subshell's own `cd` can never
// escape it. This does cost a second, cheap traversal of the same tree; the
// alternative is a hand-rolled push/pop over a callback that was not built to
// support it.
func cwdFor(f *syntax.File) map[*syntax.Stmt]cwd {
	cfg := newConfig()
	out := make(map[*syntax.Stmt]cwd)
	entry := startCwd
	syntax.Walk(f, func(n syntax.Node) bool {
		if _, ok := n.(*syntax.FuncDecl); ok {
			entry.noVars = true
		}
		return true
	})
	cwdForSequence(cfg, f.Stmts, entry, out)
	return out
}

// cwdForSequence walks one sequence of statements IN ORDER — the top level of
// a file, the body of a subshell, or a `{ }` block — threading the effective
// directory forward and recording each statement's OWN answer (the directory
// in effect when that statement runs, i.e. before its own `cd` takes effect
// for the NEXT one) into out. Returns the cwd in effect once the whole
// sequence has run, for the one caller that needs it (Block, below).
//
// entry is the cwd this sequence begins with: startCwd at the top level, a
// COPY of the enclosing scope's current cwd for a subshell, and the
// enclosing scope's OWN current cwd, by value, for a block — the return value
// is what lets the block case still affect what follows it, without this
// function itself needing to know which kind of caller it has.
func cwdForSequence(cfg *expand.Config, stmts []*syntax.Stmt, entry cwd, out map[*syntax.Stmt]cwd) cwd {
	current := entry
	for _, stmt := range stmts {
		cwdForStmt(cfg, stmt, &current, out)
	}
	return current
}

// cwdForStmt records one statement's own effective directory and advances
// current for whatever comes after it in the same sequence.
//
// A statement can be a bare `cd` (advances current for its successors), a
// Subshell (recurses into a fresh copy of current and does NOT advance current
// itself — the whole point of the scoping), a BinaryCmd (the `&&`/`||`/`|`
// chain IS a sequence in its own right, flattened in the order its programs
// actually run), or anything else (recorded at the current directory and
// nothing to advance).
func cwdForStmt(cfg *expand.Config, stmt *syntax.Stmt, current *cwd, out map[*syntax.Stmt]cwd) {
	out[stmt] = *current

	switch cmd := stmt.Cmd.(type) {
	case *syntax.CallExpr:
		if len(cmd.Args) == 0 {
			// Bare assignments: `D=/x` sets a shell variable for what follows.
			// A backgrounded one runs in a subshell and sets nothing here.
			if !stmt.Background {
				current.applyAssigns(cfg, cmd.Assigns, false)
			}
			return
		}
		if forgetsVars(cfg, current, cmd) {
			current.vars = nil
		}
		cfg = cfgWith(cfg, current.vars)
		if payload, isEval, ok := evalPayloadOf(cfg, cmd); isEval {
			if !ok {
				// `eval "$SETUP"` — a payload this package cannot read may
				// `cd` anywhere. Opaque, not unknown: see cwd.opaque for why
				// the file-target side keeps resolving.
				current.opaque = true
				current.vars = nil
				return
			}
			// eval runs its payload in THIS shell, exactly like a Block: a
			// `cd` inside it moves every statement after the eval. Its own
			// statements' entries are not recorded — nothing in the outer tree
			// points at them — so they go to a scratch map.
			*current = cwdForSequence(cfg, payload, *current, map[*syntax.Stmt]cwd{})
			return
		}
		target, isCd, ok := cdTargetOf(cfg, cmd)
		if !isCd {
			return
		}
		if !ok {
			current.unknown = true
			return
		}
		*current = current.advance(target)
	case *syntax.FuncDecl:
		// A function body is its own scope, seeded with what is known where it
		// is defined; nothing in it reaches the statements after the definition.
		cwdForStmt(cfg, cmd.Body, ptr(*current), out)
		// Calls are not followed, so what the body exports globally is applied
		// from here on, as if it had been called: over-approximate, which is
		// the safe side for "is GIT_DIR set". A `local` stays in the body.
		syntax.Walk(cmd.Body, func(n syntax.Node) bool {
			if d, ok := n.(*syntax.DeclClause); ok {
				if exp, global := exportsOf(cfg, d); exp != nil && global {
					current.env = withEnv(current.env, exp)
				}
			}
			return true
		})
	case *syntax.DeclClause:
		// `export NAME=value` is a declaration clause, not a call; so are
		// `declare -x`, `typeset -x` and `local -x`.
		if exp, _ := exportsOf(cfg, cmd); exp != nil {
			current.env = withEnv(current.env, exp)
		}
		// Every NAME=value in it also assigns the shell variable. An option
		// this package does not model (`-n` makes a name reference) forgets all.
		for _, as := range cmd.Args {
			if as.Name == nil && as.Value != nil {
				if o, err := expand.Literal(cfg, as.Value); err != nil || !isLiteral(as.Value) || (strings.HasPrefix(o, "-") && strings.Contains(o, "n")) {
					current.vars = nil
				}
			}
		}
		if cmd.Variant != nil {
			current.applyAssigns(cfg, cmd.Args, true)
		}
	case *syntax.ArithmCmd, *syntax.LetClause, *syntax.TimeClause, *syntax.CoprocClause:
		// Run something this package does not follow: an arithmetic or a
		// timed/co-process body may assign a variable.
		current.vars = nil
	case *syntax.TestClause:
		if mayAssign(cmd) {
			current.vars = nil
		}
	case *syntax.Subshell:
		// A fresh scope, seeded with a COPY of what the parent currently
		// knows. Whatever `cd`s happen inside are recorded for the statements
		// inside (cwdForSequence writes their entries into the same out map),
		// and the sequence's own return value — what the subshell ends up
		// at — is deliberately DISCARDED: current here is untouched, so the
		// NEXT top-level statement sees exactly what this one did, whatever
		// the subshell did to itself.
		cwdForSequence(cfg, cmd.Stmts, *current, out)
	case *syntax.BinaryCmd:
		// `&&`, `||` and `|`/`|&` all reach here; syntax gives no separate node
		// for a pipeline, and mvdan/sh represents `a | b` as a BinaryCmd too —
		// but the two operator families do NOT belong to the same case. This
		// was measured wrong before the split below existed: `cd /elsewhere |
		// touch y` resolved `y` to `/elsewhere/y`, because X and Y were both
		// threaded through the SAME `current` pointer regardless of which
		// operator joined them.
		//
		// `&&`/`||` are true sequencing in the current shell: X really does
		// run, THEN Y, in the same process, so a `cd` in X is exactly as real
		// for Y as one before a `;` would be. `|`/`|&` are not sequencing at
		// all — a real shell forks BOTH sides into their own subprocesses and
		// connects them with a pipe, running them CONCURRENTLY, so neither
		// side's `cd` can reach the other side, and — the half the wrong
		// version above missed — neither can reach anything AFTER the
		// pipeline either, since the parent shell that continues past it
		// never ran either side's `cd` itself.
		switch cmd.Op {
		case syntax.Pipe, syntax.PipeAll:
			// Each side gets its OWN copy of current, exactly as a Subshell's
			// body does, and neither copy is written back — current, the
			// caller's own variable, is left exactly where it was for
			// whatever statement follows this one.
			cwdForStmt(cfg, cmd.X, ptr(*current), out)
			cwdForStmt(cfg, cmd.Y, ptr(*current), out)
		default:
			// `&&`, `||`: X first, then Y — the left-to-right order
			// BinaryCmd's own left associativity already puts them in (see
			// the doc comment on this file's tests), so `a && b && c` parsed
			// as `(a && b) && c` still visits a, b, c in the order they
			// actually run. Both share `current` because both really do run
			// in the one shell this function is tracking.
			cwdForStmt(cfg, cmd.X, current, out)
			cwdForStmt(cfg, cmd.Y, current, out)
		}
	case *syntax.Block:
		// `{ cd /a; }` in the CURRENT shell — a Block is not a new process,
		// unlike a Subshell, so its `cd` really does change the enclosing
		// scope's directory and must not be reverted the way a Subshell's is.
		// Flattened into the same sequence, and — unlike the Subshell case —
		// the sequence's return value IS taken, and written back into
		// current, so a statement after the block sees whatever `cd` happened
		// inside it.
		*current = cwdForSequence(cfg, cmd.Stmts, *current, out)
	case *syntax.IfClause:
		// `if`/`elif`/`else` all run in the CURRENT shell — none of them fork,
		// unlike a Subshell — so a `cd` in any branch is exactly as real
		// afterward as one before a `;` would be. What is NOT knowable
		// statically is WHICH branch ran, and that is the whole difficulty:
		// unlike a Block, where every statement unconditionally runs, at most
		// ONE arm's body executes, chosen by a condition this package does
		// not evaluate. See cwdForIfChain for how the whole elif/else chain
		// is walked and reconciled — this case is just its entry point, from
		// the cwd already in effect here.
		*current = cwdForIfChain(cfg, cmd, *current, out)
	case *syntax.WhileClause:
		// `while`/`until`: the condition (Cond) runs at least the first time
		// unconditionally, so its own `cd`s are sequenced like a Block's. The
		// body (Do) may then run ZERO or more times — a static reader cannot
		// know how many without evaluating Cond repeatedly, which this
		// package refuses to do (the same refusal that keeps it from reading
		// the filesystem or running command substitutions) — so what the
		// directory is AFTER the loop depends on an iteration count nothing
		// here can name.
		//
		// mergeBranches is reused for exactly the same reason an IfClause
		// needs it: "the loop body might not run at all" and "it might run"
		// are two branches, and if they agree — because the body has no cd in
		// it, the ordinary case — the shared answer is correct regardless of
		// how many times it actually ran; the recursion inside cwdForSequence
		// already makes a SECOND iteration agree with the first for the same
		// reason (a body with no cd leaves current unchanged, so running it
		// twice is the same as running it once, is the same as not running it
		// at all).
		*current = cwdForSequence(cfg, cmd.Cond, *current, out)
		zeroTimes := *current
		onceOrMore := cwdForSequence(cfg, cmd.Do, *current, out)
		*current = mergeBranches(zeroTimes, onceOrMore)
	case *syntax.ForClause:
		// `for`/`select`: the body (Do) runs zero or more times, over a list
		// this package does not enumerate (WordIter's Items may themselves be
		// unresolvable, and a CStyleLoop's iteration count is an arithmetic
		// expression). Same shape as WhileClause's Do, and the same merge:
		// zero iterations (current unchanged) against one-or-more (the body
		// run once, which is what any FURTHER iteration would also see if the
		// body's own cd's are idempotent from a fixed starting point — and if
		// they are not, i.e. two different iterations could leave two
		// different directories, mergeBranches's disagreement rule already
		// answers unknown, which is the honest reflection of that).
		if wi, ok := cmd.Loop.(*syntax.WordIter); ok && wi.Name != nil {
			current.setVar(wi.Name.Value, "", false)
		} else if _, ok := cmd.Loop.(*syntax.CStyleLoop); ok {
			current.vars = nil
		}
		zeroTimes := *current
		onceOrMore := cwdForSequence(cfg, cmd.Do, *current, out)
		*current = mergeBranches(zeroTimes, onceOrMore)
	case *syntax.CaseClause:
		*current = cwdForCase(cfg, cmd, *current, out)
	}
}

// cwdForCase reconciles a case clause's items the same way cwdForIfChain
// reconciles an if/elif/else chain: each item's Stmts is its own mutually
// exclusive path from entry, chosen by matching Word against a pattern this
// package does not evaluate, plus the always-possible path where NO item
// matches at all (case, unlike if, has no implicit final "else" — an
// unmatched value simply runs nothing).
//
// Every item is included in the merge regardless of its terminator (`;;`,
// `;&`, `;;&`) — which is a simplification, and a deliberately conservative
// one rather than an oversight. `;&` falls through into the NEXT item's
// Stmts unconditionally and `;;&` falls through into testing the next
// pattern, so a matched item's true successor set is sometimes wider than
// "just its own Stmts." Treating every item as independent, ignoring
// fallthrough, still gives the RIGHT answer whenever it matters in practice:
// if none of the items anywhere in the clause contain a cd, every path
// (fallthrough or not) trivially agrees with unchanged, and the common case
// is unaffected. Where fallthrough could actually change the answer — one
// item cd's and a `;&`/`;;&` before it could route execution through it
// unconditionally from an item that did NOT itself cd — mergeBranches's
// disagreement rule already answers unknown for the case that contains the
// cd, which only widens how far that unknown could honestly have reached
// rather than reporting a wrong answer as a right one. Modelling fallthrough
// precisely would mean tracking which items chain into which, for a shape
// rare enough in agent-written command lines that the cost is not owed here.
func cwdForCase(cfg *expand.Config, cmd *syntax.CaseClause, entry cwd, out map[*syntax.Stmt]cwd) cwd {
	branches := make([]cwd, 0, len(cmd.Items)+1)
	// The always-possible "nothing matched" path.
	branches = append(branches, entry)
	for _, item := range cmd.Items {
		branches = append(branches, cwdForSequence(cfg, item.Stmts, entry, out))
	}
	return mergeBranches(branches...)
}

// cwdForIfChain walks one if/elif/.../else chain and returns the cwd in
// effect once it has finished, reconciling every arm that could have run.
//
// mvdan/sh represents the whole chain as NESTED IfClauses: `if a; then b;
// elif c; then d; else e; fi` is one IfClause (Cond=a, Then=b) whose Else is
// ANOTHER IfClause (Cond=c, Then=d) whose OWN Else is a THIRD IfClause with
// an EMPTY Cond and Then=e — see this file's own doc comment on the
// grammar's Else field ("if non-nil, an elif or an else"). Telling those two
// shapes apart matters: an elif is itself conditional, so its own Then and
// whatever follows IT need the same merge this function is doing; a plain
// else is NOT conditional GIVEN that every earlier condition already failed
// — its Then runs unconditionally along that path, and running it through
// this same merge machinery a second time (treating its own absent Else as
// "an empty branch that might disagree") is what a first version of this
// function got wrong, measured: `if true; then cd sub; else cd sub; fi`
// reported unknown even though both arms agree on the exact same directory,
// because the else-body's OWN (nonexistent) else was compared against it and
// manufactured a disagreement that was never really there.
//
// So a plain else is handled directly, inline, rather than by recursing into
// this function on a synthetic wrapper: entry is the cwd before entering this
// chain (already past its Cond, which the caller sequences), and env is
// walked straight through as the else-body's own answer, with no merge
// against anything.
func cwdForIfChain(cfg *expand.Config, cmd *syntax.IfClause, entry cwd, out map[*syntax.Stmt]cwd) cwd {
	entry = cwdForSequence(cfg, cmd.Cond, entry, out)
	afterThen := cwdForSequence(cfg, cmd.Then, entry, out)

	switch {
	case cmd.Else == nil:
		// No further arm at all — `if a; then b; fi` with no else. The
		// implicit "condition was false" path leaves the directory exactly
		// where entry already has it: nothing ran.
		return mergeBranches(afterThen, entry)
	case len(cmd.Else.Cond) == 0:
		// A plain else: unconditional GIVEN this path, not itself a further
		// branch to reconcile. Its Then is exactly as real as this IfClause's
		// own Then would have been, from the same entry point.
		afterElse := cwdForSequence(cfg, cmd.Else.Then, entry, out)
		return mergeBranches(afterThen, afterElse)
	default:
		// An elif: itself conditional, so its own chain (which may end in
		// ANOTHER elif, or a plain else, or nothing) is resolved by the same
		// function, recursively, from this same entry point — `cmd.Else.Cond`
		// running unconditionally along THIS path exactly as `cmd.Cond` does
		// for the outer IfClause, which is what makes the recursive call
		// correct rather than merely convenient.
		afterElifChain := cwdForIfChain(cfg, cmd.Else, entry, out)
		return mergeBranches(afterThen, afterElifChain)
	}
}

// mergeBranches reconciles what two OR MORE mutually exclusive execution
// paths leave the effective directory at, into the one answer a statement
// AFTER all of them must be resolved against.
//
// Two callers pass exactly two: an if's Then vs. its Else (a loop's body
// always exists, but "the condition was false from the start" is its own
// path, structurally identical to an empty Else), and a loop's
// zero-iterations path vs. its one-or-more path. cwdForCase passes one per
// CaseItem plus the implicit "nothing matched" path, which is why this takes
// a slice rather than a fixed pair — a case clause can have any number of
// patterns, and each is exactly as mutually exclusive with the others as an
// if's two arms are with each other.
//
// Agreement is the only thing that survives: if every path is known AND
// lands on the identical directory, that shared answer is correct no matter
// which path actually ran, which is what lets the overwhelmingly common
// case — a branch, loop body, or case item containing no `cd` at all — pass
// straight through unaffected rather than being blurred to unknown on every
// `if`/`for`/`while`/`case` a line happens to contain. Any disagreement, in
// any direction (one unknown, or two known but different), means which path
// ran decides the directory, and this package does not evaluate conditions,
// count iterations, or match case patterns, so the honest answer is unknown.
//
// Called with zero branches by nothing today, and the empty case is answered
// honestly anyway rather than left to panic on branches[0]: no branches ran
// is not evidence of a directory, known or otherwise.
func mergeBranches(branches ...cwd) cwd {
	out := mergeDirs(branches...)
	out.env = mergeEnvs(branches...)
	out.vars = mergeVars(branches...)
	for _, b := range branches {
		out.noVars = out.noVars || b.noVars
	}
	return out
}

// mergeVars keeps a variable only where every path agrees on its value; a
// variable one path left unknown, or set differently, is unknown afterwards.
func mergeVars(branches ...cwd) map[string]string {
	if len(branches) == 0 {
		return nil
	}
	var out map[string]string
	for k, v := range branches[0].vars {
		same := true
		for _, b := range branches[1:] {
			if bv, ok := b.vars[k]; !ok || bv != v {
				same = false
				break
			}
		}
		if same {
			if out == nil {
				out = map[string]string{}
			}
			out[k] = v
		}
	}
	return out
}

// forgetsVars reports whether this call may assign a shell variable by a route
// this package does not follow: a builtin that writes one, a program word it
// cannot read, an arithmetic or `:=` expansion in any word.
func forgetsVars(cfg *expand.Config, c *cwd, call *syntax.CallExpr) bool {
	if mayAssign(call) || !isLiteral(call.Args[0]) {
		return true
	}
	first, err := expand.Literal(cfg, call.Args[0])
	if err != nil {
		return true
	}
	if first == "printf" {
		for _, a := range call.Args[1:] {
			if lit, err := expand.Literal(cfg, a); err != nil || strings.HasPrefix(lit, "-v") {
				return true
			}
		}
	}
	return varWriters[first]
}

// mayAssign reports whether anything under n assigns a shell variable from
// inside an expansion: `$((D=1))`, `${D:=x}`, an arithmetic command.
func mayAssign(n syntax.Node) bool {
	found := false
	syntax.Walk(n, func(n syntax.Node) bool {
		switch x := n.(type) {
		case *syntax.ArithmExp, *syntax.ArithmCmd:
			found = true
		case *syntax.ParamExp:
			if x.Exp != nil && (x.Exp.Op == syntax.AssignUnset || x.Exp.Op == syntax.AssignUnsetOrNull) {
				found = true
			}
		}
		return !found
	})
	return found
}

// mergeEnvs is the union of what any path exported: a variable one path set
// may be set at runtime, and a consumer asking "is GIT_DIR set" must hear
// yes. The value survives only where every path agrees on it, else "".
func mergeEnvs(branches ...cwd) map[string]string {
	var out map[string]string
	for _, b := range branches {
		for k, v := range b.env {
			if out == nil {
				out = map[string]string{}
			}
			if old, seen := out[k]; seen && old != v {
				v = ""
			} else if !seen {
				for _, o := range branches {
					if ov, ok := o.env[k]; !ok || ov != v {
						v = ""
						break
					}
				}
			}
			out[k] = v
		}
	}
	return out
}

func mergeDirs(branches ...cwd) cwd {
	if len(branches) == 0 {
		return cwd{unknown: true}
	}
	first := branches[0]
	first.env = nil
	if first.unknown {
		return cwd{unknown: true}
	}
	for _, b := range branches[1:] {
		if b.unknown || b.dir != first.dir {
			return cwd{unknown: true}
		}
		// An opaque eval on any path may have moved the shell on that path:
		// the agreed directory stands, opaque.
		first.opaque = first.opaque || b.opaque
	}
	return first
}

// stmtAt is the reverse index fileTargetsAt needs: given the *syntax.Stmt the
// outer walk is currently on, what effective directory applies to a target
// found under it.
//
// A tiny wrapper rather than reading the map directly at each call site, so
// the "stmt was nil, or this file carried no cwd map at all" case — which
// should not arise from fileTargetsAt's own construction, but costs nothing to
// answer honestly — has one place to live. Nil or absent both mean "no `cd`
// information available", which resolves exactly like startCwd: a target is
// left exactly as relative as it always was, matching this package's
// behaviour before any of this file existed.
func stmtAt(cwds map[*syntax.Stmt]cwd, stmt *syntax.Stmt) cwd {
	if cwds == nil || stmt == nil {
		return startCwd
	}
	return cwds[stmt]
}

// resolveAgainst resolves every path a batch of targets names — each target's
// own Path, its Into sources, and its Payload's From sources alike — against
// one effective directory, and drops whichever targets that cannot be done
// for honestly.
//
// All three fields are resolved, not just Path, because all three name a path
// the SAME statement wrote down and every one of them is read against the
// filesystem later by a caller that has no idea a `cd` was ever in the
// picture:
//
//	Path         the target itself. Untouched if already absolute; see
//	             resolveTargetAt.
//	Into         `cp a.md b.md target/`'s sources, read by filemod's
//	             expandIntoDirectories via filepath.Join(t.Path, name) and
//	             looked up on disk. Left unresolved, a `cd`-affected Into entry
//	             would still be joined onto the (now correctly resolved) Path,
//	             which is fine for the JOIN, but any of it that also feeds a
//	             lookup keyed on the source alone would be wrong — kept
//	             resolved here for the same reason From is, and so the two
//	             stay in one place rather than one being special-cased later.
//	Payload.From the bytes-are-whatever-this-holds references PayloadCopyOf
//	             carries — `cp`, `mv`, `cat a.md b.md > c.md` — read by
//	             filemod's resolvePayload with a bare os.ReadFile(from). That
//	             call has no cwd of its own at all; it trusts the path handed
//	             to it completely. Left relative after a `cd`, `cd /elsewhere
//	             && cp a.md b.md` would report b.md's correct absolute Path
//	             while still claiming its content is `a.md` read from
//	             filemod's OWN process directory — a source that likely does
//	             not exist there, or worse, one that does and is a different
//	             file entirely.
//
// A target whose Path cannot be resolved (at is unknown) is dropped outright
// rather than kept with an empty or unresolved Path — the same convention
// targetsFor already applies to a word this package cannot be sure of: what
// can be seen is emitted, what cannot is left alone rather than guessed at. A
// target survives with an unresolved Into or From entry only by dropping that
// one entry, on the same honesty rule: a copy source that cannot be resolved
// is not one this package can claim bytes from, so PayloadCopyOf is corrected
// down to PayloadNone rather than left naming a path that means something
// else than it says.
func resolveAgainst(targets []FileTarget, at cwd) []FileTarget {
	if len(targets) == 0 {
		return targets
	}
	out := make([]FileTarget, 0, len(targets))
	for _, t := range targets {
		resolved, ok := resolveTargetAt(t.Path, at)
		if !ok {
			continue
		}
		t.Path = resolved

		if len(t.Into) > 0 {
			into := make([]string, 0, len(t.Into))
			for _, src := range t.Into {
				if r, ok := resolveTargetAt(src, at); ok {
					into = append(into, r)
				}
			}
			t.Into = into
		}

		if t.Payload.Kind == PayloadCopyOf {
			from := make([]string, 0, len(t.Payload.From))
			allResolved := true
			for _, src := range t.Payload.From {
				r, ok := resolveTargetAt(src, at)
				if !ok {
					allResolved = false
					break
				}
				from = append(from, r)
			}
			if allResolved {
				t.Payload.From = from
			} else {
				// One of the sources this copy names cannot be resolved. The
				// same all-or-nothing rule resolvePayload itself applies to an
				// unreadable source: a partial answer would be confidently
				// wrong rather than honestly absent, so the claim on content
				// is withdrawn rather than built on some sources and not
				// others.
				t.Payload = Payload{}
			}
		}

		out = append(out, t)
	}
	return out
}

// redirectNames are the variables that move git to another repository. A
// declaration this package cannot read (`declare $opt $name=x`) may export any
// of them, so each is recorded with an unknown value: a consumer asking "is
// GIT_DIR set" hears yes and fails closed.
var redirectNames = []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_NAMESPACE"}

// exportsOf reads a declaration clause and returns what it exports (value ""
// when not literal, and for a bare `export NAME`, whose value is whatever NAME
// already was); nil when it exports nothing. `export` always exports;
// `declare`, `typeset` and `local` do with a literal option containing `x`
// (`-x`, `-gx`, `-rx`), and, because an option this package cannot evaluate
// might be one, also with any non-literal bare word.
func exportsOf(cfg *expand.Config, d *syntax.DeclClause) (env map[string]string, global bool) {
	if d.Variant == nil {
		return nil, false
	}
	exports := d.Variant.Value == "export"
	// global: the export outlives a function body that makes it (`export`,
	// `declare -g`; never `local`). An unreadable word may be `-g`.
	global = exports
	unreadable := false
	for _, as := range d.Args {
		if as.Name != nil || as.Value == nil {
			continue
		}
		if !isLiteral(as.Value) {
			unreadable = true
			continue
		}
		if o, err := expand.Literal(cfg, as.Value); err == nil && strings.HasPrefix(o, "-") && !strings.HasPrefix(o, "--") && strings.Contains(o, "g") && d.Variant.Value != "local" {
			global = true
		}
		if o, err := expand.Literal(cfg, as.Value); err == nil && strings.HasPrefix(o, "-") && !strings.HasPrefix(o, "--") && strings.Contains(o, "x") {
			exports = true
		}
	}
	if !exports && !unreadable {
		return nil, false
	}
	if unreadable && d.Variant.Value != "local" {
		global = true
	}
	out := map[string]string{}
	if unreadable {
		for _, n := range redirectNames {
			out[n] = ""
		}
	}
	for _, as := range d.Args {
		if as.Name == nil {
			continue
		}
		out[as.Name.Value] = ""
		if as.Value != nil && !as.Append && as.Array == nil && isLiteral(as.Value) {
			if v, err := expand.Literal(cfg, as.Value); err == nil {
				out[as.Name.Value] = v
			}
		}
	}
	return out, global
}

// validEnvName reports whether s is a shell variable name.
func validEnvName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (i > 0 && r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}

// assignsOf reads the `NAME=value` prefix of one call.
func assignsOf(cfg *expand.Config, call *syntax.CallExpr) map[string]string {
	var out map[string]string
	for _, as := range call.Assigns {
		if as.Name == nil {
			continue
		}
		if out == nil {
			out = map[string]string{}
		}
		out[as.Name.Value] = ""
		if as.Value != nil && !as.Append && as.Array == nil && isLiteral(as.Value) {
			if v, err := expand.Literal(cfg, as.Value); err == nil {
				out[as.Name.Value] = v
			}
		}
	}
	return out
}

// underlay returns inv's own env with base beneath it: what the invocation
// itself set wins over what its scope or its wrapper did.
func underlay(inv map[string]string, base map[string]string) map[string]string {
	if len(base) == 0 {
		return inv
	}
	return withEnv(base, inv)
}

// withEnv returns a copy of base with add laid over it.
func withEnv(base, add map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(add))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range add {
		out[k] = v
	}
	return out
}

// plainKnown reports whether w is literals and plain `$NAME` / `${NAME}`
// references to variables cfg holds: a word whose expansion is certain.
func plainKnown(cfg *expand.Config, w *syntax.Word) bool {
	ok := true
	syntax.Walk(w, func(n syntax.Node) bool {
		switch n.(type) {
		case *syntax.CmdSubst, *syntax.ProcSubst, *syntax.ArithmExp:
			ok = false
		}
		return ok
	})
	return ok && paramsKnown(cfg, w)
}
