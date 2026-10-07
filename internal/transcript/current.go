package transcript

import (
	"os"

	"github.com/sloprail/sloprail/internal/harness"
)

// CurrentSessionPath resolves the CURRENT session's own transcript from the
// environment, or "" when it cannot. The running harness answers for itself
// (harness.CurrentTranscriptLocator): each names its session in its own
// variables and files its record in its own place.
//
// cwd is the caller's reported working directory when there is one; "" uses the
// process's own.
func CurrentSessionPath(cwd string) string {
	if l, ok := harness.Current().(harness.CurrentTranscriptLocator); ok {
		path, _ := l.CurrentTranscript(os.Getenv, cwd)
		return path
	}
	return ""
}
