// Package changeset is what a file-guard judges: the net change between two
// commits, as one value.
//
// A file-guard used to read the working tree one file at a time, so a
// half-finished edit could be judged and there was never "a concrete slice of
// the change to review". It now reads commits. This package turns a range of
// history into the payload a check receives on stdin and a judge template
// renders, resolves the grounding written into the range's commit messages, and
// says what makes two such inputs the same one.
//
// It owns no git and no database: git comes from gitrepo, verdicts live in
// sessionstate, and quotes resolve through transcript. What is here is the
// shape and the rules that shape follows.
package changeset

import "github.com/sloprail/sloprail/internal/transcript"

// Kind is the event kind a file-guard's check is handed.
const Kind = "Changeset"

// DefaultSubjectID is the id of the one subject a rule without `subjects:` has:
// the whole changeset.
const DefaultSubjectID = "changeset"

// Payload is what a check receives on stdin and a template renders.
type Payload struct {
	Event     Event     `json:"event"`
	Changeset Changeset `json:"changeset"`
	// Subject is what is being judged as one unit. Without `subjects:` it is the
	// whole changeset; with it, one entry the rule's script returned. Carried
	// from the start so `subjects:` adds a producer, not a field.
	Subject        Subject        `json:"subject"`
	TranscriptPath string         `json:"transcriptPath"`
	Context        map[string]any `json:"context"`
}

// Event is the fixed event a changeset evaluation presents.
type Event struct {
	Kind string `json:"kind"`
}

// Changeset is the net change between Base and Head.
type Changeset struct {
	Base string `json:"base"`
	Head string `json:"head"`
	// Commits are every commit in Base..Head, oldest first.
	Commits []Commit `json:"commits"`
	// Files are what the rule's `match` selected, in full.
	Files []File `json:"files"`
	// Others are the rest of the range as names only, so a judge knows what else
	// moved without paying for it.
	Others []Other `json:"others"`
	// Citations are the quotes the range's commits ground themselves in, resolved
	// against the transcripts, in the shape an event's citations have.
	Citations []transcript.Citation `json:"citations"`
}

// Commit is one commit of the range.
type Commit struct {
	SHA     string `json:"sha"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
	// Trailers maps each trailer key, in canonical case (Sloprail-Cites-User), to
	// its values in order.
	Trailers map[string][]string `json:"trailers"`
}

// File is one selected file of the changeset.
type File struct {
	Path string `json:"path"`
	// Status is A, M, D or R.
	Status string `json:"status"`
	// OldPath is where a renamed file came from; empty otherwise.
	OldPath    string   `json:"oldPath"`
	OldContent string   `json:"oldContent"`
	NewContent string   `json:"newContent"`
	OldMarkers []Marker `json:"oldMarkers"`
	NewMarkers []Marker `json:"newMarkers"`
	// Diff is this file's part of the squashed diff.
	Diff string `json:"diff"`
}

// Other is a file of the range the rule did not select.
type Other struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

// Marker is one `sr:<kind> <fqn>` annotation, in the wire form an event carries.
type Marker struct {
	Kind string `json:"kind"`
	FQN  string `json:"fqn"`
	Line int    `json:"line"`
}

// Subject is one unit of evaluation and caching.
//
// Nothing produces anything but the whole-changeset subject yet. The shape is
// here so that `subjects:` — a script returning several, each fingerprinted and
// judged on its own, possibly over a per-commit sub-range — is a new producer of
// this type and not a change to it or to the verdict key, which already carries
// a subject id.
type Subject struct {
	ID    string   `json:"id"`
	Files []string `json:"files"`
	// Range, when set, narrows the subject to a sub-range of the changeset (a
	// single commit's, for example). Unused until `subjects:` exists.
	Range   *SubRange      `json:"range,omitempty"`
	Context map[string]any `json:"context"`
}

// SubRange is a sub-range of the changeset, as commit names.
type SubRange struct {
	Base string `json:"base"`
	Head string `json:"head"`
}

// Whole is the one subject a rule without `subjects:` has: every selected file.
func Whole(cs Changeset) Subject {
	files := make([]string, 0, len(cs.Files))
	for _, f := range cs.Files {
		files = append(files, f.Path)
	}
	return Subject{ID: DefaultSubjectID, Files: files, Context: map[string]any{}}
}

// NewPayload assembles what a check receives for one subject of a changeset.
func NewPayload(cs Changeset, subject Subject, transcriptPath string, context map[string]any) Payload {
	if context == nil {
		context = map[string]any{}
	}
	if subject.Context == nil {
		subject.Context = map[string]any{}
	}
	return Payload{
		Event:          Event{Kind: Kind},
		Changeset:      cs,
		Subject:        subject,
		TranscriptPath: transcriptPath,
		Context:        context,
	}
}
