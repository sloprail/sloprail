package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The plugin's sloprail/gate/verify-before-push (OFF by default: a project opts in with `enabled:`; every
// test here but the default-off one opts in) refuses an agent's `git push`
// until `sr-checks verify` passes over the commits it would send, and
// sloprail/gate/checks-ref-sr-only keeps the results branch for sr-checks alone.
// The helpers below are copied unchanged from the old changeset/003_stop_evaluation package.

type Env = harness.Env

var (
	Turns = harness.Turns
	Bash  = harness.Bash
)

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

const refusalText = "FORBIDDEN text in the changeset"

func stopRefusals(e *Env, proj, sess string) string {
	return strings.Join(e.StopContinuations(proj, sess), "\n")
}

func recorder(ledger string) string {
	return `#!/bin/sh
payload="$(cat)"
if printf '%s' "$payload" | jq -e 'any(.changeset.files[]; .newContent | contains("FORBIDDEN"))' >/dev/null; then
  echo '{"reason":"FORBIDDEN text in the changeset"}'
  exit 1
fi
exit 0
`
}

const docsRule = "match: \"docs/**\"\nchecks:\n  - script: ./check.sh\n"

// pushGate is the shipped gate that ships off.
const pushGate = "sloprail/gate/verify-before-push"

// project is a project that opted in to the push gate.
func project(t *testing.T, ruleYAML string) (*Env, string, string) {
	t.Helper()
	return projectWith(t, ruleYAML, harness.WithEnabledShipped(pushGate))
}

// projectWith is a project built with exactly the given harness options.
func projectWith(t *testing.T, ruleYAML string, opts ...harness.Option) (*Env, string, string) {
	t.Helper()
	e := harness.New(t, append([]harness.Option{harness.WithoutShippedFileGuards()}, opts...)...)
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "docs/seed.md", "seed\n")
	led := filepath.Join(t.TempDir(), "ledger.jsonl")
	e.CommitAll(proj, "the project")
	e.FileGuard(proj, "docs", ruleYAML, map[string]string{"check.sh": recorder(led)})
	e.CommitAll(proj, "the rule")
	return e, proj, led
}
