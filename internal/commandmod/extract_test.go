package commandmod

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"

	"github.com/sloprail/sloprail/internal/module"
)

// bins is the flattened list of programs an extraction found, which is what
// nearly every case here is actually asserting on.
func bins(t *testing.T, src string) []string {
	t.Helper()
	got := []string{}
	for _, inv := range ExtractCommand(src).Invocations {
		got = append(got, inv.Bin)
	}
	return got
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestExtractCommand_Flattens is the adversarial corpus.
//
// Every case is a way one command line hides a program from a rule matching
// the string: an escape, a quote, a pipe, a chain, a subshell, a substitution,
// a wrapper, a keyword. The point of the module is that none of them work.
func TestExtractCommand_Flattens(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want []string
	}{
		// An environment prefix is not a program. This is the distinction a
		// regex cannot make and the reason for parsing at all.
		{"env prefix", `FOO=1 npm publish`, []string{"npm"}},

		// Quoting and escaping are undone by expansion. Without it the raw
		// literals read as ` publish` and `n\pm`, and a rule matching npm
		// misses both.
		{"empty quotes", `n""pm publish`, []string{"npm"}},
		{"backslash", `n\pm publish`, []string{"npm"}},
		{"single quotes", `'npm' publish`, []string{"npm"}},

		// Every program in a pipeline is about to run, not just the first.
		{"pipeline", `ls | grep x | wc -l`, []string{"ls", "grep", "wc"}},

		// A chain runs conditionally, but a rule asks what is about to run,
		// not which branch will be taken.
		{"and or chain", `a && b || c`, []string{"a", "b", "c"}},
		{"semicolons", `a; b; c`, []string{"a", "b", "c"}},

		// Nesting one level deeper must not hide anything.
		{"subshell", `(cd /tmp && rm -rf x)`, []string{"cd", "rm"}},
		{"block", `{ npm publish; }`, []string{"npm"}},

		// A substitution's own statements are walked as syntax. npm is
		// reported without any handler ever running it; echo survives because
		// one unresolvable argument does not erase the command receiving it.
		{"command substitution", `echo $(npm publish)`, []string{"echo", "npm"}},
		{"backticks", "echo `npm publish`", []string{"echo", "npm"}},

		// Wrappers: both the wrapper and what it wraps.
		{"sudo", `sudo npm publish`, []string{"sudo", "npm"}},
		{"sudo with flag value", `sudo -u root npm publish`, []string{"sudo", "npm"}},
		{"sudo double dash", `sudo -- npm publish`, []string{"sudo", "npm"}},
		{"xargs", `xargs -n1 rm`, []string{"xargs", "rm"}},
		{"env wrapper", `env FOO=1 npm publish`, []string{"env", "npm"}},
		{"stacked wrappers", `sudo nohup npm publish`, []string{"sudo", "nohup", "npm"}},
		{"bare wrapper", `sudo`, []string{"sudo"}},

		// A wrapper whose wrapped program is an empty word. sudo is genuinely
		// invoked and is reported; the nested invocation has no name, and an
		// empty bin reaching a matcher is a program no rule can mean. This is
		// the one path that reaches the empty-bin filter, since a bare empty
		// program word is already rejected as a non-program earlier.
		{"wrapper wrapping nothing", `sudo "" publish`, []string{"sudo"}},

		// Keywords and forms that are not a CallExpr at the top. A traversal
		// that matched only calls would see through none of these.
		{"time clause", `time npm publish`, []string{"npm"}},
		{"time block", `time { npm publish; }`, []string{"npm"}},
		{"negation", `! npm publish`, []string{"npm"}},
		{"background", `npm publish &`, []string{"npm"}},

		// A redirection is not a program, and does not stop one being seen.
		{"redirect", `echo hi > out.txt`, []string{"echo"}},

		// A bare assignment runs nothing.
		{"bare assignment", `FOO=1`, []string{}},
		{"export", `export FOO=1`, []string{}},

		// The program is named by a variable that expands to nothing, which
		// shifts the vector. Nothing is emitted: `publish` is an argument, and
		// reporting it as the program would be inventing one.
		//
		// The benign-looking `$UNSET npm publish` is suppressed too, and has
		// to be. It parses identically — word 0 a ParamExp that expands to
		// nothing, then literals — so no rule can tell an env prefix that
		// vanished from a program that vanished. Emitting npm here would be a
		// guess that is merely right this time, and the same guess is what
		// turns `$CMD --force deploy` into a binary named `--force`.
		{"unset program shifts vector", `$NPM publish`, []string{}},
		{"unset prefix is indistinguishable", `$UNSET npm publish`, []string{}},

		// An unresolvable argument is dropped from the vector rather than left
		// as an empty string. Keeping it would put a phantom "" in argv, and
		// here it would also stop the unwrapper at the empty word and lose the
		// nested npm entirely — the wrapper would shield what it wraps.
		{"unresolvable argument inside a wrapper", `sudo $(x) npm publish`, []string{"sudo", "npm", "x"}},
		{"unresolvable argument", `echo $UNSET hi`, []string{"echo"}},

		// `x` is the program the payload runs, and it is reported now that a
		// literal payload is re-parsed. Whether a program called `x` exists is
		// not a question a static reader answers — the same reason `nice 10`
		// reports `10`.
		{"deep nesting", `sudo sh -c 'x' && (time npm publish | tee log)`, []string{"sudo", "sh", "x", "npm", "tee"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := bins(t, tc.src); !equal(got, tc.want) {
				t.Errorf("ExtractCommand(%q) bins = %v, want %v", tc.src, got, tc.want)
			}
		})
	}
}

// TestExtractCommand_ProcSubstDoesNotPanic is the gotcha that matters most.
//
// In v3.13.1 expansion nil-dereferences on a process substitution unless a
// handler is supplied. A panic exits the guardrail non-zero, which a harness
// reads as a refusal — so the bug would not weaken a rule, it would block the
// agent's work for a reason nobody can act on.
func TestExtractCommand_ProcSubstDoesNotPanic(t *testing.T) {
	for _, src := range []string{
		`diff <(ls a)`,
		`diff <(ls a) <(ls b)`,
		`tee >(cat)`,
		`while read -r l; do echo "$l"; done < <(ls)`,
	} {
		t.Run(src, func(t *testing.T) {
			got := bins(t, src) // panics before returning if the handler is gone
			if len(got) == 0 {
				t.Errorf("ExtractCommand(%q) found nothing; expected the surrounding command at least", src)
			}
		})
	}

	// The program inside the substitution is reported too, not just the one
	// receiving its output.
	if got := bins(t, `diff <(ls a)`); !equal(got, []string{"diff", "ls"}) {
		t.Errorf("diff <(ls a) bins = %v, want [diff ls]", got)
	}
}

// TestExtractCommand_SurvivesAPanickingExpansion exercises the recover.
//
// The ProcSubst handler means no command line in the corpus can panic, so the
// recover is unreachable from the outside and a test that only feeds it text
// cannot prove it works — removing the recover leaves such a suite green. This
// injects a panic from inside expansion instead, which is what the recover
// actually guards: a bug in the parser, of the kind v3.13.1 shipped.
//
// What it must prove is that a panic becomes an empty result rather than a
// crash. A panic escaping here exits the guardrail non-zero, and a harness
// reads that as a refusal — the agent is blocked for a reason no rule
// declared.
func TestExtractCommand_SurvivesAPanickingExpansion(t *testing.T) {
	orig := newConfig
	newConfig = func() *expand.Config {
		cfg := orig()
		cfg.ProcSubst = func(*syntax.ProcSubst) (string, error) {
			panic("simulated parser bug during expansion")
		}
		return cfg
	}
	t.Cleanup(func() { newConfig = orig })

	// Reaches the panicking handler. Without the recover this crashes the
	// test binary rather than failing it.
	ev := ExtractCommand(`diff <(ls a)`)

	if ev.Raw != `diff <(ls a)` {
		t.Errorf("raw = %q, want the command line preserved", ev.Raw)
	}
	// Whatever was collected before the panic is kept — `ls`, from the
	// substitution's own statements, is walked before `diff` expands.
	for _, inv := range ev.Invocations {
		if inv.Bin == "" {
			t.Errorf("empty bin survived a panic: %+v", ev.Invocations)
		}
	}
}

// TestExtractCommand_ResolvesNothingUnsafe proves the extraction is inert.
//
// Not a test of output shape but of what did not happen: no command ran and no
// directory was read. A guardrail that executes what it inspects is worse than
// no guardrail.
func TestExtractCommand_ResolvesNothingUnsafe(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "MARKER")

	ExtractCommand(`echo $(touch ` + marker + `)`)
	ExtractCommand("echo `touch " + marker + "`")
	ExtractCommand(`$(touch ` + marker + `)`)

	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("command substitution executed: %s exists", marker)
	}

	// The substitution is still reported, so nothing was traded for the
	// safety.
	if got := bins(t, `echo $(touch /nope/MARKER)`); !equal(got, []string{"echo", "touch"}) {
		t.Errorf("bins = %v, want [echo touch]", got)
	}

	// Globs stay literal: no ReadDir handler, so the filesystem is never
	// consulted to decide what a command means. A real file in the temp dir
	// must not appear in the argument vector.
	if err := os.WriteFile(filepath.Join(dir, "real.go"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	invs := ExtractCommand(`rm ` + dir + `/*.go`).Invocations
	if len(invs) != 1 {
		t.Fatalf("got %d invocations, want 1", len(invs))
	}
	if !strings.HasSuffix(invs[0].Argv[1], "*.go") {
		t.Errorf("glob was expanded against the filesystem: argv = %v", invs[0].Argv)
	}
}

// TestExtractCommand_Undecidable pins the honest floor.
//
// These forms cannot be resolved without running them. What matters is that
// each is survivable and that nothing is invented — a fabricated program name
// is worse than a missing one, because a rule written against it would fire on
// a command that never runs it.
func TestExtractCommand_Undecidable(t *testing.T) {
	for _, tc := range []struct {
		name     string
		src      string
		wantSeen []string // what is genuinely visible
		notSeen  []string // what must not be fabricated
	}{
		{
			// The program is a variable. Nothing is emitted — `publish` is an
			// argument, not a program, and promoting it would name a binary
			// that does not exist.
			name: "program named by a variable", src: `$NPM publish`,
			notSeen: []string{"npm", "NPM", "$NPM", "publish", ""},
		},
		{
			// The sharpest form: promoting the survivor would report a flag
			// as a binary, and a rule bound to `--force` would fire on a
			// command that runs no such thing.
			name: "variable program before a flag", src: `$CMD --force deploy`,
			notSeen: []string{"--force", "force", "deploy", "CMD", ""},
		},
		{
			name: "braced variable program", src: `${BIN} install`,
			notSeen: []string{"install", "BIN", ""},
		},
		{
			// The substitution cannot run, so the program is unknown. `which`
			// is genuinely visible inside it and is reported; `publish` is an
			// argument and is not promoted.
			name: "substituted program", src: `$(which npm) publish`,
			wantSeen: []string{"which"}, notSeen: []string{"publish", "npm", ""},
		},
		{
			// Expands to the empty string. An empty bin must never reach a
			// matcher — no rule can mean a program with no name.
			name: "program expands to empty", src: `"$EDITOR" file.txt`,
			notSeen: []string{"", "file.txt", "EDITOR"},
		},
		{
			// A parameter embedded inside the program word. This one resolves
			// to a real name — `npm` — from source word 0, so neither the
			// vanished-word check nor the unresolvable check rejects it. It is
			// still a guess: the empty environment assumed ${X} was empty, and
			// at runtime it could be anything, making the program `npXm` or
			// something else entirely. Only requiring the word to be literal
			// catches it.
			name: "parameter inside the program word", src: `np${X}m publish`,
			notSeen: []string{"npm", "publish", ""},
		},
		{
			name: "parameter inside a known program", src: `ec${NOPE}ho hi`,
			notSeen: []string{"echo", "hi", ""},
		},
		{
			// A literal empty program word. Certain, and certainly not a
			// program — the empty bin has to be filtered or it reaches a
			// matcher as a name no rule can mean.
			name: "empty literal program", src: `"" npm publish`,
			notSeen: []string{"", "npm", "publish"},
		},
		{
			name: "empty single-quoted program", src: `'' npm publish`,
			notSeen: []string{"", "npm", "publish"},
		},
		{
			// eval's argument is a string it will interpret at runtime.
			// Parsing it again has no bottom, so it is left as an argument.
			name: "eval", src: `eval "npm publish"`,
			wantSeen: []string{"eval"}, notSeen: []string{"npm"},
		},
		{
			// An interpreter payload that is NOT literal stays opaque. The
			// value is a runtime parameter, so re-parsing it would be a guess
			// about the environment rather than a reading of the text.
			//
			// This case used to be `bash -c "npm publish"`, filed here as
			// undecidable. It was not: a literal payload is right there in the
			// text and is now unwrapped — see
			// TestNesting_LiteralInterpreterPayloadsAreUnwrapped. What belongs
			// in this list is the form that genuinely cannot be known, which is
			// this one.
			name: "nested interpreter from a parameter", src: `bash -c "$CMD"`,
			wantSeen: []string{"bash"}, notSeen: []string{"npm"},
		},
		{
			// The payload resolves to `npm publish` under the empty
			// environment, and that resolution is an ASSUMPTION — at runtime
			// ${X} could be anything. Reported as bash alone rather than as a
			// program inferred from a variable nobody has read.
			name: "interpreter payload with an interpolation", src: `bash -c "np${X}m publish"`,
			wantSeen: []string{"bash"}, notSeen: []string{"npm"},
		},
		{
			// The decoded payload does not exist until base64 runs.
			name: "base64 piped to sh", src: `echo cm0gLXJmIC8= | base64 -d | sh`,
			wantSeen: []string{"echo", "base64", "sh"}, notSeen: []string{"rm"},
		},
		{
			// Splitting depends on the runtime IFS.
			name: "runtime word splitting", src: `IFS=: ; cmd $ARGS`,
			wantSeen: []string{}, notSeen: []string{"rm"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := bins(t, tc.src) // must not crash
			for _, want := range tc.wantSeen {
				if !contains(got, want) {
					t.Errorf("bins = %v, missing visible program %q", got, want)
				}
			}
			for _, bad := range tc.notSeen {
				if contains(got, bad) {
					t.Errorf("bins = %v, fabricated %q — it cannot be known without running the command", got, bad)
				}
			}
		})
	}
}

// TestExtractCommand_Malformed: a line that will not parse still yields an
// event, because the raw text is what a rule about unreadable commands has to
// match on. It must not panic and must not guess.
func TestExtractCommand_Malformed(t *testing.T) {
	for _, src := range []string{
		`npm publish ; ; ;`,
		`(((`,
		`"unterminated`,
		`if then fi`,
		``,
		"\x00\xff",
	} {
		t.Run(src, func(t *testing.T) {
			ev := ExtractCommand(src)
			if ev.Raw != src {
				t.Errorf("raw = %q, want %q", ev.Raw, src)
			}
		})
	}
}

// TestExtractCommand_CarriesRaw: the unresolved line rides along so a refusal
// can quote what the agent actually wrote, rather than the resolution.
func TestExtractCommand_CarriesRaw(t *testing.T) {
	const src = `n\pm publish`
	ev := ExtractCommand(src)
	if ev.Raw != src {
		t.Errorf("raw = %q, want %q", ev.Raw, src)
	}
	if len(ev.Invocations) != 1 || ev.Invocations[0].Bin != "npm" {
		t.Fatalf("invocations = %+v, want one npm", ev.Invocations)
	}
	// Resolved in argv, escaped in raw — both, so a rule matches the first and
	// a message quotes the second.
	if got := ev.Invocations[0].Argv[0]; got != "npm" {
		t.Errorf("argv[0] = %q, want resolved %q", got, "npm")
	}
}

func TestInvocation_BinIsBasename(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{`/usr/local/bin/npm publish`, "npm"},
		{`./scripts/deploy.sh`, "deploy.sh"},
		{`../npm publish`, "npm"},
		{`npm publish`, "npm"},
	} {
		if got := bins(t, tc.src); len(got) == 0 || got[0] != tc.want {
			t.Errorf("ExtractCommand(%q) bin = %v, want %q", tc.src, got, tc.want)
		}
	}
}

func TestInvocation_Flags(t *testing.T) {
	invs := ExtractCommand(`npm publish --tag=next --dry-run -f`).Invocations
	if len(invs) != 1 {
		t.Fatalf("got %d invocations, want 1", len(invs))
	}
	want := map[string][]string{"tag": {"next"}, "dry-run": {""}, "f": {""}}
	got := invs[0].Flags
	if len(got) != len(want) {
		t.Fatalf("flags = %v, want %v", got, want)
	}
	for k, v := range want {
		// A flag without a value carries the empty string, so a rule can ask
		// whether it is present without knowing whether it takes one.
		if g, ok := got[k]; !ok || !equal(g, v) {
			t.Errorf("flags[%q] = %q (present=%v), want %q", k, g, ok, v)
		}
	}
}

// TestExtract_Payload covers the module boundary: what a harness sends becomes
// one event carrying every invocation, and anything that is not a command line
// yields nothing.
func TestExtract_Payload(t *testing.T) {
	m := New()

	t.Run("one event carrying every invocation", func(t *testing.T) {
		evs, err := m.Extract(module.Input{
			module.InputPhase:   module.PhasePre,
			module.InputPayload: pending{tool: "Bash", args: `{"command":"sudo npm publish | tee log"}`},
		})
		if err != nil {
			t.Fatal(err)
		}
		// One event, not one per invocation: a rule asks whether the line runs
		// something, and splitting would make it ask that many times.
		if len(evs) != 1 {
			t.Fatalf("got %d events, want 1", len(evs))
		}
		if evs[0].Kind != KindPreInvoke {
			t.Errorf("kind = %q, want %q", evs[0].Kind, KindPreInvoke)
		}
		list, ok := evs[0].Fields[FieldInvocations].([]any)
		if !ok || len(list) != 3 {
			t.Fatalf("invocations = %v, want 3", evs[0].Fields[FieldInvocations])
		}
	})

	t.Run("no command yields nothing", func(t *testing.T) {
		for _, args := range []string{`{"file_path":"a.txt"}`, `{}`, `not json`, `{"command":""}`} {
			evs, err := m.Extract(module.Input{
				module.InputPhase:   module.PhasePre,
				module.InputPayload: pending{tool: "Write", args: args},
			})
			if err != nil || len(evs) != 0 {
				t.Errorf("args %s: got %d events, err %v; want none", args, len(evs), err)
			}
		}
	})

	t.Run("post phase yields nothing", func(t *testing.T) {
		evs, err := m.Extract(module.Input{
			module.InputPhase:   module.PhasePost,
			module.InputPayload: pending{tool: "Bash", args: `{"command":"npm publish"}`},
		})
		if err != nil || len(evs) != 0 {
			t.Errorf("got %d events, err %v; want none", len(evs), err)
		}
	})

	t.Run("payload that is not a pending action", func(t *testing.T) {
		evs, err := m.Extract(module.Input{module.InputPhase: module.PhasePre, module.InputPayload: "nonsense"})
		if err != nil || len(evs) != 0 {
			t.Errorf("got %d events, err %v; want none", len(evs), err)
		}
	})
}

type pending struct {
	tool string
	args string
}

func (p pending) Tool() string               { return p.tool }
func (p pending) Arguments() json.RawMessage { return json.RawMessage(p.args) }

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}
