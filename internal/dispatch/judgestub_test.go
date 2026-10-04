package dispatch

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubCall is a judge call over a template that renders the event's path.
func stubCall(t *testing.T, template string) judgeCall {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "j.md.j2"), []byte(template), 0o644))
	return judgeCall{
		Dir: dir, Template: "./j.md.j2", GuardName: "g",
		InputJSON: []byte(`{"event":{"kind":"PreFileCreate","path":"a.md"}}`),
	}
}

// With a stub installed the judge never reaches sr-agent: the template is rendered, the stub is
// handed the prompt, and its answer is the verdict.
func TestStubbedJudge_AnswersFromTheStubAndRendersTheTemplate(t *testing.T) {
	var seen JudgeInvocation
	restore := InstallJudgeStub(func(inv JudgeInvocation) JudgeAnswer {
		seen = inv
		return JudgeAnswer{Stubbed: true, Pass: false, Reasoning: "too curt"}
	})
	defer restore()

	v, err := Runner{}.runJudgeStubbedForTest(stubCall(t, "judge {{ event.path }}"))
	require.NoError(t, err)
	assert.True(t, v.Refused)
	assert.Equal(t, "too curt", v.Reason)
	assert.Contains(t, seen.Prompt, "judge a.md", "the stub is shown what a model would be")
	assert.Equal(t, "./j.md.j2", seen.Template)
	assert.Equal(t, "g", seen.GuardName)
}

func TestStubbedJudge_APassingStubPermits(t *testing.T) {
	defer InstallJudgeStub(func(JudgeInvocation) JudgeAnswer { return JudgeAnswer{Stubbed: true, Pass: true} })()
	v, err := Runner{}.runJudgeStubbedForTest(stubCall(t, "x"))
	require.NoError(t, err)
	assert.False(t, v.Refused)
}

// A judge nobody stubbed is a refusal with no verdict, never a call to a model.
func TestStubbedJudge_AnUnstubbedJudgeRefusesWithNoVerdict(t *testing.T) {
	defer InstallJudgeStub(func(JudgeInvocation) JudgeAnswer { return JudgeAnswer{} })()
	v, err := Runner{}.runJudgeStubbedForTest(stubCall(t, "x"))
	require.NoError(t, err)
	assert.True(t, v.Refused)
	assert.True(t, v.NoVerdict)
	assert.Contains(t, v.Reason, "stubs no answer")
}

// A template that does not render fails before the stub is asked, as it would live.
func TestStubbedJudge_ABrokenTemplateIsNotStubbedAway(t *testing.T) {
	called := false
	defer InstallJudgeStub(func(JudgeInvocation) JudgeAnswer { called = true; return JudgeAnswer{Stubbed: true, Pass: true} })()
	v, err := Runner{}.runJudgeStubbedForTest(stubCall(t, "{{ event.path | uppper }}"))
	if err == nil {
		assert.True(t, v.Refused)
	}
	assert.False(t, called)
}

// The zero-value Runner takes the stub only while one is installed; afterwards it is the
// production sr-agent path again.
func TestWithDefaults_TheStubIsPerInstallation(t *testing.T) {
	restore := InstallJudgeStub(func(JudgeInvocation) JudgeAnswer { return JudgeAnswer{Stubbed: true, Pass: true} })
	assert.NotNil(t, Runner{}.withDefaults().runJudge)
	restore()
	assert.Nil(t, installedJudgeStub.Load())
}

// runJudgeStubbedForTest is Runner.Judge's last step with the stub the process has installed.
func (r Runner) runJudgeStubbedForTest(j judgeCall) (Verdict, error) {
	return r.withDefaults().runJudge(j)
}
