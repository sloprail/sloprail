package e2e

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The plugin's shipped sloprail/file-guard/rule-tests: a commit that changes a project rule is
// refused unless `sr-checks doctor` passes on it. Driven through the real `sr-checks run` over
// a git repository holding the shipped rule, and through `sr-checks doctor` over the plugin's
// own rules. No mock agent.

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

const gateYAML = "on:\n  - event: PreCommandInvoke\n    match: any(event.invocations, .bin == \"curl\")\nchecks:\n  - script: ./no.sh\n"

const gateScript = "#!/usr/bin/env bash\nset -uo pipefail\ncat >/dev/null\njq -n '{reason: \"use the fetch tool, not curl\"}'\nexit 1\n"

// repoWithShippedRule makes the project a git repository on main holding the SHIPPED
// rule-tests (copied from this checkout, without its own cases), as a project that installed the
// plugin would have it, and returns the repository.
func repoWithShippedRule(t *testing.T) *harness.RuleProject {
	t.Helper()
	p := harness.NewRuleProject(t)
	src := filepath.Join(harness.RepoRoot(t), "marketplace", "plugins", "sloprail", ".sloprail", "file-guard", "rule-tests")
	dst := filepath.Join(p.DotDir, "file-guard", "rule-tests")
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		if rel == "tests" || strings.HasPrefix(rel, "tests"+string(filepath.Separator)) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		info, _ := d.Info()
		out, err := os.OpenFile(filepath.Join(dst, rel), os.O_CREATE|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	p.Write("README.md", "a project\n")
	p.Git("", "init", "-q", "-b", "main")
	return p
}

// caseFile writes a no-curl case (a one-event trajectory) into the project.
func caseFile(p *harness.RuleProject, name, expect, command string) {
	dir := ".sloprail/gate/no-curl/tests/" + name + "/"
	p.Write(dir+"case.yaml", "expect: "+expect+"\n")
	p.Write(dir+"setup.sh", "echo hi > README.md\ngit add -A\ngit commit -q -m base\n")
	p.Write(dir+"trajectory.yaml", "- kind: PreCommandInvoke\n  command: '"+command+"'\n")
}

func commit(p *harness.RuleProject, msg string) {
	p.Git("", "add", "-A")
	p.Git("", "commit", "-q", "-m", msg)
}

// run judges main..HEAD the way an agent (or CI) does.
func run(p *harness.RuleProject) harness.Result {
	return p.Sr("", "sr-checks", "run", "--base", "main", "--head", "HEAD")
}
