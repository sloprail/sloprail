package harness

import "testing"

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
{"message":{"content":[{"content":"File written successfully to deny/notes.md","is_error":false,"tool_use_id":"w1","type":"tool_result"}],"role":"user"},"type":"user"}`,
			refused: false,
		},
		{
			// The same trap from the content side rather than the path.
			name: "permitted write whose content says blocked",
			output: `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"w1","name":"Write","input":{"file_path":"a/notes.md","content":"the request was blocked"}}]}}
{"message":{"content":[{"content":"File written successfully to a/notes.md","is_error":false,"tool_use_id":"w1","type":"tool_result"}],"role":"user"},"type":"user"}`,
			refused: false,
		},
		{
			// The channel this engine uses: permissionDecision "deny", exit 0.
			// Real Claude Code answers the call with this tool_result.
			name:    "refused via permissionDecision",
			output:  `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"w1","content":"PreToolUse:Write hook error: guarded/ is off limits","is_error":true}]}}`,
			refused: true,
		},
		{
			// Exit 2: the same tool_result, the reason quoted as
			// "[<command>]: <stderr>".
			name:    "refused via exit 2 on stderr",
			output:  `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"w1","content":"PreToolUse:Write hook error: [sr-session pre-tool]: guarded/ is off limits","is_error":true}]}}`,
			refused: true,
		},
		{
			// The refusal's words in the agent's own tool input are not a
			// refusal.
			name: "the agent writes the refusal text into a file",
			output: `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"w1","name":"Write","input":{"file_path":"a.md","content":"PreToolUse:Write hook error: nope"}}]}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"w1","content":"File written successfully to a.md","is_error":false}]}}`,
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
			output:  `{"message":{"content":[{"content":"File written successfully to a/notes.md","is_error":false,"tool_use_id":"w1","type":"tool_result"}],"role":"user"},"type":"user"}`,
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
