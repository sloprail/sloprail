package harness

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Binaries builds the sloprail services once per test process and returns the directory
// holding them, for a test that needs only the binaries and no mock agent: `sr-checks test`
// and `doctor` run a rule's cases with no model and no harness, so their tests must not skip
// when the mock is absent.
func Binaries(t *testing.T) string {
	t.Helper()
	return build(t)
}

// RuleProject is a throwaway project for the rule-test commands: a directory the test writes
// rules and cases into, and runs the built binaries over.
type RuleProject struct {
	t      *testing.T
	bin    string
	home   string
	Dir    string
	DotDir string // Dir/.sloprail
	shims  string

	extraEnv []string
}

// NewRuleProject makes an empty project directory (not yet a git repository: most tests of
// `sr-checks test` need none, the cases build their own).
func NewRuleProject(t *testing.T) *RuleProject {
	t.Helper()
	root, err := os.MkdirTemp("", "slop-ruletest-")
	if err != nil {
		t.Fatalf("harness: temp project: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	p := &RuleProject{t: t, bin: Binaries(t), home: filepath.Join(root, "home"), Dir: filepath.Join(root, "project")}
	p.DotDir = filepath.Join(p.Dir, ".sloprail")
	for _, d := range []string{p.home, p.DotDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

// T is the test the project belongs to.
func (p *RuleProject) T() *testing.T { return p.t }

// Write puts a file under the project (a path relative to it), executable when it is a script.
func (p *RuleProject) Write(rel, body string) {
	p.t.Helper()
	path := filepath.Join(p.Dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		p.t.Fatal(err)
	}
	mode := os.FileMode(0o644)
	if strings.HasSuffix(rel, ".sh") {
		mode = 0o755
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		p.t.Fatal(err)
	}
}

// Shim puts an executable called name first on the PATH the binaries run with, after the
// build under test: a `claude` that answers a judge, say.
func (p *RuleProject) Shim(name, script string) {
	p.t.Helper()
	if p.shims == "" {
		p.shims = filepath.Join(filepath.Dir(p.Dir), "shims")
		if err := os.MkdirAll(p.shims, 0o755); err != nil {
			p.t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(p.shims, name), []byte(script), 0o755); err != nil {
		p.t.Fatal(err)
	}
}

// Env is the environment the binaries run in: the host's, scrubbed of any enclosing session,
// with a home of its own and the build under test first on PATH.
func (p *RuleProject) Env() []string {
	path := p.bin + string(os.PathListSeparator)
	if p.shims != "" {
		path += p.shims + string(os.PathListSeparator)
	}
	return append(append(HostEnv(), "HOME="+p.home, "PATH="+path+os.Getenv("PATH")), p.extraEnv...)
}

// SetEnv adds a variable the binaries run with (`CLAUDECODE=1`, for a live judge).
func (p *RuleProject) SetEnv(kv string) { p.extraEnv = append(p.extraEnv, kv) }

// Sr runs one built service binary (`sr-checks`, `sr-session`, ...) in dir ("" is the project).
func (p *RuleProject) Sr(dir, binary string, args ...string) Result {
	p.t.Helper()
	if dir == "" {
		dir = p.Dir
	}
	cmd := exec.Command(filepath.Join(p.bin, binary), args...)
	cmd.Dir = dir
	cmd.Env = p.Env()
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		p.t.Fatalf("harness: run %s %v: %v\n%s", binary, args, err, out)
	}
	return Result{Output: string(out), Code: code}
}

// Test runs `sr-checks test` over the project's own .sloprail.
func (p *RuleProject) Test(args ...string) Result {
	p.t.Helper()
	return p.Sr("", "sr-checks", append([]string{"test", "--rules-dir", p.DotDir}, args...)...)
}

// Doctor runs `sr-checks doctor` over the project's own .sloprail.
func (p *RuleProject) Doctor(args ...string) Result {
	p.t.Helper()
	return p.Sr("", "sr-checks", append([]string{"doctor", "--rules-dir", p.DotDir}, args...)...)
}

// RepoRoot is this checkout's root, for a test that runs the shipped rules.
func RepoRoot(t *testing.T) string {
	t.Helper()
	return repoRoot(t)
}

// Git runs git in dir with an identity of its own and returns what it printed.
func (p *RuleProject) Git(dir string, args ...string) string {
	p.t.Helper()
	if dir == "" {
		dir = p.Dir
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(p.Env(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		p.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}
