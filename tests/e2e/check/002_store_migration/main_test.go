package e2e

import (
	"os"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/internal/checkcache"
	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// store_migration: a `sloprail/checks` ref written by an OLDER sloprail (the released v0.4.1's
// v2026-10-03 layout, or main's v2026-10-07) is NOT converted by `sr-checks run` or `verify`: the
// new layout starts empty beside it and the older directory stays untouched. The explicit
// `sr-checks migrate-keys` carries it across, by REBUILDING each subject's newest stored pass's key
// at its recorded range from git and the rule's subjects script: no judge is asked (the capturing
// judge would record a call), and the verdicts are then found under the new key. The old stores
// are written with the engine's own test writer (checkcache.PutAsOlder), not by an old binary.

type Env = harness.Env

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

const (
	judgeRule = "match: \"docs/**\"\nsubjects: ./subjects.sh\nchecks:\n  - judge: ./rubric.md.j2\n"
	rubric    = "Does this change to the docs hold up?\n{{ change }}\n"
	// subjects names one subject, "api", over docs/a.md, with a fingerprint of its own.
	subjectsScript = "#!/bin/sh\ncat >/dev/null\necho '[{\"id\":\"api\",\"files\":[\"docs/a.md\"],\"fingerprint\":\"schema-v1\"}]'\n"

	promptFile = ".git/judge-prompt"
	ruleName   = "file-guard/docs"
	currentDir = "v2026-10-08"
)

// older is one layout an earlier release wrote: its directory and the schema of its keys.
type older struct{ dir, version string }

var (
	release  = older{"v2026-10-03", "sr1"} // v0.4.1
	mainSR2  = older{"v2026-10-07", "sr2"} // main, before the citation-free key
	layouts  = map[string]older{"v0.4.1 (v2026-10-03)": release, "main (v2026-10-07)": mainSR2}
	noJudges = "SR_CHECKS_JUDGE_MOCKS={}"
)

// fixture is a repository with the judged, subject-split rule, one docs/a.md change after it,
// and the range base..head.
type fixture struct {
	e          *Env
	proj       string
	base, head string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	e := harness.New(t, harness.WithoutShippedFileGuards(), harness.NoAutoCheck())
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "docs/seed.md", "seed\n")
	e.CommitAll(proj, "the project")
	e.FileGuard(proj, "docs", judgeRule, map[string]string{"rubric.md.j2": rubric, "subjects.sh": subjectsScript})
	base := e.CommitAll(proj, "the rule")
	e.WriteFile(proj, "docs/a.md", "the release is Friday\n")
	head := e.CommitAll(proj, "add a")
	return &fixture{e: e, proj: proj, base: base, head: head}
}

// storeRun writes one stored run into the ref the way an older release did: a passing guard
// verdict for a subject over base..head, under a key that is nothing the new engine computes.
func (f *fixture) storeRun(t *testing.T, o older, id, subject, base, head string) {
	t.Helper()
	run := checkcache.Run{
		ID: id, RunAt: "2026-10-01T10:00:00.000Z", Rule: ruleName, RuleHash: "old-hash",
		BaseRef: base, HeadRef: head, Complete: true,
		Checks: []checkcache.Check{{Subject: subject, Kind: "guard", Status: checkcache.StatusPass, Fingerprint: "old-key-" + id}},
	}
	if err := checkcache.PutAsOlder(checkcache.Options{Dir: f.proj}, o.dir, o.version, run); err != nil {
		t.Fatalf("writing the %s store: %v", o.dir, err)
	}
}

func (f *fixture) env() []string {
	return append(append([]string{}, harness.NoSessionEnv...), noJudges)
}

func (f *fixture) run(base, head string) harness.Result {
	return f.e.CLIDirectEnv(f.proj, f.env(), "sr-checks", "run", "--base", base, "--head", head)
}

func (f *fixture) migrate() harness.Result {
	return f.e.CLIDirectEnv(f.proj, f.env(), "sr-checks", "migrate-keys")
}

// dirOid is the object id of a layout's directory in the ref: the same while it is untouched.
func (f *fixture) dirOid(dir string) string {
	return strings.TrimSpace(f.e.Git(f.proj, "rev-parse", "refs/sloprail/checks:"+dir))
}

func (f *fixture) verify(base, head string) harness.Result {
	return f.e.CLIDirectEnv(f.proj, f.env(), "sr-checks", "verify", "--base", base, "--head", head)
}

func (f *fixture) refDirs(t *testing.T) string {
	t.Helper()
	return f.e.Git(f.proj, "ls-tree", "--name-only", "refs/sloprail/checks")
}

func (f *fixture) migrations(t *testing.T) int {
	t.Helper()
	out := strings.TrimSpace(f.e.Git(f.proj, "log", "--format=%s", "--grep=re-key", "refs/sloprail/checks"))
	if out == "" {
		return 0
	}
	return len(strings.Split(out, "\n"))
}

func (f *fixture) judgeCalls() int { return f.e.JudgeCalls(f.proj, promptFile, "") }
