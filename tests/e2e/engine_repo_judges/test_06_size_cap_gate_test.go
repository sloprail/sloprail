package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// INVARIANT: the deterministic size cap refuses BEFORE the write lands.
//
// rule-quality and skill-quality are each a plain file-guard (the judge, at Stop)
// plus a PreFileWrite gate whose only check is the size cap, shared through
// size-cap-lib.sh. A judge never runs in a gate, but a file too large to ever be
// judged is knowable from the pending bytes, so it is refused before it lands.
// The gate sources the lib from the file-guard folder, so both natures are
// installed. The judge's model is stubbed to PASS, so a refusal can only be the cap.

// oversized is past the 600000-byte cap.
func oversized() string { return "# big\n\n" + strings.Repeat("x", 650000) + "\n" }

func sizeCapProject(t *testing.T, e *harness.Env, name string) string {
	t.Helper()
	proj := e.Project()
	e.GitInit(proj)
	installGuardrail(t, proj, gateNature, name)
	installGuardrail(t, proj, fileGuardNature, name)
	e.CommitAll(proj, "install "+name)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)
	return proj
}

func TestSizeCapGateRefusesAnOversizedWriteBeforeItLands(t *testing.T) {
	for _, c := range []struct{ name, path, tag string }{
		{"rule-quality", "guardrails/x/rules/y/RULE.md", "RULE QUALITY"},
		{"skill-quality", "skills/x/SKILL.md", "SKILL QUALITY"},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := New(t)
			proj := sizeCapProject(t, e, c.name)
			sid := "s-cap-big-" + c.name
			got := e.Run(proj, sid, "write a huge file", Turns("done",
				harness.Write("w1", c.path, oversized()),
			))
			if e.Exists(proj, c.path) {
				t.Errorf("the oversized %s landed: the gate did not refuse it", c.path)
			}
			if !got.Refused() || !strings.Contains(got.Output, c.tag) || !strings.Contains(got.Output, "bytes, which is too") {
				t.Errorf("no size refusal from the gate (%s):\n%.2000s", c.name, got.Output)
			}
		})
	}
}

func TestSizeCapGatePermitsANormalWrite(t *testing.T) {
	for _, c := range []struct{ name, path, tag string }{
		{"rule-quality", "guardrails/x/rules/y/RULE.md", "RULE QUALITY"},
		{"skill-quality", "skills/x/SKILL.md", "SKILL QUALITY"},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := New(t)
			proj := sizeCapProject(t, e, c.name)
			sid := "s-cap-ok-" + c.name
			e.Run(proj, sid, "write a normal file", Turns("done",
				harness.Write("w1", c.path, "# Fine\n\nA normal body.\n"),
			).ThenCommit("write the file"))
			if !e.Exists(proj, c.path) {
				t.Errorf("a normal %s did not land", c.path)
			}
			if sawRefusal(e.BlockingErrors(proj, sid), c.tag) {
				t.Errorf("a normal write was refused:\n%v", e.BlockingErrors(proj, sid))
			}
		})
	}
}
