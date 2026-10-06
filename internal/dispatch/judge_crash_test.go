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

// A verdict is final: a judge that answered pass:false is not asked again.
func TestJudgeVerdictIsNotRetried(t *testing.T) {
	dir, ledger := shimSrAgent(t, "echo 'JUDGE-REASON: no' >&2\nexit 1\n")

	v, err := askJudge(judgeCall{Dir: dir, GuardName: "g"}, "judge it")
	require.NoError(t, err)
	assert.True(t, v.Refused)
	assert.Empty(t, v.Unavailable)
	assert.Equal(t, 1, calls(t, ledger))
}
