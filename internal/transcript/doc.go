// Package transcript reads a harness's record of a session into the one shape
// every rule is written against.
//
// A harness records a great deal more than a rule ever asks about — routing,
// versions, request ids, its own bookkeeping. Carrying that through would make
// this shape a mirror of Claude Code's, which is the opposite of the point:
// every field kept here is a field each new harness must be normalised into.
//
// The shape is taken from what Claude Code already writes, because a format
// that has carried real sessions is a better starting point than one reasoned
// out cold. It is ours from then on, and Claude Code is simply the harness
// needing no translation today.
package transcript

import "github.com/sloprail/sloprail/internal/harness"

// Entry, EntryType and the Entry* kinds are defined by the harness-neutral
// internal/harness package, which is where a harness implementation
// normalises its own record format into them; they are re-exported here so the
// readers of a session need not name two packages.
type (
	Entry     = harness.Entry
	EntryType = harness.EntryType
)

const (
	EntryUser       = harness.EntryUser
	EntryAssistant  = harness.EntryAssistant
	EntrySystem     = harness.EntrySystem
	EntryAttachment = harness.EntryAttachment
)
