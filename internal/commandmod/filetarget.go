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

	// Payload is what the LINE determines about the resulting bytes, which for
	// most commands is nothing.
	//
	// The zero value is PayloadNone, so a target built without thinking about
	// content claims none — which is why every construction site in this
	// package could stay as it was and only the ones that genuinely know
	// something had to change.
	//
	// It is a statement about the line, not the outcome: `cp a.md b.md` yields
	// PayloadCopyOf naming a.md, and turning that into bytes is filemod's,
	// because it means reading the filesystem and this package does not. See
	// payload.go for the whole argument.
	Payload Payload
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
func FileTargets(raw string) []FileTarget {
	return fileTargetsAt(raw, 0)
}

// fileTargetsAt is FileTargets at a known interpreter-payload depth.
//
// The depth is threaded through the parse rather than kept in a package
// variable, for the reason walkAt states: two payloads in one line are
// independent, and a shared counter would make the second one's budget depend
// on the first one's.
//
// Its own recover, so a panic parsing a PAYLOAD cannot take the outer line's
// targets with it. A payload is text an agent chose and this is one of the two
// places the module feeds such text back into the parser, so if any input can
// find a parser bug it is this one — and losing the outer line's files on top
// of the payload's would turn a partial answer into no answer.
func fileTargetsAt(raw string, depth int) (targets []FileTarget) {
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
	// The statement currently being walked, so a CallExpr can reach the
	// here-document that hangs off its parent.
	//
	// `tee f.md <<'EOF'` names its file through the known-binary table (a
	// CallExpr operand) while the bytes are on the Stmt's redirection list, so
	// neither node can answer alone. Walk visits the Stmt first and every
	// CallExpr it contains before moving on, so recording it here is enough to
	// pair them.
	//
	// A nested statement — a command substitution, a subshell — overwrites this
	// and restores nothing, which is deliberate: its own heredoc is the one its
	// calls should see, and by the time the outer statement's later calls are
	// visited the walk has already left. The failure mode if that reasoning is
	// ever wrong is an UNCLAIMED payload rather than a wrong one, because
	// stmtHdoc is consulted only for a program known to consume stdin.
	var stmt *syntax.Stmt
	syntax.Walk(f, func(n syntax.Node) bool {
		switch node := n.(type) {
		case *syntax.Stmt:
			stmt = node
			targets = append(targets, fromRedirs(cfg, node)...)
		case *syntax.CallExpr:
			targets = append(targets, fromCall(cfg, node, depth, stmtHdoc(stmt))...)
		}
		return true
	})
	return targets
}

// stmtHdoc returns the literal here-document attached to a statement, if it has
// one that is knowable.
//
// Used for the programs that write their STDIN to a named file — `tee`, and
// `dd of=`. For those the heredoc is the resulting content, where for `cat` it
// is the output of a redirection and is handled by payloadForStmt instead.
func stmtHdoc(stmt *syntax.Stmt) Payload {
	if stmt == nil {
		return Payload{}
	}
	for _, r := range stmt.Redirs {
		if p, ok := hdocPayload(r); ok {
			return p
		}
	}
	return Payload{}
}

// fromRedirs reads the file-touching redirections off one statement.
//
// This is the half with no vocabulary to maintain. A redirection is SYNTAX: the
// shell grammar says `>` truncates a file and `>>` appends to one, and no
// project renames `>`. Where a binary allowlist can fall behind a tool being
// renamed and go silent, this cannot drift at all — the only thing that could
// change it is the shell grammar itself, and then the parser stops parsing
// rather than quietly reporting nothing.
// It takes the whole statement rather than its redirection list because the
// resulting CONTENT is a property of the pair: the redirection says where the
// bytes go and the command says what produces them. `echo hi > f.md` is one
// statement holding both, and a function given only the redirects could name
// the file but never its contents.
func fromRedirs(cfg *expand.Config, stmt *syntax.Stmt) []FileTarget {
	if stmt == nil {
		return nil
	}
	// Worked out once for the statement, not once per redirection: the command
	// producing the bytes is the same whichever file they are sent to.
	produced := payloadForStmt(cfg, stmt)

	var targets []FileTarget
	for _, r := range stmt.Redirs {
		if !writesAFile(r) {
			continue
		}
		path, ok := literalWord(cfg, r.Word)
		if !ok {
			continue
		}
		// What the redirection does with those bytes differs by operator, and
		// the difference is the file's whole prior contents.
		//
		//	>   truncates, so the result is exactly the produced bytes
		//	>>  appends, so the result is the file's current bytes plus them —
		//	    which only the caller can read, hence PayloadAppend rather than
		//	    PayloadLiteral
		//
		// Every other writing operator — `&>`, `<>`, the noclobber forms —
		// claims nothing. `<>` does not truncate and writes at an offset, and
		// the `&>` family merges stderr, whose contents are not knowable from
		// the line. Claiming the produced bytes for those would be wrong in a
		// way no test would catch until it refused someone's work.
		payload := Payload{}
		switch r.Op {
		case syntax.RdrOut, syntax.RdrClob:
			payload = produced
		case syntax.AppOut, syntax.AppClob:
			if produced.Kind == PayloadLiteral {
				payload = Payload{Kind: PayloadAppend, Text: produced.Text}
			}
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
		targets = append(targets, withPayload(targetsFor([]string{path}, Write), payload)...)
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
//
// The `got[0] == ""` half is EQUIVALENT today and kept anyway, so the survivor
// is read as an equivalence rather than as this guard being untested. Every
// path built here goes through targetsFor, which drops the empty string for its
// own reasons — so `> ""` is declined one step later whether or not this
// condition exists. Verified by mutation: relaxing it alone leaves the suite
// green, and removing BOTH it and targetsFor's drop turns five tests red.
//
// Kept because the two are the same only by the coincidence that every caller
// happens to route through targetsFor. Naming the requirement HERE, where the
// redirection's path is chosen, is the spelling that cannot rot.
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
