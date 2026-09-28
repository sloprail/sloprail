// Package repo holds checks over the repository's own files rather than over a
// binary: things every shipped file must satisfy, whoever wrote it.
package repo

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Every shell script the repo ships parses under bash — the shell hooks, checks,
// preparers and eval scorers run under. A script that does not parse can fail
// OPEN: a sourced helper that stops at a syntax error defines nothing, and the
// `.` that loaded it does not fail, so a `when` reading a claim from it can read
// "no" and waive. (An apostrophe inside a double-quoted ${var:-…} is valid zsh
// and a bash syntax error; one shipped for a moment in sloprail-content.)
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
