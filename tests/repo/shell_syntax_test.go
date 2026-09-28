// Package repo holds checks over the repository's own files rather than over a
// binary: things every shipped file must satisfy, whoever wrote it.
package repo

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/syntax"
)

// Every shell script the repo ships parses under bash — the shell hooks, checks,
// preparers and eval scorers run under. A script that does not parse can fail
// OPEN: bash runs a sourced helper only up to its first syntax error, and
// whether the `.` then fails depends on the bash version and the error, so a
// caller can read on with part of the helper and decide on it — a `when` can
// read "no" and waive. (An apostrophe inside a double-quoted ${var:-…} is valid
// zsh and a bash syntax error; one shipped for a moment in sloprail-content.)
//
// "Ships" is every tracked *.sh, and every tracked file whose shebang names
// bash, outside the e2e test tree and testdata (which may hold broken scripts on
// purpose).
func TestTrackedShellScriptsParseUnderBash(t *testing.T) {
	root := repoRoot(t)
	scripts := trackedBashScripts(t, root)
	if len(scripts) < 20 {
		t.Fatalf("found only %d shell scripts under %s — the walk is wrong", len(scripts), root)
	}
	for _, bad := range bashSyntaxErrors(root, scripts) {
		t.Errorf("%s", bad)
	}
}

// The check names the file that does not parse — proven on a copy of a shipped
// guard script with the zsh-only construct added.
func TestBashSyntaxErrorsNamesTheFile(t *testing.T) {
	root := repoRoot(t)
	src, err := os.ReadFile(filepath.Join(root, "marketplace/plugins/sloprail-tasks/.sloprail/file-guard/task-body-is-human-authored/body-changed.sh"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	name := "body-changed.sh"
	broken := string(src) + "\nx=\"${y:-it's}\"\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(broken), 0o755); err != nil {
		t.Fatal(err)
	}
	bad := bashSyntaxErrors(dir, []string{name})
	if len(bad) != 1 || !strings.Contains(bad[0], name) {
		t.Fatalf("the broken copy was not reported by name: %q", bad)
	}
	if got := bashSyntaxErrors(root, []string{"marketplace/plugins/sloprail-tasks/.sloprail/file-guard/task-body-is-human-authored/body-changed.sh"}); len(got) != 0 {
		t.Fatalf("the shipped original was reported: %q", got)
	}
}

// bashSyntaxErrors runs `bash -n` on each file (relative to root) and returns a
// line per file that does not parse, naming it.
func bashSyntaxErrors(root string, files []string) []string {
	var bad []string
	for _, f := range files {
		cmd := exec.Command("bash", "-n", f)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			bad = append(bad, f+" does not parse under bash: "+strings.TrimSpace(string(out)))
		}
	}
	return bad
}

// trackedBashScripts lists tracked *.sh files and tracked files with a bash
// shebang, outside tests/ and any testdata/.
func trackedBashScripts(t *testing.T, root string) []string {
	t.Helper()
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	var scripts []string
	for _, f := range strings.Split(string(out), "\x00") {
		if f == "" || strings.HasPrefix(f, "tests/") || strings.Contains(f, "/testdata/") {
			continue
		}
		if strings.HasSuffix(f, ".sh") || hasBashShebang(filepath.Join(root, f)) {
			scripts = append(scripts, f)
		}
	}
	return scripts
}

// hasBashShebang reports whether a file's first line is a shebang naming bash.
func hasBashShebang(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	line, err := bufio.NewReader(f).ReadString('\n')
	if err != nil && line == "" {
		return false
	}
	return strings.HasPrefix(line, "#!") && strings.Contains(line, "bash")
}

// repoRoot is the root of the git checkout this test runs in.
func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("git rev-parse: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// bash4Only matches constructs bash 3.2 — macOS's /bin/bash, which a hook may
// run under — does not have: case fall-through (;& ;;&), |&, coproc, case
// modification (${x,,} ${x^^}), associative arrays, mapfile/readarray, [[ -v,
// namerefs (declare -n), parameter transformation (${x@Q}).
var bash4Only = []struct{ name, pattern string }{
	{"case fall-through ;& or ;;&", `(^|[^;&])(;;&|;&)[[:space:]]*($|#)`},
	{"|& (pipe stderr)", `(^|[^\[\^|])\|&`},
	{"coproc", `(^|[[:space:];&|(])coproc([[:space:]]|$)`},
	{"case modification ${x,,} / ${x^^}", `\$\{[A-Za-z_][A-Za-z0-9_]*(,,?|\^\^?)[^}]*\}`},
	{"associative array (declare/local/typeset -A)", `(^|[[:space:];])(declare|local|typeset)[[:space:]]+-[a-zA-Z]*A`},
	{"mapfile / readarray", `(^|[[:space:];&|(])(mapfile|readarray)([[:space:]]|$)`},
	{"[[ -v (variable is set)", `\[\[[[:space:]]+!?[[:space:]]*-v[[:space:]]`},
	{"nameref (declare/local/typeset -n)", `(^|[[:space:];])(declare|local|typeset)[[:space:]]+-[a-zA-Z]*n`},
	{"${x@Q} (parameter transformation)", `\$\{[A-Za-z_][A-Za-z0-9_]*@[QEPAaKkUuL]\}`},
}

// Shipped scripts use only what bash 3.2 has: a hook may run under macOS's
// /bin/bash, and a construct it lacks is a syntax error there — the partial
// load a sourced helper's sentinel exists to catch, or a check that cannot run.
// Where /bin/bash is a different bash from the one on PATH (a Mac with a newer
// bash installed), every script is also parsed with it.
func TestShippedScriptsAreBash32Compatible(t *testing.T) {
	root := repoRoot(t)
	scripts := trackedBashScripts(t, root)
	compiled := make([]*regexp.Regexp, len(bash4Only))
	for i, c := range bash4Only {
		compiled[i] = regexp.MustCompile(c.pattern)
	}
	for _, f := range scripts {
		b, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			t.Fatal(err)
		}
		findings, err := bash4Findings(b, f, compiled)
		if err != nil {
			t.Errorf("%s does not parse, so it cannot be checked for bash 3.2: %v", f, err)
			continue
		}
		for _, finding := range findings {
			t.Errorf("%s", finding)
		}
	}

	pathBash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatalf("no bash on PATH: %v", err)
	}
	if sys, err := filepath.EvalSymlinks("/bin/bash"); err == nil {
		if onPath, err := filepath.EvalSymlinks(pathBash); err == nil && sys != onPath {
			for _, f := range scripts {
				cmd := exec.Command("/bin/bash", "-n", f)
				cmd.Dir = root
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Errorf("%s does not parse under /bin/bash: %s", f, strings.TrimSpace(string(out)))
				}
			}
		}
	}
}

// bash4Findings scans a script's CODE for bash-4-only constructs: the script is
// parsed, and string contents, heredoc bodies and comments are blanked first
// (by their parsed spans, lines kept), so `|&` in a jq regex or `;&` in a
// message is data, not a construct.
func bash4Findings(src []byte, name string, compiled []*regexp.Regexp) ([]string, error) {
	code, file, err := codeOnly(src, name)
	if err != nil {
		return nil, err
	}
	var out []string
	// Case fall-through (;& and ;;&) read from the parsed case items, wherever
	// it sits on the line.
	syntax.Walk(file, func(n syntax.Node) bool {
		if item, ok := n.(*syntax.CaseItem); ok && (item.Op == syntax.Fallthrough || item.Op == syntax.Resume) {
			out = append(out, fmt.Sprintf("%s:%d uses case fall-through (%s), which bash 3.2 does not have",
				name, item.OpPos.Line(), item.Op))
		}
		return true
	})
	for n, line := range strings.Split(code, "\n") {
		for i, re := range compiled {
			if re.MatchString(line) {
				out = append(out, fmt.Sprintf("%s:%d uses %s, which bash 3.2 does not have: %s",
					name, n+1, bash4Only[i].name, strings.TrimSpace(strings.Split(string(src), "\n")[n])))
			}
		}
	}
	return out, nil
}

// codeOnly is src with every byte that is not code — inside a quoted string
// ('…', $'…', the literal parts of "…"), a heredoc body, a comment — replaced
// by a space. Newlines stay, so line numbers do.
func codeOnly(src []byte, name string) (string, *syntax.File, error) {
	lang := syntax.LangBash
	if posixShebang(src) {
		lang = syntax.LangPOSIX
	}
	file, err := syntax.NewParser(syntax.Variant(lang), syntax.KeepComments(true)).Parse(bytes.NewReader(src), name)
	if err != nil {
		return "", nil, err
	}
	out := []byte(string(src))
	blank := func(from, to uint) {
		for i := from; i < to && int(i) < len(out); i++ {
			if out[i] != '\n' {
				out[i] = ' '
			}
		}
	}
	syntax.Walk(file, func(n syntax.Node) bool {
		switch x := n.(type) {
		case *syntax.SglQuoted:
			blank(x.Pos().Offset(), x.End().Offset())
		case *syntax.DblQuoted:
			for _, part := range x.Parts {
				if lit, ok := part.(*syntax.Lit); ok {
					blank(lit.Pos().Offset(), lit.End().Offset())
				}
			}
		case *syntax.Redirect:
			if x.Hdoc != nil {
				blank(x.Hdoc.Pos().Offset(), x.Hdoc.End().Offset())
			}
		case *syntax.Comment:
			blank(x.Pos().Offset(), x.End().Offset())
		}
		return true
	})
	return string(out), file, nil
}

// A construct in data — a string, a heredoc, a comment — is not flagged; the
// same construct in code is.
func TestBash4ScanReadsCodeNotData(t *testing.T) {
	compiled := make([]*regexp.Regexp, len(bash4Only))
	for i, c := range bash4Only {
		compiled[i] = regexp.MustCompile(c.pattern)
	}
	for _, src := range []string{
		"jq '[$raw | splits(\"\\n|;|&&|\\|\\|\")] | any(.[];' f\n",
		"echo \"use |& or ;& carefully\"\n",
		"cat <<'EOF'\ndeclare -A m\nmapfile x\nEOF\n",
		"x=1 # coproc here is a comment\n",
	} {
		if got, err := bash4Findings([]byte(src), "data.sh", compiled); err != nil || len(got) != 0 {
			t.Errorf("data flagged as code (err %v): %q", err, got)
		}
	}
	for _, src := range []string{
		"cmd |& tee log\n", "declare -A seen\n", "mapfile -t lines < f\n",
		"case $x in a) echo a ;& b) echo b ;; esac\n", "if [[ -v name ]]; then :; fi\n",
	} {
		if got, err := bash4Findings([]byte(src), "code.sh", compiled); err != nil || len(got) == 0 {
			t.Errorf("code not flagged (err %v): %s", err, src)
		}
	}
}

// The scan catches each construct, and not the character class that looks like
// one (`[^|&;]` in a grep pattern).
func TestBash4OnlyScanCatchesEachConstruct(t *testing.T) {
	for _, line := range []string{
		`  a) echo a ;&`, `  a) echo a ;;&`, `cmd |& tee log`, `coproc worker { cat; }`,
		`echo "${name,,}"`, `echo "${name^^}"`, `declare -A seen`, `local -A m`, `mapfile -t lines < f`, `readarray lines`,
		`if [[ -v name ]]; then`, `[[ ! -v name ]]`, `declare -n ref=name`, `local -n ref=$1`, `printf '%s' "${val@Q}"`,
	} {
		hit := false
		for _, c := range bash4Only {
			if regexp.MustCompile(c.pattern).MatchString(line) {
				hit = true
			}
		}
		if !hit {
			t.Errorf("not caught: %s", line)
		}
	}
	for _, line := range []string{
		`grep -qE "(claude)[^|&;]*(--print)"`, `case $x in a) echo a ;; esac`, `a && b || c`, `echo "${name:-x}"`, `declare -F f`,
		`[ -n "$x" ]`, `[[ -n $x ]]`, `echo user@example.com`, `local name=x`,
	} {
		for _, c := range bash4Only {
			if regexp.MustCompile(c.pattern).MatchString(line) {
				t.Errorf("false positive (%s): %s", c.name, line)
			}
		}
	}
}
