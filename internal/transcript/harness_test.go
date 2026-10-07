package transcript

import (
	"github.com/sloprail/sloprail/internal/harness"
	claudeformat "github.com/sloprail/sloprail/internal/harness/claudecode/record"
)

// These tests exercise the neutral readers over Claude Code's real line format.
// They cannot import internal/harness/claudecode (it imports this package), so
// they register a harness that supplies only the transcript half, from the
// format package both share.
type transcriptsOnly struct{ harness.Harness }

func (transcriptsOnly) Name() string                     { return "claudecode" }
func (transcriptsOnly) Transcripts() harness.Transcripts { return claudeformat.Transcripts{} }

func init() { harness.Register(transcriptsOnly{}) }
