package e2e

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// stop_concurrency: at Stop the file-guards are evaluated concurrently — their
// judges overlap, so a Stop costs the slowest judge, not the sum — and the cheap
// script checks of every rule run before any judge, so a script refusal does not
// wait on a model. Neither may lose a refusal.
//
// Run as `env -u CLAUDECODE -u CLAUDE_CODE_SESSION_ID go test ...`.

type Env = harness.Env

var Turns = harness.Turns

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

// New is harness.New with the plugin's authoring file-guards switched off: this
// package is about other rules.
func New(t *testing.T) *Env { return harness.New(t, harness.WithoutShippedFileGuards()) }

// judgeRubric is a judge rule's template. The slow shim refuses a prompt that
// contains VERDICT-FAIL and names the rule from the RULE= line.
func judgeRubric(name string, fail bool) string {
	body := "RULE=" + name + "\n"
	if fail {
		body += "VERDICT-FAIL\n"
	}
	return body + "{{ change }}\n"
}

// judgeRule adds a judge file-guard over dir/**.
func judgeRule(e *Env, proj, name, dir string, fail bool) {
	e.FileGuard(proj, name, "match: \""+dir+"/**\"\nchecks:\n  - judge: ./rubric.md.j2\n",
		map[string]string{"rubric.md.j2": judgeRubric(name, fail)})
}

// project is a committed repository with an initial file under every dir.
func project(t *testing.T, dirs ...string) (*Env, string) {
	t.Helper()
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	for _, d := range dirs {
		e.WriteFile(proj, d+"/seed.md", "seed\n")
	}
	e.CommitAll(proj, "the project")
	return e, proj
}

// judgeStarts reads the slow shim's log: one entry per judge call.
func judgeStarts(t *testing.T, path string) []start {
	t.Helper()
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []start
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		sec, err := strconv.ParseInt(f[1], 10, 64)
		if err != nil {
			t.Fatalf("bad judge log line %q", line)
		}
		out = append(out, start{rule: f[0], sec: sec})
	}
	return out
}

type start struct {
	rule string
	sec  int64
}

// judged is the rules the slow shim was asked about, sorted.
func judged(t *testing.T, path string) []string {
	t.Helper()
	var rules []string
	for _, s := range judgeStarts(t, path) {
		rules = append(rules, s.rule)
	}
	slices.Sort(rules)
	return rules
}

func ledgerPath(t *testing.T) string { return filepath.Join(t.TempDir(), "judge-log") }
