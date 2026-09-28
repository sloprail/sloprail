// Package repo holds checks over the repository's own files rather than over a
// binary: things every shipped file must satisfy, whoever wrote it.
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
		for n, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			for i, re := range compiled {
				if re.MatchString(line) {
					t.Errorf("%s:%d uses %s, which bash 3.2 does not have: %s", f, n+1, bash4Only[i].name, strings.TrimSpace(line))
				}
			}
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
