package commandmod

import (
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// Effect is what a command line is about to do to one path.
//
// Two values, not three, and the omission is the design rather than an
// oversight. There is no Create: a Pre event announcing a creation must carry
// the content that would be written, and a command line does not say what bytes
// will result — `sed -i` states a transformation, not an outcome. So a command
// aimed at a path that does not exist yet reports Write and the file module
// resolves it against the tree, which is the only place the answer actually is.
// See events/main.tsp in the spec for the argument, and extractCommand below
// for what that resolution does with a path that is absent.
type Effect int

const (
	// Write means the file's bytes are about to change: a redirection into it,
	// a `sed -i`, a `cp` onto it. What they will change TO is not known and is
	// not claimed.
	Write Effect = iota

	// Remove means the file is about to stop existing — `rm`, or the source of
	// an `mv`.
	Remove
)

// FileTarget is one path a command line is about to touch, and what it will do
// to it.
type FileTarget struct {
	// Path is the path as the command line names it, with quoting undone and
	// nothing else done to it. It is not resolved against a root or cleaned
	// here: this package knows what a command line says, and where the
	// repository sits is the file module's question.
	Path string

	// Effect is what is about to happen to it.
	Effect Effect
}

// FileTargets finds every path a command line is about to change.
//
// Exported for the same reason ExtractCommand is: it is a pure function of a
// string, so it can be tested by handing it a command line and reading what
// comes back, with no harness and no payload in the way. The file module is its
// caller — see filemod's extractPending, and the placement argument there.
//
// It returns paths, never events. What kind of event a path deserves depends on
// what is at that path in the tree, and this package does not read the tree:
// doing so would make a command's meaning depend on the filesystem, which is
// exactly what safeConfig refuses for expansion and for the same reason.
//
// Like walk, it never returns an error and recovers from a panic. A parser bug
// here would otherwise exit the guardrail non-zero, which a harness reads as a
// refusal — so a bug would block the agent's work rather than degrade the rule.
// Whatever was collected before a panic is kept: half a list still lets a rule
// fire, where none lets it silently pass.
func FileTargets(raw string) (targets []FileTarget) {
	defer func() {
		_ = recover()
	}()

	f, err := syntax.NewParser().Parse(strings.NewReader(raw), "")
	if err != nil {
		// An unparseable line yields no targets rather than a guess, the same
		// answer walk gives and for the same reason.
		return nil
	}

	cfg := newConfig()

	// Two node types, because the two sources live at different levels of the
	// tree and a traversal matching one would miss the other entirely.
	//
	// Redirections hang off *syntax.Stmt, NOT off the CallExpr — which is why
	// walk, matching CallExpr alone, has never seen one. `echo x > out.md` is a
	// Stmt whose Cmd is the CallExpr for echo and whose Redirs holds the `>`.
	//
	// Known binaries are read off the CallExpr, which is where the argument
	// vector is.
	syntax.Walk(f, func(n syntax.Node) bool {
		switch node := n.(type) {
		case *syntax.Stmt:
			targets = append(targets, fromRedirs(cfg, node.Redirs)...)
		case *syntax.CallExpr:
			targets = append(targets, fromCall(cfg, node)...)
		}
		return true
	})
	return targets
}

// fromRedirs reads the file-touching redirections off one statement.
//
// This is the half with no vocabulary to maintain. A redirection is SYNTAX: the
// shell grammar says `>` truncates a file and `>>` appends to one, and no
// project renames `>`. Where a binary allowlist can fall behind a tool being
// renamed and go silent, this cannot drift at all — the only thing that could
// change it is the shell grammar itself, and then the parser stops parsing
// rather than quietly reporting nothing.
func fromRedirs(cfg *expand.Config, redirs []*syntax.Redirect) []FileTarget {
	var targets []FileTarget
	for _, r := range redirs {
		if !writesAFile(r) {
			continue
		}
		path, ok := literalWord(cfg, r.Word)
		if !ok {
			continue
		}
		// Every writing redirection is Write and none is Remove. `>` truncates
		// a file rather than unlinking it — the path still exists afterwards,
		// with different bytes — so reporting it as a removal would announce a
		// deletion that does not happen.
		//
		// Built through targetsFor rather than here, so a redirection's path
		// passes the same certainty tests an operand does. The glob check is the
		// one that matters: `> *.md` is exactly as unknowable as `rm *.md`, and
		// a second construction site would be a second place to forget that.
		targets = append(targets, targetsFor([]string{path}, Write)...)
	}
	return targets
}

// writesAFile reports whether a redirection operator sends output INTO a file
// that the command line names.
//
// Enumerated positively rather than by excluding the read operators, so an
// operator this list has not heard of is treated as touching nothing. That is
// the safe direction for an unknown: a missed event is a rule that does not
// fire on one exotic spelling, while a wrong one is a rule firing on a file the
// command never writes.
//
// What is deliberately NOT here:
//
//	RdrIn, Hdoc, DashHdoc, WordHdoc  reads. `< in.md` and `<<EOF` take input
//	                                 from a file or a here-document; neither
//	                                 changes the file named.
//	DplIn, DplOut                    file-descriptor duplication — `2>&1`. The
//	                                 word is a descriptor number, not a path,
//	                                 and reporting it would announce a file
//	                                 called "1".
//	RdrInOut                         `<>` opens for reading AND writing without
//	                                 truncating. It genuinely can write, and it
//	                                 is included below for that reason.
func writesAFile(r *syntax.Redirect) bool {
	switch r.Op {
	case syntax.RdrOut, // >  truncate and write
		syntax.AppOut,     // >> append
		syntax.RdrClob,    // >| write, overriding noclobber
		syntax.AppClob,    // >>| append, overriding noclobber (zsh)
		syntax.RdrAll,     // &> both streams, truncating
		syntax.RdrAllClob, // &>| the same, overriding noclobber (zsh)
		syntax.AppAll,     // &>> both streams, appending
		syntax.AppAllClob, // &>>| the same, overriding noclobber (zsh)
		syntax.RdrInOut:   // <> open for read and write
		return true
	}
	return false
}

// literalWord resolves one word to a certain path, or declines.
//
// The same literalness test resolve applies to a program name, applied here for
// the same reason: `> $OUT` resolves to a path only by assuming the empty
// environment is the real one, and a file event naming a path the command will
// not actually touch is a rule firing on the wrong file. Declining is the
// honest answer, and it is the one this whole package already gives for a
// program it cannot name.
//
// A word that expands to several fields is also declined. `> $FILES` producing
// two paths means the shell would fail on an ambiguous redirect anyway, and
// picking one of them would be a guess about which.
func literalWord(cfg *expand.Config, w *syntax.Word) (string, bool) {
	if w == nil || !isLiteral(w) {
		return "", false
	}
	got, err := expand.Fields(cfg, w)
	if err != nil || len(got) != 1 || got[0] == "" {
		return "", false
	}
	return got[0], true
}
