package e2e

// guardrail_fired_check must recognise a refusal the way the engine prints it —
// `[<plugin>/]file-guard/<name>` or `[<plugin>/]gate/<name>` — and must not take
// a path under .sloprail/ (a rule read, listed or committed) for one.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestT052_02_GuardrailFiredRecognisesTheEnginesRefusalForm(t *testing.T) {
	for name, tc := range map[string]struct {
		record string
		want   string
	}{
		"a plugin's file-guard": {
			`{"content":"changes:\n  - UNIT.md (modified) — sloprail-content/file-guard/unit-publish-approved, sloprail-content/file-guard/other"}`,
			"FIRED=fired COUNT=1"},
		"an example's file-guard": {
			`{"content":"changes:\n  - memories/runbook.md (modified) — file-guard/unit-publish-approved\nCommit these"}`,
			"FIRED=fired COUNT=1"},
		"a gate": {
			`{"content":"refused by sloprail/gate/unit-publish-approved."}`,
			"FIRED=fired COUNT=1"},
		"the older quoted form": {
			`{"content":"file-guard \"unit-publish-approved\" refused"}`,
			"FIRED=fired COUNT=1"},
		"a rule's path is not a refusal": {
			`{"content":"create mode 100644 .sloprail/file-guard/unit-publish-approved/file-guard.yaml\n.sloprail/gate/unit-publish-approved/gate.yaml"}`,
			"FIRED=never-fired COUNT=0"},
		"another rule with the name as a prefix": {
			`{"content":"— file-guard/unit-publish-approved-extra"}`,
			"FIRED=never-fired COUNT=0"},
	} {
		t.Run(name, func(t *testing.T) {
			session := filepath.Join(t.TempDir(), "session.jsonl")
			if err := os.WriteFile(session, []byte(tc.record+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("sh", "-c", `. "$SHARED/trajectory-health.sh"
guardrail_fired_check unit-publish-approved
printf 'FIRED=%s COUNT=%s\n' "$GF_STATUS" "$GF_COUNT"`)
			cmd.Env = append(os.Environ(), "SHARED="+sharedEval(t), "SR_EVAL_TRANSCRIPT="+session)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("sh: %v\n%s", err, out)
			}
			if !strings.Contains(string(out), tc.want) {
				t.Fatalf("got %q, want %q", out, tc.want)
			}
		})
	}
}
