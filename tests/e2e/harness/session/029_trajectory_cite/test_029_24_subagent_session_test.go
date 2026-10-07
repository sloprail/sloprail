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
// On a harness that cannot tie a sub-agent to its parent (CapSubagentParentLink absent:
// Cursor) the same cases assert the declared behaviour instead: cite called from a
// sub-agent always errors (exit 3), and what a sub-agent printed is not reachable from the
// main agent (exit 1). Those run the mock, which writes the sub-agent's conversation.
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

// unlinkedSession runs the mock through the session the fixtures hold, for a harness that
// cannot link a sub-agent to its parent: the root prints the shared line and dispatches one
// sub-agent (two when other), each printing the lines the fixtures hold. Returns the root's
// record and the sub-agents' by dispatch prompt.
func unlinkedSession(t *testing.T, e *Env, other bool) (root string, subs map[string]string) {
	t.Helper()
	proj := e.Project()
	e.GitInit(proj)
	script := func(name string, sc harness.Scenario) string {
		path := filepath.Join(proj, name)
		if err := sc.Script(path); err != nil {
			t.Fatalf("write sub-agent scenario: %v", err)
		}
		return path
	}
	first := script("sub-abc.sh", Turns("sub done",
		Bash("sb1", "echo measured SUBOUT-4417 attempts"),
		Bash("sb2", "echo SHARED-LINE from notes"),
	))
	turns := []harness.Turn{
		Bash("rb1", "echo SHARED-LINE from notes"),
		harness.Dispatch("d1", "DISPATCH words: measure the retry budget", first, ""),
	}
	if other {
		second := script("sub-def.sh", Turns("sub done", Bash("sc1", "echo measured SUBOUT-4417 attempts")))
		turns = append(turns, harness.Dispatch("d2", "DISPATCH words: measure it too", second, ""))
	}
	const sess = "s-029-24"
	e.Run(proj, sess, "the END USER asked for the retry budget", Turns("done", turns...))
	subs = map[string]string{}
	for _, p := range e.SubagentRecordPaths(proj, sess) {
		body := readFile(t, p)
		for _, words := range []string{"measure the retry budget", "measure it too"} {
			if strings.Contains(body, words) {
				subs[words] = p
			}
		}
	}
	want := 1
	if other {
		want = 2
	}
	if len(subs) != want {
		t.Fatalf("premise: the mock should have written one record per dispatched sub-agent, found %v", subs)
	}
	return e.TranscriptPath(proj, sess), subs
}

// requireCiteRefused checks that cite from the sub-agent's trajectory is refused outright
// (exit 3) whatever the pool, with the reason on stderr.
func requireCiteRefused(t *testing.T, e *Env, sub string) {
	t.Helper()
	for _, sources := range []string{"user", "tool_result", "user,tool_result"} {
		res := citeFrom(e, sub, sources, "SHARED-LINE")
		if res.Code != 3 {
			t.Errorf("--source-types %s: cite from a sub-agent exited %d, want 3:\n%s", sources, res.Code, res.Output)
		}
		if !res.Saw("cannot be tied to the user's conversation") || !res.Saw("main agent") {
			t.Errorf("--source-types %s: the refusal does not say why:\n%s", sources, res.Output)
		}
	}
}

// citeFromFixture is citeFrom over a record this test forged in Claude Code's layout
// (subagentSessionFiles), which is read as that harness's record whichever harness drives the run.
func citeFromFixture(e *Env, path, sources, quote string) harness.Result {
	return e.CLIDirectEnv(dirOf(path), []string{"SLOPRAIL_HARNESS=claude"}, "sr-session", "trajectory", "cite", "--path", path, "--source-types", sources, quote)
}

func citeFrom(e *Env, path, sources, quote string) harness.Result {
	return e.CLIDirect(dirOf(path), "sr-session", "trajectory", "cite", "--path", path, "--source-types", sources, quote)
}

// T029_24: a sub-agent's tool output resolves under tool_result — from the
// root's trajectory and from the sub-agent's own alike — to the sub-agent's
// record, exit 0.
func TestT029_24_SubagentToolOutputResolves(t *testing.T) {
	e := New(t)
	if !harness.HasCap(t, harness.CapSubagentParentLink) {
		root, subs := unlinkedSession(t, e, false)
		sub := subs["measure the retry budget"]
		requireCiteRefused(t, e, sub)
		// From the main agent the sub-agent's output is not reachable: not found, not refused.
		if res := citeFrom(e, root, "tool_result", "SUBOUT-4417 attempts"); res.Code != 1 {
			t.Errorf("a sub-agent's output cited from the main agent exited %d, want 1:\n%s", res.Code, res.Output)
		}
		return
	}
	root, sub := subagentSessionFiles(t)
	for _, from := range []string{root, sub} {
		res := citeFromFixture(e, from, "tool_result", "SUBOUT-4417 attempts")
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
	if !harness.HasCap(t, harness.CapSubagentParentLink) {
		_, subs := unlinkedSession(t, e, false)
		sub := subs["measure the retry budget"]
		requireCiteRefused(t, e, sub)
		// The user's words and the dispatch prompt alike: nothing is cited from a sub-agent.
		for _, quote := range []string{"END USER asked", "DISPATCH words"} {
			if res := citeFrom(e, sub, "user", quote); res.Code != 3 {
				t.Errorf("cite %q from a sub-agent exited %d, want 3:\n%s", quote, res.Code, res.Output)
			}
		}
		return
	}
	root, sub := subagentSessionFiles(t)

	res := citeFromFixture(e, sub, "user", "END USER asked")
	if res.Code != 0 || strings.TrimSpace(res.Output) != root+":1" {
		t.Fatalf("the end user's words from a sub-agent's trajectory: exit %d, stdout %q; want 0 and %s:1", res.Code, res.Output, root)
	}
	for _, sources := range []string{"user", "user,tool_result"} {
		res = citeFromFixture(e, sub, sources, "DISPATCH words")
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
	if !harness.HasCap(t, harness.CapSubagentParentLink) {
		root, subs := unlinkedSession(t, e, true)
		for _, sub := range subs {
			requireCiteRefused(t, e, sub)
		}
		// Neither sub-agent's output is reachable from the main agent, so there is no ambiguity.
		if res := citeFrom(e, root, "tool_result", "SUBOUT-4417"); res.Code != 1 {
			t.Errorf("a quote only sub-agents printed, cited from the main agent, exited %d, want 1:\n%s", res.Code, res.Output)
		}
		return
	}
	root, sub := subagentSessionFiles(t)
	other := filepath.Join(filepath.Dir(sub), "agent-def.jsonl")
	if err := os.WriteFile(other, []byte(strings.Join([]string{
		sidechainUserMsg("t0", "def", "DISPATCH words: measure it too"),
		toolCallLine("t1", true, "toolu_other", "echo measured"),
		toolOutputLine("t2", true, "toolu_other", "measured SUBOUT-4417 attempts"),
	}, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := citeFromFixture(e, root, "tool_result", "SUBOUT-4417")
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
	if !harness.HasCap(t, harness.CapSubagentParentLink) {
		root, subs := unlinkedSession(t, e, false)
		requireCiteRefused(t, e, subs["measure the retry budget"])
		// The main agent's own output resolves in its own record, however a sub-agent printed it too.
		res := citeFrom(e, root, "tool_result", "SHARED-LINE")
		if res.Code != 0 || !strings.HasPrefix(strings.TrimSpace(res.Output), root+":") {
			t.Errorf("the main agent's own output: exit %d, stdout %q; want 0 and a line of %s", res.Code, res.Output, root)
		}
		return
	}
	root, sub := subagentSessionFiles(t)
	for from, want := range map[string]string{root: root + ":3", sub: sub + ":5"} {
		res := citeFromFixture(e, from, "tool_result", "SHARED-LINE")
		if res.Code != 0 {
			t.Fatalf("from %s: a quote in the caller's own record and a sub-agent's exited %d, want 0:\n%s", from, res.Code, res.Output)
		}
		if got := nonEmptyLines(res.Output); len(got) != 1 || got[0] != want {
			t.Errorf("from %s: got %q, want the caller's own line %s", from, got, want)
		}
	}
}
