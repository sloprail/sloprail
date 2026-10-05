package repo

import (
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Nothing a test, a CI step, a script or a Makefile target runs may write the MACHINE's git
// configuration (--global / --system). On a CI runner that is harmless; copied onto a developer's
// machine ("run the shard exactly as CI does") it rewrites their real ~/.gitconfig, and every
// later commit they make carries a test identity. That happened: a workflow step
// `git config --global user.email ...` was run locally by an agent and a developer's commits
// (a harness-mocks PR) were authored "local test <ci-local@sloprail.invalid>".
//
// The identity tests need is given as environment by the Makefile's test targets instead
// (GIT_AUTHOR_* / GIT_COMMITTER_*), which dies with the process and touches no file.

// globalGitConfigWrites reports the lines of body that write the global or system git config.
// Reads (--get*, --list, -l, --show-origin) are fine.
func globalGitConfigWrites(body string) []string {
	var out []string
	for i, line := range strings.Split(body, "\n") {
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "//") || strings.HasPrefix(t, "#") {
			continue // prose about the rule, not a call
		}
		if !strings.Contains(line, "config") || !globalScope.MatchString(line) || readOnlyConfig.MatchString(line) {
			continue
		}
		if shellGitConfig.MatchString(line) || goGitConfig.MatchString(line) {
			out = append(out, strings.TrimSpace(line)+" (line "+strconv.Itoa(i+1)+")")
		}
	}
	return out
}

var (
	globalScope    = regexp.MustCompile(`--(global|system)\b`)
	readOnlyConfig = regexp.MustCompile(`--(get|get-all|get-regexp|list|show-origin)\b|\s-l\b`)
	shellGitConfig = regexp.MustCompile(`\bgit\b[^|;&]*\bconfig\b`)
	goGitConfig    = regexp.MustCompile(`"config"\s*,`)
)

func TestNothingWritesTheMachinesGitConfig(t *testing.T) {
	root := repoRoot(t)
	files, err := exec.Command("git", "-C", root, "ls-files").Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	scanned := 0
	for _, rel := range strings.Split(strings.TrimSpace(string(files)), "\n") {
		if !scannedForGitConfig(rel) {
			continue
		}
		scanned++
		for _, hit := range globalGitConfigWrites(readRepoFile(t, root, rel)) {
			t.Errorf("%s writes the machine's git config: %s\n\tgive tests an identity through the environment (the Makefile's test targets export GIT_AUTHOR_*), never `git config --global`", rel, hit)
		}
	}
	if scanned < 50 {
		t.Fatalf("scanned only %d files — the file selection is wrong", scanned)
	}
}

// scannedForGitConfig selects what can run on a developer's machine: workflows, the Makefile,
// scripts, shell and Go sources (including the tests, which run there).
func scannedForGitConfig(rel string) bool {
	if rel == "tests/repo/test_isolation_test.go" { // names the patterns it forbids
		return false
	}
	switch {
	case strings.HasPrefix(rel, ".github/workflows/"), rel == "Makefile", strings.HasPrefix(rel, "scripts/"):
		return true
	}
	switch filepath.Ext(rel) {
	case ".sh", ".go", ".bash":
		return true
	}
	return false
}

func TestGlobalGitConfigWriteDetector(t *testing.T) {
	bad := []string{
		"git config --global user.email ci@sloprail.invalid",
		"          git config --global user.name  sloprail CI",
		"git -C /x config --system core.autocrlf false",
		"git config --add --global safe.directory /x",
		`exec.Command("git", "config", "--global", "user.email", "x")`,
	}
	for _, line := range bad {
		if len(globalGitConfigWrites(line)) == 0 {
			t.Errorf("not detected: %s", line)
		}
	}
	ok := []string{
		"git config user.email t@t",
		"git config --local user.email t@t",
		"git config --global --list",
		"git config --global --get user.email",
		"git config --global --list --show-origin | grep user",
		`git -C "$d" -c user.name=t commit -m x`,
		"# the global config is never written",
	}
	for _, line := range ok {
		if hits := globalGitConfigWrites(line); len(hits) != 0 {
			t.Errorf("false positive on %q: %v", line, hits)
		}
	}
}

// The identity a CI runner lacks comes from the Makefile's environment, for every target that runs
// tests — so no workflow step has to write it, and none can be copied onto a developer's machine.
func TestMakefileGivesEveryTestTargetAGitIdentity(t *testing.T) {
	makefile := readRepoFile(t, repoRoot(t), "Makefile")
	targets := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^(test[a-z0-9-]*):`).FindAllStringSubmatch(makefile, -1) {
		targets[m[1]] = true
	}
	list := regexp.MustCompile(`(?m)^TEST_TARGETS\s*:=\s*(.+)$`).FindStringSubmatch(makefile)
	if list == nil {
		t.Fatal("Makefile: no TEST_TARGETS list")
	}
	listed := map[string]bool{}
	for _, f := range strings.Fields(list[1]) {
		listed[f] = true
	}
	for tgt := range targets {
		if !listed[tgt] {
			t.Errorf("Makefile target %q runs tests but is not in TEST_TARGETS, so it has no test git identity", tgt)
		}
	}
	for _, v := range []string{"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL"} {
		if !strings.Contains(makefile, "$(TEST_TARGETS): export "+v+" :=") {
			t.Errorf("Makefile does not export %s for the test targets", v)
		}
	}
}
