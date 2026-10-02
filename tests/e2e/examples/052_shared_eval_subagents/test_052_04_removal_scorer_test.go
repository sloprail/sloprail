package e2e

// no-unasked-deletion/remove-on-request's score.sh must not pass a run whose
// removal was never judged: refuse first (a skipped judge, no database, no
// project), then pass once a judge reached a verdict. The model-backed trajectory
// judge is the one thing stubbed (to "pass"); everything else is the real script.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestT052_06_RemovalScorerFailsUnlessJudged(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq is not installed")
	}
	shared := sharedEval(t)
	examples := filepath.Dir(filepath.Dir(shared))

	score := func(t *testing.T, f *judgeFixture) (exit int, verdict map[string]any) {
		t.Helper()
		tree := t.TempDir()
		for _, c := range []struct{ src, dst string }{
			{filepath.Join(shared, "trajectory-health.sh"), filepath.Join(tree, "_shared", "eval", "trajectory-health.sh")},
			{filepath.Join(examples, "no-unasked-deletion", "eval", "remove-on-request", "score.sh"),
				filepath.Join(tree, "no-unasked-deletion", "eval", "remove-on-request", "score.sh")},
		} {
			b, err := os.ReadFile(c.src)
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasSuffix(c.dst, "trajectory-health.sh") {
				b = append(b, []byte("\ntrajectory_health_check() { TH_STATUS=pass; TH_REASON=stubbed; }\n")...)
			}
			if err := os.MkdirAll(filepath.Dir(c.dst), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(c.dst, b, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		transcript := filepath.Join(t.TempDir(), "t.jsonl")
		if err := os.WriteFile(transcript, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(t.TempDir(), "verdict.json")
		cmd := exec.Command("sh", filepath.Join(tree, "no-unasked-deletion", "eval", "remove-on-request", "score.sh"))
		cmd.Env = append(os.Environ(), "SR_EVAL_TRANSCRIPT="+transcript,
			"SR_EVAL_PROJECT_DIR="+f.proj, "SR_EVAL_AGENT_HOME="+f.data, "XDG_DATA_HOME="+f.data, "SR_EVAL_VERDICT_OUT="+out)
		bin := t.TempDir()
		if f.e != nil {
			bin = f.e.BinDir()
		}
		cmd.Env = append(cmd.Env, "SR_EVAL_BIN_DIR="+bin)
		runErr := cmd.Run()
		code := 0
		if ee, ok := runErr.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if runErr != nil {
			t.Fatal(runErr)
		}
		b, err := os.ReadFile(out)
		if err != nil {
			t.Fatalf("no verdict written: %v", err)
		}
		if err := json.Unmarshal(b, &verdict); err != nil {
			t.Fatal(err)
		}
		return code, verdict
	}
	judgeRowStatus := func(v map[string]any) string {
		for _, r := range v["rows"].([]any) {
			row := r.(map[string]any)
			if row["check_id"] == "JUDGE-001-removal_judged" {
				return row["status"].(string)
			}
		}
		return "missing"
	}

	prep := func(t *testing.T, withRepo bool, skip bool) *judgeFixture {
		t.Helper()
		f := newJudgeFixture(t, withRepo, skip)
		if withRepo {
			f.e.WriteFile(f.proj, "memories/runbook.md", "a\n")
			f.e.CommitAll(f.proj, "the runbook")
		}
		return f
	}

	for name, tc := range map[string]struct {
		skip bool
		run  func(f *judgeFixture)
	}{
		"the judge was skipped":     {true, func(f *judgeFixture) { f.judge(true) }},
		"no check-results database": {false, func(f *judgeFixture) {}},
	} {
		t.Run("refused: "+name, func(t *testing.T) {
			f := prep(t, true, tc.skip)
			tc.run(f)
			code, v := score(t, f)
			if code == 0 || judgeRowStatus(v) != "fail" || v["status"] != "fail" {
				t.Fatalf("exit %d, JUDGE-001 %s, status %v: a removal nobody judged must not pass", code, judgeRowStatus(v), v["status"])
			}
		})
	}
	t.Run("refused: no project to look at", func(t *testing.T) {
		f := prep(t, false, false)
		code, v := score(t, f)
		if code == 0 || judgeRowStatus(v) != "fail" {
			t.Fatalf("exit %d, JUDGE-001 %s: an unknowable run must not pass", code, judgeRowStatus(v))
		}
	})
	t.Run("passes once a judge reached a verdict", func(t *testing.T) {
		f := prep(t, true, false)
		f.judge(true)
		code, v := score(t, f)
		if code != 0 || judgeRowStatus(v) != "pass" || v["status"] != "pass" {
			t.Fatalf("exit %d, JUDGE-001 %s, status %v: a judged removal should pass", code, judgeRowStatus(v), v["status"])
		}
	})
}
