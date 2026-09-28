package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// cite searches the whole SESSION a trajectory belongs to: the user pool in its
// root record alone, the tool_result pool in the root's and every sub-agent's.
// A sub-agent's tool output is recorded only in its own file, so without this a
// sub-agent could not cite what its own tools printed — and a `cite
// --source-types tool_result '<q>' && <cmd>` chain inside one would always fail.
//
// Hand-authored, for the one shape the mock cannot emit: it writes a sub-agent's
// tool calls into the ROOT record, where Claude Code writes them into the
// sub-agent's own <session>/subagents/agent-<id>.jsonl.

// subagentSessionFiles writes a root record and one sub-agent record beneath it
// and returns both paths.
func subagentSessionFiles(t *testing.T) (root, sub string) {
	t.Helper()
	dir := t.TempDir()
	root = filepath.Join(dir, "s-029-24.jsonl")
	sub = filepath.Join(dir, "s-029-24", "subagents", "agent-abc.jsonl")
	if err := os.MkdirAll(filepath.Dir(sub), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(path string, lines ...string) {
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(root,
		userMsg("u1", "the END USER asked for the retry budget"),
		toolCallLine("a1", false, "toolu_root", "cat notes.txt"),
		toolOutputLine("r1", false, "toolu_root", "SHARED-LINE from notes"),
	)
	write(sub,
		sidechainUserMsg("s0", "abc", "DISPATCH words: measure the retry budget"),
		toolCallLine("s1", true, "toolu_sub", "echo measured"),
		toolOutputLine("s2", true, "toolu_sub", "measured SUBOUT-4417 attempts"),
		toolCallLine("s3", true, "toolu_sub2", "cat notes.txt"),
		toolOutputLine("s4", true, "toolu_sub2", "SHARED-LINE from notes"),
	)
	return root, sub
}

func toolCallLine(uuid string, sidechain bool, id, command string) string {
	sc := "false"
	if sidechain {
		sc = "true"
	}
	return `{"type":"assistant","uuid":"` + uuid + `","isSidechain":` + sc + `,"message":{"role":"assistant","content":[` +
		`{"type":"tool_use","id":"` + id + `","name":"Bash","input":{"command":` + jsonStr(command) + `}}]}}`
}

func toolOutputLine(uuid string, sidechain bool, id, body string) string {
	sc := "false"
	if sidechain {
		sc = "true"
	}
	return `{"type":"user","uuid":"` + uuid + `","isSidechain":` + sc + `,"message":{"role":"user","content":[` +
		`{"type":"tool_result","tool_use_id":"` + id + `","content":` + jsonStr(body) + `}]}}`
}

func citeFrom(e *Env, path, sources, quote string) harness.Result {
	return e.CLIDirect(dirOf(path), "sr-session", "trajectory", "cite", "--path", path, "--source-types", sources, quote)
}

// T029_24: a sub-agent's tool output resolves under tool_result — from the
// root's trajectory and from the sub-agent's own alike — to the sub-agent's
// record, exit 0.
func TestT029_24_SubagentToolOutputResolves(t *testing.T) {
	e := New(t)
	root, sub := subagentSessionFiles(t)
	for _, from := range []string{root, sub} {
		res := citeFrom(e, from, "tool_result", "SUBOUT-4417 attempts")
		if res.Code != 0 {
			t.Fatalf("citing a sub-agent's tool output from %s exited %d, want 0:\n%s", from, res.Code, res.Output)
		}
		if got := nonEmptyLines(res.Output); len(got) != 1 || got[0] != sub+":3" {
			t.Errorf("from %s: got %q, want the sub-agent's line %s:3", from, got, sub)
		}
	}
}

// T029_25: the user pool is the root's alone. From a sub-agent's trajectory the
// end user's words resolve in the root (exit 0), and the dispatch prompt — the
// parent agent's words, present in the sub-agent's record — is not found (exit
// 1), not refused: the session's own user pool was searched and it is not there.
func TestT029_25_UserPoolIsTheRootsFromASubagent(t *testing.T) {
	e := New(t)
	root, sub := subagentSessionFiles(t)

	res := citeFrom(e, sub, "user", "END USER asked")
	if res.Code != 0 || strings.TrimSpace(res.Output) != root+":1" {
		t.Fatalf("the end user's words from a sub-agent's trajectory: exit %d, stdout %q; want 0 and %s:1", res.Code, res.Output, root)
	}
	for _, sources := range []string{"user", "user,tool_result"} {
		res = citeFrom(e, sub, sources, "DISPATCH words")
		if res.Code != 1 {
			t.Errorf("--source-types %s: citing the dispatch prompt exited %d, want 1 (not the user's words, not tool output):\n%s", sources, res.Code, res.Output)
		}
		// And says why, on stderr: the quote is the parent's prompt.
		if !res.Saw("That quote is from your dispatch prompt, written by the parent agent.") || !res.Saw("You are a sub-agent:") {
			t.Errorf("--source-types %s: the miss does not tell the sub-agent it quoted its dispatch prompt:\n%s", sources, res.Output)
		}
	}
}

// T029_26: one quote that two sub-agents printed, and the caller's own record
// did not, is ambiguous from the root: exit 2, with both candidates.
func TestT029_26_AmbiguousAcrossRecords(t *testing.T) {
	e := New(t)
	root, sub := subagentSessionFiles(t)
	other := filepath.Join(filepath.Dir(sub), "agent-def.jsonl")
	if err := os.WriteFile(other, []byte(strings.Join([]string{
		sidechainUserMsg("t0", "def", "DISPATCH words: measure it too"),
		toolCallLine("t1", true, "toolu_other", "echo measured"),
		toolOutputLine("t2", true, "toolu_other", "measured SUBOUT-4417 attempts"),
	}, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := citeFrom(e, root, "tool_result", "SUBOUT-4417")
	if res.Code != 2 {
		t.Fatalf("a quote in two sub-agents' records exited %d, want 2:\n%s", res.Code, res.Output)
	}
	want := []string{sub + ":3", other + ":3"}
	if got := nonEmptyLines(res.Output); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("candidates %q, want %q", got, want)
	}
}

// T029_27: one quote in the root's output and a sub-agent's — both read the
// same file — resolves in the CALLER's own record, exit 0: the entries are
// identical, so no longer quote could tell them apart, and the root could cite
// its own output before it dispatched anyone.
func TestT029_27_TheCallersOwnOutputFirst(t *testing.T) {
	e := New(t)
	root, sub := subagentSessionFiles(t)
	for from, want := range map[string]string{root: root + ":3", sub: sub + ":5"} {
		res := citeFrom(e, from, "tool_result", "SHARED-LINE")
		if res.Code != 0 {
			t.Fatalf("from %s: a quote in the caller's own record and a sub-agent's exited %d, want 0:\n%s", from, res.Code, res.Output)
		}
		if got := nonEmptyLines(res.Output); len(got) != 1 || got[0] != want {
			t.Errorf("from %s: got %q, want the caller's own line %s", from, got, want)
		}
	}
}
