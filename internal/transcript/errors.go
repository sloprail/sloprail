package transcript

import "errors"

// The failure kinds a caller may want to tell apart, named rather than left to
// be recognised by the words in a message. A caller matching on text is a
// caller that breaks when the text is improved, and these messages are written
// to be read by a person — which means they will be.
//
// Every one of these is a refusal to answer, never a quiet default. What they
// distinguish is WHICH thing was wrong, so a hook can say so; none of them
// offers a way to carry on regardless.
var (
	// ErrNoSessionRoot: the user's own words were asked of a sub-agent's record
	// whose session's root record is not where the layout puts it. A sub-agent's
	// "user" message is the parent agent's dispatch, so without the root there
	// is nothing of the end user's to search.
	ErrNoSessionRoot = errors.New("a sub-agent's record whose session root is not found holds none of the end user's words")

	// ErrNoTranscriptPath: nothing was named to read. A hook runs where the
	// harness's record exists, so this is the caller's own omission.
	ErrNoTranscriptPath = errors.New("no transcript path given")

	// ErrNoOriginRecord: every entry in a transcript has a parent, so the
	// conversation it belongs to has no beginning in it. Not a session we
	// understand, and guessing at an origin would key state on a guess.
	ErrNoOriginRecord = errors.New("transcript has no origin record")

	// ErrContinuationMissing: a record says it continues an earlier one, and no
	// other transcript holds what it names. The environment lost a file;
	// answering anyway would answer wrongly.
	ErrContinuationMissing = errors.New("the record this transcript continues is in no other transcript")

	// ErrNoProjectDir: crossing a restart needs the conversation's other
	// transcripts, and where those sit was not resolved.
	ErrNoProjectDir = errors.New("the harness's project directory is unknown")

	// ErrChainRunaway: the walk across restarts revisited a file or ran past
	// its bound. A real conversation resumes a handful of times.
	ErrChainRunaway = errors.New("the chain of continuations loops or is longer than a real conversation")

	// ErrExpressionNotBoolean: an expression evaluated to something that is not
	// a yes or a no. The expression is wrong about itself.
	ErrExpressionNotBoolean = errors.New("expression did not produce a boolean")

	// ErrExpressionNeverRan: an expression could not be evaluated against a
	// single entry it was offered. It has told us nothing about the session, and
	// reporting nothing matched would report no violations without having
	// looked.
	ErrExpressionNeverRan = errors.New("expression could not be evaluated against any entry")

	// ErrNotAnAgentID: what was offered as a sub-agent's id is not a name, so no
	// file path may be built from it. An id carrying a path separator would
	// traverse out of the directory it is joined into and read — then key state
	// against — another conversation entirely.
	ErrNotAnAgentID = errors.New("the reported agent id is not an agent id")
)
