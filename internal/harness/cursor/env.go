package cursor

import "strings"

// sessionKeys identify the enclosing Cursor agent session to a process it starts.
// All were recorded in a Shell tool's or a hook's environment (harness-mocks
// cursor-mock/snapshots/runs/subprocess-session-env). CLAUDE_PROJECT_DIR is here
// because Cursor sets it too, for Claude-compatible hooks, and a leaked one would
// make the Claude Code path believe it runs inside a Claude session. An explicit
// list, not a CURSOR_ prefix: CURSOR_API_KEY is a credential a real cursor-agent
// needs.
var sessionKeys = map[string]bool{
	"CURSOR_AGENT":           true,
	"CURSOR_CONVERSATION_ID": true,
	"CURSOR_REQUEST_ID":      true,
	"CURSOR_INVOKED_AS":      true,
	"CURSOR_PROJECT_DIR":     true,
	"CURSOR_TRANSCRIPT_PATH": true,
	"CURSOR_VERSION":         true,
	"CURSOR_RIPGREP_PATH":    true,
	"CLAUDE_PROJECT_DIR":     true,
	"SLOPRAIL_LAUNCHED_BY":   true,
}

// hermeticKeys are what Hermetic additionally drops: where a platform keeps a
// user's data, so a launcher that gives the process its own HOME decides.
var hermeticKeys = map[string]bool{
	"SLOP_SUBBIN_DIR": true,
	"XDG_DATA_HOME":   true,
	"XDG_CONFIG_HOME": true,
	"XDG_STATE_HOME":  true,
	"XDG_CACHE_HOME":  true,
	"LocalAppData":    true,
}

// Session returns environ without the enclosing Cursor session's identity
// variables. environ is not modified.
func Session(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		key, _, _ := strings.Cut(kv, "=")
		if !sessionKeys[key] {
			out = append(out, kv)
		}
	}
	return out
}

// Hermetic is Session plus hermeticKeys and every SLOPRAIL_* / SR_* variable.
func Hermetic(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, kv := range Session(environ) {
		key, _, _ := strings.Cut(kv, "=")
		if hermeticKeys[key] || strings.HasPrefix(key, "SLOPRAIL_") || strings.HasPrefix(key, "SR_") {
			continue
		}
		out = append(out, kv)
	}
	return out
}
