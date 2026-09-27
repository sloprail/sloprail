package commandmod

import (
	"strings"
	"testing"
)

// This file covers what a word turns into: quoting, escaping, the basename
// reduction, and flag parsing. Nesting lives in nesting_test.go.

// argvOf is the resolved vector of the first invocation.
func argvOf(t *testing.T, src string) []string {
	t.Helper()
	invs := ExtractCommand(src).Invocations
	if len(invs) == 0 {
		t.Fatalf("ExtractCommand(%q) found no invocation", src)
	}
	return invs[0].Argv
}

// TestQuoting_IsUndoneBeforeMatching is the reason expansion is mandatory over
// raw syntax.
//
// Every spelling here is a way to write `npm publish` that a raw literal reads
// as something else: `n\pm` keeps its backslash, `n""pm` loses its program word
// entirely. A rule matching npm misses all of them unless expansion has run
// first — and every one is a spelling an agent can choose deliberately.
func TestQuoting_IsUndoneBeforeMatching(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want []string
	}{
		{"plain", `npm publish`, []string{"npm", "publish"}},
		{"double quoted program", `"npm" publish`, []string{"npm", "publish"}},
		{"single quoted program", `'npm' publish`, []string{"npm", "publish"}},
		{"empty double quotes inside", `n""pm publish`, []string{"npm", "publish"}},
		{"empty single quotes inside", `n''pm publish`, []string{"npm", "publish"}},
		{"backslash escape", `n\pm publish`, []string{"npm", "publish"}},
		{"several backslashes", `n\p\m publish`, []string{"npm", "publish"}},
		{"partial single quotes", `np'm' publish`, []string{"npm", "publish"}},
		{"partial double quotes", `n"p"m publish`, []string{"npm", "publish"}},
		{"quote concatenation", `'n'"p"m publish`, []string{"npm", "publish"}},
		{"ansi c quoting", `$'npm' publish`, []string{"npm", "publish"}},
		{"ansi c hex escape", `$'n\x70m' publish`, []string{"npm", "publish"}},
		{"ansi c octal escape", `$'n\160m' publish`, []string{"npm", "publish"}},
		{"escaped whole word", `\n\p\m publish`, []string{"npm", "publish"}},

		// Quoting in the arguments, not the program. Splitting is what a quote
		// prevents, so a quoted argument with a space stays one field.
		{"quoted argument keeps its space", `npm publish "a b"`, []string{"npm", "publish", "a b"}},
		{"escaped space keeps one word", `npm publish a\ b`, []string{"npm", "publish", "a b"}},
		{"single quoted argument", `npm publish 'a  b'`, []string{"npm", "publish", "a  b"}},
		{"quoted empty argument survives", `npm publish ""`, []string{"npm", "publish", ""}},
		{"quotes inside quotes", `npm publish "it's"`, []string{"npm", "publish", "it's"}},
		{"escaped quote", `npm publish \"x\"`, []string{"npm", "publish", `"x"`}},
		{"dollar in single quotes is literal", `npm publish '$HOME'`, []string{"npm", "publish", "$HOME"}},
		{"escaped dollar is literal", `npm publish \$HOME`, []string{"npm", "publish", "$HOME"}},

		// A program word with a space in it. The quote is what makes it one
		// word, and the basename reduction is applied to the whole thing.
		{"program word containing a space", `"my prog" x`, []string{"my prog", "x"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := argvOf(t, tc.src); !equal(got, tc.want) {
				t.Errorf("ExtractCommand(%q) argv = %q, want %q", tc.src, got, tc.want)
			}
		})
	}
}

// TestQuoting_ExpansionsThatCannotBeKnown: a word whose value depends on the
// runtime environment. The environment used for extraction is empty on purpose
// — reading the real one would make the same command line resolve differently
// for two agents — so these resolve to nothing, and nothing is invented.
func TestQuoting_ExpansionsThatCannotBeKnown(t *testing.T) {
	for _, tc := range []struct {
		name    string
		src     string
		want    []string
		notSeen []string
	}{
		// Word 0 is a parameter. It vanishes, which would shift `publish` into
		// the program's place; the literalness test is what stops that.
		{"variable program", `$NPM publish`, []string{}, []string{"npm", "publish", "NPM", ""}},
		{"braced variable program", `${BIN} install`, []string{}, []string{"install", "BIN", ""}},
		{"quoted variable program", `"$EDITOR" file.txt`, []string{}, []string{"file.txt", "EDITOR", ""}},

		// A parameter inside an otherwise literal program word. It resolves to a
		// real name, which is exactly why provenance alone is not enough — the
		// empty environment assumed the parameter was empty, and at runtime it
		// is anything.
		{"parameter inside the program word", `np${X}m publish`, []string{}, []string{"npm", "publish"}},
		{"parameter suffix", `npm${X} publish`, []string{}, []string{"npm", "publish"}},
		{"default value expansion", `${BIN:-npm} publish`, []string{}, []string{"npm", "publish"}},

		// The same three inside DOUBLE QUOTES. Quoting changes nothing about
		// what is knowable — the parameter is still a parameter and the empty
		// environment still assumed it was empty — but it is a different AST
		// shape, so it is a different path through isLiteral: the unquoted
		// cases above reach the `*syntax.Lit`/default arms, these reach the
		// `*syntax.DblQuoted` arm and its walk over the inner parts.
		//
		// Without these, that walk can be deleted — every word inside double
		// quotes reported literal — and the whole repository's suite stays
		// green, because every other double-quote case in this file has a
		// fully literal payload (`"npm"`, `n""pm`, `n"p"m`) and every
		// parameter-in-the-program case is unquoted. The mutant reports `npm`
		// for the first of these and `pre` for the third: a rule firing on a
		// program name that was inferred from an assumption about the
		// environment, which is the one outcome resolve.go calls worse than
		// missing it.
		{"quoted parameter inside the program word", `"np${X}m" publish`, []string{}, []string{"npm", "publish"}},
		{"quoted parameter suffix", `"npm${X}" publish`, []string{}, []string{"npm", "publish"}},
		{"quoted parameter prefix", `"pre$SUF" publish`, []string{}, []string{"pre", "publish"}},

		// A parameter in an ARGUMENT does not disqualify the program. echo is
		// genuinely about to run; only the unresolvable word is dropped.
		{"parameter in an argument", `echo $UNSET hi`, []string{"echo"}, nil},
		{"quoted parameter in an argument", `echo "$UNSET" hi`, []string{"echo"}, nil},
		{"arithmetic in an argument", `echo $((1+1))`, []string{"echo"}, nil},

		// Word splitting depends on the runtime IFS, which is not knowable. The
		// program is literal and genuinely about to run, so it is reported; the
		// arguments it will receive are not, and nothing is invented for them.
		{"runtime word splitting", `IFS=: ; cmd $ARGS`, []string{"cmd"}, []string{"rm"}},

		// A tilde is not a parameter, and is left alone rather than resolved
		// against whoever the guardrail runs as.
		{"tilde in an argument", `npm publish ~/x`, []string{"npm"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := binsOf(tc.src)
			if !equal(got, tc.want) && len(tc.want) > 0 {
				t.Errorf("ExtractCommand(%q) bins = %v, want %v", tc.src, got, tc.want)
			}
			if len(tc.want) == 0 && len(got) != 0 {
				t.Errorf("ExtractCommand(%q) bins = %v, want none", tc.src, got)
			}
			for _, bad := range tc.notSeen {
				if contains(got, bad) {
					t.Errorf("ExtractCommand(%q) fabricated %q; bins = %v", tc.src, bad, got)
				}
			}
		})
	}
}

// TestQuoting_ExtractionIgnoresTheProcessEnvironment: the expansion runs
// against an EMPTY environment, never the guardrail's own.
//
// This is a determinism property and it is load-bearing. If a real variable
// were read, the same command line would resolve differently depending on where
// it ran — a rule would fire for one agent and not another, and a shell whose
// NPM happened to be set would turn `$NPM publish` into a reported npm while an
// unset one reported nothing. Neither answer is reproducible, and a guardrail
// whose verdict depends on its own environment cannot be reasoned about.
//
// Setting the variables for real is what makes this a test rather than a
// restatement: if extraction consulted os.Environ, these would resolve.
func TestQuoting_ExtractionIgnoresTheProcessEnvironment(t *testing.T) {
	for _, kv := range [][2]string{
		{"NPM", "npm"}, {"BIN", "npm"}, {"EDITOR", "vim"},
		{"X", ""}, {"CMD", "rm"}, {"ARGS", "a b"}, {"UNSET", "surprise"},
	} {
		t.Setenv(kv[0], kv[1])
	}

	for _, tc := range []struct {
		src     string
		notSeen []string
	}{
		{`$NPM publish`, []string{"npm"}},
		{`${BIN} install`, []string{"npm"}},
		{`"$EDITOR" file.txt`, []string{"vim"}},
		{`$CMD -rf /`, []string{"rm"}},
		{`np${X}m publish`, []string{"npm"}},
	} {
		t.Run(tc.src, func(t *testing.T) {
			got := binsOf(tc.src)
			for _, bad := range tc.notSeen {
				if contains(got, bad) {
					t.Errorf("ExtractCommand(%q) resolved %q from the process environment; "+
						"bins = %v. Extraction must not depend on where it runs.", tc.src, bad, got)
				}
			}
		})
	}

	// The value must not leak into an ARGUMENT either — a rule reading argv
	// would see a string the command never receives.
	invs := ExtractCommand(`echo $UNSET hi`).Invocations
	if contains(invs[0].Argv, "surprise") {
		t.Errorf("argv = %q, leaked a value from the process environment", invs[0].Argv)
	}
}

// TestQuoting_UnresolvableArgumentIsDroppedNotBlanked: a word that cannot be
// resolved leaves no field in argv at all.
//
// Blanking it would put a phantom "" in the vector, and inside a wrapper the
// unwrapper would stop at that empty word and lose the nested program — the
// wrapper would shield what it wraps.
func TestQuoting_UnresolvableArgumentIsDroppedNotBlanked(t *testing.T) {
	invs := ExtractCommand(`echo $UNSET hi`).Invocations
	if len(invs) != 1 {
		t.Fatalf("got %d invocations, want 1", len(invs))
	}
	if !equal(invs[0].Argv, []string{"echo", "hi"}) {
		t.Errorf("argv = %q, want [echo hi] with the unresolvable word absent", invs[0].Argv)
	}

	// The wrapper case, which is where blanking would actually cost a rule its
	// match: npm has to survive the unresolvable word in front of it.
	assertBins(t, `sudo $(x) npm publish`, []string{"sudo", "npm", "x"})
}

// TestBasename_IsTheNameARuleIsWrittenAgainst.
//
// A rule says `npm`. It must not have to enumerate every path npm can live at,
// and — the direction that matters — an agent must not be able to evade it by
// spelling the path out.
func TestBasename_IsTheNameARuleIsWrittenAgainst(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want string
	}{
		{"bare", `npm publish`, "npm"},
		{"absolute path", `/usr/local/bin/npm publish`, "npm"},
		{"relative path", `./node_modules/.bin/npm publish`, "npm"},
		{"parent relative", `../npm publish`, "npm"},
		{"dot slash", `./npm publish`, "npm"},
		{"redundant slashes", `/usr//local///bin/npm publish`, "npm"},
		{"trailing dot segment", `/usr/local/bin/./npm publish`, "npm"},
		{"script with extension", `./scripts/deploy.sh`, "deploy.sh"},
		// A backslash-separated path only stays a path when it is quoted. Unquoted,
		// the shell consumes each backslash as an escape before this module sees
		// the word — which is what a real shell does too, so `C:\tools\npm`
		// genuinely invokes a program named `C:toolsnpm` and reporting that is
		// correct. The quoted spellings are the ones where the separators survive
		// to be normalised, and those must reduce to `npm` or a rule could be
		// evaded by writing a Windows path.
		{"quoted windows separators", `"C:\tools\npm" publish`, "npm"},
		{"single quoted windows separators", `'C:\tools\npm' publish`, "npm"},
		{"escaped windows separators", `C:\\tools\\npm publish`, "npm"},
		{"quoted mixed separators", `"C:\tools/bin\npm" publish`, "npm"},

		// Every quoting spelling of the path reaches the same basename, because
		// expansion runs first. A quoted path is not a way round a rule.
		{"quoted absolute path", `"/usr/local/bin/npm" publish`, "npm"},
		{"single quoted path", `'/usr/local/bin/npm' publish`, "npm"},
		{"escaped path", `/usr/local/bin/n\pm publish`, "npm"},
		{"path with an escaped space", `"./my dir/npm" publish`, "npm"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := binsOf(tc.src)
			if len(got) == 0 || got[0] != tc.want {
				t.Errorf("ExtractCommand(%q) bin = %v, want %q", tc.src, got, tc.want)
			}
		})
	}

	// argv keeps the path as written, so a refusal can quote what the agent
	// typed and a rule can still match on the full spelling if it wants to.
	if got := argvOf(t, `/usr/local/bin/npm publish`); got[0] != "/usr/local/bin/npm" {
		t.Errorf("argv[0] = %q, want the path preserved", got[0])
	}
}

// TestBasename_DegenerateProgramWords pins what a path-shaped word with no name
// in it reduces to. None of these is a program anyone means, and the value is
// what a rule would have to compare against — so it is written down rather than
// left to be discovered by a rule that mysteriously matches.
func TestBasename_DegenerateProgramWords(t *testing.T) {
	for _, tc := range []struct{ src, wantBin string }{
		{`/ x`, "/"},
		{`. ./script.sh`, "."},
		{`.. x`, ".."},
		{`a/b/ x`, "b"},    // a trailing slash is dropped, as path.Base does
		{`//// x`, "/"},    // all separators reduce to the root
		{`./ x`, "."},      // a trailing slash on a dot segment
		{`-npm x`, "-npm"}, // a leading dash is part of the name, not a flag here

		// An unquoted backslash is an escape, consumed before this module sees
		// the word — so these are not paths at all, and the name really is what
		// the escapes left behind. Same as a real shell.
		{`.\ x`, ". x"},                        // `\ ` is an escaped space: one word
		{`C:\tools\npm publish`, "C:toolsnpm"}, // escapes eaten, no separators left

		// An escaped backslash survives expansion as one real backslash, and
		// THAT one is normalised to a separator — so the word is the root. This
		// is the normalisation doing its job on the only backslash that ever
		// reaches it.
		{`\\ x`, "/"},
	} {
		t.Run(tc.src, func(t *testing.T) {
			got := binsOf(tc.src)
			if len(got) == 0 || got[0] != tc.wantBin {
				t.Errorf("ExtractCommand(%q) bin = %v, want %q", tc.src, got, tc.wantBin)
			}
		})
	}

	// An empty program word never reaches a matcher, in any spelling. A bin
	// with no name is a program no rule can mean, and one that compared equal
	// to a rule's empty default would fire on every such line.
	for _, src := range []string{`"" npm publish`, `'' npm publish`, `$'' npm publish`} {
		if got := binsOf(src); len(got) != 0 {
			t.Errorf("ExtractCommand(%q) bins = %v, want none — an empty bin must not reach a matcher", src, got)
		}
	}
}

// TestFlags_Parsing covers every spelling of a flag.
//
// The contract is narrow on purpose: an inline value is read, a separated one
// is not, because whether `--tag next` is a value or a positional depends on
// the program's own option table and guessing would report a value the command
// never receives.
func TestFlags_Parsing(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want map[string][]string
	}{
		{"short", `npm publish -f`, map[string][]string{"f": {""}}},
		{"long", `npm publish --dry-run`, map[string][]string{"dry-run": {""}}},
		{"long with inline value", `npm publish --tag=next`, map[string][]string{"tag": {"next"}}},
		{"short with inline value", `npm publish -m=x`, map[string][]string{"m": {"x"}}},
		{"clustered short flags", `npm publish -abc`, map[string][]string{"abc": {""}}},
		{"several flags", `npm publish --tag=next --dry-run -f`, map[string][]string{"tag": {"next"}, "dry-run": {""}, "f": {""}}},

		// A separated value is NOT read as a value. `--tag` is present with an
		// empty value and `next` stays a positional — a rule asks whether the
		// flag is there without having to know whether it takes one.
		{"long with separated value", `npm publish --tag next`, map[string][]string{"tag": {""}}},
		{"short with separated value", `npm publish -t next`, map[string][]string{"t": {""}}},

		// An inline value containing its own `=` keeps everything after the
		// first one, which is what a shell hands the program.
		{"value containing equals", `npm publish --set=a=b`, map[string][]string{"set": {"a=b"}}},
		{"empty inline value", `npm publish --tag=`, map[string][]string{"tag": {""}}},

		// `--` ends the flags. Everything after is positional by definition, so
		// a flag-shaped word after it is not a flag — reading it as one would
		// let a rule about `--force` fire on a line that passes the string
		// `--force` as an argument to a program.
		{"double dash ends flags", `npm publish --tag=next -- --force -x`, map[string][]string{"tag": {"next"}}},
		{"only a double dash", `npm publish --`, map[string][]string{}},
		{"double dash first", `npm -- --force`, map[string][]string{}},

		// A lone `-` is conventionally stdin, not a flag.
		{"lone dash", `npm publish -`, map[string][]string{}},

		// A flag-shaped word that is really a path. This one IS read as a flag,
		// because nothing here can tell them apart — the leading dash is the
		// only signal available.
		{"flag that looks like a path", `npm publish -/tmp/x`, map[string][]string{"/tmp/x": {""}}},

		// A path that merely contains a dash is not a flag: the dash is not
		// leading.
		{"path containing a dash", `npm publish ./-notaflag`, map[string][]string{}},
		{"path with a dashed segment", `npm publish src/a-b`, map[string][]string{}},

		// Degenerate dashes. None of these names a flag, and none may produce an
		// empty-named entry a rule could match by accident.
		{"triple dash", `npm publish ---triple`, map[string][]string{"triple": {""}}},
		{"dash equals", `npm publish -=x`, map[string][]string{}},
		{"double dash equals", `npm publish --=y`, map[string][]string{}},
		{"all dashes", `npm publish ---`, map[string][]string{}},

		// A repeated flag: every occurrence is kept, in order — a caller after
		// only the last (or the first) reads got[k][len(got[k])-1] (or [0]),
		// and one that means "either value" reads `"a" in .flags.tag`.
		{"repeated flag keeps every value", `npm publish --tag=a --tag=b`, map[string][]string{"tag": {"a", "b"}}},
		{"repeated valueless flag", `npm publish -f -f`, map[string][]string{"f": {"", ""}}},
		{"value then valueless", `npm publish --tag=a --tag`, map[string][]string{"tag": {"a", ""}}},

		// The program word is never a flag, even when it is spelled like one.
		{"program itself is dash shaped", `-npm publish`, map[string][]string{}},

		// Quoting does not hide a flag: expansion runs first, so a rule about
		// `--force` fires on every spelling of it.
		{"quoted flag", `npm publish "--force"`, map[string][]string{"force": {""}}},
		{"escaped flag", `npm publish \-\-force`, map[string][]string{"force": {""}}},
		{"partially quoted flag", `npm publish --for"ce"`, map[string][]string{"force": {""}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			invs := ExtractCommand(tc.src).Invocations
			if len(invs) == 0 {
				t.Fatalf("ExtractCommand(%q) found no invocation", tc.src)
			}
			got := invs[0].Flags
			if len(got) != len(tc.want) {
				t.Fatalf("ExtractCommand(%q) flags = %v, want %v", tc.src, got, tc.want)
			}
			for k, v := range tc.want {
				g, ok := got[k]
				if !ok || !equal(g, v) {
					t.Errorf("flags[%q] = %q (present=%v), want %q", k, g, ok, v)
				}
			}
		})
	}
}

// TestFlags_NeverCarryAnEmptyName: a map entry keyed by the empty string would
// be reachable as `"" in .flags`, which is a match no rule means and every
// dashes-only word would produce.
func TestFlags_NeverCarryAnEmptyName(t *testing.T) {
	for _, src := range []string{
		`npm publish -=x`, `npm publish --=y`, `npm publish ---`, `npm publish --`,
		`npm publish -`, `npm publish ----=z`,
	} {
		invs := ExtractCommand(src).Invocations
		if _, ok := invs[0].Flags[""]; ok {
			t.Errorf("ExtractCommand(%q) flags = %v, carries an empty name", src, invs[0].Flags)
		}
	}
}

// TestFlags_AreNotSharedBetweenInvocations: each invocation gets its own map.
// A shared one would let a wrapper's flags appear on what it wraps, and a rule
// about `npm --force` would fire on `sudo --force npm publish`.
func TestFlags_AreNotSharedBetweenInvocations(t *testing.T) {
	invs := ExtractCommand(`sudo -E npm publish --tag=next`).Invocations
	if len(invs) != 2 {
		t.Fatalf("got %d invocations, want 2", len(invs))
	}
	if _, ok := invs[0].Flags["tag"]; !ok {
		// sudo's argv holds the whole line, so it does see the tag — that is
		// consistent with argv, and the assertion below is the one that matters.
		t.Logf("sudo flags = %v", invs[0].Flags)
	}
	if _, ok := invs[1].Flags["E"]; ok {
		t.Errorf("npm flags = %v, carries sudo's own flag", invs[1].Flags)
	}
	invs[0].Flags["injected"] = []string{"x"}
	if _, ok := invs[1].Flags["injected"]; ok {
		t.Error("invocations share one flag map")
	}
}

// TestAdversarial_NothingCrashesAndNothingIsInvented.
//
// A panic here exits the guardrail non-zero, which a harness reads as a refusal
// — so a malformed line would not weaken a rule, it would block the agent for a
// reason nobody can act on. Every one of these must return.
func TestAdversarial_NothingCrashesAndNothingIsInvented(t *testing.T) {
	for _, tc := range []struct {
		name     string
		src      string
		wantNone bool // must produce no invocation at all
	}{
		{"empty", ``, true},
		{"spaces only", `   `, true},
		{"tabs and newlines only", "\t\n \n\t", true},
		{"comment only", `# npm publish`, true},
		{"comment after a command", `npm publish # comment`, false},
		{"unterminated double quote", `npm publish "`, true},
		{"unterminated single quote", `npm publish '`, true},
		{"unterminated ansi c quote", `npm publish $'`, true},
		{"unterminated substitution", `echo $(npm publish`, true},
		{"unterminated backtick", "echo `npm publish", true},
		{"unterminated subshell", `(npm publish`, true},
		{"unterminated brace group", `{ npm publish;`, true},
		{"unterminated heredoc", "sh <<EOF\nnpm publish\n", true},
		{"unbalanced parens", `(((`, true},
		{"stray semicolons", `npm publish ; ; ;`, true},
		{"keywords with no body", `if then fi`, true},
		{"lone done", `done`, false},
		{"lone fi", `fi`, true},
		{"arithmetic that is not", `((npm publish))`, true},
		{"only an operator", `&&`, true},
		{"only a pipe", `|`, true},
		{"trailing operator", `npm publish &&`, true},
		{"NUL byte", "npm\x00publish", false},
		{"NUL byte alone", "\x00", true},
		{"invalid utf8", "\xff\xfe npm", false},
		{"NUL and invalid utf8", "\x00\xff", true},
		{"carriage returns", "npm publish\r\nnpm whoami\r\n", false},
		{"vertical tab", "npm\vpublish", false},
		{"unicode program name", `ｎｐｍ publish`, false},
		{"emoji argument", `npm publish 🚀`, false},
		{"very long single word", strings.Repeat("a", 100000), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ev := ExtractCommand(tc.src) // must return rather than panic

			// The raw line always rides along, malformed or not. It is what a
			// rule about unreadable commands matches on, and what a refusal
			// quotes back — dropping the event would mean the one line nobody
			// could read is also the one nobody is watching.
			if ev.Raw != tc.src {
				t.Errorf("raw = %q, want %q", ev.Raw, tc.src)
			}
			if tc.wantNone && len(ev.Invocations) != 0 {
				t.Errorf("ExtractCommand(%q) invocations = %+v, want none", tc.src, ev.Invocations)
			}
			// Whatever came back, no invocation may carry a nameless program or
			// a nil vector: both are shapes a matcher would read as a value.
			for _, inv := range ev.Invocations {
				if inv.Bin == "" {
					t.Errorf("empty bin in %+v", ev.Invocations)
				}
				if len(inv.Argv) == 0 {
					t.Errorf("empty argv in %+v", ev.Invocations)
				}
				if inv.Flags == nil {
					t.Errorf("nil flags in %+v", ev.Invocations)
				}
			}
		})
	}
}

// TestAdversarial_UnparseableLinesReportNothingRatherThanGuess: a line the
// parser rejects yields no invocations at all, not a partial read of the words
// it managed. Half a parse is a guess, and a guessed program is worse than a
// missing one.
func TestAdversarial_UnparseableLinesReportNothingRatherThanGuess(t *testing.T) {
	for _, src := range []string{
		`npm publish "`,
		`npm publish $(`,
		`npm publish; ; ;`,
		`(npm publish`,
	} {
		if got := binsOf(src); len(got) != 0 {
			t.Errorf("ExtractCommand(%q) bins = %v, want none from an unparseable line", src, got)
		}
	}
}

// TestAdversarial_LargeInput: size is not a way to get past the parser. A 1MB
// line and a line with twenty thousand statements both come back complete.
func TestAdversarial_LargeInput(t *testing.T) {
	t.Run("1MB argument", func(t *testing.T) {
		src := "npm publish " + strings.Repeat("a", 1<<20)
		invs := ExtractCommand(src).Invocations
		if len(invs) != 1 || invs[0].Bin != "npm" {
			t.Fatalf("invocations = %d, want one npm", len(invs))
		}
		if len(invs[0].Argv) != 3 || len(invs[0].Argv[2]) != 1<<20 {
			t.Errorf("argv lengths = %d/%d, want the whole argument preserved",
				len(invs[0].Argv), len(invs[0].Argv[2]))
		}
	})

	t.Run("1MB of many arguments", func(t *testing.T) {
		src := "npm publish" + strings.Repeat(" x", 1<<19)
		invs := ExtractCommand(src).Invocations
		if len(invs) != 1 || len(invs[0].Argv) != 2+(1<<19) {
			t.Errorf("argv len = %d, want %d", len(invs[0].Argv), 2+(1<<19))
		}
	})

	t.Run("twenty thousand statements", func(t *testing.T) {
		got := binsOf(strings.Repeat("npm publish; ", 20000))
		if len(got) != 20000 {
			t.Errorf("got %d invocations, want 20000 — a long line must not be truncated", len(got))
		}
	})

	t.Run("a thousand pipeline stages", func(t *testing.T) {
		got := binsOf("npm publish" + strings.Repeat(" | cat", 1000))
		if len(got) != 1001 {
			t.Errorf("got %d invocations, want 1001", len(got))
		}
	})
}
