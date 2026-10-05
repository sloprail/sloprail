package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T057_15: the push gate ships OFF. A push with commits nobody judged is not refused by default;
// the project that lists `sloprail/gate/verify-before-push` under `enabled:` gets the refusal, and
// `disabled:` still wins over `enabled:`.
func TestT057_15_AnUnverifiedPushIsNotRefusedByDefault(t *testing.T) {
	e, proj, _ := projectWith(t, docsRule) // no WithEnabledShipped: the plugin as shipped
	bare := e.Origin(proj)
	e.Run(proj, "s-057-15a", "commit and push", Turns("done",
		harness.CommitFile("c1", "docs/a.md", "clean words", "add a"),
		Bash("p1", "git push -q origin HEAD:refs/heads/work"),
	))
	if exec.Command("git", "-C", bare, "rev-parse", "--verify", "-q", "refs/heads/work").Run() != nil {
		t.Fatal("the push gate is off by default, but an unverified push was refused")
	}
}

func TestT057_15_OptingInRefusesTheSamePush(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	bare := e.Origin(proj)
	res := e.Run(proj, "s-057-15b", "commit and push", Turns("done",
		harness.CommitFile("c1", "docs/a.md", "clean words", "add a"),
		Bash("p1", "git push -q origin HEAD:refs/heads/work"),
	))
	if !res.Refused() {
		t.Fatalf("with `enabled: [%s]` the unverified push must be refused:\n%s", pushGate, res.Output)
	}
	if exec.Command("git", "-C", bare, "rev-parse", "--verify", "-q", "refs/heads/work").Run() == nil {
		t.Fatal("the refused push reached the remote")
	}
}

// A fake `gh` that logs its arguments and answers the three calls `sr-checks run` makes.
const fakeGh = `#!/bin/sh
echo "$@" >> "$GH_LOG"
case "$1 $2" in
  "pr list") echo '[{"number":5}]' ;;
  "pr checks") echo '[{"name":"sr-checks verify","bucket":"fail","link":"https://github.com/o/r/actions/runs/4242/job/7"}]'; exit 1 ;;
  "run rerun") ;;
esac
exit 0
`

func ghRun(t *testing.T, gh string) (res harness.Result, log string) {
	t.Helper()
	e, proj, _ := projectWith(t, docsRule)
	logFile := filepath.Join(t.TempDir(), "gh.log")
	e.InstallShim("gh", strings.ReplaceAll(gh, "$GH_LOG", logFile))
	base := e.Git(proj, "rev-list", "--max-parents=0", "HEAD")
	res = e.Run(proj, "s-057-15g", "judge", Turns("done",
		harness.CommitFile("c1", "docs/a.md", "clean words", "add a"),
		Bash("j1", "sr-checks run --base "+base+" --head HEAD"),
	))
	b, _ := os.ReadFile(logFile)
	return res, string(b)
}

// `sr-checks run` re-runs the failed `sr-checks verify` job of the head's open pull request.
func TestT057_15_RunReRunsAFailedVerifyJobOfTheOpenPullRequest(t *testing.T) {
	res, log := ghRun(t, fakeGh)
	if !strings.Contains(log, "run rerun 4242 --failed") {
		t.Fatalf("the failed verify job was not re-run; gh was called with:\n%s\n%s", log, res.Output)
	}
}

// Without a usable gh the run says how to re-run by hand, in one line, and its own status stands.
func TestT057_15_RunWithoutAUsableGhSaysHowToReRun(t *testing.T) {
	res, _ := ghRun(t, "#!/bin/sh\nexit 1\n")
	if !res.Saw("gh run rerun <run-id> --failed") {
		t.Fatalf("no hint how to re-run the verify job:\n%s", res.Output)
	}
}
