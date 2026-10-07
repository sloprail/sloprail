package e2e

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

const judgeDelaySeconds = 5

// T006_01: four judge rules over a multi-file changeset each take
// judgeDelaySeconds to answer. One after another that is four times the delay;
// concurrently the Stop costs about one. Every rule is still asked, and the
// refusal of EVERY failing judge reaches the agent — none is lost to the overlap.
// sr:proves fileguard/refusals-independent
func TestT006_01_JudgesRunConcurrentlyAndEveryRefusalSurfaces(t *testing.T) {
	e, proj := project(t, "a", "b", "c", "d")
	judgeRule(e, proj, "ra", "a", true)
	judgeRule(e, proj, "rb", "b", false)
	judgeRule(e, proj, "rc", "c", true)
	judgeRule(e, proj, "rd", "d", true)
	e.CommitAll(proj, "the rules")
	log := ledgerPath(t)
	e.InstallJudgeClaudeSlow(log, judgeDelaySeconds)

	e.Run(proj, "s-006-01", "write", Turns("done",
		harness.CommitFile("c1", "a/x.md", "x", "add a"),
		harness.CommitFile("c2", "b/x.md", "x", "add b"),
		harness.CommitFile("c3", "c/x.md", "x", "add c"),
		harness.CommitFile("c4", "d/x.md", "x", "add d"),
	))

	joined := strings.Join(e.BlockingErrorsFrom(proj, "s-006-01", "Stop"), "\n")
	for _, want := range []string{"JUDGE-NO-ra", "JUDGE-NO-rc", "JUDGE-NO-rd"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("the refusal %q was lost; blocking errors:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "JUDGE-NO-rb") {
		t.Fatalf("the passing rule refused:\n%s", joined)
	}
	asked := judged(t, log)
	if want := []string{"ra", "rb", "rc", "rd"}; !slices.Equal(asked, want) {
		t.Fatalf("judges asked = %v, want each of %v exactly once (a fail is replayed, never re-asked)", asked, want)
	}
	// Overlap, measured in the judges' own start times rather than the run's wall
	// time (which carries the mock's and the hooks' overhead). One after another the
	// last begins at least 3*delay after the first (less a second of clock
	// resolution); concurrently they all begin within start-up noise. Half the
	// sequential bound is a wide margin on both sides.
	starts := judgeStarts(t, log)
	first, last := starts[0].sec, starts[0].sec
	for _, s := range starts {
		first, last = min(first, s.sec), max(last, s.sec)
	}
	if spread := last - first; spread >= 2*judgeDelaySeconds {
		t.Fatalf("the judges started %ds apart (delay %ds; one after another is at least %ds): they did not run concurrently: %v", spread, judgeDelaySeconds, 3*judgeDelaySeconds-1, starts)
	}
}

// T006_02: a script refusal in one rule does not hide the other rules' judges: the
// judges of the rules whose cheap checks passed still run in the same Stop, and
// their refusals surface beside the script's.
// sr:proves fileguard/refusals-independent
func TestT006_02_AScriptRefusalDoesNotHideOtherRulesJudges(t *testing.T) {
	e, proj := project(t, "s", "j1", "j2")
	e.FileGuard(proj, "script", "match: \"s/**\"\nchecks:\n  - script: ./check.sh\n", map[string]string{
		"check.sh": "#!/bin/sh\npayload=\"$(cat)\"\nif printf '%s' \"$payload\" | grep -q FORBIDDEN; then\n  echo '{\"reason\":\"SCRIPT-SAYS-NO\"}'\n  exit 1\nfi\nexit 0\n",
	})
	judgeRule(e, proj, "j1", "j1", true)
	judgeRule(e, proj, "j2", "j2", true)
	e.CommitAll(proj, "the rules")
	log := ledgerPath(t)
	e.InstallJudgeClaudeSlow(log, judgeDelaySeconds)

	e.Run(proj, "s-006-02", "write", Turns("done",
		harness.CommitFile("c1", "j1/x.md", "x", "add j1"),
		harness.CommitFile("c2", "j2/x.md", "x", "add j2"),
		harness.CommitFile("c3", "s/x.md", "FORBIDDEN", "add s"),
	))
	first := strings.Join(e.BlockingErrorsFrom(proj, "s-006-02", "Stop"), "\n")
	for _, want := range []string{"SCRIPT-SAYS-NO", "JUDGE-NO-j1", "JUDGE-NO-j2"} {
		if !strings.Contains(first, want) {
			t.Fatalf("%q did not surface in the same Stop:\n%s", want, first)
		}
	}
	got := judged(t, log)
	slices.Sort(got)
	if !slices.Equal(got, []string{"j1", "j2"}) {
		t.Fatalf("judges asked = %v, want j1 and j2 once each", got)
	}
}

// T006_03: the judge's own claude session runs with every hook disabled. Empty
// `hooks`/`enabledPlugins` objects do not disable the project's or a plugin's
// hooks (settings merge), so a judge session would run sloprail's own Stop hook
// and nest a judge inside it; `disableAllHooks` is what stops it.
func TestT006_03_TheJudgeSessionRunsWithHooksDisabled(t *testing.T) {
	e, proj := project(t, "a")
	judgeRule(e, proj, "ra", "a", false)
	e.CommitAll(proj, "the rules")
	argv := ledgerPath(t)
	e.InstallJudgeClaudeRecordingArgv(argv, `{"pass": true, "reasoning": "fine"}`)

	e.Run(proj, "s-006-03", "write", Turns("done", harness.CommitFile("c1", "a/x.md", "x", "add a")))

	raw, err := os.ReadFile(argv)
	if err != nil {
		t.Fatalf("the judge was never launched: %v", err)
	}
	body := string(raw)
	if !harness.JudgeHooksDisabled(body) {
		t.Fatalf("the judge was launched with its hooks on; argv:\n%s", body)
	}
}
