// Package e2e covers the shared eval scripts' reading of a run's sub-agents:
// examples/_shared/eval/trajectory-health.sh, which every fixture's score.sh
// sources to show a judge the whole trajectory and to count a guard's refusals.
//
// A session's sub-agent records sit under <session>/subagents/ — and, for a
// workflow's agents, two levels deeper, under subagents/workflows/wf_<id>/. A
// flat `subagents/*.jsonl` glob missed the nested ones, so a refusal a
// workflow's agent met was shown to no judge and counted by no score.
package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func sharedEval(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	dir := filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "examples", "_shared", "eval")
	if _, err := os.Stat(filepath.Join(dir, "trajectory-health.sh")); err != nil {
		t.Fatalf("shared eval scripts not found: %v", err)
	}
	return dir
}

func writeLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// T052_01: a refusal in a WORKFLOW sub-agent's record reaches the condensed
// trajectory the judge reads, under its own header, and counts as the guard
// firing — beside a flat sub-agent's.
func TestT052_01_WorkflowSubagentsAreRead(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not installed")
	}
	shared := sharedEval(t)
	session := filepath.Join(t.TempDir(), "s-052.jsonl")
	writeLines(t, session,
		`{"type":"user","uuid":"u1","message":{"role":"user","content":"do the work"}}`)
	writeLines(t, filepath.Join(strings.TrimSuffix(session, ".jsonl"), "subagents", "agent-flat1.jsonl"),
		`{"type":"user","uuid":"f1","isSidechain":true,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"FLAT-REFUSAL-7731 (file-guard \"guarded-thing\")"}]}}`)
	writeLines(t, filepath.Join(strings.TrimSuffix(session, ".jsonl"), "subagents", "workflows", "wf_9", "agent-wf1.jsonl"),
		`{"type":"user","uuid":"w1","isSidechain":true,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t2","content":"WORKFLOW-REFUSAL-4402 (file-guard \"guarded-thing\")"}]}}`)

	script := `. "$SHARED/trajectory-health.sh"
root="$(mktemp)"
jq -r -f "$SHARED/condense-transcript.jq" "$SR_EVAL_TRANSCRIPT" > "$root"
trajectory_condense "$SHARED/condense-transcript.jq" "$root"
guardrail_fired_check guarded-thing
printf '\nFIRED=%s COUNT=%s\n' "$GF_STATUS" "$GF_COUNT"
`
	cmd := exec.Command("sh", "-c", script)
	cmd.Env = append(os.Environ(), "SHARED="+shared, "SR_EVAL_TRANSCRIPT="+session)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sh: %v\n%s", err, out)
	}
	got := string(out)
	for _, want := range []string{
		"do the work",
		"=== SUB-AGENT agent-flat1:", "FLAT-REFUSAL-7731",
		"=== SUB-AGENT agent-wf1:", "WORKFLOW-REFUSAL-4402",
		"FIRED=fired COUNT=2",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the condensed trajectory / fired count lacks %q:\n%s", want, got)
		}
	}
}
