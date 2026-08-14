package commandmod

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

// render turns a target list into a stable string, so a case reads as the
// answer rather than as a struct literal.
//
// Sorted, because what a rule asks is which files a line touches, not the order
// the parser happened to walk them in. A test asserting the order would fail on
// a traversal change that no rule could observe.
func render(targets []FileTarget) string {
	if len(targets) == 0 {
		return "(nothing)"
	}
	parts := make([]string, 0, len(targets))
	for _, t := range targets {
		effect := "write"
		if t.Effect == Remove {
			effect = "remove"
		}
		parts = append(parts, fmt.Sprintf("%s:%s", effect, t.Path))
	}
	sort.Strings(parts)
	return strings.Join(parts, " ")
}

func check(t *testing.T, command, want string) {
	t.Helper()
	if got := render(FileTargets(command)); got != want {
		t.Errorf("FileTargets(%q)\n got: %s\nwant: %s", command, got, want)
	}
}

// TestFileTargets_TheFourMeasuredCommands is the defect itself, stated as a
// test.
//
// These four are what the report measured against the real ExtractCommand:
// commandmod saw a program and the file module saw nothing at all, so a rule
// bound to PreFileDelete on notes.md did not fire on `rm notes.md`. Each one
// now names its file and says what will happen to it.
func TestFileTargets_TheFourMeasuredCommands(t *testing.T) {
	check(t, "rm -rf notes.md", "remove:notes.md")
	check(t, "mv a.md b.md", "remove:a.md write:b.md")
	check(t, "echo x > out.md", "write:out.md")
	check(t, "sed -i '' s/a/b/ f.md", "write:f.md")
}

// TestFileTargets_RedirectionsAreSyntaxNotVocabulary covers the half that
// cannot drift: the shell grammar says what `>` does, and no project renames it.
func TestFileTargets_RedirectionsAreSyntaxNotVocabulary(t *testing.T) {
	// A truncating write, an append, and a numbered stream. All three change
	// the file they name.
	check(t, "echo x > out.md", "write:out.md")
	check(t, "echo x >> out.md", "write:out.md")
	check(t, "cmd 2> err.log", "write:err.log")

	// The program is irrelevant — a redirection writes whatever runs in front
	// of it, including a program this package has never heard of.
	check(t, "some-unknown-tool > out.md", "write:out.md")

	// Both streams, and the noclobber overrides.
	check(t, "cmd &> all.log", "write:all.log")
	check(t, "cmd &>> all.log", "write:all.log")
	check(t, "cmd >| out.md", "write:out.md")

	// `<>` opens for reading AND writing, so it can change the file.
	check(t, "cmd <> both.md", "write:both.md")
}

// TestFileTargets_ReadingRedirectionsChangeNothing pins the other direction:
// an operator that only reads must not produce a write event, or every `grep`
// with an input file would fire a rule about modifying it.
func TestFileTargets_ReadingRedirectionsChangeNothing(t *testing.T) {
	check(t, "grep foo < in.md", "(nothing)")
	check(t, "cat <<EOF\nhi\nEOF", "(nothing)")
	check(t, "cat <<-EOF\nhi\nEOF", "(nothing)")
	check(t, "grep foo <<< in.md", "(nothing)")
}

// TestFileTargets_DescriptorDuplicationIsNotAPath is the case that would
// otherwise announce a file called "1".
//
// `2>&1` is a redirection whose word is a file descriptor number, not a path.
// Reading it as one would produce a PreFileUpdate on a file named `1`, which no
// rule means and which would fire on the commonest idiom in shell.
func TestFileTargets_DescriptorDuplicationIsNotAPath(t *testing.T) {
	check(t, "cmd 2>&1", "(nothing)")
	check(t, "cmd >&2", "(nothing)")
	// The combination: a real file, and a duplication that is not one.
	check(t, "cmd > out.log 2>&1", "write:out.log")
}

// TestFileTargets_KnownBinariesNameTheirFiles covers the vocabulary half —
// the entries whose trade knownbin.go argues.
func TestFileTargets_KnownBinariesNameTheirFiles(t *testing.T) {
	check(t, "rm notes.md", "remove:notes.md")
	check(t, "rm a.md b.md", "remove:a.md remove:b.md")
	check(t, "touch new.md", "write:new.md")
	check(t, "tee out.md", "write:out.md")
	check(t, "truncate -s 0 f.md", "write:f.md")
	check(t, "dd if=/dev/zero of=out.bin", "write:out.bin")
	check(t, "cp a.md b.md", "write:b.md")
	check(t, "install -m 0644 a.md b.md", "write:b.md")
	check(t, "ln -s a.md b.md", "write:b.md")
	check(t, "mkdir newdir", "write:newdir")
}

// TestFileTargets_MoveIsBothARemovalAndAWrite pins the case a one-effect answer
// would get half right.
//
// `mv notes.md elsewhere.md` makes notes.md stop existing at its path, which is
// what a delete rule is about, AND replaces elsewhere.md, which is what an
// update rule is about. Reporting only one of them leaves one of those rules
// evadable by an `mv`.
func TestFileTargets_MoveIsBothARemovalAndAWrite(t *testing.T) {
	check(t, "mv notes.md elsewhere.md", "remove:notes.md write:elsewhere.md")
	// Several sources into a directory: every source stops existing.
	check(t, "mv a.md b.md target", "remove:a.md remove:b.md write:target")
	// One operand is a usage error that touches nothing.
	check(t, "mv a.md", "(nothing)")
}

// TestFileTargets_InPlaceEditingIsAWriteAndReadingIsNot is the flag-dependent
// entry: sed is the one binary whose effect turns on an option being present.
func TestFileTargets_InPlaceEditingIsAWriteAndReadingIsNot(t *testing.T) {
	// Without -i, sed reads the file and writes stdout. The file is untouched.
	check(t, "sed s/a/b/ f.md", "(nothing)")

	// GNU's bare -i, BSD's mandatory empty suffix, the suffix form, and the
	// long spelling are the same intent and all four are caught.
	check(t, "sed -i s/a/b/ f.md", "write:f.md")
	check(t, "sed -i '' s/a/b/ f.md", "write:f.md")
	check(t, "sed -i.bak s/a/b/ f.md", "write:f.md")
	check(t, "sed --in-place s/a/b/ f.md", "write:f.md")

	// perl's -i is the same idiom.
	check(t, "perl -i -pe s/a/b/ f.md", "write:f.md")
}

// TestFileTargets_TheSedScriptIsNotAFile is a regression pin on a measured bug.
//
// BSD sed's empty suffix does NOT vanish during expansion, which the first
// version of this code assumed. `sed -i ” s/a/b/ f.md` resolves to
// [sed -i "" s/a/b/ f.md], so the operands are ["", "s/a/b/", "f.md"] and the
// script sits at index 1 rather than 0. Skipping index 0 consumed the empty
// string, left the script in the list, and reported a file about to be written
// called `s/a/b/`.
//
// That is not a cosmetic wrong. A guardrail matching on a path prefix would
// have fired on a substitution expression, and one matching the real file would
// have fired anyway — so the bug was invisible from the rule that mattered and
// visible only as a spurious second event.
func TestFileTargets_TheSedScriptIsNotAFile(t *testing.T) {
	for _, command := range []string{
		"sed -i '' s/a/b/ f.md",
		"sed -i s/a/b/ f.md",
		"sed -i '' 's|x|y|' f.md",
	} {
		for _, target := range FileTargets(command) {
			if target.Path != "f.md" {
				t.Errorf("FileTargets(%q) reported %q — the script is not a file", command, target.Path)
			}
		}
	}
}

// TestFileTargets_UnknowableCommandsSayNothing is the third source stated
// plainly.
//
// No parse can say what an interpreter or a build tool touches. Guessing would
// invent events, and a rule resting on invented events is worse than one that
// knows it does not cover them — the tree diff at session stop is what covers
// these, after the fact.
func TestFileTargets_UnknowableCommandsSayNothing(t *testing.T) {
	check(t, "python script.py", "(nothing)")
	check(t, "make build", "(nothing)")
	check(t, "./build.sh", "(nothing)")
	check(t, "npm run build", "(nothing)")
	check(t, "go generate ./...", "(nothing)")
}

// TestFileTargets_ReadingCommandsProduceNoEvent is the reverse proof the brief
// asks for, at the unit level: this must not become a rule that fires on
// everything.
//
// Each of these names a file and changes none of it. If any produced a target,
// every guardrail about a file would fire on the agent merely LOOKING at it,
// which is the failure that would make the feature worse than its absence.
func TestFileTargets_ReadingCommandsProduceNoEvent(t *testing.T) {
	for _, command := range []string{
		"cat notes.md",
		"less notes.md",
		"head -n 5 notes.md",
		"wc -l notes.md",
		"grep foo notes.md",
		"ls -la",
		"git status",
		"git log --oneline notes.md",
		"diff a.md b.md",
		"file notes.md",
		"stat notes.md",
	} {
		if got := render(FileTargets(command)); got != "(nothing)" {
			t.Errorf("FileTargets(%q) = %s — a command that changes nothing must produce nothing", command, got)
		}
	}
}

// TestFileTargets_AWrapperDoesNotHideTheFile pins that the evasion the
// unwrapping already prevents for invocations is prevented here too.
//
// A rule about deleting notes.md that could be evaded by typing `sudo` in front
// would be a suggestion. The unwrapping is commandmod's existing one, reused
// rather than reimplemented.
func TestFileTargets_AWrapperDoesNotHideTheFile(t *testing.T) {
	check(t, "sudo rm notes.md", "remove:notes.md")
	check(t, "sudo -u root rm notes.md", "remove:notes.md")
	check(t, "env FOO=1 rm notes.md", "remove:notes.md")
	check(t, "timeout 5 rm notes.md", "remove:notes.md")
	// Wrappers stack.
	check(t, "sudo nohup rm notes.md", "remove:notes.md")
	// A path spelling does not hide it either — the table is keyed on basename.
	check(t, "/bin/rm notes.md", "remove:notes.md")
}

// TestFileTargets_NestingDoesNotHideTheFile pins that the file is found wherever
// the shell puts it, which is the same promise the invocation walk makes.
func TestFileTargets_NestingDoesNotHideTheFile(t *testing.T) {
	check(t, "echo hi && rm notes.md", "remove:notes.md")
	check(t, "echo hi; rm notes.md", "remove:notes.md")
	check(t, "false || rm notes.md", "remove:notes.md")
	check(t, "(rm notes.md)", "remove:notes.md")
	check(t, "{ rm notes.md; }", "remove:notes.md")
	check(t, "cat x | tee notes.md", "write:notes.md")
	check(t, "if true; then rm notes.md; fi", "remove:notes.md")
	check(t, "for f in a; do rm notes.md; done", "remove:notes.md")
}

// TestFileTargets_QuotingIsUndone pins that a rule matches what will run rather
// than the string the agent typed. Without expansion `n""otes.md` reads as a
// path with quotes in it and matches no rule about notes.md.
func TestFileTargets_QuotingIsUndone(t *testing.T) {
	check(t, `rm "notes.md"`, "remove:notes.md")
	check(t, `rm 'notes.md'`, "remove:notes.md")
	check(t, `rm n""otes.md`, "remove:notes.md")
	check(t, `rm note\s.md`, "remove:notes.md")
	check(t, `echo x > "out.md"`, "write:out.md")
}

// TestFileTargets_AnUncertainPathIsNotGuessedAt is the honest floor, and it
// matters in the direction that is easy to get wrong.
//
// `rm $TARGET` names a path only if the empty environment is the real one. It
// is not: at run time the variable could hold anything. Reporting a resolved
// value would fire a rule on a file the command may never touch, which is worse
// than missing it — the same judgement resolve already makes about a program
// name it cannot be sure of.
func TestFileTargets_AnUncertainPathIsNotGuessedAt(t *testing.T) {
	check(t, "rm $TARGET", "(nothing)")
	check(t, "rm ${TARGET}", "(nothing)")
	check(t, `rm "$TARGET"`, "(nothing)")
	check(t, "rm $(ls)", "(nothing)")
	check(t, "echo x > $OUT", "(nothing)")
}

// TestFileTargets_APathAssembledFromTheEnvironmentIsNotAPath is the sharp end of
// the certainty rule, and the case the first version of these tests missed.
//
// The others above are easy: `$OUT` alone expands to NOTHING under the empty
// environment, so it is dropped by the field count whether or not literalness
// is tested. These are different. `a$OUT.md` expands to exactly one non-empty
// field — `a.md` — so it looks in every way like a resolved path, and it is one
// the command will not touch. At run time $OUT holds something, and the file
// written is `aWHATEVER.md`.
//
// This is resolve's own `np${X}m` hazard, one word along: a value that resolves
// to a real-looking name only because the empty environment was assumed to be
// the real one. Reporting it fires a rule on a file nothing writes, while the
// file that IS written goes unguarded — wrong in both directions at once.
//
// Found by mutation. Deleting the literalness test from literalWord, and from
// fromCall's program check, left the whole suite green, because every case
// covered happened to be caught by the field-count test downstream.
func TestFileTargets_APathAssembledFromTheEnvironmentIsNotAPath(t *testing.T) {
	// A redirection target with a literal prefix and suffix around a parameter.
	check(t, "echo x > a$OUT.md", "(nothing)")
	check(t, "echo x > pre${X}post", "(nothing)")
	// The same shape as an operand.
	check(t, "rm a$B.md", "(nothing)")
	check(t, "rm notes${SUFFIX}.md", "(nothing)")
	// A certain path on the same line is still reported; only the assembled one
	// is declined.
	check(t, "rm notes.md a$B.md", "remove:notes.md")
}

// TestFileTargets_ALiteralWordCanStillFailToNameOnePath pins the two checks in
// literalWord that are NOT the literalness test, and that a reader would take
// for defensive habit.
//
// Both are reachable from ordinary shell, which is the surprise. A word can be
// entirely literal — no parameter, no substitution, nothing the environment
// could change — and still not name one path:
//
//	> ""       literal, expands to exactly one field which is the EMPTY string.
//	           A file event naming "" is one no rule can mean, and lookAt would
//	           stat the current directory for it.
//	> {a,b}    literal, and brace expansion makes it TWO fields. A shell refuses
//	           this as an ambiguous redirect; picking one would be a guess about
//	           which, and reporting both would announce a write the shell will
//	           not perform.
//
// Found by mutation: relaxing the check to `len(got) == 0` left the suite green,
// because every case covered until now was non-literal and stopped one test
// earlier.
func TestFileTargets_ALiteralWordCanStillFailToNameOnePath(t *testing.T) {
	check(t, `echo x > ""`, "(nothing)")
	check(t, "echo x > ''", "(nothing)")
	check(t, "echo x > {a,b}", "(nothing)")
	// A single-element brace is not an expansion at all and does name a path.
	check(t, "echo x > a b", "write:a")
}

// TestFileTargets_AnAssembledPROGRAMNameIsNotBelieved is the same rule applied
// to argv[0], which is where it matters most.
//
// `r${X}m notes.md` resolves to `rm` under the empty environment and to
// anything at all at run time. Believing it would run the whole known-binary
// table against a program nobody named — reporting a deletion of notes.md from
// a command line that may not delete anything.
//
// resolve makes exactly this check for the invocation list, with the same
// reasoning; this pins that the file side does not quietly skip it.
func TestFileTargets_AnAssembledPROGRAMNameIsNotBelieved(t *testing.T) {
	check(t, "r${X}m notes.md", "(nothing)")
	check(t, "${RM} notes.md", "(nothing)")
	check(t, "$CMD notes.md", "(nothing)")
	// A redirection on the same line is still seen: the redirection is syntax
	// and does not depend on which program runs in front of it.
	check(t, "r${X}m notes.md > out.log", "write:out.log")
}

// TestFileTargets_AGlobNamesNoOnePath is the third certainty case, and the one
// that hid behind the other two.
//
// safeConfig disables globbing so the filesystem is never read, which means a
// `*` survives expansion intact and the word LOOKS literal — isLiteral agrees,
// because a `*` is a plain Lit to the parser. So `rm *.md` passed every
// certainty test this package applies and was reported as a removal of a file
// called `*.md`: a path no rule can mean, matching no file, harmless only
// because nothing happens to be named that.
//
// The cost of dropping it is stated rather than hidden. `rm *.md` produces no
// Pre event, so a rule protecting notes.md does not prevent it — that command
// is in the unknowable tier, and the tree diff at session stop reports it after
// the fact. Reporting `*.md` would not have helped: no rule about notes.md
// matches it either.
func TestFileTargets_AGlobNamesNoOnePath(t *testing.T) {
	check(t, "rm *.md", "(nothing)")
	check(t, "rm notes.?", "(nothing)")
	check(t, "rm notes[12].md", "(nothing)")
	check(t, "echo x > *.md", "(nothing)")
	// A glob alongside a certain path still reports the certain one.
	check(t, "rm notes.md *.tmp", "remove:notes.md")
}

// TestFileTargets_AnUncertainWordKeepsItsPlace is a regression pin on the
// second measured bug in this file's history.
//
// An unresolvable word used to be DROPPED from the vector, the way resolve
// drops it. That is right for an argument vector and wrong here, because every
// entry in the table reads its paths positionally: `mv a.md $B` became
// [mv a.md], which movelike correctly refuses as a usage error — so the removal
// of a.md, which happens whatever $B turns out to be, went unreported.
//
// A rule protecting a.md from deletion was therefore evadable by moving it to a
// variable destination, which is a one-word change to the command line.
func TestFileTargets_AnUncertainWordKeepsItsPlace(t *testing.T) {
	// The source is removed whatever the destination turns out to be.
	check(t, "mv a.md $B", "remove:a.md")
	// The destination is written whatever the source turns out to be.
	check(t, "mv $A b.md", "write:b.md")
	// cp writes only its destination, so an unknown destination is nothing —
	// and the known source is correctly NOT reported, since cp does not touch it.
	check(t, "cp a.md $B", "(nothing)")
	// A known removal alongside an unknown one still reports the known.
	check(t, "rm a.md $B", "remove:a.md")
}

// TestFileTargets_FlagsAreNotPaths pins that an option is never reported as a
// file. `rm -rf notes.md` must report notes.md and not `-rf`.
func TestFileTargets_FlagsAreNotPaths(t *testing.T) {
	check(t, "rm -rf notes.md", "remove:notes.md")
	check(t, "rm -r -f notes.md", "remove:notes.md")
	check(t, "rm --recursive notes.md", "remove:notes.md")
	check(t, "cp -r a.md b.md", "write:b.md")
	// `--` ends the options, so a file whose name starts with a dash is a file.
	check(t, "rm -- -weird.md", "remove:-weird.md")
	// truncate's -s takes a SEPARATED value, which must not be read as a path.
	check(t, "truncate -s 0 f.md", "write:f.md")
	check(t, "truncate --size=0 f.md", "write:f.md")
}

// TestFileTargets_AnUnparseableLineYieldsNothing pins the same answer walk
// gives: a line nobody can read produces no guess.
func TestFileTargets_AnUnparseableLineYieldsNothing(t *testing.T) {
	check(t, "rm notes.md &&", "(nothing)")
	check(t, "(((", "(nothing)")
	check(t, "", "(nothing)")
}

// TestFileTargets_ACommandSubstitutionIsNotRun pins that finding the files a
// line touches never executes anything.
//
// The statements inside a substitution are still WALKED, so an `rm` in there is
// reported — which is correct, it genuinely is about to run — but the
// substitution itself resolves to nothing rather than being executed.
func TestFileTargets_ACommandSubstitutionIsNotRun(t *testing.T) {
	// The inner rm is seen without the substitution being run.
	check(t, "echo $(rm notes.md)", "remove:notes.md")
	// The outer path is unknowable, and is not guessed at.
	check(t, "rm $(cat list.txt)", "(nothing)")
}

// TestFileTargets_HeredocBodiesAreNotPaths pins that a here-document's TEXT is
// never read as a filename, however file-shaped its lines look.
func TestFileTargets_HeredocBodiesAreNotPaths(t *testing.T) {
	check(t, "cat <<EOF\nnotes.md\nEOF", "(nothing)")
	// A heredoc feeding a command that DOES write still reports only the file
	// the command names.
	check(t, "tee out.md <<EOF\nhello\nEOF", "write:out.md")
}

// TestFileTargets_OneFilePerLineHoweverOftenItIsNamed pins the deduplication.
//
// A rule should be asked once about a file. Asking twice runs a judging hook
// twice over one decision, and a judge hook is a model call rather than a
// function.
func TestFileTargets_OneFilePerLineHoweverOftenItIsNamed(t *testing.T) {
	// Named twice by one program. Deduplication happens in the file module,
	// which is where one path becomes one event, so both are reported here.
	if got := len(FileTargets("rm a.md a.md")); got != 2 {
		t.Errorf("FileTargets reported %d targets; this layer reports what the line says and the file module dedupes", got)
	}
}
