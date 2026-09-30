package repo

import (
	"bufio"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/syntax"
)

// A guard script that sources a helper proves the helper loaded WHOLE. bash
// runs a sourced file up to its first syntax error (whether the `.` then fails
// depends on the bash version), so a helper can load partly: some functions
// defined, a later one missing. Checking one function is not enough — the
// missing one may be what decides. So every helper ends with a sentinel on its
// last line (`<name>_loaded=1`), and every caller unsets it, sources, and checks
// it; a script that reads on after a partial load can decide on nothing, and
// for a `when`, exit 1 waives the requirement.

// sourcedCall is one `.` or `source` a script runs: its argument as written in
// the source, and the line it is on.
type sourcedCall struct {
	arg  string
	line int
}

// sourcedCalls parses a script with a real shell parser (mvdan.cc/sh, the one
// commandmod uses) and returns every `.`/`source` it runs — at any depth: in a
// command substitution ($(…) or backticks), a subshell, a case arm, an
// if/while, either side of && and ||, behind `command`/`builtin`/`exec` (with
// their flags) or `time`. The argument is the next word, printed from its
// source span, so a resolver sees exactly what the script says.
//
// A script that does not parse is an ERROR, never a partial answer: a lexer
// that loses track of a quote or heredoc would silently hide every source after
// it, and a parse error names the file and position instead.
//
// Out of scope: a source built at run time — `eval ". $lib"`, a `.` inside a
// string handed to `bash -c` — is data to the parser, not a command, and is not
// found. Guard scripts source helpers directly.
func sourcedCalls(src []byte, name string) ([]sourcedCall, error) {
	lang := syntax.LangBash
	if posixShebang(src) {
		lang = syntax.LangPOSIX
	}
	file, err := syntax.NewParser(syntax.Variant(lang)).Parse(bytes.NewReader(src), name)
	if err != nil {
		return nil, err
	}
	var out []sourcedCall
	syntax.Walk(file, func(n syntax.Node) bool {
		call, ok := n.(*syntax.CallExpr)
		if !ok {
			return true
		}
		args := call.Args
		for len(args) > 0 {
			switch args[0].Lit() {
			case "command", "builtin", "exec":
				args = args[1:]
				for len(args) > 0 && strings.HasPrefix(args[0].Lit(), "-") {
					args = args[1:]
				}
				continue
			}
			break
		}
		if len(args) >= 2 && (args[0].Lit() == "." || args[0].Lit() == "source") {
			w := args[1]
			out = append(out, sourcedCall{
				arg:  string(src[w.Pos().Offset():w.End().Offset()]),
				line: int(w.Pos().Line()),
			})
		}
		return true
	})
	return out, nil
}

// posixShebang reports whether a script's shebang names sh rather than bash.
func posixShebang(src []byte) bool {
	first, _, _ := strings.Cut(string(src), "\n")
	if !strings.HasPrefix(first, "#!") || strings.Contains(first, "bash") {
		return false
	}
	fields := strings.Fields(strings.TrimPrefix(first, "#!"))
	if len(fields) == 0 {
		return false
	}
	last := fields[len(fields)-1]
	if filepath.Base(fields[0]) == "env" && len(fields) > 1 {
		last = fields[1]
	}
	return filepath.Base(last) == "sh" || filepath.Base(fields[0]) == "sh"
}

// sourcedArgs is the arguments sourcedCalls finds in a script.
func sourcedArgs(script string) ([]string, error) {
	calls, err := sourcedCalls([]byte(script), "script")
	if err != nil {
		return nil, err
	}
	var args []string
	for _, c := range calls {
		args = append(args, c.arg)
	}
	return args, nil
}

// sentinelLine is a helper's last line: its loaded sentinel.
var sentinelLine = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*_loaded)=1$`)

// Every shipped guard script that sources a helper unsets the helper's own
// sentinel before, and checks it after; and every helper so sourced ends with
// its sentinel.
func TestSourcedHelpersAreCheckedLoaded(t *testing.T) {
	root := repoRoot(t)
	checked := 0
	for _, f := range trackedBashScripts(t, root) {
		if !strings.Contains(f, "/.sloprail/") {
			continue // eval scorers and tools source a shared harness, not a guard helper
		}
		lines := readLines(t, filepath.Join(root, f))
		calls, err := sourcedCalls(mustRead(t, filepath.Join(root, f)), f)
		if err != nil {
			t.Errorf("%s does not parse, so its sources cannot be checked: %v", f, err)
			continue
		}
		for _, c := range calls {
			i, arg := c.line-1, c.arg
			{
				checked++
				helper := resolveHelper(filepath.Dir(filepath.Join(root, f)), arg, lines[:i])
				if helper == "" {
					t.Errorf("%s:%d sources %s, which this check cannot resolve to a file — name it so it can", f, i+1, arg)
					continue
				}
				sentinel := lastLineSentinel(t, helper)
				if sentinel == "" {
					t.Errorf("%s (sourced by %s:%d) does not end with a loaded sentinel (`<name>_loaded=1` as its last line)", rel(root, helper), f, i+1)
					continue
				}
				before := strings.Join(lines[max(0, i-6):i], "\n")
				after := strings.Join(lines[i+1:min(len(lines), i+5)], "\n")
				if !strings.Contains(before, "unset "+sentinel) || !strings.Contains(after, "${"+sentinel) {
					t.Errorf("%s:%d sources %s without `unset %s` before and a check of it after — a partly-loaded helper would decide", f, i+1, rel(root, helper), sentinel)
				}
			}
		}
	}
	if checked < 11 {
		t.Fatalf("found only %d helper-sourcing lines, want at least the 11 guard scripts ship — the scan is wrong", checked)
	}
}

// A partial load of rules-lib.sh — collect_applicable_rules defined, a syntax
// error inside rule_applies — used to leave the check that looked for
// collect_applicable_rules satisfied, so no rule applied and the judge ran with
// none. The prepare refuses it, naming the helper.
func TestPrepareRefusesAPartlyLoadedRulesLib(t *testing.T) {
	root := repoRoot(t)
	guard := filepath.Join(root, "marketplace/plugins/sloprail-content/.sloprail/file-guard/unit-satisfies-rules")
	dir := t.TempDir()
	copyFile(t, filepath.Join(guard, "prepare-judge-rules.sh"), filepath.Join(dir, "prepare-judge-rules.sh"))
	lib := string(mustRead(t, filepath.Join(guard, "rules-lib.sh")))
	broken := strings.Replace(lib, "rule_applies() {\n", "rule_applies() {\n  x=\"${y:-it's}\"\n", 1)
	if broken == lib {
		t.Fatal("rules-lib.sh has no `rule_applies() {` line to break")
	}
	if err := os.WriteFile(filepath.Join(dir, "rules-lib.sh"), []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out := runScript(t, dir, "prepare-judge-rules.sh", nil,
		`{"event":{"kind":"PostFileUpdate","path":"memories/topics/t/units/01/UNIT.md","newContent":"---\ntags: [x]\n---\nbody\n"}}`)
	if code == 0 || !strings.Contains(out, "rules-lib.sh") {
		t.Errorf("a partly-loaded rules-lib.sh was not refused by name (exit %d):\n%s", code, out)
	}
}

// The two `when` scripts, where exit 1 waives the requirement, APPLY it (exit
// 0) when their helper loads only partly — a helper whose reader is defined and
// would answer "waive", stopped before its sentinel. Two ways to stop:
//
//   - a syntax error in a later function (on some bash versions the `.` then
//     fails too, which a caller's `|| exit 0` also catches);
//   - a sourced file that returns early (`return 0`): the `.` SUCCEEDS on every
//     bash version, so only the sentinel check stands between a partial reader
//     and a waiver — this variant is what proves the check.
//
// Each has a control run with the REAL helper on the same payload, which waives
// (exit 1), so the test cannot pass for a reason other than the sentinel.
func TestWhenScriptsApplyWhenTheirHelperLoadsPartly(t *testing.T) {
	root := repoRoot(t)
	// A stub sr-file for the publish reader's control run: the unit reads as
	// drafting.
	stubBin := t.TempDir()
	if err := os.WriteFile(filepath.Join(stubBin, "sr-file"), []byte("#!/bin/sh\n[ \"$1\" = field ] && { echo drafting; exit 0; }\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	const syntaxError = "later() { x=\"${y:-it's}\"; }\n"
	const returnsEarly = "return 0\nlater() { :; }\n"
	for _, tc := range []struct{ guard, script, helper, reader, sentinel, payload string }{
		{
			"marketplace/plugins/sloprail-tasks/.sloprail/file-guard/task-body-is-human-authored",
			"body-changed.sh", "lib-body.sh",
			"task_body() { printf 'same body\\n'; }\n", "lib_body_loaded=1\n",
			`{"event":{"kind":"PostFileUpdate","path":"memories/tasks/a/b/TASK.md","newContentKnown":true,"oldContent":"---\nstatus: open\n---\nsame body\n","newContent":"---\nstatus: open\n---\nsame body\n"}}`,
		},
		{
			"marketplace/plugins/sloprail-content/.sloprail/file-guard/unit-publish-approved",
			"enters-published.sh", "publish-claim.sh",
			"publish_claim_norm() { printf '%s' \"$1\"; }\npublish_claim() { claim=no claim_status=drafting claim_why=; }\n", "publish_claim_loaded=1\n",
			`{"event":{"kind":"PostFileUpdate","path":"memories/topics/t/units/01/UNIT.md","newContentKnown":true,"oldContent":"---\nstatus: drafting\n---\n","newContent":"---\nstatus: drafting\n---\nedited\n"}}`,
		},
	} {
		t.Run(tc.script, func(t *testing.T) {
			env := []string{"PATH=" + stubBin + string(os.PathListSeparator) + os.Getenv("PATH")}

			control := t.TempDir()
			lib := strings.TrimSuffix(tc.script, ".sh") + "-lib.sh"
			copyFile(t, filepath.Join(root, tc.guard, tc.script), filepath.Join(control, tc.script))
			copyFile(t, filepath.Join(root, tc.guard, lib), filepath.Join(control, lib))
			copyFile(t, filepath.Join(root, tc.guard, tc.helper), filepath.Join(control, tc.helper))
			if code, out := runScript(t, control, tc.script, env, tc.payload); code != 1 {
				t.Fatalf("control: with the real %s this payload should waive (exit 1), got %d — the test's premise is wrong:\n%s", tc.helper, code, out)
			}

			for stop, stopper := range map[string]string{"syntax error": syntaxError, "returns early": returnsEarly} {
				partial := t.TempDir()
				copyFile(t, filepath.Join(root, tc.guard, tc.script), filepath.Join(partial, tc.script))
				copyFile(t, filepath.Join(root, tc.guard, lib), filepath.Join(partial, lib))
				helper := tc.reader + stopper + tc.sentinel
				if err := os.WriteFile(filepath.Join(partial, tc.helper), []byte(helper), 0o644); err != nil {
					t.Fatal(err)
				}
				if code, out := runScript(t, partial, tc.script, env, tc.payload); code != 0 {
					t.Errorf("with %s stopped before its sentinel (%s), %s exited %d (1 waives the requirement), want 0 (apply):\n%s", tc.helper, stop, tc.script, code, out)
				}
			}
		})
	}
}

// resolveHelper turns a sourced path into a file: the literal path, or the
// nearest preceding `var=` assignment for a `"$var"`, with the guard-folder
// variables (SR_GUARDRAIL_DIR, gdir) taken as the script's own folder.
func resolveHelper(dir, arg string, preceding []string) string {
	arg = strings.Trim(arg, `"'`)
	if v := regexp.MustCompile(`^\$\{?([A-Za-z_][A-Za-z0-9_]*)\}?$`).FindStringSubmatch(arg); v != nil {
		assign := regexp.MustCompile(`^\s*` + v[1] + `=(.+)$`)
		arg = ""
		for i := len(preceding) - 1; i >= 0; i-- {
			if m := assign.FindStringSubmatch(preceding[i]); m != nil {
				arg = strings.Trim(m[1], `"'`)
				break
			}
		}
		if arg == "" {
			return ""
		}
	}
	// A gate's entry sources its library from the file-guard folder of the same rule
	// (`$lib_dir`); a library sources its own siblings the same way.
	libDirs := []string{dir, strings.Replace(dir, string(filepath.Separator)+"gate"+string(filepath.Separator), string(filepath.Separator)+"file-guard"+string(filepath.Separator), 1)}
	if strings.HasPrefix(arg, "$lib_dir/") {
		for _, d := range libDirs {
			p := filepath.Join(d, strings.TrimPrefix(arg, "$lib_dir/"))
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	for _, guardDir := range []string{"${SR_GUARDRAIL_DIR:-.}", "$SR_GUARDRAIL_DIR", "${gdir}", "$gdir",
		`$(dirname "$0")`, `$(dirname "${BASH_SOURCE[0]}")`, "$(dirname $0)", "."} {
		if strings.HasPrefix(arg, guardDir+"/") {
			p := filepath.Join(dir, strings.TrimPrefix(arg, guardDir+"/"))
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	if !strings.Contains(arg, "$") {
		p := filepath.Join(dir, arg)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// lastLineSentinel is the sentinel variable a helper's last non-blank line
// sets, or "".
func lastLineSentinel(t *testing.T, path string) string {
	lines := readLines(t, path)
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			if m := sentinelLine.FindStringSubmatch(l); m != nil {
				return m[1]
			}
			return ""
		}
	}
	return ""
}

// runScript runs a guard script with bash in dir (as SR_GUARDRAIL_DIR), the
// payload on stdin, and returns its exit status and output.
func runScript(t *testing.T, dir, script string, env []string, payload string) (int, string) {
	t.Helper()
	cmd := exec.Command("bash", filepath.Join(dir, script))
	cmd.Dir = dir
	cmd.Env = append(append(os.Environ(), "SR_GUARDRAIL_DIR="+dir), env...)
	cmd.Stdin = strings.NewReader(payload)
	out, err := cmd.CombinedOutput()
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode(), string(out)
	}
	if err != nil {
		t.Fatalf("run %s: %v", script, err)
	}
	return 0, string(out)
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	if err := os.WriteFile(to, mustRead(t, from), 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func rel(root, path string) string {
	if r, err := filepath.Rel(root, path); err == nil {
		return r
	}
	return path
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines
}

// The source finder finds every way a script sources a file — whatever the
// argument's quoting, wherever the command sits — and never the `.` of a jq
// filter inside a string or a heredoc. It is a real parser, so nothing that
// confuses a hand-written lexer (a heredoc with a quote in it, a delimiter with
// a dash, `$(( 1 << n ))`, CRLF) can hide a later source.
func TestSourcedCallsFindEachForm(t *testing.T) {
	// Multi-line strings and heredocs: only the real source is found.
	script := "jq -r '\n  . as $all\n  | if . then 1 else . end'\n" +
		"echo \"a sentence.\n. Permitting.\"\n" +
		"cat <<'EOF'\n. \"$not_a_source\"\nEOF\n" +
		"x=$'it\\'s'\n" +
		". \"$lib\"\n"
	if got, err := sourcedCalls([]byte(script), "multi"); err != nil || len(got) != 1 || got[0].arg != `"$lib"` || got[0].line != 10 {
		t.Errorf("multi-line script: got %+v (err %v), want the one source on line 10", got, err)
	}

	// Scripts a hand-written lexer lost its place in: each ends with a real
	// source that must still be found.
	for name, prefix := range map[string]string{
		"heredoc with a backslashed delimiter and a quote": "cat <<\\EOF\nit's here\nEOF\n",
		"heredoc delimiter with a dash":                    "cat <<END-MARK\nit's\nEND-MARK\n",
		"quoted heredoc delimiter with a dot":              "cat <<'END.X'\nit's\nEND.X\n",
		"arithmetic shift, not a heredoc":                  "x=$(( 1 << n ))\n(( x << n ))\ny='a'\n",
		"heredoc inside a command substitution":            "x=\"$(cat <<EOF\nsay \"hi\"\nEOF\n)\"\n",
		"CRLF line endings":                                "a=1\r\nb='x'\r\n",
	} {
		got, err := sourcedArgs(prefix + ". \"$lib\"\n")
		if err != nil || len(got) != 1 || got[0] != `"$lib"` {
			t.Errorf("%s: got %q (err %v), want the trailing source", name, got, err)
		}
	}

	for line, want := range map[string]string{
		`. "$(dirname "$0")/lib.sh"`:                `"$(dirname "$0")/lib.sh"`,
		`. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"`: `"$(dirname "${BASH_SOURCE[0]}")/lib.sh"`,
		`. "$lib" arg1`:                             `"$lib"`,
		`. lib.bash`:                                `lib.bash`,
		`. helpers`:                                 `helpers`,
		`source helpers.inc`:                        `helpers.inc`,
		`. "$lib" 2>/dev/null || exit 0`:            `"$lib"`,
		`. ./lib.sh`:                                `./lib.sh`,
		`. <(cat lib)`:                              `<(cat lib)`,
		`. $lib`:                                    `$lib`,
		`[ -f "$lib" ] && . "$lib"`:                 `"$lib"`,
		`if . "$lib"; then :; fi`:                   `"$lib"`,
		`{ . "$lib"; }`:                             `"$lib"`,
		`  . "${SR_GUARDRAIL_DIR:-.}/pin.sh" || fail "x"`: `"${SR_GUARDRAIL_DIR:-.}/pin.sh"`,
		"case $x in a) . \"$lib\" ;; esac":                `"$lib"`,
		"x=`. \"$lib\"`":                                  `"$lib"`,
		`command . "$lib"`:                                `"$lib"`,
		`builtin source "$lib"`:                           `"$lib"`,
		`command -p . "$lib"`:                             `"$lib"`,
		`time . "$lib"`:                                   `"$lib"`,
		`x=$(. "$lib")`:                                   `"$lib"`,
		`(. "$lib")`:                                      `"$lib"`,
		`while . "$lib"; do :; done`:                      `"$lib"`,
	} {
		got, err := sourcedArgs(line)
		if err != nil || len(got) != 1 || got[0] != want {
			t.Errorf("%s: sourced %q (err %v), want exactly [%s]", line, got, err, want)
		}
	}
	for _, line := range []string{
		`jq '. as $all' f`, `jq -r '  . as $x | .foo' f`, `jq 'select(. != "")' f`, `jq '  . | length' f`,
		`jq 'map(. + 1)' f`, `echo "a. b"`, `# . "$lib" in a comment`, `x=1. y`, `jq -r '.foo'`,
		`jq '(.a // .) as $v' f`, `eval ". $lib"`, `bash -c '. "$lib"'`,
	} {
		got, err := sourcedArgs(line)
		if err != nil || len(got) != 0 {
			t.Errorf("%s: read as a source of %q (err %v)", line, got, err)
		}
	}

	// A script that does not parse fails loudly, naming where.
	if _, err := sourcedCalls([]byte("x='unclosed\n. \"$lib\"\n"), "broken.sh"); err == nil || !strings.Contains(err.Error(), "broken.sh") {
		t.Errorf("a script with an unclosed quote parsed without an error naming it: %v", err)
	}
}
