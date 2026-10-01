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

// sessionKeys are the exact names that identify the enclosing session, its
// host or its plugin/project context. An explicit list, not a CLAUDE_CODE_*
// prefix: that prefix also carries credential, proxy and cert variables
// (CLAUDE_CODE_OAUTH_TOKEN, CLAUDE_CODE_USE_*, ...) a real claude needs.
var sessionKeys = map[string]bool{
	"CLAUDECODE":                    true,
	"CLAUDE_CODE_SESSION_ID":        true,
	"CLAUDE_CODE_SESSION_ID_BACKUP": true,
	"CLAUDE_CODE_HOST_SESSION_ID":   true,
	"CLAUDE_CODE_ENTRYPOINT":        true,
	"CLAUDE_CODE_EXECPATH":          true,
	"CLAUDE_CODE_SSE_PORT":          true,
	"CLAUDE_CODE_MESSAGING_SOCKET":  true,
	"CLAUDE_CODE_MESSAGING_TOKEN":   true,
	"CLAUDE_SESSION_ID":             true,
	"CLAUDE_PROJECT_DIR":            true,
	"CLAUDE_PLUGIN_ROOT":            true,
	"CLAUDE_PLUGIN_DATA":            true,
	"CLAUDE_ENV_FILE":               true,
	"SLOPRAIL_LAUNCHED_BY":          true,
}

// harnessKeys are what Hermetic additionally drops: locations and knobs a
// harness sets itself for the process it launches.
var harnessKeys = map[string]bool{
	"CLAUDE_CONFIG_DIR":               true,
	"CLAUDE_CODE_TMPDIR":              true,
	"CLAUDE_CODE_PLUGIN_CACHE_DIR":    true,
	"CLAUDE_CODE_STOP_HOOK_BLOCK_CAP": true,
	"SLOP_SUBBIN_DIR":                 true,
}

// Session returns environ without the enclosing Claude Code session's identity
// variables (sessionKeys). Credentials, proxy/cert settings and
// CLAUDE_CONFIG_DIR are kept, so a real claude can still authenticate. environ
// is not modified.
func Session(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		key, _, _ := strings.Cut(kv, "=")
		if sessionKeys[key] {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// Hermetic is Session plus everything else that would point a test at
// operator or outer-run state: harnessKeys, and any SLOPRAIL_* / SR_* variable.
// A launcher that needs one of those sets it itself, after calling this.
func Hermetic(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, kv := range Session(environ) {
		key, _, _ := strings.Cut(kv, "=")
		if harnessKeys[key] ||
			strings.HasPrefix(key, "SLOPRAIL_") || strings.HasPrefix(key, "SR_") {
			continue
		}
		out = append(out, kv)
	}
	return out
}
