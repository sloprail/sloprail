package dispatch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// shimSrAgent puts an sr-agent running script first on PATH and returns the ledger it appends
// a line to per call.
func shimSrAgent(t *testing.T, body string) (dir, ledger string) {
	t.Helper()
	dir = t.TempDir()
	ledger = filepath.Join(dir, "ledger")
	bin := filepath.Join(dir, "bin")
	require.NoError(t, os.MkdirAll(bin, 0o755))
	script := "#!/bin/sh\necho call >>" + ledger + "\n" + body
	require.NoError(t, os.WriteFile(filepath.Join(bin, "sr-agent"), []byte(script), 0o755))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(judgeRetryBackoffEnv, "0s")
	return dir, ledger
}

func calls(t *testing.T, ledger string) int {
	t.Helper()
	b, _ := os.ReadFile(ledger)
	return strings.Count(string(b), "call\n")
}

// A judge whose substrate dies (sr-agent names the cause on stderr) is asked once more, and
// when it still dies the refusal is a no-verdict one carrying the cause.
func TestJudgeCrashIsRetriedOnceAndCarriesItsCause(t *testing.T) {
	dir, ledger := shimSrAgent(t, "echo 'sr-agent: harness-failure: usage limit' >&2\necho 'claude exited with status 1: usage limit' >&2\nexit 1\n")

	v, err := askJudge(judgeCall{Dir: dir, GuardName: "g"}, "judge it")
	require.NoError(t, err)
	assert.True(t, v.Refused)
	assert.True(t, v.NoVerdict)
	assert.Equal(t, "usage limit", v.Unavailable)
	assert.Equal(t, 2, calls(t, ledger), "a crashed judge is run exactly twice")
}

// What the harness printed never reaches the verdict's reason: only the fixed words and the cause.
func TestJudgeCrashReasonNeverCarriesHarnessOutput(t *testing.T) {
	dir, _ := shimSrAgent(t, "echo 'auth failed sk-ant-api03-SECRETSECRET at /Users/me/.claude' >&2\necho 'sr-agent: harness-failure: usage limit' >&2\nexit 1\n")

	v, err := askJudge(judgeCall{Dir: dir, GuardName: "g"}, "judge it")
	require.NoError(t, err)
	assert.Equal(t, "judge unavailable: usage limit", v.Reason)
}

// A bad login or an unknown flag fails the same way again: not retried. A marker that is not at
// the start of a line, or a judge that did answer, is no crash.
func TestJudgeCrashRetryRules(t *testing.T) {
	for cause, want := range map[string]int{"authentication": 1, "version skew": 1, "other": 2} {
		dir, ledger := shimSrAgent(t, "echo 'sr-agent: harness-failure: "+cause+"' >&2\nexit 1\n")
		_, err := askJudge(judgeCall{Dir: dir, GuardName: "g"}, "judge it")
		require.NoError(t, err)
		assert.Equal(t, want, calls(t, ledger), cause)
	}

	dir, ledger := shimSrAgent(t, "echo 'the model said sr-agent: harness-failure: usage limit' >&2\nexit 1\n")
	v, err := askJudge(judgeCall{Dir: dir, GuardName: "g"}, "judge it")
	require.NoError(t, err)
	assert.Empty(t, v.Unavailable)
	assert.Equal(t, 1, calls(t, ledger), "a marker inside other text is not sr-agent's")

	dir, ledger = shimSrAgent(t, "echo 'JUDGE-REASON: the judge did not produce a JSON verdict object' >&2\necho 'sr-agent: harness-failure: usage limit' >&2\nexit 1\n")
	v, err = askJudge(judgeCall{Dir: dir, GuardName: "g"}, "judge it")
	require.NoError(t, err)
	assert.Empty(t, v.Unavailable, "an answered (if unparseable) judge is not the judges being down")
	assert.Equal(t, 1, calls(t, ledger))
}

// A verdict is final: a judge that answered pass:false is not asked again.
func TestJudgeVerdictIsNotRetried(t *testing.T) {
	dir, ledger := shimSrAgent(t, "echo 'JUDGE-REASON: no' >&2\nexit 1\n")

	v, err := askJudge(judgeCall{Dir: dir, GuardName: "g"}, "judge it")
	require.NoError(t, err)
	assert.True(t, v.Refused)
	assert.Empty(t, v.Unavailable)
	assert.Equal(t, 1, calls(t, ledger))
}
