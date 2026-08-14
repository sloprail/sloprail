package commandmod

import (
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// The file-touching binaries this package reads, and the argument for naming
// them at all.
//
// # This IS a name allowlist, and this package argues against those
//
// extractPending's doc comment argues at length that dispatching on a NAME is
// the wrong trade, and this file does exactly that. The argument is not
// inherited in either direction, because the two lists are not the same kind of
// object, and the difference is what decides it.
//
// The objection has three parts, and they have to be taken one at a time:
//
//  1. The list tracks a vocabulary this engine does not own and is not told
//     about.
//  2. Drift is SILENT. An allowlist that has fallen behind does not error, it
//     stops producing events, and a guardrail that stops firing looks exactly
//     like a guardrail that is satisfied.
//  3. It is measured, not hypothetical. Claude Code renamed `Task` to `Agent`
//     in v2.1.63 and every module asking by name broke without a word.
//
// Part 2 transfers completely, and nothing below weakens it. Part 3 is real and
// is the reason this file is written the way it is rather than the reason not to
// write it. Part 1 is where the two cases genuinely differ.
//
// A HARNESS TOOL NAME is one vendor's product decision, changed by that vendor
// on that vendor's schedule, for that vendor's reasons, with no notice owed to
// anyone. `Task` became `Agent` because Anthropic decided it should. There is no
// standard, no committee, and no compatibility promise; the name is whatever the
// current release says it is, and there are as many vocabularies as there are
// harnesses.
//
// `rm` is not that. It is specified in POSIX.1 and has named the same operation
// since Unix v1 in 1971. There is no vendor who could rename it, because
// renaming it would break every shell script in existence — the compatibility
// pressure that makes a harness free to rename `Task` is precisely what makes
// `rm` unrenameable. The same holds for every name below: they are `mv`, `cp`,
// `ln`, `dd`, `sed`, `tee`, all POSIX utilities. A world in which `rm` is
// renamed is a world in which the shell parser this package is built on has also
// stopped working, and the failure would be loud.
//
// So the drift risk is not zero and is not the same risk. What this list can
// miss is not a rename but a MEMBER: a file-touching utility nobody thought of,
// or a new one. That is a real gap and it is why the redirection half above is
// preferred and came first — it needs no vocabulary at all. But a missing member
// fails the same way an absent feature does, and it is bounded by what this list
// covers rather than open-ended: the four commands the defect report measured
// are all here, and the tree diff at `session stop` catches whatever is not,
// which is the backstop the tool-name case does not have either.
//
// # What tips it
//
// The alternative to this list is not a shape test. There is no shape to read: a
// command line is a flat vector of strings, and `rm notes.md` and `wc notes.md`
// are the same shape — one program, one path. Where a tool call's arguments
// carry a `file_path` KEY that means something, argv position 1 means nothing at
// all. So filemod's choice was between a name and a shape that was genuinely
// available; here the choice is between a name and nothing.
//
// And nothing is the status quo, which is measured: `rm -rf notes.md` produces
// no file event, so a rule bound to PreFileDelete on notes.md does not fire, and
// the Bash tool is a hole straight through every file rule. Against that, a list
// of POSIX names that might one day be missing a member is the smaller wrong by
// a wide margin — and unlike the tool-name case, the failure it risks is one the
// Post-phase tree diff already reports.
//
// # What would change this
//
// If this list starts growing to chase project-specific tools — a repo's own
// `bin/rewrite-all`, a formatter, a codegen step — it has become the thing the
// objection warns about, and the answer then is not a longer list. It is that
// those tools are unknowable (see FileTargets's own limits) and belong to the
// tree diff.

// binSpec is what has to be known about one binary to find the paths it touches.
//
// A function rather than a table of flag names, because the shapes genuinely
// differ: `rm` writes nothing and removes every operand, `mv` removes its
// sources and writes its destination, `dd` names its file behind `of=`. A table
// expressive enough for all of them would be a small language, and reading one
// short function per binary is easier to check against the utility's own
// synopsis than reading a table entry and inferring what it means.
type binSpec func(argv []string) []FileTarget

// knownBins maps a program's basename to how its arguments name files.
//
// Keyed on the basename, so `/bin/rm` and `rm` are the same entry — the same
// normalisation Invocation.Bin already applies, and for the same reason: a rule
// should not be evadable by spelling the path out.
var knownBins = map[string]binSpec{
	// rm removes every operand. Nothing is written.
	"rm": func(argv []string) []FileTarget { return targetsFor(operands(argv), Remove) },

	// mv removes its sources and writes its destination. Both halves matter: a
	// rule protecting notes.md must fire on `mv notes.md elsewhere.md` (the file
	// stops existing at its path) and on `mv other.md notes.md` (its bytes are
	// replaced).
	//
	// The destination is the LAST operand, and only when there are at least two
	// — `mv a` is a usage error that touches nothing. With three or more the
	// last is a directory and the rest move into it; the sources still stop
	// existing at their own paths, which is what a delete rule is about, and the
	// destination is reported as written because that is what the flag-free
	// synopsis says even though the resulting path is inside it. Naming the
	// directory rather than the paths within it is the honest floor: computing
	// `dir/base(src)` would be this package deciding what the destination
	// filename is, which depends on whether the directory exists.
	"mv": movelike,
	// cp writes its destination and leaves its sources alone.
	//
	// With exactly TWO operands the destination's resulting bytes are the
	// source's current bytes, which is a PayloadCopyOf naming the source. The
	// bytes themselves are not read here — that is a filesystem access and
	// belongs to the caller (see payload.go on the split).
	//
	// With three or more the last operand is a DIRECTORY and the sources are
	// copied into it. The resulting file is `dir/base(src)`, a path this
	// package deliberately does not compute — movelike's comment gives the
	// reason: it depends on whether the directory exists, which is a question
	// about the tree. So no payload is claimed there, matching the path half's
	// existing honesty.
	"cp": func(argv []string) []FileTarget {
		ops := operands(argv)
		if len(ops) < 2 {
			return nil
		}
		dst := targetsFor(ops[len(ops)-1:], Write)
		if len(ops) == 2 {
			return withPayload(dst, Payload{Kind: PayloadCopyOf, From: ops[0]})
		}
		return dst
	},
	// install is cp with modes and ownership. Same operand shape.
	"install": func(argv []string) []FileTarget {
		ops := operands(argv)
		if len(ops) < 2 {
			return nil
		}
		return targetsFor(ops[len(ops)-1:], Write)
	},

	// touch creates or updates the mtime of every operand.
	//
	// A touch of a path that does not exist creates an EMPTY file, and this is
	// the reference case for the whole absent-versus-empty distinction: `touch
	// new.md` is the one command where `content: ""` is the TRUE answer rather
	// than a stand-in for not knowing. Before payloads existed it was
	// indistinguishable from `some-unknown-tool > new.md`, and a rule catching
	// empty files could not be written honestly.
	//
	// A touch of a file that ALREADY exists changes only the mtime, leaving the
	// bytes alone — so the literal "" would be a lie there. The distinction is
	// the tree's, not the line's, so the payload is claimed here and filemod
	// applies it only on the create branch. That is the split doing its job:
	// this package says what the line determines, and the caller decides what
	// the tree makes of it.
	"touch": func(argv []string) []FileTarget {
		targets := withPayload(targetsFor(operands(argv), Write), literalPayload(""))
		for i := range targets {
			targets[i].MTimeOnly = true
		}
		return targets
	},

	// mkdir makes directories. Reported as Write on the path, which the file
	// module then declines to turn into an event because a directory is not a
	// file — presentNotAFile, and no file event can honestly be about it. It is
	// listed so the intent is visible rather than absent, and so a path that is
	// currently a FILE and about to be replaced by a directory is not silent.
	"mkdir": func(argv []string) []FileTarget { return targetsFor(operands(argv), Write) },

	// ln creates a link at its last operand. Lstat sees the link itself, so the
	// file module treats it as a file — see lookAt on why the link and not its
	// target.
	"ln": func(argv []string) []FileTarget {
		ops := operands(argv)
		if len(ops) < 2 {
			return nil
		}
		return targetsFor(ops[len(ops)-1:], Write)
	},

	// truncate resizes its operands. `-s` takes a separated value, which
	// operands cannot know about generically, so it is handled here.
	//
	// `truncate -s 0` empties the file, which is a resulting content of "" and
	// is known exactly. Any OTHER size is not: `-s 100` pads with NUL bytes or
	// cuts at a byte offset, so the result depends on the file's current
	// contents and length, and `-s +10`/`-s -10` are relative. Only the zero
	// case is claimed, and it is claimed only when spelled as a size this
	// recognises — see truncatesToEmpty.
	"truncate": func(argv []string) []FileTarget {
		skip := map[string]bool{"-s": true, "--size": true, "-r": true, "--reference": true}
		targets := targetsFor(operandsSkipping(argv, skip), Write)
		if truncatesToEmpty(argv) {
			return withPayload(targets, literalPayload(""))
		}
		return targets
	},

	// sed rewrites its input file ONLY with -i. Without it sed writes to stdout
	// and the file it names is read, not written — so this is the one entry
	// whose effect depends on a flag being present.
	//
	// GNU sed spells it `-i`, BSD sed requires `-i ''`, and both spell the
	// suffix form `-i.bak`. All three are the same intent and all three are
	// caught: the test is a `-i` PREFIX on a short flag, which matches `-i`,
	// `-i.bak`, and clustered forms like `-ni`.
	"sed": sedlike,
	// perl -i is the same in-place idiom.
	"perl": sedlike,

	// tee writes every operand. It also writes stdout, which is not a file this
	// names.
	//
	// No payload, and this one is worth stating because it looks derivable and
	// is not. tee writes its STDIN to each operand, and stdin comes from
	// whatever is piped in — `generate | tee f.md`. The line names the file
	// perfectly and says nothing whatever about the bytes.
	//
	// The one spelling that WOULD be knowable is a heredoc into tee
	// (`tee f.md <<'EOF'`), which is handled by the statement-level heredoc
	// path in payload.go rather than here, because that path already pairs a
	// here-document with the statement it hangs off. Claiming it here as well
	// would be two answers for one line, free to disagree.
	"tee": func(argv []string) []FileTarget {
		return targetsFor(operands(argv), Write)
	},

	// dd names its output behind `of=`, not positionally.
	"dd": func(argv []string) []FileTarget {
		var paths []string
		for _, a := range argv[1:] {
			if v, ok := strings.CutPrefix(a, "of="); ok && v != "" {
				paths = append(paths, v)
			}
		}
		return targetsFor(paths, Write)
	},
}

// movelike is mv: sources removed, destination written.
// The destination's resulting bytes are the source's current bytes, exactly as
// for cp, and under the same two-operand restriction — with three or more the
// last is a directory and the resulting paths are not computed here.
//
// The source is still reported Remove. A payload on the removal would be
// meaningless: a deleted file has no resulting content, and PreFileDelete
// declares no field for one.
func movelike(argv []string) []FileTarget {
	ops := operands(argv)
	if len(ops) < 2 {
		return nil
	}
	targets := targetsFor(ops[:len(ops)-1], Remove)
	dst := targetsFor(ops[len(ops)-1:], Write)
	if len(ops) == 2 {
		dst = withPayload(dst, Payload{Kind: PayloadCopyOf, From: ops[0]})
	}
	return append(targets, dst...)
}

// sedlike is the in-place-edit family: the operands are rewritten, but only when
// the in-place flag is present.
//
// The FIRST operand is skipped, because it is the script rather than a file. In
// `sed -i ” s/a/b/ f.md` the script is `s/a/b/`, and reporting it as a path
// would fire a delete rule on a file named after a substitution expression.
//
// The empty operands are dropped BEFORE that skip, and getting this backwards
// is a measured bug rather than a hypothetical one. BSD sed requires an empty
// suffix argument — the `”` above — and it does NOT vanish during expansion:
// the vector arrives as [sed -i "" s/a/b/ f.md], so the operands are
// ["", "s/a/b/", "f.md"] and the script sits at index 1. Skipping index 0 there
// consumes the empty string, leaves the script in the list, and reports
// `s/a/b/` as a file about to be written. Dropping the empties first puts the
// script at index 0 for both the BSD and the GNU spelling, so one skip is
// correct for both.
func sedlike(argv []string) []FileTarget {
	inPlace := false
	for _, a := range argv[1:] {
		if a == "--in-place" || strings.HasPrefix(a, "--in-place=") {
			inPlace = true
			break
		}
		// A short flag cluster containing `i`. `-i`, `-i.bak` and `-ni` all
		// count; `--include` does not, which is why the double-dash forms are
		// tested first and long flags are excluded here.
		if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a[1:], "i") {
			inPlace = true
			break
		}
	}
	if !inPlace {
		// Without it the file is read and stdout is written. Nothing to report.
		return nil
	}
	ops := nonEmpty(operands(argv))
	if len(ops) < 2 {
		// The script and nothing else — no file named.
		//
		// EQUIVALENT to `< 1` and kept at `< 2` anyway, so the survivor is read
		// as an equivalence rather than as an unchecked boundary. On a
		// one-element slice `ops[1:]` is empty, so the loop below produces
		// nothing either way and a mutation between the two spellings survives.
		//
		// `< 2` is the spelling that states the requirement: sed in place needs
		// a script AND at least one file, and the number here is that sentence
		// rather than the smallest value that happens to work. It also stays
		// correct if the slice expression below ever changes.
		return nil
	}
	// Past the script.
	return targetsFor(ops[1:], Write)
}

// truncatesToEmpty reports whether a truncate invocation sets its files to zero
// length, which is the one size whose resulting content is known.
//
// Both spellings of the flag are read — `-s0`, `-s 0`, `--size=0`, `--size 0` —
// because a rule must not be evadable by a space. Anything else, including a
// relative size and a `--reference`, is declined: the result then depends on
// the file's current bytes, which this package does not read.
func truncatesToEmpty(argv []string) bool {
	for i := 1; i < len(argv); i++ {
		a := argv[i]
		switch {
		case a == "-s" || a == "--size":
			// The value is the next word.
			if i+1 < len(argv) {
				return isZeroSize(argv[i+1])
			}
			return false
		case strings.HasPrefix(a, "--size="):
			return isZeroSize(strings.TrimPrefix(a, "--size="))
		case strings.HasPrefix(a, "-s"):
			return isZeroSize(strings.TrimPrefix(a, "-s"))
		}
	}
	return false
}

// isZeroSize reports whether a truncate size argument means "empty".
//
// Only an exact, absolute zero. A leading `+` or `-` makes the size relative to
// the current length, so `-s -0` is not necessarily an empty file and is
// refused along with everything else this does not model exactly.
func isZeroSize(s string) bool {
	return s == "0"
}

// nonEmpty drops the empty operands, so a position in the list means the same
// thing whatever spelling produced it. See sedlike for the case that needs it.
func nonEmpty(ops []string) []string {
	kept := make([]string, 0, len(ops))
	for _, o := range ops {
		if o != "" {
			kept = append(kept, o)
		}
	}
	return kept
}

// operands returns the non-flag words of an argument vector, past the program.
//
// A word starting with `-` is dropped as a flag, and `--` ends the flags so
// everything after it is an operand whatever it starts with. That is what lets
// `rm -- -weird-name.md` report the file rather than treat it as options.
//
// A bare `-` is an operand: it means stdin to most utilities and is a legal
// filename to none of them, but dropping it would shift nothing and keeping it
// costs nothing — the file module resolves it against the tree and finds
// nothing there.
//
// Flags that take a SEPARATED value are the known imprecision here, and it is
// bounded by which binaries are in the table. Of them only `truncate` has one
// that matters, and it is handled by operandsSkipping. For the rest, a
// separated value would be reported as a path that does not exist, which the
// file module then declines to make an event of.
func operands(argv []string) []string {
	return operandsSkipping(argv, nil)
}

// operandsSkipping is operands, minus the words consumed by the named flags.
func operandsSkipping(argv []string, takesValue map[string]bool) []string {
	var ops []string
	endOfFlags := false
	for i := 1; i < len(argv); i++ {
		arg := argv[i]
		if endOfFlags {
			ops = append(ops, arg)
			continue
		}
		if arg == "--" {
			endOfFlags = true
			continue
		}
		if strings.HasPrefix(arg, "-") && arg != "-" {
			if takesValue[arg] {
				i++
			}
			continue
		}
		ops = append(ops, arg)
	}
	return ops
}

// targetsFor pairs each path with one effect, dropping the ones that do not
// name a file this package can be sure of.
//
// An empty path reaches here from a word that expanded to nothing, or from one
// held in place by fromCall because it could not be resolved. A file event
// naming the empty string is one no rule can mean.
//
// A path holding a glob character is dropped for a different reason, and it is
// the subtler of the two. safeConfig deliberately disables globbing so the
// filesystem is never read, which means `rm *.md` survives expansion with the
// `*` intact and LOOKS like a literal path — isLiteral agrees, because a `*` is
// a plain Lit as far as the parser is concerned. So the word passes every
// certainty test this package applies and still names no file: the shell will
// expand it against a tree nobody here has looked at, into a set of paths that
// is not knowable without looking.
//
// Reporting it as written would be a path no rule can mean and no file can
// match — harmless today only because nothing is named `*.md`, which is luck
// rather than design. Dropping it is the same answer this package gives for
// every other word it cannot resolve without acting: what can be seen is
// emitted, what cannot is left alone rather than guessed at.
//
// The cost is real and is stated rather than hidden: `rm *.md` produces no
// event, so a rule protecting notes.md does not fire on it. That is the
// unknowable tier, and the tree diff at session stop reports it after the fact.
// withPayload attaches one payload to every target in a list.
//
// A helper rather than a parameter on targetsFor, because the overwhelming
// majority of construction sites know nothing about content and should keep
// saying so by omission. Adding a parameter would make every one of them state
// PayloadNone explicitly, which is noise that invites a wrong value to be
// pasted in.
func withPayload(targets []FileTarget, p Payload) []FileTarget {
	if p.Kind == PayloadNone {
		return targets
	}
	for i := range targets {
		targets[i].Payload = p
	}
	return targets
}

func targetsFor(paths []string, effect Effect) []FileTarget {
	var targets []FileTarget
	for _, p := range paths {
		if p == "" || hasGlob(p) {
			continue
		}
		targets = append(targets, FileTarget{Path: p, Effect: effect})
	}
	return targets
}

// hasGlob reports whether a word would be expanded by the shell against the
// filesystem, and so names a set of paths rather than one path.
//
// The three pattern characters POSIX gives a shell: `*`, `?` and a `[` opening
// a bracket expression. A `[` without its closing bracket is not a pattern and
// the shell leaves it alone, but treating it as one costs only a dropped event
// on a filename nobody writes, while missing a real pattern reports a path that
// does not exist.
func hasGlob(path string) bool {
	return strings.ContainsAny(path, "*?[")
}

// fromCall reads the file-touching intent off one parsed call.
//
// It resolves the vector the same way resolve does — per word, refusing a
// program word that is not literal — and then unwraps, so `sudo rm notes.md`
// and `xargs rm` reach the table rather than stopping at the wrapper. That
// reuse is the point: a rule about a deletion must not be evadable by putting a
// `sudo` in front of it, and the unwrapping that already exists for invocations
// is the same unwrapping this needs.
//
// A path is reported only when the word naming it was literal. `rm $TARGET`
// names no path this can be sure of, and the empty environment would resolve it
// to nothing at all — reporting a guess would fire a rule on a file the command
// may never touch.
// stdin is the here-document attached to this call's statement, for the
// programs that copy stdin into a file they name. It is the zero Payload for
// the vast majority of lines, and is consulted only by those programs — see
// targetsForArgv.
func fromCall(cfg *expand.Config, call *syntax.CallExpr, depth int, stdin Payload) []FileTarget {
	if len(call.Args) == 0 {
		return nil
	}

	fields := expandPerWord(cfg, call.Args)
	if len(fields) == 0 || !fields[0].literal {
		// The same test resolve applies: an argument sliding into the program's
		// place, or a program word resolved out of an assumed environment.
		//
		// The literalness half is EQUIVALENT today and kept anyway, so the
		// survivor is read as an equivalence rather than as this guard being
		// untested. The loop below replaces every non-literal word with the
		// empty string, argv[0] included, and targetsForArgv refuses a vector
		// whose basename is empty — so `r${X}m notes.md` is declined one step
		// later whether or not this line exists. Verified by removing it: the
		// whole suite stays green.
		//
		// Kept because the two conditions are the same only by the coincidence
		// that the substitution below is unconditional. Should a later change
		// give a non-literal word its resolved value — which is exactly what a
		// populated environment would do — this becomes the only thing standing
		// between an assumed program name and the whole known-binary table
		// running against a command nobody typed. Naming the certainty
		// requirement HERE, where the program word is chosen, is the spelling
		// that cannot rot.
		return nil
	}

	// Only the words that are CERTAIN name a path. An uncertain one — lost, or
	// resolved out of the assumed environment — becomes the empty string, which
	// targetsFor drops.
	//
	// It KEEPS ITS POSITION rather than being omitted, and that is the whole
	// difference from resolve, which drops a lost word because an argument
	// vector with a hole in it is not a vector any program receives. Here
	// position is meaning: every entry in the table reads its paths positionally,
	// so a vanished word slides the rest along and changes what they are.
	//
	// Measured, on the case the comment used to claim it handled: `mv a.md $B`
	// with the word dropped becomes [mv a.md], which movelike correctly refuses
	// as a usage error touching nothing — so the removal of a.md, which happens
	// whatever $B turns out to be, went unreported. Held in place it is
	// [mv a.md ""], the source is reported Remove, and the unknown destination
	// is reported as nothing rather than as a guess.
	argv := make([]string, 0, len(fields))
	for _, f := range fields {
		if f.lost || !f.literal {
			argv = append(argv, "")
			continue
		}
		argv = append(argv, f.value)
	}
	return targetsForArgv(argv, depth, stdin)
}

// targetsForArgv reads one resolved vector against the table, unwrapping
// wrappers and interpreter payloads the way fromArgv does.
//
// depth is how many interpreter payloads have been entered to get here, and it
// is threaded for the same reason fromArgv threads it: each payload is a fresh
// parse of text an agent chose, so the descent needs the same bound. Wrapper
// unwrapping does not spend it — that recursion consumes words from a vector of
// finite length and terminates on its own.
// stdin is the statement's here-document, applied only to the programs that
// copy stdin into a file they name. `tee f.md <<'EOF'` and `dd of=f.md <<'EOF'`
// are the two, and for them the heredoc IS the resulting content.
//
// It is applied here rather than inside each binSpec so that a spec stays a
// pure function of argv — the property that makes the table readable against
// each utility's own synopsis.
func targetsForArgv(argv []string, depth int, stdin Payload) []FileTarget {
	if len(argv) == 0 || basename(argv[0]) == "" {
		return nil
	}

	var targets []FileTarget
	if spec, known := knownBins[basename(argv[0])]; known {
		own := spec(argv)
		if stdin.Kind == PayloadLiteral && consumesStdin(basename(argv[0])) {
			own = withPayload(own, stdin)
		}
		targets = append(targets, own...)
	}
	if nested := unwrap(asWords(argv)); len(nested) > 0 {
		// Recursive for the same reason fromArgv is: wrappers stack, and
		// `sudo nohup rm notes.md` is two deep.
		//
		// The heredoc travels with it: `sudo tee f.md <<'EOF'` attaches the
		// document to the outer statement, and the wrapper is transparent to
		// what the inner program does with its stdin.
		targets = append(targets, targetsForArgv(values(nested), depth, stdin)...)
	}
	// An interpreter payload names files too, and this was the gap.
	//
	// `sh -c 'rm notes.md'` reported the rm INVOCATION and no file target at
	// all, so the two halves of this package disagreed about one command line:
	// a rule about running rm fired, and a rule about deleting notes.md did
	// not. A harness that wraps everything in `bash -lc` — the case the depth
	// bound's own comment says this module exists for — made every file rule
	// cover nothing.
	//
	// The same precondition governs it as the invocation side, because it is
	// the same payload word: literal, or nothing is read.
	targets = append(targets, targetsFromPayload(asWords(argv), depth)...)
	return targets
}

// consumesStdin reports whether a program writes its standard input into the
// file its arguments name, so that a here-document on the statement is that
// file's resulting content.
//
// Only the two utilities for which that is the whole behaviour. `cat` is NOT
// here: it writes stdin to stdOUT, so a heredoc reaches a file only through a
// redirection, which payloadForStmt already pairs — listing it here as well
// would give one line two answers.
func consumesStdin(bin string) bool {
	return bin == "tee" || bin == "dd"
}

// targetsFromPayload re-parses a literal interpreter payload and returns the
// file targets inside it.
//
// Nothing when this is not an interpreter, when it names no payload, when the
// payload word was not literal, or when the depth budget is spent — each the
// case where the honest answer is that no file can be named.
func targetsFromPayload(argv []word, depth int) []FileTarget {
	if depth >= maxUnwrapDepth {
		return nil
	}
	payload, ok := interpreterPayload(argv)
	if !ok {
		return nil
	}
	return fileTargetsAt(payload, depth+1)
}

// asWords re-attaches the certainty that this path encodes positionally, so an
// argv can be handed to unwrap.
//
// Lossless in THIS direction only, and only here. The vector reaching
// targetsForArgv has already had every uncertain word replaced by "" by the
// caller above — that substitution IS the loss, and it happened before this
// function is reached. What is left is a vector in which "" means "not
// certain" and anything else means "certain", which is exactly what `literal`
// records. So the flag is recomputable from the value, which is impossible in
// general (see word: `sh -c "np${X}m publish"` resolves to a string
// indistinguishable from the certain one) but decidable here, because the
// empty string is not a word any resolved literal can be.
//
// It is also why unwrap is safe to reach from this path at all: `basename("")`
// is "", which matches no wrapper, so an uncertain word can never be unwrapped
// into a program that was only a guess.
func asWords(argv []string) []word {
	out := make([]word, 0, len(argv))
	for _, v := range argv {
		out = append(out, word{value: v, literal: v != ""})
	}
	return out
}
