package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The seams `sr-checks test` uses (stubbed judges, injected events) must not be a way to forge a
// real session's state. They are not reachable by a flag or an environment variable of the
// production hooks; the hidden `sr-session replay` acts only inside a rule-test sandbox.

func replay(p *harness.RuleProject, dir string, env []string, args ...string) string {
	cmd := exec.Command(filepath.Join(harness.Binaries(p.T()), "sr-session"), append([]string{"replay"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(p.Env(), env...)
	cmd.Stdin = strings.NewReader(`{"op":"contexts"}`)
	out, _ := cmd.CombinedOutput()
	return string(out)
}

// T002_10: replay refuses a directory that is not a sandbox.
func TestT002_10_ReplayRefusesAnythingButASandbox(t *testing.T) {
	p := newProject(t)
	notASandbox := t.TempDir()
	out := replay(p, notASandbox, nil, "--sandbox", notASandbox)
	require.Contains(t, out, "is not a rule-test sandbox")
}

// T002_11: replay refuses to run outside the sandbox's repository, and without the sandbox's own
// engine state: pointed at a real project's state it would write a real session's data.
func TestT002_11_ReplayStaysInsideTheSandbox(t *testing.T) {
	p := newProject(t)
	sandbox := t.TempDir()
	if r, err := filepath.EvalSymlinks(sandbox); err == nil {
		sandbox = r
	}
	require.NoError(t, os.WriteFile(filepath.Join(sandbox, ".sloprail-rule-test"), []byte("x"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(sandbox, "repo"), 0o755))
	elsewhere := t.TempDir()

	out := replay(p, elsewhere, nil, "--sandbox", sandbox)
	require.Contains(t, out, "replay runs in the sandbox's repository")

	out = replay(p, filepath.Join(sandbox, "repo"), []string{"XDG_DATA_HOME=" + elsewhere}, "--sandbox", sandbox)
	require.Contains(t, out, "keeps the engine's state in the sandbox")

	out = replay(p, filepath.Join(sandbox, "repo"), []string{"XDG_DATA_HOME=" + filepath.Join(sandbox, "data"), "HOME=" + elsewhere}, "--sandbox", sandbox)
	require.Contains(t, out, "runs under the sandbox's home")
}

// T002_12: the production commands carry no way to stub a judge: neither flag nor documented
// variable. (The stub is installed by a process that chose to, in code.)
func TestT002_12_ProductionCommandsOfferNoJudgeStub(t *testing.T) {
	p := newProject(t)
	for _, args := range [][]string{{"run", "--help"}, {"verify", "--help"}} {
		res := p.Sr("", "sr-checks", args...)
		require.NotContains(t, strings.ToLower(res.Output), "stub", res.Output)
		require.NotContains(t, res.Output, "live-judges")
	}
	res := p.Sr("", "sr-session", "--help")
	require.NotContains(t, res.Output, "replay", "the hidden command is not advertised")
}
