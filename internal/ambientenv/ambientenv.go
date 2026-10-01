// Package ambientenv strips an enclosing Claude Code session's identity from a
// process environment, so a subprocess a test or the eval runner launches sees
// only the variables its launcher deliberately sets.
//
// Why it exists: a developer (or agent) running `go test` from inside a live
// Claude Code session has CLAUDECODE, CLAUDE_CODE_SESSION_ID,
// CLAUDE_CODE_EXECPATH, ... in os.Environ(). Appended-to os.Environ() those ride
// into every spawned hook, script and mock, so a test passes locally on the
// outer session's identity and fails in CI where there is none (or reaches the
// operator's real claude binary). The launcher owns the scrub; nobody should
// have to remember an `env -u ...` prefix.
package ambientenv

import "strings"

// sessionKeys are exact names that identify the enclosing session or its
// plugin/project context.
var sessionKeys = map[string]bool{
	"CLAUDECODE":           true,
	"CLAUDE_PROJECT_DIR":   true,
	"CLAUDE_PLUGIN_ROOT":   true,
	"CLAUDE_PLUGIN_DATA":   true,
	"CLAUDE_SESSION_ID":    true,
	"CLAUDE_ENV_FILE":      true,
	"SLOPRAIL_LAUNCHED_BY": true,
}

// authKeys are CLAUDE_CODE_* variables that select credentials or a provider,
// not a session; a test driving a real claude needs them to authenticate.
var authKeys = map[string]bool{
	"CLAUDE_CODE_OAUTH_TOKEN": true,
	"CLAUDE_CODE_USE_BEDROCK": true,
	"CLAUDE_CODE_USE_VERTEX":  true,
	"CLAUDE_CODE_USE_FOUNDRY": true,
}

func isSession(key string) bool {
	if sessionKeys[key] {
		return true
	}
	return strings.HasPrefix(key, "CLAUDE_CODE_") && !authKeys[key]
}

// Session returns environ without the enclosing Claude Code session's
// variables: CLAUDECODE, CLAUDE_CODE_* (session id, entrypoint, execpath, sse
// port, messaging, ...; credential/provider selectors are kept),
// CLAUDE_PROJECT_DIR, CLAUDE_PLUGIN_ROOT, CLAUDE_PLUGIN_DATA, CLAUDE_SESSION_ID,
// CLAUDE_ENV_FILE and SLOPRAIL_LAUNCHED_BY. environ is not modified.
func Session(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		key, _, _ := strings.Cut(kv, "=")
		if isSession(key) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// Hermetic is Session plus everything else that would point a test at
// operator or outer-run state: CLAUDE_CONFIG_DIR, SLOP_SUBBIN_DIR, and any
// SLOPRAIL_* / SR_* variable. A launcher that needs one of those sets it itself,
// after calling this.
func Hermetic(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, kv := range Session(environ) {
		key, _, _ := strings.Cut(kv, "=")
		if key == "CLAUDE_CONFIG_DIR" || key == "SLOP_SUBBIN_DIR" ||
			strings.HasPrefix(key, "SLOPRAIL_") || strings.HasPrefix(key, "SR_") {
			continue
		}
		out = append(out, kv)
	}
	return out
}
