package codex

import "strings"

// What Codex puts in the environment of what it starts (recorded: harness-mocks
// codex-mock/snapshots/runs/subprocess-session-env, nested-session-env,
// env-names-start-hook):
//
//   - a shell command the agent runs sees CODEX_THREAD_ID and CODEX_SESSION_ID (the
//     session), CODEX_VERSION and CODEX_CI;
//   - every child, tool command or hook, sees how Codex was started when the npm
//     launcher did it: CODEX_MANAGED_BY_NPM and CODEX_MANAGED_PACKAGE_ROOT;
//   - a plugin's hook command sees PLUGIN_ROOT and PLUGIN_DATA
//     (https://developers.openai.com/codex/hooks#plugin-bundled-hooks);
//   - a hook is given no session variable at all: its session is in the payload.

// detectKeys are the variables only a process under Codex has. Detect uses them and
// nothing looser: CODEX_HOME names a directory a user may export for any reason, so
// it does not say a process runs under Codex and is not here.
var detectKeys = []string{
	"CODEX_THREAD_ID", "CODEX_SESSION_ID", "CODEX_CI", "CODEX_MANAGED_BY_NPM", "PLUGIN_ROOT",
}

// sessionKeys identify the enclosing session, its launcher or its plugin context.
// An explicit list, not a CODEX_* prefix: that prefix also carries CODEX_HOME and
// the credentials and endpoints a real codex needs.
var sessionKeys = map[string]bool{
	"CODEX_THREAD_ID":            true,
	"CODEX_SESSION_ID":           true,
	"CODEX_VERSION":              true,
	"CODEX_CI":                   true,
	"CODEX_MANAGED_BY_NPM":       true,
	"CODEX_MANAGED_PACKAGE_ROOT": true,
	"PLUGIN_ROOT":                true,
	"PLUGIN_DATA":                true,
	"CLAUDE_PLUGIN_ROOT":         true, // Codex sets these two for compatibility
	"CLAUDE_PLUGIN_DATA":         true,
	"SLOPRAIL_LAUNCHED_BY":       true,
	"SLOPRAIL_HARNESS":           true,
}

// harnessKeys are what Hermetic additionally drops: where the operator's own Codex
// keeps its state, and the platform's per-user directories.
var harnessKeys = map[string]bool{
	"CODEX_HOME":      true,
	"SLOP_SUBBIN_DIR": true,
	"XDG_DATA_HOME":   true,
	"XDG_CONFIG_HOME": true,
	"XDG_STATE_HOME":  true,
	"XDG_CACHE_HOME":  true,
	"LocalAppData":    true,
}

// Session returns environ without the enclosing Codex session's identity variables.
// Credentials, endpoints and CODEX_HOME are kept, so a real codex can authenticate.
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

// Hermetic is Session plus everything else that would point a process at operator
// or outer-run state: harnessKeys, and any SLOPRAIL_* / SR_* variable.
func Hermetic(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, kv := range Session(environ) {
		key, _, _ := strings.Cut(kv, "=")
		if harnessKeys[key] || strings.HasPrefix(key, "SLOPRAIL_") || strings.HasPrefix(key, "SR_") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// Detect reports whether environ is that of a process running under Codex.
func Detect(environ []string) bool {
	for _, kv := range environ {
		key, val, _ := strings.Cut(kv, "=")
		if val == "" {
			continue
		}
		for _, k := range detectKeys {
			if key == k {
				return true
			}
		}
	}
	return false
}
