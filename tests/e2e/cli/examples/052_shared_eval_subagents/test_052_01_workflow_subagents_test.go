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
	"fmt"
	"github.com/sloprail/sloprail/tests/e2e/harness"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func sharedEval(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	dir := filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "..", "examples", "_shared", "eval")
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
	cmd.Env = append(harness.HostEnv(), "SHARED="+shared, "SR_EVAL_TRANSCRIPT="+session)
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

// condenseRun builds a session of a long root (ending in a Stop refusal and a
// final message) and subs sub-agents, each ending in refusals refusals of its
// own, and returns what the judge would read and the root's condensed size.
func condenseRun(t *testing.T, subs, refusals int) (string, int) {
	t.Helper()
	shared := sharedEval(t)
	session := filepath.Join(t.TempDir(), "s-052.jsonl")
	text := func(uuid, ts, s string) string {
		return fmt.Sprintf(`{"type":"assistant","uuid":%q,"timestamp":%q,"message":{"role":"assistant","content":[{"type":"text","text":%q}]}}`, uuid, ts, s)
	}
	refusal := func(event, s string) string {
		return fmt.Sprintf(`{"type":"attachment","uuid":"x","attachment":{"type":"hook_blocking_error","hookEvent":%q,"blockingError":{"blockingError":%q}}}`, event, s)
	}
	filler := strings.Repeat("the agent keeps working through the task ", 18)
	var root []string
	root = append(root, `{"type":"user","uuid":"u1","message":{"role":"user","content":"do the work"}}`)
	for i := 0; i < 90; i++ {
		root = append(root, text(fmt.Sprintf("r%d", i), "2026-09-28T00:00:00Z", fmt.Sprintf("ROOT-STEP-%d %s", i, filler)))
	}
	root = append(root, refusal("Stop", "ROOT-STOP-REFUSAL-9001 memories/a.md was changed without a citation"),
		text("rz", "2026-09-28T09:00:00Z", "FINAL-MESSAGE-9002 all done"))
	writeLines(t, session, root...)
	for s := 1; s <= subs; s++ {
		var sub []string
		sub = append(sub, `{"type":"user","uuid":"s0","isSidechain":true,"message":{"role":"user","content":"sub-task"}}`)
		for i := 0; i < 40; i++ {
			sub = append(sub, text(fmt.Sprintf("s%d", i), fmt.Sprintf("2026-09-28T01:%02d:00Z", s), fmt.Sprintf("SUB-%d-STEP-%d %s", s, i, filler)))
		}
		for r := 1; r <= refusals; r++ {
			sub = append(sub, refusal("SubagentStop", fmt.Sprintf("SUB-REFUSAL-%d-%d the file-guard refused: %s", s, r, strings.Repeat("memories/tasks/x/TASK.md was changed without a citation ", 4))))
		}
		// Named so that id order is not start order.
		writeLines(t, filepath.Join(strings.TrimSuffix(session, ".jsonl"), "subagents", fmt.Sprintf("agent-a%02d5b2e5c7ac5da1d-sub%d.jsonl", (subs+1-s), s)), sub...)
	}
	script := `. "$SHARED/trajectory-health.sh"
root="$(mktemp)"
jq -r -f "$SHARED/condense-transcript.jq" "$SR_EVAL_TRANSCRIPT" > "$root"
wc -c < "$root" >&2
trajectory_condense "$SHARED/condense-transcript.jq" "$root"
`
	cmd := exec.Command("sh", "-c", script)
	cmd.Env = append(harness.HostEnv(), "SHARED="+shared, "SR_EVAL_TRANSCRIPT="+session)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("sh: %v\n%s", err, stderr.String())
	}
	n, _ := strconv.Atoi(strings.TrimSpace(stderr.String()))
	return stdout.String(), n
}

// checkCondensed asserts what every condensed trajectory owes the judge: it
// fits the budget by construction (no cut at the budget), ends on a whole line,
// and every agent's count line tells the truth about the refusals shown under
// it.
func checkCondensed(t *testing.T, got string) {
	t.Helper()
	if len(got) > 60000 {
		t.Errorf("the condensed trajectory is %d bytes, over the 60000 budget", len(got))
	}
	if strings.Contains(got, "cut by the scorer at the budget") {
		t.Errorf("the budget was overrun and the output cut")
	}
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("the last line is not whole: %q", got[max(0, len(got)-80):])
	}
	header := regexp.MustCompile(`^--- \[(.+)\] (\d+) of (\d+) refusal\(s\) shown ---$`)
	lines := strings.Split(got, "\n")
	shown := map[string]string{}
	for i := 0; i < len(lines); i++ {
		m := header.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		n := 0
		for j := i + 1; j < len(lines) && strings.HasPrefix(lines[j], "(x"); j++ {
			n++
		}
		if strconv.Itoa(n) != m[2] {
			t.Errorf("%s: the count line says %s shown, %d are", m[1], m[2], n)
		}
		shown[m[1]] = m[2] + " of " + m[3]
	}
	notice := regexp.MustCompile(`=== SUB-AGENT (\S+): left out by the scorer for length \((\d+ of \d+) refusal\(s\) shown above\) ===`)
	for _, m := range notice.FindAllStringSubmatch(got, -1) {
		if shown[m[1]] != m[2] {
			t.Errorf("%s: left out saying %q, but REFUSALS shows %q", m[1], m[2], shown[m[1]])
		}
	}
}

// T052_02: a run too long for the judge's budget — a 68k-character root and
// eight sub-agents, each ending in a SubagentStop refusal, the root ending in a
// Stop refusal and its final message. Every refusal, the final message and the
// scorer's omission marker are in what the judge reads, and it fits the budget.
func TestT052_02_TruncationKeepsEveryRefusal(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not installed")
	}
	got, rootSize := condenseRun(t, 8, 1)
	if rootSize < 60000 {
		t.Fatalf("the root condenses to %d bytes, under the budget, so this tests nothing", rootSize)
	}
	checkCondensed(t, got)
	want := []string{"ROOT-STOP-REFUSAL-9001", "FINAL-MESSAGE-9002", "omitted by the scorer"}
	for s := 1; s <= 8; s++ {
		want = append(want, fmt.Sprintf("SUB-REFUSAL-%d-1", s))
	}
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("the condensed trajectory lacks %q", w)
		}
	}
	if i, j := strings.Index(got, "SUB-1-STEP"), strings.Index(got, "SUB-8-STEP"); i >= 0 && j >= 0 && j < i {
		t.Errorf("the sub-agents are not in the order they started")
	}
}

// T052_03: thirty sub-agents of ten refusals each overflow even the refusal
// section. Every agent is still named, with an honest count of what is shown;
// the notices of those left out fit the budget; the first and the last to start
// keep their steps; and the output ends on a whole line within the budget.
func TestT052_03_ManySubagentsAreAllAccountedFor(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not installed")
	}
	got, _ := condenseRun(t, 30, 10)
	checkCondensed(t, got)
	for s := 1; s <= 30; s++ {
		name := fmt.Sprintf("agent-a%02d5b2e5c7ac5da1d-sub%d", 31-s, s)
		if !strings.Contains(got, "["+name+"]") {
			t.Errorf("%s has no count line in REFUSALS", name)
		}
		if !strings.Contains(got, "=== SUB-AGENT "+name+":") {
			t.Errorf("%s is neither shown nor noticed as left out", name)
		}
	}
	for _, w := range []string{"ROOT-STOP-REFUSAL-9001", "FINAL-MESSAGE-9002", "SUB-1-STEP", "SUB-30-STEP", "left out by the scorer for length"} {
		if !strings.Contains(got, w) {
			t.Errorf("the condensed trajectory lacks %q", w)
		}
	}
}
