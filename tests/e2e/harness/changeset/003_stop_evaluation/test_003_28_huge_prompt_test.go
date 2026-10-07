package e2e

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T003_28: a judge prompt larger than the OS's ARG_MAX (~1 MB on macOS, argv and
// environment together) is judged. The prompt travels on stdin at every hop —
// dispatch -> sr-agent -> claude — never as an argument or an environment value,
// so it no longer dies with "sr-agent: Argument list too long" and wedges the
// session fail-closed.
func TestT003_28_AJudgePromptBeyondArgMaxIsJudged(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "docs/seed.md", "seed\n")
	e.CommitAll(proj, "the project")

	// A rubric of 1.3 MB: far past ARG_MAX, so it can only arrive through a pipe.
	big := rubric + strings.Repeat("filler line of the rubric that makes the prompt huge\n", 26000)
	e.FileGuard(proj, "docs", judgeRule, map[string]string{"rubric.md.j2": big})
	e.CommitAll(proj, "the judged rule")

	// The claude stand-in reads its prompt from STDIN (it is given none as an
	// argument), records how large the prompt and its own argv were, and answers.
	record := filepath.Join(t.TempDir(), "claude-record")
	e.InstallJudgeAgent(`#!/bin/sh
tmp="$(mktemp)"
cat > "$tmp"
argv=0
for arg in "$@"; do argv=$((argv + ${#arg})); done
printf 'stdin=%s argv=%s\n' "$(wc -c < "$tmp" | tr -d ' ')" "$argv" >> ` + shellQ(record) + `
out="$(sed -n 's/.*Write your answer to the file \([^ ]*\)\. .*/\1/p' "$tmp" | tail -1)"
rm -f "$tmp"
if [ -n "$out" ]; then
  printf '%s\n' '{"pass": false, "reasoning": "HUGE-JUDGE-SAYS-NO"}' > "$out"
fi
exit 0
`)

	e.Run(proj, "s-003-28", "write the docs", Turns("done", harness.CommitFile("c1", "docs/a.md", "the release is Friday", "add a")))

	joined := strings.Join(e.BlockingErrorsFrom(proj, "s-003-28", "Stop"), "\n")
	if strings.Contains(joined, "Argument list too long") {
		t.Fatalf("the prompt still travels as an argument or environment value:\n%s", joined)
	}
	if !strings.Contains(joined, "HUGE-JUDGE-SAYS-NO") {
		t.Fatalf("the huge-prompt judge was never reached, or its refusal was lost:\n%s", joined)
	}
	body, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("claude was never run: %v", err)
	}
	var stdin, argv int
	for _, f := range strings.Fields(strings.Split(string(body), "\n")[0]) {
		if v, ok := strings.CutPrefix(f, "stdin="); ok {
			stdin, _ = strconv.Atoi(v)
		}
		if v, ok := strings.CutPrefix(f, "argv="); ok {
			argv, _ = strconv.Atoi(v)
		}
	}
	if stdin < 1<<20 {
		t.Fatalf("claude read %d bytes on stdin; want the whole >1 MB prompt", stdin)
	}
	if argv > 64<<10 {
		t.Fatalf("claude's argv holds %d bytes; the prompt must not be an argument", argv)
	}
}

func shellQ(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
