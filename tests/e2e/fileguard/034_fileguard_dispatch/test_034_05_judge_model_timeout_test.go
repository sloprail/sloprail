package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This file covers a file-guard JUDGE with a per-judge MODEL and TIMEOUT
// (dot-dir-file-store Check model/timeout). It proves the whole config path
// end-to-end: the check's `model` reaches the sr-agent invocation (which builds
// `claude -p --model <resolved> …`, so the alias's harness-native name appears in
// the recorded argv), the custom `timeout` is accepted, and the judge still runs
// to a real verdict that blocks. The precise --model wiring is unit-tested in
// internal/dispatch; this is the end-to-end confirmation through the real
// binaries and the mock.

// judgeGuardModelTimeout is a file-guard judge carrying a custom model and
// timeout. size-lg resolves to `opus` under the Claude Code harness, which is
// what the recorded argv is asserted to carry.
const judgeGuardModelTimeout = `match: memories/**/*.md
checks:
  - judge: ./judge.md.j2
    model: size-lg
    timeout: 90s
`

// T034_11: a file-guard judge with `model: size-lg` and `timeout: 90s` blocks on
// a failing verdict, AND the check's model reached the harness — the recorded
// claude argv carries `--model opus` (size-lg's resolution). The custom timeout
// is generous, so the run completes normally under it.
func TestT034_11_JudgeModelAndTimeoutReachTheInvocation(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "substantive-memory", judgeGuardModelTimeout, map[string]string{"judge.md.j2": judgeGuardTemplate})
	commitGuards(t, proj) // keep the guard's own judge.md.j2 out of the cycle diff

	// A recording shim: writes the failing verdict AND records the claude argv, so
	// the test can assert the check's model reached the harness invocation.
	argvFile := filepath.Join(proj, "claude-argv.txt")
	e.InstallJudgeClaudeRecordingArgv(argvFile, `{"pass": false, "reasoning": "this memory is one word, not substantive"}`)

	e.Run(proj, "s-034-11", "write a thin memory", Turns("done",
		Write("w1", "memories/note.md", "meh"),
	))

	// The judge ran to a verdict and blocked — the custom model/timeout did not
	// break the judge path.
	blocks := e.BlockingErrorsFrom(proj, "s-034-11", "Stop")
	if len(blocks) == 0 {
		t.Fatalf("a file-guard judge with a custom model/timeout did not block on a failing verdict")
	}
	joined := strings.Join(blocks, "\n")
	if !containsStr(joined, "not substantive") {
		t.Errorf("the judge's reasoning did not reach the agent:\n%s", joined)
	}

	// The check's model reached the harness: sr-agent resolved size-lg to opus and
	// invoked `claude … --model opus …`, recorded here.
	argv, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("the recording shim did not capture the claude argv (was the judge invoked?): %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(argv)), "\n")
	if !hasAdjacentPair(lines, "--model", "opus") {
		t.Errorf("the judge's model (size-lg) did not reach the harness as `--model opus`; argv was:\n%s", string(argv))
	}
}

// hasAdjacentPair reports whether flag is immediately followed by value in the
// argv lines — `--model` then `opus` as two consecutive arguments, which is how
// sr-agent passes a resolved model to the harness.
func hasAdjacentPair(lines []string, flag, value string) bool {
	for i := 0; i+1 < len(lines); i++ {
		if lines[i] == flag && lines[i+1] == value {
			return true
		}
	}
	return false
}
