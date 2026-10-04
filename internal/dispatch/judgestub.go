package dispatch

import "sync/atomic"

// A rule's tests answer its judges from a canned table instead of a model
// (`sr-checks test`, see internal/ruletest). The seam is a process-wide hook that
// ONLY a process which deliberately installs it has: the production binaries'
// hook points (sr-session pre-tool, stop, ...) and `sr-checks run` never call
// InstallJudgeStub, so a real session cannot reach it by an environment variable,
// a flag or a file. The two callers are `sr-checks test`/`doctor` and the
// sandbox-only `sr-session replay`, which both refuse to act outside the
// throwaway sandbox they were handed.

// JudgeInvocation is one judge the engine is about to ask.
type JudgeInvocation struct {
	// GuardName is the rule's name (its folder name).
	GuardName string
	// Dir is the rule's folder; Template is the judge file as the rule declares
	// it (`./is-clean.md.j2`), resolved against Dir.
	Dir, Template string
	// Prompt is the template rendered against the check's input, exactly the
	// text a model would be shown (without the verdict instruction).
	Prompt string
}

// JudgeAnswer is what the stub says for a judge. Stubbed false means the case
// stubbed nothing for it: the judge refuses without a verdict and the stub's
// owner records the invocation as a failure of the case.
type JudgeAnswer struct {
	Stubbed   bool
	Pass      bool
	Reasoning string
}

// JudgeStub answers a judge invocation.
type JudgeStub func(JudgeInvocation) JudgeAnswer

var installedJudgeStub atomic.Pointer[JudgeStub]

// InstallJudgeStub makes every judge this process asks go to stub instead of
// sr-agent, until the returned function is called. The template is still rendered
// first, so a template that does not render fails exactly as it would live.
func InstallJudgeStub(stub JudgeStub) (restore func()) {
	prev := installedJudgeStub.Swap(&stub)
	return func() { installedJudgeStub.Store(prev) }
}

// stubbedJudge is the runJudge a process with a stub installed uses.
func stubbedJudge(stub JudgeStub) func(judgeCall) (Verdict, error) {
	return func(j judgeCall) (Verdict, error) {
		rendered, refusal, err := renderJudgePrompt(j)
		if err != nil {
			return Verdict{}, err
		}
		if refusal != "" {
			return refuse(refusal), nil
		}
		ans := stub(JudgeInvocation{GuardName: j.GuardName, Dir: j.Dir, Template: j.Template, Prompt: rendered})
		if !ans.Stubbed {
			return refuseNoVerdict("the judge " + j.Template + " was reached, but this test case stubs no answer for it (add it under `judges:` in case.yaml)"), nil
		}
		if ans.Pass {
			return pass(), nil
		}
		return refuse(ans.Reasoning), nil
	}
}
