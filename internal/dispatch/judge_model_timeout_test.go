package dispatch

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/declaration"
)

// These cover the per-judge MODEL and TIMEOUT config (dot-dir-file-store Check
// model/timeout): the check's own values reach the judge run, the model is what
// sr-agent is invoked with, an unset model defaults to size-md, an unset timeout
// defaults to the engine's 30s, and — fail-closed — a judge that exceeds ITS
// timeout is still a refusal.

// The check's Model and Timeout reach the judgeCall the runner builds — the
// threading from declaration.Check through runJudgeCheck.
func TestJudgeCheck_ModelAndTimeoutThreadToJudgeCall(t *testing.T) {
	var got judgeCall
	r := Runner{
		skillLoaded: func(string, string, string) (bool, error) { return true, nil },
		runJudge: func(j judgeCall) (Verdict, error) {
			got = j
			return pass(), nil
		},
	}
	req := gateReq([]declaration.Check{{
		Judge:   "j.md.j2",
		Model:   "size-xl",
		Timeout: "45s",
	}}, nil)

	v, err := r.Run(req)
	require.NoError(t, err)
	assert.False(t, v.Refused)
	assert.Equal(t, "size-xl", got.Model, "the check's model must reach the judge run")
	assert.Equal(t, 45*time.Second, got.Timeout, "the check's timeout must be parsed and reach the judge run")
}

// The request's Workspace reaches the judgeCall, which is what hands sr-agent the
// project as a read-only `--add-dir:readonly`.
func TestJudgeCheck_WorkspaceThreadsToJudgeCall(t *testing.T) {
	var got judgeCall
	r := Runner{
		skillLoaded: func(string, string, string) (bool, error) { return true, nil },
		runJudge: func(j judgeCall) (Verdict, error) {
			got = j
			return pass(), nil
		},
	}
	req := gateReq([]declaration.Check{{Judge: "j.md.j2"}}, nil)
	req.Workspace = "/work/proj"

	_, err := r.Run(req)
	require.NoError(t, err)
	assert.Equal(t, "/work/proj", got.Workspace, "the workspace must reach the judge run")
	assert.Equal(t, "/guard", got.Dir, "the judge still starts in the rule's own folder")
}

// An unset model and timeout leave the judgeCall's fields at their zero values,
// which the judge path resolves to the engine defaults (size-md, 30s).
func TestJudgeCheck_UnsetModelTimeoutAreZeroOnJudgeCall(t *testing.T) {
	var got judgeCall
	r := Runner{
		skillLoaded: func(string, string, string) (bool, error) { return true, nil },
		runJudge: func(j judgeCall) (Verdict, error) {
			got = j
			return pass(), nil
		},
	}
	req := gateReq([]declaration.Check{{Judge: "j.md.j2"}}, nil)

	_, err := r.Run(req)
	require.NoError(t, err)
	assert.Empty(t, got.Model, "an unset model leaves judgeCall.Model empty (resolved to the default downstream)")
	assert.Zero(t, got.Timeout, "an unset timeout leaves judgeCall.Timeout zero (resolved to the default downstream)")
}

// A malformed timeout that somehow reached the runner (the loader validates it,
// so this is the defensive path) fails CLOSED — the judge is not asked under a
// timeout the rule did not actually specify.
func TestJudgeCheck_MalformedTimeoutFailsClosed(t *testing.T) {
	judgeAsked := false
	r := Runner{
		skillLoaded: func(string, string, string) (bool, error) { return true, nil },
		runJudge: func(judgeCall) (Verdict, error) {
			judgeAsked = true
			return pass(), nil
		},
	}
	req := gateReq([]declaration.Check{{Judge: "j.md.j2", Timeout: "not-a-duration"}}, nil)

	v, err := r.Run(req)
	require.NoError(t, err)
	assert.True(t, v.Refused, "a timeout that cannot be parsed must refuse, not silently fall back")
	assert.False(t, judgeAsked, "the model is not asked when the timeout could not be read")
	assert.Contains(t, v.Reason, "timeout")
}

// judgeCommand carries the check's model to sr-agent as `--model <that>`, quoted.
func TestJudgeCommand_CarriesCustomModel(t *testing.T) {
	cmd := judgeCommand("/tmp/verify.sh", "size-xxl", nil, nil, "")
	assert.Contains(t, cmd, "--model 'size-xxl'",
		"the check's model must be the --model sr-agent is invoked with")
	// A concrete, comma-separated set passes straight through (sr-agent's --model
	// takes exactly this format).
	cmd = judgeCommand("/tmp/verify.sh", "claude-opus-5,size-md", nil, nil, "")
	assert.Contains(t, cmd, "--model 'claude-opus-5,size-md'")
}

// judgeCommand carries the check's allowed_tools to sr-agent as `--allowed-tools`,
// space-joined and quoted; a check that named none omits the flag so sr-agent
// grants only the answer folder's scoped Edit rule its own verdict file needs.
func TestJudgeCommand_CarriesAllowedTools(t *testing.T) {
	cmd := judgeCommand("/tmp/verify.sh", "size-md", []string{"Read", "WebFetch"}, nil, "")
	assert.Contains(t, cmd, "--allowed-tools 'Read WebFetch'",
		"the check's allowed_tools must reach sr-agent's --allowed-tools")

	// None named: the flag is absent entirely.
	bare := judgeCommand("/tmp/verify.sh", "size-md", nil, nil, "")
	assert.NotContains(t, bare, "--allowed-tools",
		"a judge that named no tools must not pass an empty --allowed-tools")
}

// judgeCommand hands sr-agent the workspace as `--add-dir:readonly`, quoted (a project
// path may hold a space), so the judge can read the project it judges and never
// write it; a judge with no workspace gets no project access.
func TestJudgeCommand_CarriesTheWorkspaceAsAReadonlyDir(t *testing.T) {
	cmd := judgeCommand("/tmp/verify.sh", "size-md", nil, nil, "/work/my proj")
	assert.Contains(t, cmd, "--add-dir:readonly '/work/my proj'")

	bare := judgeCommand("/tmp/verify.sh", "size-md", nil, nil, "")
	assert.NotContains(t, bare, "--add-dir", "no workspace, no project access")
}

// The prompt names the workspace when the engine knows it — the judge starts in
// the rule's folder and the material's paths are repository-relative — and says
// nothing when it does not.
func TestWorkspaceNote(t *testing.T) {
	note := workspaceNote("/work/proj")
	assert.Contains(t, note, "/work/proj")
	assert.Contains(t, note, "relative to it")
	assert.Contains(t, note, "cannot change them")
	assert.Empty(t, workspaceNote(""))
}

// A scoped rule keeps its spaces through the command line: the rules are joined
// into one single-quoted --allowed-tools argument, which sr-agent splits back
// paren-aware, so `Bash(git show:*)` arrives whole.
func TestJudgeCommand_ScopedToolRulesSurviveTheCommandLine(t *testing.T) {
	cmd := judgeCommand("/tmp/verify.sh", "size-md", []string{"Bash(git show:*)", "WebFetch(domain:code.claude.com)"}, nil, "")
	assert.Contains(t, cmd, "--allowed-tools 'Bash(git show:*) WebFetch(domain:code.claude.com)'")
}

// A check's disallowed_tools reach sr-agent as --disallowed-tools, joined and
// quoted like allowed_tools, scoped rules whole; none named, no flag.
func TestJudgeCommand_CarriesDisallowedTools(t *testing.T) {
	cmd := judgeCommand("/tmp/verify.sh", "size-md", []string{"Bash(curl:*)"}, []string{"Bash(curl * -o *)", "Bash(curl * -d @*)"}, "")
	assert.Contains(t, cmd, "--disallowed-tools 'Bash(curl * -o *) Bash(curl * -d @*)'")
	assert.NotContains(t, judgeCommand("/tmp/verify.sh", "size-md", nil, nil, ""), "--disallowed-tools")
}

// The check's disallowed_tools reach the judgeCall the runner builds.
func TestJudgeCheck_DisallowedToolsThreadToJudgeCall(t *testing.T) {
	var got judgeCall
	r := Runner{
		skillLoaded: func(string, string, string) (bool, error) { return true, nil },
		runJudge: func(j judgeCall) (Verdict, error) {
			got = j
			return pass(), nil
		},
	}
	req := gateReq([]declaration.Check{{Judge: "j.md.j2", DisallowedTools: []string{"Bash(curl * -o *)"}}}, nil)
	_, err := r.Run(req)
	require.NoError(t, err)
	assert.Equal(t, []string{"Bash(curl * -o *)"}, got.DisallowedTools)
}

// judgeCall.model() resolves the default when the check named none, so
// judgeCommand is never handed an empty model.
func TestJudgeCall_ModelDefaultsToSizeMD(t *testing.T) {
	assert.Equal(t, defaultJudgeModel, judgeCall{}.model(), "an unset model resolves to the engine default")
	assert.Equal(t, "size-md", defaultJudgeModel, "the default is the middle rung, size-md")
	assert.Equal(t, "size-lg", judgeCall{Model: "size-lg"}.model(), "a set model wins over the default")

	// And the default reaches the command line when the check set no model.
	cmd := judgeCommand("/tmp/verify.sh", judgeCall{}.model(), nil, nil, "")
	assert.Contains(t, cmd, "--model 'size-md'")
}

// runShell honours a custom, short timeout: a command that would run longer is
// KILLED and reported expired — the mechanism a per-judge timeout rides on, and
// the fail-closed guarantee (an over-time judge is stopped, then refused at the
// call site).
func TestRunShell_CustomTimeoutKillsLongRun(t *testing.T) {
	start := time.Now()
	_, _, _, expired, _, startErr := runShell("", "sleep 10", nil, nil, 200*time.Millisecond)
	elapsed := time.Since(start)

	require.NoError(t, startErr, "the command started; it is the timeout under test")
	assert.True(t, expired, "a command exceeding its timeout must be reported expired")
	assert.Less(t, elapsed, 5*time.Second, "the custom 200ms bound must fire, not the 30s default")
}

// runShell with a zero timeout falls back to the default bound rather than
// running unbounded — a quick command still completes normally under it.
func TestRunShell_ZeroTimeoutUsesDefault(t *testing.T) {
	_, _, code, expired, _, startErr := runShell("", "printf ok", nil, nil, 0)
	require.NoError(t, startErr)
	assert.False(t, expired, "a quick command does not hit the default bound")
	assert.Equal(t, 0, code)
}

// A judge whose sr-agent run exceeds ITS timeout refuses (fail-closed), and the
// refusal reads as "did not answer" — the whole reason the timeout is per-judge:
// a longer bound is allowed, but exceeding whatever bound applies is still a
// refusal, never a pass. Exercised through the real judge path (askJudge) with a
// verifier and a stub sr-agent that sleeps past the bound.
func TestJudge_ExceedingCustomTimeoutRefuses(t *testing.T) {
	dir := t.TempDir()
	// A fake `sr-agent` on PATH that sleeps well past the tiny timeout below, so
	// the run is killed rather than answering.
	installStubOnPath(t, dir, "sr-agent", "#!/bin/sh\nsleep 10\n")

	j := judgeCall{
		Dir:       dir,
		GuardName: "g",
		Timeout:   150 * time.Millisecond,
	}
	start := time.Now()
	v, err := askJudge(j, "the rendered prompt")
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.True(t, v.Refused, "a judge that exceeds its own timeout must refuse (fail-closed)")
	assert.Contains(t, v.Reason, "did not answer")
	assert.Less(t, elapsed, 3*time.Second, "the per-judge 150ms bound must fire, not the 30s default")
}

// installStubOnPath writes an executable stub of the given name into dir and puts
// dir at the front of PATH for the test, so a name-resolved binary (sr-agent,
// invoked by name like the real judge path) finds the stub.
func installStubOnPath(t *testing.T, dir, name, script string) {
	t.Helper()
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, []byte(script), 0o755))
	require.NoError(t, os.Chmod(p, 0o755))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
