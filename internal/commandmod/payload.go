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

	// PayloadCopyOf means the resulting bytes are whatever other paths hold
	// right now, named in Payload.From. `cp a.md b.md`, `mv a.md b.md`,
	// `dd if=a.md of=b.md`, and `cat a.md b.md > c.md`.
	//
	// A reference rather than the bytes, because resolving it means reading the
	// filesystem, which is the caller's half of the split.
	//
	// SEVERAL sources rather than one, because `cat` concatenates: the result is
	// their bytes in order, and a single-source shape could not say that. A copy
	// is the one-element case of the same statement, so both spellings resolve
	// through one branch in filemod rather than two that could disagree about
	// what an unreadable source means.
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

	// From are the paths whose current contents become the result, IN ORDER, for
	// PayloadCopyOf. Spelled as the command line spells them, like
	// FileTarget.Path.
	//
	// One entry for a copy, several for a concatenation. The order is the
	// command's own — `cat b.md a.md > c.md` is not the same file as
	// `cat a.md b.md > c.md`, so a set would be the wrong shape here.
	From []string
}

// literalPayload is a Payload for bytes known outright.
func literalPayload(text string) Payload {
	return Payload{Kind: PayloadLiteral, Text: text}
}

// copyPayload is a Payload for bytes that are another path's current bytes.
//
// Named rather than written out at each site, because every producer of a copy
// reference — cp, mv, install, dd — states the same thing, and a helper keeps
// the single-source case from being spelled four slightly different ways.
func copyPayload(from string) Payload {
	return Payload{Kind: PayloadCopyOf, From: []string{from}}
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
//	printf '%s' a           %s takes its argument verbatim.
//	printf '%d' 5           %d takes an argument that is ALREADY a decimal
//	                        integer, rendered as itself. See printfDecimal for
//	                        why the subset stops there.
//	printf '%s %d\n' a 1    several conversions, consumed left to right.
//	printf '%s\n' a b c     surplus arguments REUSE the format, which is
//	                        printf's specified loop.
//	printf '%s\n' a         the commonest spelling in real scripts, so the two
//	                        escapes that appear in it — \n and \t — are honoured.
//	anything else           declined.
//
// What is still declined, and why each is not an oversight:
//
//	%5s, %.2f, %-3d   width and precision. Renderable in principle, and the
//	                  padding rules (which side, with what, and how a precision
//	                  interacts with a width) are where an approximation would
//	                  be silently wrong by whitespace — invisible in a diff.
//	                  Rare enough in agent-written lines that the boundary is
//	                  not worth the risk.
//	%f, %x, %o, %c    numeric conversions whose output depends on a locale, a
//	                  default precision, or a character encoding.
//	%b, %q            shell-dependent: %b is a bash extension and %q's quoting
//	                  style differs between implementations.
//	\101, \x41        octal and hex escapes. Specified, but whether the format
//	                  string's `\101` is interpreted at all differs between
//	                  printf(1) and the shell builtin.
//
// Declining is not a failure. It yields PayloadNone, the target still produces
// its event, and only the content is left unclaimed — which is the honest
// answer for a format this cannot render exactly.
func printfOutput(args []string) (Payload, bool) {
	if len(args) == 0 {
		return Payload{}, false
	}
	format, rest := args[0], args[1:]

	tmpl, ok := printfEscapes(format)
	if !ok {
		return Payload{}, false
	}

	// Read off the RAW format while printfOnce walks the escaped one, and the
	// two must agree about where the conversions are or vi indexes the wrong
	// verb. They do, because printfEscapes maps only `\n`, `\t` and `\\` —
	// none of which produces or consumes a `%` — and DECLINES every other
	// escape rather than passing it through. So the `%` positions are the same
	// in both strings, and a widening of printfEscapes that ever emitted a
	// percent would have to move this call to `tmpl`.
	verbs, ok := printfVerbs(format)
	if !ok {
		return Payload{}, false
	}

	// A format carrying no conversions at all: the output is the format, once,
	// with escapes expanded. No trailing newline is added — printf, unlike
	// echo, adds nothing it was not told to.
	//
	// Surplus arguments would make printf reuse the format, and a format with
	// no conversions consumes NO arguments — so the loop would never terminate
	// were it not for printf's own rule that it stops when a pass consumes
	// nothing. `printf 'x' a b` therefore prints "x" exactly once. That is
	// specified, but it is a rule about a shape nobody writes deliberately, and
	// it stays declined rather than modelled.
	if len(verbs) == 0 {
		if len(rest) > 0 {
			return Payload{}, false
		}
		// Through printfOnce even so, rather than returning tmpl raw. A format
		// with no CONVERSIONS can still carry a `%%`, which is a literal
		// percent that has to be collapsed — `printf '100%%\n'` writes
		// "100%\n". Returning the template here skipped that collapse and
		// reported the doubled percent, which is a wrong answer rather than an
		// absent one. The pass consumes no arguments, so it renders once and
		// stops.
		text, _, ok := printfOnce(tmpl, verbs, nil)
		if !ok {
			return Payload{}, false
		}
		return literalPayload(text), true
	}

	// With conversions the format is applied repeatedly until the arguments run
	// out, which is printf's specified behaviour and the reason `printf '%s\n'
	// a b c` prints three lines. A single pass is the len(rest) <= len(verbs)
	// case of the same loop.
	//
	// With NO arguments the format is still applied once, with every conversion
	// taking its empty value — `printf '%s\n'` prints one newline. Modelled
	// because it is the same rule, and because declining it would make an
	// argument-free format behave differently from an argument-free `%s`.
	var out strings.Builder
	for {
		text, consumed, ok := printfOnce(tmpl, verbs, rest)
		if !ok {
			return Payload{}, false
		}
		out.WriteString(text)
		if consumed == 0 {
			// The pass consumed NOTHING, so another would render the same
			// bytes forever. printf's own rule is that the format stops
			// repeating once a pass takes no argument, and this is that rule —
			// stated as a property of the pass rather than as a count of the
			// arguments left.
			//
			// EQUIVALENT today — measured. Reaching this line needs a format
			// with at least one verb (the len(verbs)==0 shortcut took the rest)
			// whose pass still takes no argument, and printfVerbs admits only
			// `s` and `d`, both of which consume one. So no vector the current
			// code produces gets here, and removing it leaves the suite green.
			//
			// It is kept because the equivalence is exactly the invariant a
			// widening would break. Every verb this renders happens to consume
			// an argument TODAY; the next one added — a `%%` miscounted as a
			// verb, or a conversion that legitimately takes none — makes this
			// the only thing between a miscount and an infinite loop. Measured,
			// not hypothetical: a mutation to printfVerbs alone produced that
			// vector, and the suite HUNG rather than going red, which is the one
			// failure mode nobody reads. Terminating on the pass rather than on
			// the argument count is what makes the loop correct by construction
			// instead of by coincidence.
			break
		}
		rest = rest[consumed:]
		if len(rest) == 0 {
			break
		}
	}
	return literalPayload(out.String()), true
}

// printfOnce renders one pass of the format, consuming as many arguments as it
// has conversions and reporting how many it took.
//
// An exhausted argument list does not end the pass: printf renders the REST of
// the format with the missing arguments taken as empty (`%s`) or zero (`%d`),
// which is why `printf '%s=%s\n' a` prints "a=\n" rather than stopping at the
// `=`. Modelling that is the difference between a right answer and a truncated
// one on a line whose argument count is off by one — a real thing to write.
func printfOnce(tmpl string, verbs []byte, args []string) (string, int, bool) {
	var b strings.Builder
	used := 0
	next := func() string {
		if used < len(args) {
			v := args[used]
			used++
			return v
		}
		used++
		return ""
	}

	vi := 0
	for i := 0; i < len(tmpl); i++ {
		if tmpl[i] != '%' {
			b.WriteByte(tmpl[i])
			continue
		}
		i++
		if tmpl[i] == '%' {
			// A literal percent, which printfVerbs has already agreed is one.
			b.WriteByte('%')
			continue
		}
		switch verbs[vi] {
		case 's':
			b.WriteString(next())
		case 'd':
			text, ok := printfDecimal(next())
			if !ok {
				return "", 0, false
			}
			b.WriteString(text)
		}
		vi++
	}
	if used > len(args) {
		// The pass ran past the end of the arguments, so it consumed all of
		// them and no more.
		used = len(args)
	}
	return b.String(), used, true
}

// printfVerbs reads the conversion letters out of a format, declining any the
// renderer does not model exactly.
//
// A `%%` is a literal percent and is not a conversion — it consumes no
// argument, which is why it is recognised here rather than being left to look
// like an unknown verb.
//
// Anything between the `%` and the letter — a width, a precision, a flag — is
// refused rather than skipped. That is the strict-allowlist direction the sed
// analysis argues for, applied here: a renderer that IGNORED a width would
// silently produce unpadded bytes for `%5s`, which is a confidently wrong
// answer rather than an absent one.
func printfVerbs(format string) ([]byte, bool) {
	var verbs []byte
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			continue
		}
		i++
		if i >= len(format) {
			// A trailing lone `%`. Its behaviour is unspecified.
			return nil, false
		}
		switch format[i] {
		case '%':
			// A literal percent. No verb, no argument.
			//
			// EQUIVALENT today — measured, not assumed. Counting `%%` as a verb
			// instead leaves the suite green, because printfOnce recognises a
			// literal percent STRUCTURALLY (it tests tmpl[i] itself and
			// `continue`s) before ever indexing verbs, and advances vi only on a
			// real conversion. So a spurious entry is never read.
			//
			// Kept because the two are the same only while those two functions
			// agree about what a `%%` is, in two separate places, by
			// coincidence. This is the one that states it: a literal percent
			// consumes no argument, which is what decides how many arguments a
			// pass takes and therefore where the format-reuse loop stops. The
			// same mutation used to HANG rather than fail, for exactly that
			// reason — see TestPayload_PrintfAlwaysTerminates, which is now the
			// thing standing between a miscount here and an infinite loop.
		case 's', 'd':
			verbs = append(verbs, format[i])
		default:
			return nil, false
		}
	}
	return verbs, true
}

// printfDecimal renders a %d argument, and declines anything that is not
// already exactly a decimal integer.
//
// The narrowness is the point. printf accepts far more than this — leading and
// trailing whitespace, a `0x` prefix, a leading `'` taking a character's
// numeric value, and an out-of-range value that is a diagnostic on some
// implementations and a saturated one on others. Rendering those means deciding
// what each implementation does, and being wrong produces bytes the file never
// holds.
//
// What is accepted is the case where the answer cannot be in doubt: an optional
// sign followed by digits, with no leading zeros to normalise away, printed as
// itself. `printf '%d' 007` is declined rather than rendered as "7", because
// the normalisation is exactly the kind of judgement this is refusing to make.
func printfDecimal(arg string) (string, bool) {
	digits := arg
	if digits == "" {
		// A missing argument is zero, which is specified and unambiguous.
		return "0", true
	}
	if digits[0] == '+' || digits[0] == '-' {
		digits = digits[1:]
	}
	if digits == "" {
		return "", false
	}
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return "", false
		}
	}
	if len(digits) > 1 && digits[0] == '0' {
		// A leading zero would be normalised away, and normalising is a
		// judgement rather than a reading.
		return "", false
	}
	if arg[0] == '+' {
		// printf drops a leading plus. Declined rather than rendered, because
		// dropping it is the same normalisation refused above.
		return "", false
	}
	return arg, true
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
	// `cat a.md b.md > c.md` — the output is the named files' bytes in order,
	// which is a reference rather than a literal, so it is not producedOutput's
	// to answer. producedOutput renders bytes from the ARGUMENTS alone and never
	// refers to the tree; this refers to it, and filemod resolves it.
	if p, ok := catConcatenation(argv); ok {
		return p
	}
	p, ok := producedOutput(argv)
	if !ok {
		return Payload{}
	}
	return p
}

// catConcatenation reads `cat a.md b.md` as a reference to its operands' bytes,
// in order.
//
// The result of a redirection from it is exactly those files concatenated,
// which is derivable — the one thing needed is READING them, and that is
// filemod's half of the split. So this returns a PayloadCopyOf naming several
// sources, the shape Payload.From is a slice for.
//
// # What is refused, and why the flag test is an allowlist
//
// cat's flags TRANSFORM its output. `-n` numbers the lines, `-b` numbers the
// non-blank ones, `-s` squeezes repeated blanks, and `-v`/`-e`/`-t`/`-A` render
// non-printing characters visibly. Every one of them means the output is not
// the input's bytes, and a version that skipped flags generically would report
// `cat -n a.md > b.md` as an exact copy of a.md — bytes the file never holds.
//
// So NO flag is permitted at all, rather than a list of the harmful ones being
// excluded: a flag this has not heard of is refused with the rest, which is the
// same strict-allowlist direction dd's operand test takes and for the same
// reason.
//
// A bare `-` operand means STDIN, whose bytes the line does not carry, so a cat
// naming one claims nothing even though every other operand is a real file.
func catConcatenation(argv []string) (Payload, bool) {
	if len(argv) == 0 || basename(argv[0]) != "cat" {
		return Payload{}, false
	}
	var sources []string
	for _, a := range argv[1:] {
		if a == "" || a == "-" || strings.HasPrefix(a, "-") {
			// A flag, a `-` meaning stdin, or a word the caller could not
			// resolve. None of them is a file whose bytes are on the line.
			return Payload{}, false
		}
		if hasGlob(a) {
			// `cat *.md > all.md` names a set of files the shell expands
			// against a tree this package does not read — the same refusal
			// targetsFor applies to a path, applied to a source.
			return Payload{}, false
		}
		sources = append(sources, a)
	}
	if len(sources) == 0 {
		// Bare `cat`, which reads stdin. isPassThrough's case, not this one.
		return Payload{}, false
	}
	return Payload{Kind: PayloadCopyOf, From: sources}, true
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
