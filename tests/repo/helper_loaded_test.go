package repo

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A guard script that sources a helper proves the helper loaded WHOLE. bash
// runs a sourced file up to its first syntax error (whether the `.` then fails
// depends on the bash version), so a helper can load partly: some functions
// defined, a later one missing. Checking one function is not enough — the
// missing one may be what decides. So every helper ends with a sentinel on its
// last line (`<name>_loaded=1`), and every caller unsets it, sources, and checks
// it; a script that reads on after a partial load can decide on nothing, and
// for a `when`, exit 1 waives the requirement.

// sourcedPaths returns the argument of every `.` or `source` command on one
// line (see sourcedPathsIn).
func sourcedPaths(line string) []string { return sourcedPathsIn([]string{line})[0] }

// sourcedPathsIn returns, per line of a script, the argument of every `.` or
// `source` command on it: at the start of the line or of any command on it
// (after ; && || { ( ! and the if/then/do/else/elif keywords), its argument read
// as one shell word — quotes, $(…) and <(…) nesting included.
//
// Only CODE counts. Quoting is tracked across lines — '…', "…", $'…' — along
// with comments and heredoc bodies, so the `.` of a jq program in a multi-line
// '…' string (`if . then`, `. as $x`, `. end`) or a sentence in a "…" message is
// never read as a source. A `.` whose next token is a jq operator is not one
// either, for a filter on a single line.
func sourcedPathsIn(lines []string) [][]string {
	out := make([][]string, len(lines))
	var quote byte // '\'', '"', or 'E' for $'…'
	heredoc := ""
	for n, line := range lines {
		if heredoc != "" {
			if strings.TrimLeft(line, "\t") == heredoc {
				heredoc = ""
			}
			continue
		}
		code := make([]bool, len(line))
		pending := ""
	scan:
		for i := 0; i < len(line); i++ {
			c := line[i]
			switch quote {
			case '\'':
				if c == '\'' {
					quote = 0
				}
				continue
			case 'E', '"':
				if c == '\\' {
					i++
					continue
				}
				if (quote == 'E' && c == '\'') || (quote == '"' && c == '"') {
					quote = 0
				}
				continue
			}
			switch {
			case c == '\\':
				i++
				continue
			case c == '\'' && i > 0 && line[i-1] == '$':
				quote = 'E'
			case c == '\'':
				quote = '\''
			case c == '"':
				quote = '"'
			case c == '#' && (i == 0 || strings.IndexByte(" \t;|&(", line[i-1]) >= 0):
				break scan
			case c == '<' && strings.HasPrefix(line[i:], "<<") && !strings.HasPrefix(line[i:], "<<<"):
				if m := heredocStart.FindStringSubmatch(line[i:]); m != nil {
					pending = m[1]
				}
			}
			code[i] = true
		}
		if pending != "" {
			heredoc = pending
		}
		out[n] = sourcedPathsAt(line, code)
	}
	return out
}

// heredocStart is a heredoc's opening: << or <<-, then its delimiter, quoted
// or not.
var heredocStart = regexp.MustCompile(`^<<-?\s*['"]?([A-Za-z_][A-Za-z0-9_]*)['"]?`)

// sourcedPathsAt returns the sourced arguments on a line, considering only the
// positions code marks as code.
func sourcedPathsAt(line string, code []bool) []string {
	var out []string
	for i := 0; i < len(line); i++ {
		if !code[i] {
			continue
		}
		var cmdLen int
		switch {
		case strings.HasPrefix(line[i:], "source") && (i+6 == len(line) || line[i+6] == ' ' || line[i+6] == '\t'):
			cmdLen = 6
		case line[i] == '.' && i+1 < len(line) && (line[i+1] == ' ' || line[i+1] == '\t'):
			cmdLen = 1
		default:
			continue
		}
		if !atCommandStart(line[:i]) {
			continue
		}
		rest := strings.TrimLeft(line[i+cmdLen:], " \t")
		word := shellWord(rest)
		if word == "" || jqOperand(word) {
			continue
		}
		out = append(out, word)
	}
	return out
}

// commandStart ends the text before a command: a list operator, a grouping,
// or a keyword that takes a command after it.
var commandStart = regexp.MustCompile(`(^|[;&|{(!]|\b(if|then|do|else|elif|while|until))\s*$`)

func atCommandStart(before string) bool { return commandStart.MatchString(before) }

// jqOperand reports whether what follows a `.` is a jq operator — the `.` is a
// filter, not a source.
func jqOperand(word string) bool {
	switch word {
	case "as", "|", "[", "==", "!=", ")", "]", ",", "+", "-", "*", "/", "//", "and", "or", "<", ">", "<=", ">=", "?", "}":
		return true
	}
	return strings.HasPrefix(word, "|") || strings.HasPrefix(word, ")") || strings.HasPrefix(word, "]")
}

// shellWord reads the first shell word of s: up to unquoted whitespace, a list
// operator or a redirection, with "…", '…', $(…) and <(…) kept whole.
func shellWord(s string) string {
	var b strings.Builder
	depth := 0 // $( or <( nesting
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote == '\'':
			if c == '\'' {
				quote = 0
			}
		case quote == '"' && depth == 0 && c == '"':
			quote = 0
		case c == '\\' && i+1 < len(s):
			b.WriteByte(c)
			i++
			c = s[i]
		case (c == '$' || c == '<') && i+1 < len(s) && s[i+1] == '(':
			depth++
			b.WriteByte(c)
			i++
			c = '('
		case c == ')' && depth > 0:
			depth--
		case c == '"' && quote == 0:
			quote = '"'
		case c == '"' && depth > 0:
			// a quote inside $(…) within "…": its own pair, kept verbatim
		case c == '\'' && quote == 0 && depth == 0:
			quote = '\''
		case quote == 0 && depth == 0 && strings.IndexByte(" \t;&|<>", c) >= 0:
			return b.String()
		}
		b.WriteByte(c)
	}
	return b.String()
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
		sourced := sourcedPathsIn(lines)
		for i := range lines {
			for _, arg := range sourced[i] {
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
	if checked < 8 {
		t.Fatalf("found only %d helper-sourcing lines — the scan is wrong", checked)
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
			`{"event":{"kind":"PreFileUpdate","path":"memories/tasks/a/b/TASK.md","resultKnown":true,"oldContent":"---\nstatus: open\n---\nsame body\n","newContent":"---\nstatus: open\n---\nsame body\n"}}`,
		},
		{
			"marketplace/plugins/sloprail-content/.sloprail/file-guard/unit-publish-approved",
			"enters-published.sh", "publish-claim.sh",
			"publish_claim_norm() { printf '%s' \"$1\"; }\npublish_claim() { claim=no claim_status=drafting claim_why=; }\n", "publish_claim_loaded=1\n",
			`{"event":{"kind":"PreFileUpdate","path":"memories/topics/t/units/01/UNIT.md","resultKnown":true,"oldContent":"---\nstatus: drafting\n---\n","newContent":"---\nstatus: drafting\n---\nedited\n"}}`,
		},
	} {
		t.Run(tc.script, func(t *testing.T) {
			env := []string{"PATH=" + stubBin + string(os.PathListSeparator) + os.Getenv("PATH")}

			control := t.TempDir()
			copyFile(t, filepath.Join(root, tc.guard, tc.script), filepath.Join(control, tc.script))
			copyFile(t, filepath.Join(root, tc.guard, tc.helper), filepath.Join(control, tc.helper))
			if code, out := runScript(t, control, tc.script, env, tc.payload); code != 1 {
				t.Fatalf("control: with the real %s this payload should waive (exit 1), got %d — the test's premise is wrong:\n%s", tc.helper, code, out)
			}

			for stop, stopper := range map[string]string{"syntax error": syntaxError, "returns early": returnsEarly} {
				partial := t.TempDir()
				copyFile(t, filepath.Join(root, tc.guard, tc.script), filepath.Join(partial, tc.script))
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

// The source matcher finds every way a script sources a file — whatever the
// argument's quoting, and wherever the command sits on the line — and not the
// `.` of a jq filter inside a string.
func TestSourcedPathsCatchesEachForm(t *testing.T) {
	// Multi-line strings and heredocs: only the real source is found.
	script := []string{
		`jq -r '`,
		`  . as $all`,
		`  | if . then 1 else . end'`,
		`echo "a sentence.`,
		`. Permitting."`,
		`cat <<'EOF'`,
		`. "$not_a_source"`,
		`EOF`,
		`x=$'it\'s'`,
		`. "$lib"`,
	}
	got := sourcedPathsIn(script)
	for n, g := range got {
		if n == len(script)-1 {
			if len(g) != 1 || g[0] != `"$lib"` {
				t.Errorf("the real source on the last line was not found: %q", g)
			}
		} else if len(g) != 0 {
			t.Errorf("line %d (%s) inside a string or heredoc was read as a source of %q", n+1, script[n], g)
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
		`if . "$lib"; then`:                         `"$lib"`,
		`{ . "$lib"; }`:                             `"$lib"`,
		`  . "${SR_GUARDRAIL_DIR:-.}/pin.sh" || fail "x"`: `"${SR_GUARDRAIL_DIR:-.}/pin.sh"`,
	} {
		got := sourcedPaths(line)
		if len(got) != 1 || got[0] != want {
			t.Errorf("%s: sourced %q, want [%s]", line, got, want)
		}
	}
	for _, line := range []string{
		`. as $all`, `  . as $x | .foo`, `select(. != "")`, `  . | length`, `map(. + 1)`, `echo "a. b"`,
		`# . "$lib" in a comment`, `x=1. y`, `jq -r '.foo'`, `(.a // .) as $v`,
	} {
		if got := sourcedPaths(line); len(got) != 0 {
			t.Errorf("%s: read as a source of %q", line, got)
		}
	}
}
