package commandmod

import (
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// This file says what a command line's WRITE will put in a file, when the line
// says it at all.
//
// # Why it stops short of the answer
//
// `cp a.md b.md` determines b's resulting bytes completely — they are a's
// current bytes — but reading a.md is a filesystem access, and this package
// does not touch the filesystem. safeConfig disables globbing and command
// substitution for exactly that reason, and FileTargets's own doc says it
// "returns paths, never events" because "what kind of event a path deserves
// depends on what is at that path in the tree, and this package does not read
// the tree".
//
// That purity is worth keeping: it is what lets every case here be tested by
// handing FileTargets a string and reading the answer back, with no temp
// directory in the way.
//
// So the split follows the one that already exists for paths:
//
//	commandmod   says what the LINE says. A literal payload where the line
//	             carries one, a reference to another path where the line names
//	             one, or nothing.
//	filemod      resolves it. It has a cwd, it already stats and reads files,
//	             and it is where `content` is decided today.
//
// Payload is that middle term — the honest statement of what the line
// determines, with the filesystem left to the caller.

// PayloadKind says how a target's resulting content is determined.
type PayloadKind int

const (
	// PayloadNone means the line does not determine the resulting bytes.
	//
	// The zero value, deliberately: every FileTarget built anywhere in this
	// package is honest by default, and a construction site that has not
	// thought about content cannot accidentally claim to know it.
	PayloadNone PayloadKind = iota

	// PayloadLiteral means the resulting bytes are known outright and are in
	// Payload.Text. `echo hi > f.md`, a quoted heredoc, `touch new.md`.
	PayloadLiteral

	// PayloadCopyOf means the resulting bytes are whatever another path holds
	// right now, named in Payload.From. `cp a.md b.md`, `mv a.md b.md`.
	//
	// A reference rather than the bytes, because resolving it means reading the
	// filesystem, which is the caller's half of the split.
	PayloadCopyOf

	// PayloadAppend means the resulting bytes are the target's CURRENT contents
	// followed by Payload.Text. `echo x >> f.md`.
	//
	// Distinct from PayloadLiteral because the base is the file itself, which
	// only the caller can read — and distinct from PayloadCopyOf because the
	// literal tail has to be carried alongside the reference.
	PayloadAppend
)

// Payload is what a command line determines about one target's resulting bytes.
type Payload struct {
	Kind PayloadKind

	// Text is the literal bytes, for PayloadLiteral and PayloadAppend. Exact,
	// including any trailing newline — see echoOutput, where the newline is the
	// difference between a correct fingerprint and a wrong one.
	Text string

	// From is the path whose current contents become the result, for
	// PayloadCopyOf. Spelled as the command line spells it, like FileTarget.Path.
	From string
}

// literalPayload is a Payload for bytes known outright.
func literalPayload(text string) Payload {
	return Payload{Kind: PayloadLiteral, Text: text}
}

// producedOutput reports what a command's own stdout would be, when the command
// is one whose output is fully determined by its arguments.
//
// This is the writer half of a redirection: `echo hi > f.md` puts echo's stdout
// into f.md, so the resulting bytes are echo's output. The reader half — which
// file receives it — is fromRedirs's, and the two are paired in payloadFor.
//
// Only literal-output programs are here, and the bar is that the ARGUMENTS
// fully determine the bytes. A program whose output depends on the filesystem,
// the clock, the environment or its own logic is not knowable from the line,
// and guessing would be worse than silence.
//
// # Why this is a name table, and why that is defensible here
//
// knownBins already argues it at length and the argument carries over
// unchanged: `echo` and `printf` are POSIX utilities specified since Unix v1,
// and unlike a harness tool name there is no vendor who could rename them
// without breaking every shell script in existence. What this table can miss is
// a MEMBER, not a rename, and a missing member degrades to PayloadNone — no
// content claimed — which is the safe direction.
func producedOutput(argv []string) (Payload, bool) {
	if len(argv) == 0 {
		return Payload{}, false
	}
	switch basename(argv[0]) {
	case "echo":
		return literalPayload(echoOutput(argv[1:])), true
	case "printf":
		return printfOutput(argv[1:])
	case "true", ":":
		// Both produce no output at all, so a redirection from them truncates
		// the target to empty. `: > f.md` is the idiomatic truncate, and this
		// is the case where "" is the TRUE answer rather than a stand-in for
		// not knowing — which is the distinction the whole change is about.
		return literalPayload(""), true
	}
	return Payload{}, false
}

// echoOutput renders what `echo` writes to stdout.
//
// The arguments are joined with single spaces and a newline is appended, which
// is POSIX echo's whole behaviour. Getting the newline right matters more than
// it looks: a rule fingerprinting the resulting file, or asking whether it ends
// cleanly, gets a different answer for "hi" than for "hi\n", and the second is
// what actually lands.
//
// `-n` suppresses the trailing newline. It is recognised only in the FIRST
// position, which is where a shell recognises it — `echo hi -n` prints "hi -n"
// followed by a newline, and treating the flag as positional-anywhere would
// silently drop a literal argument.
//
// `-e` is deliberately NOT interpreted, and this is the one place the answer is
// narrower than it could be. It enables backslash escapes, but WHICH escapes
// and whether they are on by default differ between bash's builtin, sh's, and
// /bin/echo — so the resulting bytes depend on which shell runs the line, which
// this package does not know. A line carrying `-e` therefore declines rather
// than guessing: see the `-e` branch below.
func echoOutput(args []string) string {
	newline := true
	for len(args) > 0 && args[0] == "-n" {
		newline = false
		args = args[1:]
	}
	out := strings.Join(args, " ")
	if newline {
		out += "\n"
	}
	return out
}

// echoIsKnowable reports whether an echo's output can be rendered exactly.
//
// `-e` and `-E` are refused: escape handling differs between shells, so the
// bytes depend on the interpreter rather than on the line. Declining is the
// same answer this package gives for every other word it cannot resolve
// without assuming something.
func echoIsKnowable(args []string) bool {
	for _, a := range args {
		if a == "-e" || a == "-E" {
			return false
		}
		if a != "-n" {
			// Past the leading flags; the rest are operands whatever they are.
			return true
		}
	}
	return true
}

// printfOutput renders what `printf` writes, for the formats where that is
// exact.
//
// printf is a small language and rendering it in general would mean
// reimplementing it — including numeric conversion, width and precision, and
// escape sequences whose behaviour varies. So this handles the subset that is
// unambiguous and declines the rest:
//
//	printf 'literal'        a format with no % and no backslash. The output is
//	                        the format itself, with NO trailing newline — the
//	                        difference from echo, and a real one.
//	printf '%s' a           one %s per argument, output is the arguments
//	                        concatenated in order.
//	printf '%s\n' a         the commonest spelling in real scripts, so the two
//	                        escapes that appear in it — \n and \t — are honoured.
//	anything else           declined.
//
// Declining is not a failure. It yields PayloadNone, the target still produces
// its event, and only the content is left unclaimed — which is the honest
// answer for a format this cannot render exactly.
func printfOutput(args []string) (Payload, bool) {
	if len(args) == 0 {
		return Payload{}, false
	}
	format, rest := args[0], args[1:]

	// A format carrying no conversions at all: the output is the format, once,
	// with escapes expanded. No trailing newline is added — printf, unlike
	// echo, adds nothing it was not told to.
	if !strings.Contains(format, "%") {
		text, ok := printfEscapes(format)
		if !ok || len(rest) > 0 {
			// Surplus arguments make printf REUSE the format, which is a loop
			// this does not model. Declined rather than rendered once.
			return Payload{}, false
		}
		return literalPayload(text), true
	}

	// The only conversion handled is %s, and only when the format is exactly
	// one %s plus literal text. Anything richer — %d, widths, several
	// conversions — is a rendering this would have to guess at.
	if strings.Count(format, "%") != 1 || !strings.Contains(format, "%s") {
		return Payload{}, false
	}
	if len(rest) != 1 {
		// No argument, or several. Several means the format repeats, which is
		// the loop declined above.
		return Payload{}, false
	}
	tmpl, ok := printfEscapes(format)
	if !ok {
		return Payload{}, false
	}
	return literalPayload(strings.Replace(tmpl, "%s", rest[0], 1)), true
}

// printfEscapes expands the backslash escapes printf's FORMAT is specified to
// interpret, declining any it does not model exactly.
//
// Only the two that appear in real scripts are handled — `\n` and `\t` — plus
// `\\`. Everything else, including the octal and hex forms, is declined: those
// are where an approximate answer would be silently wrong, and an unclaimed
// content costs only a rule that does not fire on content.
func printfEscapes(s string) (string, bool) {
	if !strings.Contains(s, `\`) {
		return s, true
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' {
			b.WriteByte(s[i])
			continue
		}
		if i+1 >= len(s) {
			// A trailing lone backslash is not a form this models.
			return "", false
		}
		i++
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case '\\':
			b.WriteByte('\\')
		default:
			return "", false
		}
	}
	return b.String(), true
}

// hdocPayload reads a here-document as literal bytes, when it is literal.
//
// # The quoted/unquoted distinction, which is the whole of it
//
//	cat > f.md <<'EOF'    the delimiter is QUOTED, so the body is taken
//	                      verbatim. `$X` stays `$X`. Fully known.
//	cat > f.md <<EOF      the delimiter is BARE, so the body is expanded —
//	                      parameters, command substitution, arithmetic. What it
//	                      resolves to depends on an environment this package
//	                      does not have, and safeConfig deliberately refuses to
//	                      assume the empty one is real.
//
// That is the same literalness rule isLiteral already applies to a word, at the
// level of a here-document, and it is read off the DELIMITER rather than by
// scanning the body for `$`. Reading the delimiter is what the shell itself
// does, and it is right even for a quoted heredoc whose body happens to contain
// a dollar sign — which a body scan would wrongly refuse.
//
// `<<-` (DashHdoc) strips leading TABS from each line, which changes the bytes.
// It is modelled rather than declined, because the stripping is mechanical and
// exactly specified: tabs only, leading only.
func hdocPayload(r *syntax.Redirect) (Payload, bool) {
	if r == nil || r.Hdoc == nil {
		return Payload{}, false
	}
	if r.Op != syntax.Hdoc && r.Op != syntax.DashHdoc {
		return Payload{}, false
	}
	// The delimiter word carries the quoting. A quoted delimiter — `<<'EOF'` or
	// `<<"EOF"` — means a verbatim body; a bare one means an expanded body this
	// cannot know.
	if !hdocIsQuoted(r.Word) {
		return Payload{}, false
	}
	// With a quoted delimiter the body is a single literal part, which is how
	// the parser represents verbatim text.
	text, ok := literalPartsText(r.Hdoc)
	if !ok {
		return Payload{}, false
	}
	if r.Op == syntax.DashHdoc {
		text = stripLeadingTabs(text)
	}
	return literalPayload(text), true
}

// hdocIsQuoted reports whether a here-document's delimiter was quoted, which is
// what decides whether the body is verbatim.
func hdocIsQuoted(w *syntax.Word) bool {
	if w == nil {
		return false
	}
	for _, part := range w.Parts {
		switch part.(type) {
		case *syntax.SglQuoted, *syntax.DblQuoted:
			return true
		}
	}
	return false
}

// literalPartsText concatenates a word's parts when every one of them is a
// plain literal, and declines otherwise.
//
// The decline is what keeps an unquoted heredoc's `$X` from being read as the
// empty string: an expansion is not a Lit, so the whole body is refused rather
// than silently resolved against an environment nobody has.
func literalPartsText(w *syntax.Word) (string, bool) {
	var b strings.Builder
	for _, part := range w.Parts {
		lit, ok := part.(*syntax.Lit)
		if !ok {
			return "", false
		}
		b.WriteString(lit.Value)
	}
	return b.String(), true
}

// stripLeadingTabs implements `<<-`: leading TABS are removed from every line,
// spaces are not. The distinction is the specification's, and a version that
// stripped whitespace generally would report bytes the shell does not produce.
func stripLeadingTabs(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimLeft(l, "\t")
	}
	return strings.Join(lines, "\n")
}

// payloadForStmt works out what one statement's redirection would put in the
// file it names.
//
// The statement is the right unit because the two halves live on it: the
// redirection says WHERE the bytes go, and the call says WHAT produces them.
// `echo hi > f.md` is one Stmt holding both, which is why this takes a Stmt
// rather than a Redirect.
//
// A here-document attached to the same statement wins over the command's own
// output, because that is what actually happens: `cat > f.md <<'EOF'` sends the
// heredoc through cat, and cat's stdout is its stdin. Modelling cat as a
// pass-through this way covers the overwhelmingly common spelling without a
// general theory of what every program does with its input.
func payloadForStmt(cfg *expand.Config, stmt *syntax.Stmt) Payload {
	if stmt == nil {
		return Payload{}
	}

	// A here-document on this statement, fed through a pass-through program.
	for _, r := range stmt.Redirs {
		p, ok := hdocPayload(r)
		if !ok {
			continue
		}
		if !isPassThrough(cfg, stmt) {
			// The heredoc is this program's INPUT, and what it does with it is
			// the program's business — `sort <<'EOF'` reorders it, `wc` counts
			// it. Only a program that copies stdin to stdout lets the input
			// stand as the output.
			return Payload{}
		}
		return p
	}

	call, ok := stmt.Cmd.(*syntax.CallExpr)
	if !ok {
		return Payload{}
	}
	argv, ok := literalArgv(cfg, call)
	if !ok {
		return Payload{}
	}
	if basename(firstOr(argv)) == "echo" && !echoIsKnowable(argv[1:]) {
		return Payload{}
	}
	p, ok := producedOutput(argv)
	if !ok {
		return Payload{}
	}
	return p
}

// isPassThrough reports whether a statement's program copies its stdin to its
// stdout unchanged, so that a here-document fed to it IS the resulting bytes.
//
// `cat` with no operands is the case that matters and the one real scripts use.
// `cat` WITH operands concatenates those files instead of stdin, so the
// heredoc is not the whole output and the answer is no.
//
// A statement with no command at all — `> f.md <<'EOF'` — is also a
// pass-through in effect: the redirection alone truncates the file and the
// heredoc supplies nothing to any program. That spelling is vanishingly rare
// and is deliberately NOT claimed, because what a bare redirection does with an
// attached heredoc is shell-dependent.
func isPassThrough(cfg *expand.Config, stmt *syntax.Stmt) bool {
	call, ok := stmt.Cmd.(*syntax.CallExpr)
	if !ok {
		return false
	}
	argv, ok := literalArgv(cfg, call)
	if !ok || len(argv) == 0 {
		return false
	}
	if basename(argv[0]) != "cat" {
		return false
	}
	// Operands mean it is concatenating files, not passing stdin through.
	return len(operands(argv)) == 0
}

// literalArgv resolves a call's words to a certain argument vector, declining
// unless every word is literal.
//
// Stricter than fromCall, which keeps a position for an uncertain word so the
// remaining operands do not slide. Here the whole POINT is the exact bytes, and
// one unresolved word means the output is not knowable at all — `echo $GREETING
// > f.md` determines nothing about the resulting content, even though it
// determines the path perfectly well. So the path half and the content half
// apply different tests to the same line, each as strict as its own answer
// needs.
func literalArgv(cfg *expand.Config, call *syntax.CallExpr) ([]string, bool) {
	if call == nil || len(call.Args) == 0 {
		return nil, false
	}
	fields := expandPerWord(cfg, call.Args)
	argv := make([]string, 0, len(fields))
	for _, f := range fields {
		if f.lost || !f.literal {
			return nil, false
		}
		argv = append(argv, f.value)
	}
	if len(argv) == 0 {
		return nil, false
	}
	return argv, true
}

// firstOr returns argv[0], or "" for an empty vector.
func firstOr(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	return argv[0]
}
