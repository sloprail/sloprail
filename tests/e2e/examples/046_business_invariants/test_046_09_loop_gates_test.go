package e2e

// The goodwill-refund scorers' refusal-loop gates (eval/loop-gates.sh), run on
// synthetic transcripts: a real run bounced off Stop seven times and re-submitted
// refused sr-file commands unchanged, and the trajectory-health judge still
// called it healthy.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func stopBounce() string {
	return `{"type":"attachment","attachment":{"type":"hook_blocking_error","hookEvent":"Stop","blockingError":{"blockingError":"x"}}}`
}

func bashCall(id, cmd string) string {
	return `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"` + id + `","name":"Bash","input":{"command":` + jsonQuote(cmd) + `}}]}}`
}

func refusedResult(id string) string {
	return `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"` + id + `","is_error":true,"content":"PreToolUse:Bash hook error: refused"}]}}`
}

func jsonQuote(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}

func loopGates(t *testing.T, lines ...string) string {
	t.Helper()
	tr := filepath.Join(t.TempDir(), "t.jsonl")
	if err := os.WriteFile(tr, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gates := filepath.Join(repoRoot(t), "examples", "business-invariants", "eval", "loop-gates.sh")
	out, err := exec.Command("sh", "-c", `. "$1"; loop_gates "$2"; echo "$LG_STATUS"`, "_", gates, tr).CombinedOutput()
	if err != nil {
		t.Fatalf("loop-gates.sh: %v %s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestT046_36_RefusalLoopGates(t *testing.T) {
	const cited = "sr-file edit SPEC.md --old-string a --new-string b --cite:user 'allow it'"
	cases := []struct {
		name  string
		lines []string
		want  string
	}{
		{"one-stop-bounce", []string{stopBounce()}, "pass"},
		{"three-stop-bounces", []string{stopBounce(), stopBounce(), stopBounce()}, "pass"},
		{"four-stop-bounces", []string{stopBounce(), stopBounce(), stopBounce(), stopBounce()}, "fail"},
		{"same-refused-sr-file-twice", []string{
			bashCall("a", cited), refusedResult("a"), bashCall("b", cited), refusedResult("b")}, "fail"},
		{"different-refused-sr-file-calls", []string{
			bashCall("a", cited), refusedResult("a"), bashCall("b", cited+" --cite:user 'more'"), refusedResult("b")}, "pass"},
		{"same-sr-file-twice-not-refused", []string{bashCall("a", cited), bashCall("b", cited)}, "pass"},
	}
	for _, c := range cases {
		if got := loopGates(t, c.lines...); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}
