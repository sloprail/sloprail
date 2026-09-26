package commandmod

import (
	"path"

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
}

// startCwd is the effective directory nothing has yet moved out of: the empty,
// known state every top-level sequence begins in, and what a subshell's own
// scope is seeded with on entry (COPIED, not shared — see cwdForSequence).
var startCwd = cwd{}

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
		return cwd{dir: path.Clean(target)}
	}
	if c.dir == "" {
		// Relative, composed onto "wherever this sequence started" — which
		// stays exactly that: still relative, one level deeper. Not resolved
		// against any root, because this package has none.
		return cwd{dir: path.Clean(target)}
	}
	return cwd{dir: path.Clean(path.Join(c.dir, target))}
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

// cdTargetOf reads a `cd` invocation's own argument, and reports whether this
// call is a `cd` at all.
//
// Only the shape this package can be sure of is resolved: exactly one operand,
// itself a single literal word that expanded to exactly one field. Everything
// else — no operand, `cd -`, more than one operand (which real cd also
// rejects, but a non-literal one is refused for the same reason literalWord
// refuses everywhere else in this package: guessing at an environment this
// does not have), or an operand that is not certain — reports ok=false, which
// callers read as "this cd could not be resolved" rather than "this is not a
// cd".
//
// cfg is the same expansion FileTargets already builds once per call and
// passes down; a `cd` argument is resolved exactly the way any other operand
// in this package is.
func cdTargetOf(cfg *expand.Config, call *syntax.CallExpr) (target string, isCd bool, ok bool) {
	if len(call.Args) == 0 || !isLiteral(call.Args[0]) {
		return "", false, false
	}
	got, err := expand.Fields(cfg, call.Args[0])
	if err != nil || len(got) != 1 || got[0] != "cd" {
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
	if !isLiteral(arg) {
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
	return argFields[0], isCd, true
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
	cwdForSequence(cfg, f.Stmts, startCwd, out)
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
		target, isCd, ok := cdTargetOf(cfg, cmd)
		if !isCd {
			return
		}
		if !ok {
			current.unknown = true
			return
		}
		*current = current.advance(target)
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
		// `&&`, `||` and `|` all reach here; syntax gives no separate node for
		// a pipeline, and mvdan/sh represents `a | b` as a BinaryCmd too. A
		// pipe's two sides do not run in the same shell as each other in a real
		// shell (each is its own subprocess), but neither side can `cd` the
		// PARENT shell either way, and what this package cares about is only
		// the SEQUENCE `cd` itself appears in — `&&`/`||`/`;` — so treating a
		// pipeline identically costs nothing: `cd` is never usefully one side
		// of a pipe, and if it appears there its effect is scoped to that
		// subprocess exactly the way a subshell's is, which recursing here
		// already gives it.
		//
		// X first, then Y — the left-to-right order BinaryCmd's own left
		// associativity already puts them in (see the doc comment on this
		// file's tests), so `a && b && c` parsed as `(a && b) && c` still
		// visits a, b, c in the order they actually run.
		cwdForStmt(cfg, cmd.X, current, out)
		cwdForStmt(cfg, cmd.Y, current, out)
	case *syntax.Block:
		// `{ cd /a; }` in the CURRENT shell — a Block is not a new process,
		// unlike a Subshell, so its `cd` really does change the enclosing
		// scope's directory and must not be reverted the way a Subshell's is.
		// Flattened into the same sequence, and — unlike the Subshell case —
		// the sequence's return value IS taken, and written back into
		// current, so a statement after the block sees whatever `cd` happened
		// inside it.
		*current = cwdForSequence(cfg, cmd.Stmts, *current, out)
	}
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
