package harness

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/internal/transcript"
	"gopkg.in/yaml.v3"
)

// Refused reads the real refusal tool_result, not the presence of a word.
//
// The helper this replaced substring-scanned the whole mock stream for
// "deny"/"denied"/"block"/"blocked". The stream contains the agent's own tool
// input — the path it asked to write, and the content — so a fully PERMITTED
// write to `deny/notes.md` was reported as refused. Two copies of that helper
// shipped, in 013 and 014, and every test asserting a refusal would have passed
// on a permitted write as soon as a fixture used such a path.
//
// The refusal streams below are the shape real Claude Code writes (harness-mocks
// EVIDENCE.md: 67 such tool_results in real transcripts, all is_error).
func TestRefused_ReadsTheMarkerNotAWord(t *testing.T) {
	cases := []struct {
		name    string
		output  string
		refused bool
	}{
		{
			// The case that exposed the defect. A guardrail permitting
			// everything, writing to a path containing a trigger word.
			name: "permitted write to a path containing deny",
			output: `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"w1","name":"Write","input":{"file_path":"deny/notes.md","content":"hello"}}]}}
{"message":{"content":[{"content":"File created successfully at: deny/notes.md (file state is current in your context — no need to Read it back)","is_error":false,"tool_use_id":"w1","type":"tool_result"}],"role":"user"},"type":"user"}`,
			refused: false,
		},
		{
			// The same trap from the content side rather than the path.
			name: "permitted write whose content says blocked",
			output: `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"w1","name":"Write","input":{"file_path":"a/notes.md","content":"the request was blocked"}}]}}
{"message":{"content":[{"content":"File created successfully at: a/notes.md (file state is current in your context — no need to Read it back)","is_error":false,"tool_use_id":"w1","type":"tool_result"}],"role":"user"},"type":"user"}`,
			refused: false,
		},
		{
			// The channel this engine uses: permissionDecision "deny", exit 0.
			// Real Claude Code answers the call with this tool_result.
			name:    "refused via permissionDecision",
			output:  mustDriver().RefusalOutput("guarded/ is off limits"),
			refused: true,
		},
		{
			// Exit 2: the same tool_result, the reason quoted as
			// "[<command>]: <stderr>".
			name:    "refused via exit 2 on stderr",
			output:  mustDriver().RefusalOutput("[sr-session pre-tool]: guarded/ is off limits"),
			refused: true,
		},
		{
			// The refusal's words in the agent's own tool input are not a
			// refusal.
			name: "the agent writes the refusal text into a file",
			output: `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"w1","name":"Write","input":{"file_path":"a.md","content":"PreToolUse:Write hook error: nope"}}]}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"w1","content":"File created successfully at: a.md (file state is current in your context — no need to Read it back)","is_error":false}]}}`,
			refused: false,
		},
		{
			// Nor is a tool's successful output that happens to quote one.
			name:    "a successful tool result quoting a refusal",
			output:  `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"b1","content":"PreToolUse:Bash hook error: from a log file","is_error":false}]}}`,
			refused: false,
		},
		{
			// The mock's former invented text is not a real refusal and must
			// not count as one.
			name:    "the mock's old invented marker",
			output:  `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"w1","content":"Tool call blocked by a PreToolUse hook: x","is_error":true}]}}`,
			refused: false,
		},
		{
			name:    "a clean permitted write",
			output:  `{"message":{"content":[{"content":"File created successfully at: a/notes.md (file state is current in your context — no need to Read it back)","is_error":false,"tool_use_id":"w1","type":"tool_result"}],"role":"user"},"type":"user"}`,
			refused: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := Result{Output: tc.output}
			if got := r.Refused(); got != tc.refused {
				t.Errorf("Refused() = %v, want %v", got, tc.refused)
			}
			if got := r.Permitted(); got != !tc.refused {
				t.Errorf("Permitted() = %v, want %v", got, !tc.refused)
			}
		})
	}
}

// TestHookRefusalReasonAgreesWithTheEngine: the harness's copy of the refusal
// recogniser must answer as the engine's does.
func TestHookRefusalReasonAgreesWithTheEngine(t *testing.T) {
	for _, body := range []string{
		"PreToolUse:Write hook error: guarded/ is off limits",
		"PreToolUse:Bash hook error: [sr-session pre-tool]: no",
		"PreToolUse:Bash hook error: ",
		"PostToolUse:Bash hook error: x",
		"Tool call blocked by a PreToolUse hook: x",
		"PreToolUse:Write hook failed",
		"",
	} {
		gotR, gotOK := hookRefusalReason(body)
		wantR, wantOK := transcript.HookRefusalReason(body)
		if gotR != wantR || gotOK != wantOK {
			t.Errorf("%q: harness (%q, %v), engine (%q, %v)", body, gotR, gotOK, wantR, wantOK)
		}
	}
}

// TestStopContinuations counts a refused Stop only once the record shows the
// agent went on past it. The records are the order the mock writes, which is
// real Claude Code's: feedback turn, attachment, that Stop's own summary.
func TestStopContinuations(t *testing.T) {
	const (
		feedback = `{"type":"user","isMeta":true,"message":{"role":"user","content":"Stop hook feedback:\nnot acceptable"}}`
		attach   = `{"type":"attachment","attachment":{"type":"hook_blocking_error","hookEvent":"Stop","blockingError":{"blockingError":"not acceptable"}}}`
		summary  = `{"type":"system","subtype":"stop_hook_summary","preventedContinuation":false}`
		capWarn  = `{"type":"system","subtype":"informational","isMeta":false}`
		reply    = `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"fixed"}]}}`
		prompt   = `{"type":"user","message":{"role":"user","content":"go"}}`
	)
	join := func(ls ...string) string { return strings.Join(ls, "\n") }
	cases := []struct {
		name   string
		record string
		want   int
	}{
		{"a clean end", join(prompt, reply, summary), 0},
		{"refused, then the harness gave up at its cap", join(prompt, feedback, attach, summary, capWarn), 0},
		{"refused, continued, ended at a later Stop", join(prompt, feedback, attach, summary, summary), 1},
		{"refused, continued with a reply", join(prompt, feedback, attach, summary, reply, summary), 1},
		{"refused twice, the second given up", join(prompt, feedback, attach, summary, feedback, attach, summary, capWarn), 1},
		{"a non-meta user turn saying the words is not feedback", join(`{"type":"user","message":{"role":"user","content":"Stop hook feedback:\nx"}}`, summary, summary), 0},
	}
	for _, tc := range cases {
		if got := len(stopContinuationsIn(tc.record)); got != tc.want {
			t.Errorf("%s: %d continuations, want %d", tc.name, got, tc.want)
		}
	}
}

// DisablePluginGuardrail's config merge: parsed and rewritten, never appended to as
// text. Appending "  - name" at the end of the file breaks when `disabled:` is not the
// last key (the entry lands under the key that follows) or the file has no trailing
// newline (the entry is glued onto the last line).
func TestMergeDisabled(t *testing.T) {
	read := func(t *testing.T, body string) map[string]any {
		t.Helper()
		var m map[string]any
		if err := yaml.Unmarshal([]byte(body), &m); err != nil {
			t.Fatalf("the merged config is not valid YAML: %v\n%s", err, body)
		}
		return m
	}
	disabled := func(t *testing.T, m map[string]any) []string {
		t.Helper()
		var out []string
		list, _ := m["disabled"].([]any)
		for _, v := range list {
			out = append(out, v.(string))
		}
		return out
	}
	cases := []struct {
		name, body string
		want       []string
		keep       map[string]any
	}{
		{"empty file", "", []string{"p/gate/x"}, nil},
		{"disabled is not the last key", "disabled:\n  - p/gate/old\nstop_hook_block_cap: 3\n", []string{"p/gate/old", "p/gate/x"}, map[string]any{"stop_hook_block_cap": 3}},
		{"no trailing newline", "stop_hook_block_cap: 3", []string{"p/gate/x"}, map[string]any{"stop_hook_block_cap": 3}},
		{"no trailing newline after disabled", "disabled:\n  - p/gate/old", []string{"p/gate/old", "p/gate/x"}, nil},
		{"disabled with nothing after it", "disabled:\nstop_hook_block_cap: 3\n", []string{"p/gate/x"}, map[string]any{"stop_hook_block_cap": 3}},
		{"an entry already there is not repeated", "disabled: [p/gate/x]\n", []string{"p/gate/x"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := mergeDisabled(c.body, []string{"p/gate/x"})
			if err != nil {
				t.Fatal(err)
			}
			m := read(t, got)
			if d := disabled(t, m); strings.Join(d, ",") != strings.Join(c.want, ",") {
				t.Fatalf("disabled = %v, want %v\n%s", d, c.want, got)
			}
			for k, v := range c.keep {
				if m[k] != v {
					t.Errorf("key %s = %v, want %v (it must survive the merge)\n%s", k, m[k], v, got)
				}
			}
		})
	}
	if _, err := mergeDisabled("disabled: nope\n", []string{"x"}); err == nil {
		t.Error("a `disabled:` that is not a list was merged into")
	}
}
