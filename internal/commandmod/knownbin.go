package commandmod

import (
	"strings"

	"github.com/sloprail/sloprail/internal/grounding"

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
	// rm removes every operand. Nothing is written. With -r/-R/--recursive an
	// operand that is a directory goes with everything inside it — see
	// FileTarget.Recursive.
	"rm": func(argv []string) []FileTarget {
		return markRecursive(targetsFor(operands(argv), Remove), rmIsRecursive(argv))
	},

	// mv removes its sources and writes its destination. Both halves matter: a
	// rule protecting notes.md must fire on `mv notes.md elsewhere.md` (the file
	// stops existing at its path) and on `mv other.md notes.md` (its bytes are
	// replaced).
	//
	// The destination is the LAST operand, and only when there are at least two
	// — `mv a` is a usage error that touches nothing. The sources still stop
	// existing at their own paths, which is what a delete rule is about.
	//
	// Where the destination is a DIRECTORY the resulting paths are
	// `dir/base(src)`, and those are now reported — see copyTargets and
	// FileTarget.Into. This package still does not decide WHETHER it is a
	// directory, because that is a question about the tree; it states both
	// readings and filemod picks.
	"mv": movelike,
	// cp writes its destination and leaves its sources alone.
	//
	// With exactly TWO operands the destination's resulting bytes are the
	// source's current bytes, which is a PayloadCopyOf naming the source. The
	// bytes themselves are not read here — that is a filesystem access and
	// belongs to the caller (see payload.go on the split).
	//
	// With a DIRECTORY destination the sources land at `dir/base(src)`. Both
	// readings are stated rather than one being chosen — see copyTargets.
	"cp": func(argv []string) []FileTarget {
		// `-t`/`-T` invert the operand reading — see reshapesCopyOperands, which
		// is the same refusal install has always made, applied to the sibling
		// that shares its operand shape.
		if reshapesCopyOperands(argv) {
			return nil
		}
		// `-S SUFFIX` takes a separated value, exactly as it does for install
		// one entry below. Unskipped, the suffix slides into operand position
		// and becomes a phantom source: `cp -S .bak a.md b.md` reported `.bak`
		// among the files copied into b.md.
		ops := operandsSkipping(argv, map[string]bool{"-S": true, "--suffix": true})
		if len(ops) < 2 {
			return nil
		}
		return copyTargets(ops)
	},
	// install is cp with modes and ownership. Same operand shape, so the same
	// two-operand copy analysis applies unchanged: `install a.md b.md` leaves
	// b.md holding a.md's current bytes, and the mode it sets is not something
	// a file event carries.
	//
	// Two flags change the SHAPE rather than decorating it, and both are
	// refused outright:
	//
	//	-d   `install -d dir` MAKES directories rather than copying, so every
	//	     operand is a directory and none is a source. No file event can
	//	     honestly be about the result.
	//	-t   `install -t DIR a.md b.md` names the destination as a FLAG VALUE,
	//	     which inverts the reading entirely: every operand is then a source
	//	     and the destination is not among them. Skipping the value and
	//	     reading the rest positionally is a MEASURED wrong answer — b.md was
	//	     reported as the destination carrying a.md's bytes, when the real
	//	     results are DIR/a.md and DIR/b.md and b.md is only read. It could be
	//	     modelled (it is the directory case with the directory named
	//	     elsewhere) but `-t` is vanishingly rare in agent-written lines, and
	//	     an unclaimed line costs a rule that does not fire where a wrong one
	//	     costs a rule that fires on the wrong file.
	//
	// Its flags that take a separated value — `-m 644`, `-o user`, `-g group` —
	// are skipped, and getting that wrong would be worse here than for most
	// binaries: an unskipped `644` slides into operand position and becomes
	// either a phantom source or, with two real operands beside it, the
	// DESTINATION. That would attach one file's bytes to a path named after a
	// permission bit.
	"install": func(argv []string) []FileTarget {
		// The two flags that change the operand SHAPE rather than decorate it,
		// so neither can be handled by skipping a value.
		// `-t` and `-T` are shared with cp and mv, so the check is too — see
		// reshapesCopyOperands, which is also what makes it cluster-aware. The
		// whole-string comparison this replaces was defeated by `install -Dt DIR a`,
		// which reached copyTargets with source and destination inverted.
		if reshapesCopyOperands(argv) {
			return nil
		}
		for _, a := range argv[1:] {
			if a == "--" {
				break
			}
			if a == "--directory" {
				return nil
			}
			// `-d` clusters too: `install -dv a b` walked past a bare `-d`
			// comparison and reported b as a copy of a, when the line creates
			// directories and copies nothing.
			if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && a != "-" &&
				strings.ContainsRune(a[1:], 'd') {
				return nil
			}
		}
		skip := map[string]bool{
			"-m": true, "--mode": true,
			"-o": true, "--owner": true,
			"-g": true, "--group": true,
			"-S": true, "--suffix": true,
		}
		ops := operandsSkipping(argv, skip)
		if len(ops) < 2 {
			return nil
		}
		return copyTargets(ops)
	},

	// cat is deliberately NOT here, and the omission is the design. It writes no
	// file: `cat a.md b.md > c.md` names c.md through the REDIRECTION, which
	// fromRedirs already finds, and a.md and b.md are read rather than written.
	// An entry here would report them as targets and fire a delete-or-write rule
	// on files the command only reads.
	//
	// What cat determines is the resulting CONTENT of that redirection — its
	// operands' bytes in order — and that is stated in payloadForStmt, where the
	// redirection and the command are already paired.

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
	// Its flags that take a SEPARATED value are skipped, for the reason install
	// gives about its own `-m 644`: an unskipped value slides into operand
	// position and becomes a phantom file. That was not merely an over-report
	// here — `touch -r ref.md a.md` produced a PreFileCreate for ref.md, a file
	// the line only READS as an mtime reference, carrying the literal ""
	// payload below. A rule guarding creations fired on a file nothing writes.
	//
	//	-r/--reference FILE   copy this file's times
	//	-d/--date STRING      use this time
	//	-t STAMP              use this timestamp
	//
	// `-t` is a value-taking flag here and NOT the destination-directory flag it
	// is for cp — the same two letters meaning different things one entry apart,
	// which is why each binary states its own.
	"touch": func(argv []string) []FileTarget {
		skip := map[string]bool{
			"-r": true, "--reference": true,
			"-d": true, "--date": true,
			"-t": true,
		}
		targets := withPayload(targetsFor(operandsSkipping(argv, skip), Write), literalPayload(""))
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
	//
	// `-m/--mode` takes a separated value, skipped for the same reason install
	// skips its own: `mkdir -m 755 d` reported `755` as a path being written.
	// Harmless today only because filemod declines a non-file, which is luck
	// rather than design — the mode string could name an existing file.
	"mkdir": func(argv []string) []FileTarget {
		return targetsFor(operandsSkipping(argv, map[string]bool{"-m": true, "--mode": true}), Write)
	},

	// ln creates a link at its last operand. Lstat sees the link itself, so the
	// file module treats it as a file — see lookAt on why the link and not its
	// target.
	//
	// # Why no payload, for either kind of link
	//
	// A SYMLINK's bytes are its target PATH, not the target's contents — that is
	// what a symlink is. So `ln -s a.md b.md` does not leave b.md holding a.md's
	// bytes, and a PayloadCopyOf naming a.md would be the confidently-wrong
	// class of answer: a rule reading `content` would judge text that is not
	// there. The bytes that ARE there are the string "a.md", and claiming that
	// as a file's content would be worse still — it would report a rule a
	// four-character markdown file that no author means.
	//
	// A HARD link is a second name for the same inode, so its contents genuinely
	// are the target's. It is unclaimed anyway, because it is unclaimed for the
	// path half too: lookAt cannot tell a hard link from an ordinary file, and
	// `ln a.md b.md` creating b.md is reported as a create whose bytes filemod
	// would have to read from a.md — derivable, but for a spelling that
	// essentially does not appear in agent-written command lines. A shape nobody
	// writes is maintenance with no reader.
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
	//
	// With an `if=` and nothing that makes the copy PARTIAL, dd is a copy and
	// the destination's bytes are the source's — the same statement cp makes,
	// through a different spelling. See ddIsAWholeCopy for what "partial" covers
	// and why the test is an allowlist.
	"dd": func(argv []string) []FileTarget {
		var paths []string
		var in string
		for _, a := range argv[1:] {
			if v, ok := strings.CutPrefix(a, "of="); ok && v != "" {
				paths = append(paths, v)
			}
			if v, ok := strings.CutPrefix(a, "if="); ok && v != "" {
				in = v
			}
		}
		targets := targetsFor(paths, Write)
		if in != "" && ddIsAWholeCopy(argv) {
			return withPayload(targets, copyPayload(in))
		}
		return targets
	},

	// sr-file is sloprail's OWN file tool, so its vocabulary is this engine's to
	// know rather than a vendor's to rename. Statically it claims only the
	// target and the effect, never the content: the result depends on the file
	// (edit) or on words this parser blanks when it cannot resolve them. A line
	// made of nothing but sr-file is resolved exactly by running it in resolve
	// mode (services/sr-session); this entry is the fallback for every other
	// line, so an sr-file write mixed into one still produces its file event.
	"sr-file": func(argv []string) []FileTarget { return srFileTargets(argv[1:]) },
	"sr": func(argv []string) []FileTarget {
		if len(argv) > 1 && argv[1] == "file" {
			return srFileTargets(argv[2:])
		}
		return nil
	},
}

func srFileTargets(args []string) []FileTarget {
	fc, ok := grounding.TargetOf(args)
	if !ok {
		return nil
	}
	effect := Write
	if fc.Verb == grounding.VerbDelete {
		effect = Remove
	}
	targets := targetsFor([]string{fc.Path}, effect)
	for i := range targets {
		targets[i].Grounded = &fc
	}
	return targets
}

// ddIsAWholeCopy reports whether a dd invocation copies its input file entire,
// so the output's bytes are the input's bytes.
//
// An ALLOWLIST of operands, not a denylist of the dangerous ones, and that
// choice is the whole safety of this entry. dd has a long and
// implementation-varying operand vocabulary — `conv=ucase` upper-cases,
// `conv=swab` swaps byte pairs, `cbs=`/`conv=block` pads records, `iflag=` and
// `oflag=` change the I/O semantics, and GNU, BSD and busybox do not agree on
// the full set. A denylist naming `bs`, `count`, `skip` and `seek` would let
// every one of those through as a whole copy, reporting bytes the file never
// holds.
//
// So only the operands whose presence provably does not change the RESULTING
// BYTES are permitted:
//
//	if=, of=       the files themselves
//	bs=, ibs=, obs=  the block size. It changes how the bytes are read and
//	                 written, not which ones — WITHOUT a count= to multiply, a
//	                 block size copies the whole input either way.
//	status=        controls dd's own progress output on stderr, not the data.
//
// Everything else, known or unknown, means no claim. `count=` bounds the copy,
// `skip=`/`seek=` offset it, and an operand this has never heard of is exactly
// the case where guessing is worst.
func ddIsAWholeCopy(argv []string) bool {
	permitted := map[string]bool{
		"if": true, "of": true,
		"bs": true, "ibs": true, "obs": true,
		"status": true,
	}
	for _, a := range argv[1:] {
		name, _, ok := strings.Cut(a, "=")
		if !ok || !permitted[name] {
			return false
		}
	}
	return true
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
	// The same inversion cp and install refuse, and the worst of the three
	// here: with `-t` unhandled the flag's value sat in the leading operands
	// and mv reported the DESTINATION DIRECTORY as a Remove — a rule about
	// deletions firing on a directory the command creates into.
	if reshapesCopyOperands(argv) {
		return nil
	}
	ops := operands(argv)
	if len(ops) < 2 {
		return nil
	}
	// A source that is a directory moves whole, contents included, with no
	// flag asked for — so every source is a recursive removal from where it was.
	return append(markRecursive(targetsFor(ops[:len(ops)-1], Remove), true), copyTargets(ops)...)
}

// rmIsRecursive reports whether an rm invocation removes directories with
// their contents: `-r`, `-R`, `--recursive`, or either letter inside a bundle
// of short flags (`-rf`, `-fR`). Flags stop at `--`, as they do for rm itself.
func rmIsRecursive(argv []string) bool {
	for _, arg := range argv[1:] {
		switch {
		case arg == "--":
			return false
		case arg == "--recursive":
			return true
		case strings.HasPrefix(arg, "--"), !strings.HasPrefix(arg, "-"), arg == "-":
			continue
		case strings.ContainsAny(arg[1:], "rR"):
			return true
		}
	}
	return false
}

// markRecursive sets Recursive on every target when the line removes
// directories whole. See FileTarget.Recursive.
func markRecursive(targets []FileTarget, recursive bool) []FileTarget {
	if !recursive {
		return targets
	}
	for i := range targets {
		targets[i].Recursive = true
	}
	return targets
}

// reshapesCopyOperands reports whether a cp-shaped invocation carries a flag
// that changes WHICH operand is the destination, rather than decorating the
// copy.
//
// # Why these two, and why refusing beats modelling
//
// `-t DIR` names the destination as a FLAG VALUE, so every operand becomes a
// source and the destination is not among them. `-T` asserts the opposite —
// the destination is never a directory — so the `Into` reading copyTargets
// states is not merely unknown but false.
//
// install refused `-t` from the start, with the argument written out at its
// entry above: reading the rest positionally is "a MEASURED wrong answer",
// because b.md is reported as the destination carrying a.md's bytes when the
// real results are DIR/a.md and DIR/b.md and b.md is only READ. That argument
// was never applied to cp and mv, which reach the same copyTargets by the same
// operand shape, so both carried the defect the comment describes. Measured
// before this:
//
//	cp -t target-dir a.md b.md   -> b.md Written, Into [target-dir a.md]
//	mv -t target-dir a.md b.md   -> target-dir REMOVED, plus b.md Written
//
// An unclaimed line costs a rule that does not fire; a wrong one costs a rule
// that fires on the wrong file, and the second is the failure this package
// treats as unacceptable. So the line is declined outright rather than modelled.
//
// # Why it is cluster-aware
//
// install's own guard compared whole strings, which its sibling sedlike right
// below already knew was not enough — short flags cluster. `install -Dt DIR a`
// walked straight past `a == "-t"` and inverted source and destination anyway,
// so the guard that existed did not hold its own case. A clustered short group
// is any single-dash argument that is not itself a long flag, and a `t` or `T`
// anywhere in it reshapes the operands.
func reshapesCopyOperands(argv []string) bool {
	for _, a := range argv[1:] {
		if a == "--" {
			// Everything after is an operand, so no later word is a flag.
			return false
		}
		switch a {
		case "--target-directory", "--no-target-directory":
			return true
		}
		if strings.HasPrefix(a, "--target-directory=") {
			return true
		}
		if strings.HasPrefix(a, "--") || !strings.HasPrefix(a, "-") || a == "-" {
			continue
		}
		// A clustered short group: `-t`, `-Dt`, `-T`, `-vT`.
		if strings.ContainsAny(a[1:], "tT") {
			return true
		}
	}
	return false
}

// copyTargets builds the destination of a copy-shaped invocation, stating both
// readings of the last operand.
//
// The three utilities that share cp's operand shape — cp, mv, install — share
// this, because they share the ambiguity exactly. With TWO operands the
// destination is a path whose bytes become the source's; with three or more the
// last is necessarily a directory and each source lands at base(source) inside
// it. But two operands can ALSO be a copy into a directory (`cp a.md dir/`),
// and which one it is depends on the tree.
//
// So both are stated and neither is chosen: Payload for the file reading, Into
// for the directory reading, and filemod picks with a stat it has already done.
// See FileTarget.Into.
//
// With three or more operands no Payload is set, because the file reading does
// not exist there — `cp a.md b.md c.md` with c.md a regular file is an error,
// not a copy onto it.
func copyTargets(ops []string) []FileTarget {
	if len(ops) < 2 {
		// EQUIVALENT today and kept anyway, so the survivor is read as an
		// equivalence rather than as an unchecked boundary. Every caller —
		// cp, install, movelike — applies the same test before calling, so
		// relaxing it here alone leaves the suite green. Verified by
		// measurement.
		//
		// Kept because it states the requirement where the DESTINATION is
		// chosen: a copy needs a source and a destination, and `ops[:len-1]`
		// below is a slice expression that is only meaningful once that holds.
		// A caller added later without its own guard would otherwise reach the
		// slice, and a one-element vector would make the single operand both
		// the source and the destination.
		return nil
	}
	sources := ops[:len(ops)-1]
	dst := targetsFor(ops[len(ops)-1:], Write)
	if len(ops) == 2 {
		dst = withPayload(dst, copyPayload(sources[0]))
	}
	for i := range dst {
		dst[i].Into = sources
	}
	return dst
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
			// Only where the spec claimed NOTHING. `dd if=a.md of=b.md <<'EOF'`
			// reads its input from a.md and never looks at stdin, so the
			// heredoc is discarded by the shell — overwriting the copy
			// reference with it would report bytes that never reach the file.
			//
			// The same guard is why this is a loop rather than a withPayload:
			// withPayload sets every target unconditionally, which is right
			// where a spec claims nothing and wrong here.
			for i := range own {
				if own[i].Payload.Kind == PayloadNone {
					own[i].Payload = stdin
				}
			}
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
	// The infix form — `find . -exec rm notes.md \;` — for the same reason the
	// payload branch below exists: without it the two halves of this package
	// disagree about one command line, a rule about RUNNING rm firing where a
	// rule about DELETING notes.md does not.
	//
	// The heredoc does NOT travel with it, and that is the difference from the
	// wrapper branch above. A wrapper is transparent to stdin — `sudo tee f.md
	// <<'EOF'` hands the document straight through — but find gives the command
	// it runs no stdin of its own, and `-exec` clauses are run once per matched
	// path. Attaching the outer statement's document to the inner command would
	// claim content for a file that never receives it.
	for _, cmd := range unwrapInfix(asWords(argv)) {
		targets = append(targets, targetsForArgv(values(cmd), depth, Payload{})...)
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
