package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The plugin's sloprail/gate/no-merge-over-refusals refuses `gh pr merge` while the
// branch being merged has open refusals recorded this session, judged at its tip.
// It ships on by default, so these tests install nothing: the plugin's own gate fires.
var (
	Turns = harness.Turns
	Bash  = harness.Bash
)

// fakeGH puts a `gh` on the PATH of every hook of e that answers `gh pr view [N] --json
// headRefName,headRefOid` the way a pull request would: N is looked up in prs (number ->
// head branch; an unknown number fails, as gh does for a PR that does not exist), and with
// no number the current branch is the head. The oid is that branch's tip in the repository
// the call runs in. Every other `gh` call (the merge itself) succeeds and does nothing.
// ghDirs is where each Env's fake gh keeps its pull requests.
var ghDirs sync.Map

func fakeGH(t *testing.T, e *harness.Env, prs map[string]string) {
	t.Helper()
	dir := t.TempDir()
	ghDirs.Store(e, dir)
	for n, branch := range prs {
		if err := os.WriteFile(filepath.Join(dir, "pr-"+n), []byte(branch), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	e.InstallPathShim("gh", fmt.Sprintf(`#!/bin/sh
[ "$1" = pr ] && [ "$2" = view ] || exit 0
shift 2
n=""
for a in "$@"; do
  case "$a" in [0-9]*) n="$a"; break ;; esac
done
if [ -n "$n" ]; then
  b="$(cat %q/pr-"$n" 2>/dev/null)" || exit 1
else
  b="$(git branch --show-current)"
fi
[ -n "$b" ] || exit 1
oid="$(git rev-parse "refs/heads/$b")" || exit 1
printf '{"headRefName":"%%s","headRefOid":"%%s"}\n' "$b" "$oid"
`, dir))
}

// setPR makes pull request n of e's fake gh one whose head is branch.
func setPR(t *testing.T, e *harness.Env, n, branch string) {
	t.Helper()
	v, _ := ghDirs.Load(e)
	ghDir, _ := v.(string)
	if err := os.WriteFile(filepath.Join(ghDir, "pr-"+n), []byte(branch), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}
