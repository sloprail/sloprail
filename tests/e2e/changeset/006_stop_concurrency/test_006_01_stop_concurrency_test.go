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

// T006_02: a script refusal is returned without waiting on any judge: the cheap
// checks of every rule run first, and the judges are not started. The judges are
// not lost — once the script passes, the next Stop asks them and their refusals
// surface.
func TestT006_02_AScriptRefusalSurfacesWithoutRunningJudges(t *testing.T) {
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
	first := e.BlockingErrorsFrom(proj, "s-006-02", "Stop")
	if !strings.Contains(strings.Join(first, "\n"), "SCRIPT-SAYS-NO") {
		t.Fatalf("the script refusal did not surface:\n%s", strings.Join(first, "\n"))
	}
	if got := judged(t, log); len(got) != 0 {
		t.Fatalf("judges ran (%v) although a script check had already refused", got)
	}
	if strings.Contains(strings.Join(first, "\n"), "JUDGE-NO") {
		t.Fatalf("a judge refusal surfaced before any judge could have run:\n%s", strings.Join(first, "\n"))
	}

	// Fix the script's input: the judges are now reached, and both refuse.
	e.Run(proj, "s-006-02", "fix", Turns("fixed", harness.CommitFile("c4", "s/x.md", "clean", "fix s")))
	later := strings.Join(e.BlockingErrorsFrom(proj, "s-006-02", "Stop"), "\n")
	for _, want := range []string{"JUDGE-NO-j1", "JUDGE-NO-j2"} {
		if !strings.Contains(later, want) {
			t.Fatalf("the deferred judge refusal %q was lost:\n%s", want, later)
		}
	}
	if asked := judged(t, log); !slices.Equal(asked, []string{"j1", "j2"}) {
		t.Fatalf("judges asked after the fix = %v, want j1 and j2", asked)
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
	if !strings.Contains(body, `"disableAllHooks":true`) {
		t.Fatalf("the judge's claude was launched without disableAllHooks; argv:\n%s", body)
	}
}
