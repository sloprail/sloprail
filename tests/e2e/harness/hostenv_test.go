package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A suite run from inside a live Claude Code session has that session's
// identity in the test process env. A process the harness spawns must see only
// what the harness sets, so the result is the same locally and in CI with no
// `env -u ...` prefix.
func TestSpawnedProcessSeesNoAmbientSession(t *testing.T) {
	ambient := map[string]string{
		"CLAUDECODE":             "1",
		"CLAUDE_CODE_SESSION_ID": "ambient-session",
		"CLAUDE_CODE_ENTRYPOINT": "ambient-entry",
		"CLAUDE_CODE_EXECPATH":   "/ambient/claude",
		"CLAUDE_CODE_SSE_PORT":   "1234",
		"CLAUDE_PROJECT_DIR":     "/ambient/project",
		"CLAUDE_PLUGIN_ROOT":     "/ambient/plugin",
		"CLAUDE_CONFIG_DIR":      "/ambient/config",
		"SLOPRAIL_LAUNCHED_BY":   "ambient",
		"SR_WORKSPACE":           "/ambient/ws",
		"SLOP_SUBBIN_DIR":        "/ambient/bin",
	}
	for k, v := range ambient {
		t.Setenv(k, v)
	}
	// Credentials are not session identity and nothing overrides them: they must
	// survive the scrub.
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "oauth-survives")

	e := New(t)
	probe := filepath.Join(e.BinDir(), "envprobe")
	if err := os.WriteFile(probe, []byte("#!/bin/sh\nenv\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	res := e.CLIDirectStdinEnv(t.TempDir(), "", []string{"CLAUDE_CODE_SESSION_ID=from-test"}, "envprobe")

	got := map[string]string{}
	for _, line := range strings.Split(res.Output, "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			got[k] = v
		}
	}
	for k, v := range ambient {
		if got[k] == v {
			t.Errorf("ambient %s=%s reached the spawned process", k, v)
		}
	}
	// The probe is handed CLAUDE_CODE_SESSION_ID=from-test; it must see exactly
	// that, never the ambient "ambient-session".
	if got["CLAUDE_CODE_SESSION_ID"] != "from-test" || got["CLAUDE_CODE_SESSION_ID"] == ambient["CLAUDE_CODE_SESSION_ID"] {
		t.Errorf("probe must see the harness's session id, got %q", got["CLAUDE_CODE_SESSION_ID"])
	}
	if got["CLAUDE_CODE_OAUTH_TOKEN"] != "oauth-survives" {
		t.Errorf("a non-session CLAUDE_CODE_* credential was stripped, got %q", got["CLAUDE_CODE_OAUTH_TOKEN"])
	}
	if got["SLOP_SUBBIN_DIR"] != e.BinDir() {
		t.Errorf("SLOP_SUBBIN_DIR is the harness's own, got %q", got["SLOP_SUBBIN_DIR"])
	}
}
