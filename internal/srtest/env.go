package srtest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/sloprail/sloprail/internal/harnessmock"
)

// A case's environment is built from scratch: nothing the caller has set reaches it unless it is
// named here (an allowlist, so a variable nobody thought of, GIT_DIR, XDG_CONFIG_HOME, a caller's
// TMPDIR, a stray FOO, cannot leak in and make a case pass on one machine and fail on another).

// callerKeys are the only variables copied from the caller in every run.
var callerKeys = []string{
	// Where the pinned mock `claude` is, when the caller says (else the mock is found on PATH, see toolDirs).
	"A10N_CLAUDE_MOCK",
}

// liveJudgeKeys are the variables copied from the caller only with --live-judges: what a real
// `claude` (run by a judge) needs to authenticate and to reach the network. HOME is the case's fake
// one, so credentials must come through these, not through the caller's ~/.claude.
var liveJudgeKeys = []string{
	// API key / token / endpoint.
	"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "CLAUDE_CODE_OAUTH_TOKEN",
	// Bedrock / Vertex / Foundry routing and their credentials.
	"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY",
	"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AWS_REGION",
	"ANTHROPIC_VERTEX_PROJECT_ID", "CLOUD_ML_REGION",
	// Proxy and TLS trust, without which a network-restricted machine cannot reach the API.
	"HTTPS_PROXY", "HTTP_PROXY", "NO_PROXY", "ALL_PROXY", "https_proxy", "http_proxy", "no_proxy", "all_proxy",
	"SSL_CERT_FILE", "SSL_CERT_DIR", "NODE_EXTRA_CA_CERTS",
	"CLAUDE_CODE_CLIENT_CERT", "CLAUDE_CODE_CLIENT_KEY", "CLAUDE_CODE_CLIENT_KEY_PASSPHRASE",
}

// systemPath is what PATH always ends with: the base system tools (cat, mkdir, env...), never the
// caller's whole PATH.
const systemPath = "/usr/bin:/bin"

// toolNames are the external programs the cases call (found once per run on the caller's PATH; only
// their directories are put on the case PATH).
var toolNames = []string{"jq", "git", "bash"}

// toolDirs are the directories of the programs a case needs that the system path may not hold: jq,
// git and bash, the pinned mock claude, and, with live judges, the real claude.
func toolDirs(live bool) []string {
	names := append([]string{}, toolNames...)
	if live {
		names = append(names, "claude")
	}
	var dirs []string
	for _, n := range names {
		if p, err := exec.LookPath(n); err == nil {
			dirs = append(dirs, filepath.Dir(p))
		}
	}
	if p, err := harnessmock.Path(); err == nil {
		dirs = append(dirs, filepath.Dir(p))
	}
	return dirs
}

// envSpec is what sr-test itself decides about one case's environment.
type envSpec struct {
	Home, Tmp           string   // the case's fake home and its own TMPDIR
	GitConfig           string   // the case's git configuration file (beside HOME, so HOME stays empty)
	CaseDir, EventsFile string   // SR_TEST_CASE_DIR, SR_EVENTS_FILE
	Target, SloprailDir string   // SR_TEST_TARGET_DIR, SR_TEST_SLOPRAIL_DIR
	Plugins             []string // SR_TEST_PLUGIN_DIR
	BinDirs, ToolDirs   []string // PATH = BinDirs + ToolDirs + systemPath
	LiveJudges          bool
}

// caseEnv builds one case's environment: the allowlisted variables of the caller, then what sr-test sets.
func caseEnv(caller []string, s envSpec) []string {
	keys := append([]string{}, callerKeys...)
	if s.LiveJudges {
		keys = append(keys, liveJudgeKeys...)
	}
	var env []string
	for _, kv := range caller {
		k, _, _ := strings.Cut(kv, "=")
		for _, a := range keys {
			if k == a {
				env = append(env, kv)
				break
			}
		}
	}
	env = append(env,
		"HOME="+s.Home,
		"TMPDIR="+s.Tmp,
		"PATH="+joinPath(s.BinDirs, s.ToolDirs),
		// git reads no system or user configuration of the machine: only the case's own file.
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+s.GitConfig,
		"SR_TEST_CASE_DIR="+s.CaseDir,
		"SR_EVENTS_FILE="+s.EventsFile,
		"SR_TEST_TARGET_DIR="+s.Target,
		"SR_TEST_SLOPRAIL_DIR="+s.SloprailDir,
	)
	env = append(env, TestIdentityEnv()...)
	if len(s.Plugins) > 0 {
		env = append(env, "SR_TEST_PLUGIN_DIR="+strings.Join(s.Plugins, string(os.PathListSeparator)))
	}
	if !s.LiveJudges {
		env = append(env, "SR_CHECKS_JUDGE_MOCKS={}")
	}
	return env
}

// joinPath is the dirs in order, each once, then systemPath.
func joinPath(groups ...[]string) string {
	seen := map[string]bool{}
	var out []string
	for _, g := range groups {
		for _, d := range g {
			if d != "" && !seen[d] {
				seen[d] = true
				out = append(out, d)
			}
		}
	}
	return strings.Join(append(out, systemPath), string(os.PathListSeparator))
}
