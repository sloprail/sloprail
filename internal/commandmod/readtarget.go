package commandmod

import (
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// This file is the READ-side counterpart to filetarget.go's write detection: it
// answers "does this command line read this file's bytes", for the callers that
// need to know a path was genuinely looked at rather than only named.
//
// The two are symmetric in mechanics — the same parser, the same per-word
// expansion and certainty test, the same wrapper/interpreter-payload unwrapping —
// and deliberately reuse every one of those primitives rather than re-deriving
// them. What differs is the vocabulary: filetarget.go's knownBins names what a
// binary WRITES (rm, mv, cp, sed -i, …); readBins below names what a binary
// READS its operands AS — the file-reading utilities filetarget.go explicitly
// excludes (see its comment on `cat` being deliberately absent there).
//
// The one caller this exists for today asks a narrower question than
// FileTargets does: not "every file this line touches", but "does this line
// read THIS ONE path". ReadsFile answers exactly that, so a caller checking one
// known path (a skill's SKILL.md) does not have to enumerate a whole target list
// and compare paths itself — though it is built on the same target list this
// file constructs, for a caller that ever needs the fuller answer.

// readBins are the binaries whose bare operands are files they read in full —
// the read-only mirror of knownBins.
//
// Only whole-file readers. `grep`, `awk`, `wc` and friends also read their
// operands, but this list exists for one purpose — deciding whether a command
// line is evidence that an agent looked at a file's CONTENT the way opening it
// would — and the utilities named here are the ones whose ordinary use is
// exactly that: printing or paging a file's bytes for a person or an agent to
// read. Keeping the list to that use, rather than every program that happens to
// open() a path, is the same judgement filemod's knownBins makes about writes:
// a name worth maintaining is one whose omission has a measured cost, and an
// agent reading a file with `awk '{print}' f` rather than `cat f` is not the
// case this exists to catch.
var readBins = map[string]bool{
	"cat":  true,
	"head": true,
	"tail": true,
	"less": true,
	"more": true,
	"bat":  true,
}

// ReadTarget is one path a command line is about to read in full.
type ReadTarget struct {
	// Path is the path as the command line names it, quoting undone, exactly as
	// FileTarget.Path is — not resolved against a root or cleaned.
	Path string
}

// ReadTargets finds every path a command line reads whole, through one of
// readBins or an input redirection (`< file`, `<<< "$(cat file)"` is not
// resolved, but a plain `< file` is syntax and needs no vocabulary at all).
//
// Mirrors FileTargets exactly in structure — parse once, walk both node kinds
// that matter, recover from a panic the same way and for the same reason (a
// parser bug here must degrade the read-detection rather than exit the
// process non-zero, which a harness would read as an unrelated refusal).
func ReadTargets(raw string) []ReadTarget {
	return readTargetsAt(raw, 0)
}

func readTargetsAt(raw string, depth int) (targets []ReadTarget) {
	defer func() {
		_ = recover()
	}()

	f, err := syntax.NewParser().Parse(strings.NewReader(raw), "")
	if err != nil {
		return nil
	}

	cfg := newConfig()
	syntax.Walk(f, func(n syntax.Node) bool {
		switch node := n.(type) {
		case *syntax.Stmt:
			targets = append(targets, readsFromRedirs(cfg, node)...)
		case *syntax.CallExpr:
			targets = append(targets, readsFromCall(cfg, node, depth)...)
		}
		return true
	})
	return targets
}

// readsFromRedirs reads the file-reading redirections off one statement — `<`
// and heredoc-from-file forms are the shell's own syntax, needing no binary
// vocabulary at all, the same reasoning fromRedirs gives for the write side.
//
// Only RdrIn (`<`) and RdrInOut (`<>`, which can read as well as write) name a
// file that is read. `<<` and `<<<` read a literal body the line carries, not a
// file, so they name no path here.
func readsFromRedirs(cfg *expand.Config, stmt *syntax.Stmt) []ReadTarget {
	if stmt == nil {
		return nil
	}
	var targets []ReadTarget
	for _, r := range stmt.Redirs {
		switch r.Op {
		case syntax.RdrIn, syntax.RdrInOut:
		default:
			continue
		}
		path, ok := literalWord(cfg, r.Word)
		if !ok || path == "" || hasGlob(path) {
			continue
		}
		targets = append(targets, ReadTarget{Path: path})
	}
	return targets
}

// readsFromCall reads the file-reading intent off one parsed call, the same
// resolve-per-word-then-dispatch shape fromCall uses for writes.
func readsFromCall(cfg *expand.Config, call *syntax.CallExpr, depth int) []ReadTarget {
	if len(call.Args) == 0 {
		return nil
	}

	fields := expandPerWord(cfg, call.Args)
	if len(fields) == 0 || !fields[0].literal {
		// The same certainty requirement resolve and fromCall apply to the
		// program word: an argument that slid into position, or one resolved
		// only by assuming an empty environment, names no program this can be
		// sure of.
		return nil
	}

	argv := make([]string, 0, len(fields))
	for _, f := range fields {
		if f.lost || !f.literal {
			argv = append(argv, "")
			continue
		}
		argv = append(argv, f.value)
	}
	return readTargetsForArgv(argv, depth)
}

// readTargetsForArgv reads one resolved vector against readBins, unwrapping
// wrappers and interpreter payloads exactly as targetsForArgv does for writes —
// the same recursive structure, reused rather than re-derived, because the
// unwrapping question ("what does this line actually run, through a sudo or a
// sh -c") does not depend on whether the answer sought is a read or a write.
func readTargetsForArgv(argv []string, depth int) []ReadTarget {
	if len(argv) == 0 || basename(argv[0]) == "" {
		return nil
	}

	var targets []ReadTarget
	if readBins[basename(argv[0])] {
		for _, p := range operands(argv) {
			if p == "" || hasGlob(p) {
				continue
			}
			targets = append(targets, ReadTarget{Path: p})
		}
	}
	if nested := unwrap(asWords(argv)); len(nested) > 0 {
		targets = append(targets, readTargetsForArgv(values(nested), depth)...)
	}
	for _, cmd := range unwrapInfix(asWords(argv)) {
		targets = append(targets, readTargetsForArgv(values(cmd), depth)...)
	}
	if depth < maxUnwrapDepth {
		if payload, ok := interpreterPayload(asWords(argv)); ok {
			targets = append(targets, readTargetsAt(payload, depth+1)...)
		}
	}
	return targets
}

// ReadsFile reports whether a command line reads path's bytes in full, either
// through a known whole-file reader (readBins) or a `<`/`<>` redirection.
//
// path is compared for exact string equality against what the command line
// names, after quoting is undone and expansion is applied — the same certainty
// standard every other path in this package is held to, so `cat $F` where F is
// unset in the empty environment answers false rather than a guess. A caller
// wanting to compare against a resolved absolute path should pass one; this
// does no normalisation of its own; commandmod does not resolve against a root
// for the same reason FileTargets does not — that is a question about the tree,
// which is the caller's to answer, not the command line's.
func ReadsFile(raw, path string) bool {
	if path == "" {
		return false
	}
	for _, t := range ReadTargets(raw) {
		if t.Path == path {
			return true
		}
	}
	return false
}
