package transcript

import (
	"os"

	"github.com/sloprail/sloprail/internal/harness"
)

// EnvHookTranscript is the variable the engine sets on a check it runs from a hook: the
// session's record as the hook resolved it (from the payload, or the harness's own
// locator when the payload names none).
const EnvHookTranscript = "SR_TRANSCRIPT"

// CurrentSessionPath resolves the CURRENT session's own transcript from the
// environment, or "" when it cannot. The running harness answers for itself
// (harness.CurrentTranscriptLocator): each names its session in its own
// variables and files its record in its own place. When the harness has none to
// name, a tool a hook started has the engine's answer in SR_TRANSCRIPT: a hook
// payload can carry no transcript path yet (Cursor's first tool call of a run),
// and that hook's environment names no session either.
//
// cwd is the caller's reported working directory when there is one; "" uses the
// process's own.
func CurrentSessionPath(cwd string) string {
	if l, ok := harness.Current().(harness.CurrentTranscriptLocator); ok {
		if path, _ := l.CurrentTranscript(os.Getenv, cwd); path != "" {
			return path
		}
	}
	return os.Getenv(EnvHookTranscript)
}
