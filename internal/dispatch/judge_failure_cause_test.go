package dispatch

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sr:proves judges/failed-judge-refuses-in-fixed-words
func TestJudgeFailureNamesEveryKnownCauseInFixedWords(t *testing.T) {
	const secret = "sk-ant-api03-SECRETSECRET"
	for _, cause := range []string{"usage limit", "authentication", "version skew", "other"} {
		dir, _ := shimSrAgent(t, "echo 'token "+secret+"' >&2\necho 'sr-agent: harness-failure: "+cause+"' >&2\nexit 1\n")
		v, err := askJudge(judgeCall{Dir: dir, GuardName: "g"}, "judge it")
		require.NoError(t, err)
		assert.True(t, v.Refused, cause)
		assert.True(t, v.NoVerdict, cause)
		assert.Equal(t, cause, v.Unavailable)
		assert.Contains(t, v.Reason, "judge unavailable: "+cause)
		assert.NotContains(t, v.Reason, "SECRET", cause)
	}
}
