package dispatch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sloprail/sloprail/internal/scriptexec"
)

// JudgeMocksEnv, when set, replaces the model behind every judge with a script: a JSON
// object mapping a judge id to the absolute path of a script. The id is
// `<plugin>/<nature>/<rule>/<template stem>` for a plugin's rule and
// `<nature>/<rule>/<template stem>` for the project's own (the stem of
// `tests-prove-it.md.j2` is `tests-prove-it`).
//
// The script gets the judge's input on stdin (the rendered prompt, then the check's payload
// as JSON) and prints `{"pass": bool, "reasoning": "..."}`. A script that fails to run,
// exits non-zero or prints anything else makes the check an ERROR, never a pass; so does a
// judge with no entry, so a model is never reached by accident while the variable is set.
// The variable is read by the engine itself, so it holds under sr-test, `sr-checks run`
// and the hooks alike.
const JudgeMocksEnv = "SR_CHECKS_JUDGE_MOCKS"

const defaultMockTimeout = 2 * time.Minute

// qualified is the rule's `<plugin>/<nature>/<name>`, or `<nature>/<name>` when the caller
// did not name it.
func (req Request) qualified() string {
	if req.Qualified != "" {
		return req.Qualified
	}
	return string(req.Nature) + "/" + req.GuardName
}

// JudgeID is a judge's id under SR_CHECKS_JUDGE_MOCKS.
func JudgeID(qualified, template string) string {
	stem := filepath.Base(template)
	stem = strings.TrimSuffix(stem, ".j2")
	stem = strings.TrimSuffix(stem, filepath.Ext(stem))
	return qualified + "/" + stem
}

// runJudgeMaybeMocked is the production runJudge: the model, unless SR_CHECKS_JUDGE_MOCKS is set.
func runJudgeMaybeMocked(j judgeCall) (Verdict, error) {
	raw, set := os.LookupEnv(JudgeMocksEnv)
	if !set {
		return runJudgeAgent(j)
	}
	return runMockedJudge(j, raw)
}

func runMockedJudge(j judgeCall, raw string) (Verdict, error) {
	var mocks map[string]string
	if err := json.Unmarshal([]byte(raw), &mocks); err != nil {
		return Verdict{}, fmt.Errorf("%s is not a JSON object of judge id to script path: %w", JudgeMocksEnv, err)
	}
	qualified := j.Qualified
	if qualified == "" {
		qualified = "gate/" + j.GuardName
	}
	id := JudgeID(qualified, j.Template)
	script, ok := mocks[id]
	if !ok {
		return Verdict{}, fmt.Errorf("the judge %q has no entry in %s (it holds %d); a judge is never sent to a model while the variable is set", id, JudgeMocksEnv, len(mocks))
	}
	rendered, refusal, err := renderJudgePrompt(j)
	if err != nil {
		return Verdict{}, err
	}
	if refusal != "" {
		return refuse(refusal), nil
	}
	timeout := j.Timeout
	if timeout <= 0 {
		timeout = defaultMockTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd, err := scriptexec.Command(ctx, script)
	if err != nil {
		return Verdict{}, fmt.Errorf("the mock for judge %q cannot be run: %w", id, err)
	}
	cmd.Dir = j.Dir
	cmd.Env = append(os.Environ(), j.Env...)
	cmd.Stdin = strings.NewReader(rendered + "\n\n" + string(j.InputJSON) + "\n")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return Verdict{}, fmt.Errorf("the mock for judge %q (%s) failed: %w: %s", id, script, err, strings.TrimSpace(stderr.String()))
	}
	var out struct {
		Pass      *bool  `json:"pass"`
		Reasoning string `json:"reasoning"`
	}
	dec := json.NewDecoder(&stdout)
	if err := dec.Decode(&out); err != nil || out.Pass == nil {
		return Verdict{}, fmt.Errorf("the mock for judge %q (%s) did not print {\"pass\": bool, \"reasoning\": string}", id, script)
	}
	if *out.Pass {
		return pass(), nil
	}
	reason := strings.TrimSpace(out.Reasoning)
	if reason == "" {
		reason = "the judge mock " + id + " failed this without saying why"
	}
	return refuse(reason), nil
}
