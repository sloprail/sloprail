package e2e

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
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
// A harness whose record holds the tools' results (CapRecordHoldsToolResults) is given
// a hand-authored session, for the one shape the mock cannot emit: Claude Code writes
// a sub-agent's tool calls into the sub-agent's own <session>/subagents/agent-<id>.jsonl,
// and the mock writes them into the ROOT record. A harness whose record holds none
// (Cursor: sloprail keeps the outputs, a sub-agent is a conversation of its own) is
// driven through its mock, which writes exactly that shape; the line numbers are then the
// harness's own (a result's is not a transcript line), so those cases check the record a
// citation names and that it names a line, not which one.

// the words of the session, one per pool.
const (
	userWords     = "the END USER asked for the retry budget"
	dispatchWords = "DISPATCH words: measure the retry budget"
	dispatchOther = "DISPATCH words: measure it too"
)

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
		userMsg("u1", userWords),
		toolCallLine("a1", false, "toolu_root", "cat notes.txt"),
		toolOutputLine("r1", false, "toolu_root", "SHARED-LINE from notes"),
	)
	write(sub,
		sidechainUserMsg("s0", "abc", dispatchWords),
		toolCallLine("s1", true, "toolu_sub", "echo measured"),
		toolOutputLine("s2", true, "toolu_sub", "measured SUBOUT-4417 attempts"),
		toolCallLine("s3", true, "toolu_sub2", "cat notes.txt"),
		toolOutputLine("s4", true, "toolu_sub2", "SHARED-LINE from notes"),
	)
	return root, sub
}

// mockedSubagentSession runs the mock through the same session: the root prints the
// shared line, then dispatches the sub-agent (and a second one when other is set), each
// printing what the hand-authored records hold. Returns the root's record and the
// sub-agent records by the dispatch prompt each was given.
func mockedSubagentSession(t *testing.T, e *Env, other bool) (root string, subs map[string]string) {
	t.Helper()
	proj := e.Project()
	e.GitInit(proj)
	script := func(name string, s harness.Scenario) string {
		path := filepath.Join(proj, name)
		if err := s.Script(path); err != nil {
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
		harness.Dispatch("d1", dispatchWords, first, ""),
	}
	if other {
		second := script("sub-def.sh", Turns("sub done", Bash("sc1", "echo measured SUBOUT-4417 attempts")))
		turns = append(turns, harness.Dispatch("d2", dispatchOther, second, ""))
	}
	const sess = "s-029-24"
	e.Run(proj, sess, userWords, Turns("done", turns...))

	subs = map[string]string{}
	for _, p := range e.SubagentRecordPaths(proj, sess) {
		body := readFile(t, p)
		for _, words := range []string{dispatchWords, dispatchOther} {
			if strings.Contains(body, words) {
				subs[words] = p
			}
		}
	}
	if len(subs) != map[bool]int{false: 1, true: 2}[other] {
		t.Fatalf("premise: the mock should have written one record per dispatched sub-agent, found %v", subs)
	}
	return e.TranscriptPath(proj, sess), subs
}

// subagentSession is the session the cases read, whatever the harness: the root's
// record, the first sub-agent's, the second's when other is set, and whether the lines
// a citation names are the ones the hand-authored records fix.
func subagentSession(t *testing.T, e *Env, other bool) (root, sub, second string, exact bool) {
	t.Helper()
	if !harness.HasCap(t, harness.CapRecordHoldsToolResults) {
		root, subs := mockedSubagentSession(t, e, other)
		return root, subs[dispatchWords], subs[dispatchOther], false
	}
	root, sub = subagentSessionFiles(t)
	if other {
		second = filepath.Join(filepath.Dir(sub), "agent-def.jsonl")
		if err := os.WriteFile(second, []byte(strings.Join([]string{
			sidechainUserMsg("t0", "def", dispatchOther),
			toolCallLine("t1", true, "toolu_other", "echo measured"),
			toolOutputLine("t2", true, "toolu_other", "measured SUBOUT-4417 attempts"),
		}, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root, sub, second, true
}

// cited reports whether a cite line names record path, at line when the harness fixes
// it (exact) and at some line otherwise.
func cited(got, path string, line int, exact bool) bool {
	if exact {
		return got == path+":"+strconv.Itoa(line)
	}
	n, err := strconv.Atoi(strings.TrimPrefix(got, path+":"))
	return strings.HasPrefix(got, path+":") && err == nil && n > 0
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
	root, sub, _, exact := subagentSession(t, e, false)
	for _, from := range []string{root, sub} {
		res := citeFrom(e, from, "tool_result", "SUBOUT-4417 attempts")
		if res.Code != 0 {
			t.Fatalf("citing a sub-agent's tool output from %s exited %d, want 0:\n%s", from, res.Code, res.Output)
		}
		if got := nonEmptyLines(res.Output); len(got) != 1 || !cited(got[0], sub, 3, exact) {
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
	root, sub, _, _ := subagentSession(t, e, false)

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
	root, sub, other, exact := subagentSession(t, e, true)
	res := citeFrom(e, root, "tool_result", "SUBOUT-4417")
	if res.Code != 2 {
		t.Fatalf("a quote in two sub-agents' records exited %d, want 2:\n%s", res.Code, res.Output)
	}
	paths := []string{sub, other}
	sort.Strings(paths) // the session's records in lexical order
	got := nonEmptyLines(res.Output)
	ok := len(got) == len(paths)
	for i := 0; ok && i < len(paths); i++ {
		ok = cited(got[i], paths[i], 3, exact)
	}
	if !ok {
		t.Errorf("candidates %q, want the line of each of %q", got, paths)
	}
}

// T029_27: one quote in the root's output and a sub-agent's — both read the
// same file — resolves in the CALLER's own record, exit 0: the entries are
// identical, so no longer quote could tell them apart, and the root could cite
// its own output before it dispatched anyone.
func TestT029_27_TheCallersOwnOutputFirst(t *testing.T) {
	e := New(t)
	root, sub, _, exact := subagentSession(t, e, false)
	for from, want := range map[string]string{root: root, sub: sub} {
		res := citeFrom(e, from, "tool_result", "SHARED-LINE")
		if res.Code != 0 {
			t.Fatalf("from %s: a quote in the caller's own record and a sub-agent's exited %d, want 0:\n%s", from, res.Code, res.Output)
		}
		line := 3
		if want == sub {
			line = 5
		}
		if got := nonEmptyLines(res.Output); len(got) != 1 || !cited(got[0], want, line, exact) {
			t.Errorf("from %s: got %q, want the caller's own line %s:%d", from, got, want, line)
		}
	}
}
