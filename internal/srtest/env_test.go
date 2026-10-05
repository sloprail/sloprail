package srtest

import (
	"strings"
	"testing"
)

func envMap(env []string) map[string]string {
	m := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v
	}
	return m
}

var callerAmbient = []string{
	"PATH=/caller/bin:/usr/bin", "HOME=/real/home", "TMPDIR=/real/tmp", "FOO=bar",
	"GIT_DIR=/x.git", "GIT_CONFIG_GLOBAL=/real/gitconfig", "GIT_AUTHOR_NAME=me", "XDG_CONFIG_HOME=/real/xdg",
	"CLAUDECODE=1", "CLAUDE_CODE_SESSION_ID=s", "SR_WORKSPACE=/w", "LANG=en_US.UTF-8",
	"A10N_CLAUDE_MOCK=/mock/a10n-claude-mock",
	"ANTHROPIC_API_KEY=k", "HTTPS_PROXY=http://p", "CLAUDE_CODE_OAUTH_TOKEN=t",
}

func spec(live bool) envSpec {
	return envSpec{Home: "/case/home", GitConfig: "/case/gitconfig", Tmp: "/case/tmp", CaseDir: "/case/case", EventsFile: "/case/events.jsonl",
		Target: "/t", SloprailDir: "/s", BinDirs: []string{"/sr/bin"}, ToolDirs: []string{"/opt/homebrew/bin", "/sr/bin"}, LiveJudges: live}
}

func TestCaseEnvIsBuiltFromScratch(t *testing.T) {
	got := envMap(caseEnv(callerAmbient, spec(false)))
	for _, k := range []string{"FOO", "GIT_DIR", "XDG_CONFIG_HOME", "CLAUDECODE", "CLAUDE_CODE_SESSION_ID", "SR_WORKSPACE", "LANG",
		"ANTHROPIC_API_KEY", "HTTPS_PROXY", "CLAUDE_CODE_OAUTH_TOKEN"} {
		if v, ok := got[k]; ok {
			t.Errorf("%s=%s leaked from the caller", k, v)
		}
	}
	want := map[string]string{
		"A10N_CLAUDE_MOCK":      "/mock/a10n-claude-mock",
		"HOME":                  "/case/home",
		"TMPDIR":                "/case/tmp",
		"PATH":                  "/sr/bin:/opt/homebrew/bin:/usr/bin:/bin",
		"GIT_CONFIG_NOSYSTEM":   "1",
		"GIT_CONFIG_GLOBAL":     "/case/gitconfig",
		"GIT_AUTHOR_NAME":       TestGitName,
		"GIT_COMMITTER_EMAIL":   TestGitEmail,
		"SR_EVENTS_FILE":        "/case/events.jsonl",
		"SR_TEST_CASE_DIR":      "/case/case",
		"SR_TEST_TARGET_DIR":    "/t",
		"SR_TEST_SLOPRAIL_DIR":  "/s",
		"SR_CHECKS_JUDGE_MOCKS": "{}",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s=%q, want %q", k, got[k], v)
		}
	}
	if _, ok := got["SR_TEST_PLUGIN_DIR"]; ok {
		t.Error("SR_TEST_PLUGIN_DIR set for a project case")
	}
}

func TestCaseEnvLiveJudgesPassTheirListedVariablesOnly(t *testing.T) {
	got := envMap(caseEnv(callerAmbient, spec(true)))
	for k, v := range map[string]string{"ANTHROPIC_API_KEY": "k", "HTTPS_PROXY": "http://p", "CLAUDE_CODE_OAUTH_TOKEN": "t"} {
		if got[k] != v {
			t.Errorf("%s=%q, want %q", k, got[k], v)
		}
	}
	if _, ok := got["SR_CHECKS_JUDGE_MOCKS"]; ok {
		t.Error("SR_CHECKS_JUDGE_MOCKS set with live judges")
	}
	for _, k := range []string{"FOO", "GIT_DIR", "CLAUDECODE", "LANG"} {
		if _, ok := got[k]; ok {
			t.Errorf("%s leaked", k)
		}
	}
}

func TestCaseEnvPluginDirs(t *testing.T) {
	s := spec(false)
	s.Plugins = []string{"/p1", "/p2"}
	if got := envMap(caseEnv(nil, s))["SR_TEST_PLUGIN_DIR"]; got != "/p1:/p2" {
		t.Errorf("SR_TEST_PLUGIN_DIR=%q", got)
	}
}
