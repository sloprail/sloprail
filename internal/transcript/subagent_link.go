package transcript

import (
	"errors"

	"github.com/sloprail/sloprail/internal/harness"
)

// ErrSubagentUnlinked: a citation was asked of a sub-agent on a harness that cannot tie a
// sub-agent's session to the conversation that dispatched it (harness.SubagentsUnlinkable).
// It is a refusal to judge here, not a verdict: the quote was never searched, so neither
// "resolved" nor "not there" is true. Callers render it as itself and never store it as a
// failure, so the main agent's own run judges the same citation fresh.
var ErrSubagentUnlinked = errors.New("on this harness a sub-agent's session cannot be tied to the user's conversation, so a citation cannot be resolved from a sub-agent: citations, and the checks that resolve them (sr-checks run), must be made from the main agent. Hand it the quote or the output to cite, and run sr-checks there")

// SubagentsLinkable reports whether the running harness can tie a sub-agent's session to
// the conversation that dispatched it.
func SubagentsLinkable() bool {
	u, ok := harness.Current().Transcripts().(harness.SubagentsUnlinkable)
	return !ok || !u.SubagentsUnlinkable()
}

// CitationsUnavailable is the one place that decides a citation cannot be resolved for
// the trajectory at path: ErrSubagentUnlinked when the harness cannot link sub-agents and
// path is (or the caller says it is, subagent) a sub-agent's. Everything that resolves a
// citation asks it, directly or through citeInSession.
func CitationsUnavailable(path string, subagent bool) error {
	if SubagentsLinkable() {
		return nil
	}
	if subagent || IsSubagentTranscript(path) {
		return ErrSubagentUnlinked
	}
	return nil
}
