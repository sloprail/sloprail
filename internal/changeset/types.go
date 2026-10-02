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

import (
	"slices"
	"strings"

	"github.com/sloprail/sloprail/internal/transcript"
)

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
	// against the transcripts, in the shape an event's citations have, each with
	// the commits that carried it and the selected files those commits changed.
	// They accumulate over the whole range, for a judge; `require: citation`
	// reads them per file (see ForFile).
	Citations []Citation `json:"citations"`
}

// Citation is one resolved quote of the range, and where in the range it came
// from. The quote's own fields are an event citation's, flattened.
type Citation struct {
	transcript.Citation
	// Commits are the SHAs of the range's commits whose trailers quote it.
	Commits []string `json:"commits"`
	// Files are the selected files those commits changed.
	Files []string `json:"files"`
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
	// Commits are the SHAs of the range's commits that changed this file, oldest
	// first, a rename followed back to the name the file had before it.
	Commits []string `json:"commits"`
	// Substantive is the subset of Commits whose change to this file is more than
	// whitespace. Not part of the wire form: it decides which commits must carry a
	// citation (ForFile).
	Substantive []string `json:"-"`
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
// Subjects (by Role) produces them: one file per requirement, the whole changeset
// per check. A rule's `subjects:` script (ParseSubjects) returns several instead, each
// fingerprinted and judged on its own; the verdict key already carries a subject id.
type Subject struct {
	ID    string   `json:"id"`
	Files []string `json:"files"`
	// Range, when set, narrows the subject to a sub-range of the changeset (a
	// single commit's, for example). Not produced yet.
	Range   *SubRange      `json:"range,omitempty"`
	Context map[string]any `json:"context"`
	// Fingerprint is what the `subjects:` script says the subject's verdict depends on
	// besides its files' content (a file the checks open with their own tools): added to the
	// verdict's cache key. Session-independent; empty for the default subject.
	Fingerprint string `json:"fingerprint,omitempty"`
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

// Role is what a subject is for: a rule's requirements and its checks do not
// default to the same unit.
type Role int

const (
	// Requirement: the unit a `require` entry (and its `when`) is evaluated for.
	// Without `subjects:` it is ONE SELECTED FILE, so a requirement applies to the
	// files whose `when` applies and is named for each it fails on.
	Requirement Role = iota
	// Check: the unit a script or judge is handed. Without `subjects:` it is the
	// whole changeset, judged once.
	Check
)

// Subjects is the one place a rule's subjects come from. Today it is the default
// for the role; `subjects:` will replace the body (a rule's script returning the
// list) and nothing that calls it changes: every caller already takes a slice of
// Subject and keys its results by Subject.ID.
func Subjects(cs Changeset, role Role) []Subject {
	if role == Check {
		return []Subject{Whole(cs)}
	}
	out := make([]Subject, 0, len(cs.Files))
	for _, f := range cs.Files {
		out = append(out, Subject{ID: f.Path, Files: []string{f.Path}, Context: map[string]any{}})
	}
	return out
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

// Change is the combined diff of the changeset's selected files: what a judge's
// `{{ change }}` renders. Each file's own part of the squashed diff, in order.
func (cs Changeset) Change() string {
	var b strings.Builder
	for _, f := range cs.Files {
		b.WriteString(f.Diff)
		if f.Diff != "" && !strings.HasSuffix(f.Diff, "\n") {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// ForFile is the citations that ground a file: those quoted by the last commit of the
// range that changed its content by more than whitespace. A citation grounds the
// change it rode on, so an uncited real change on top of a cited one leaves the file
// uncited, while a cited real change on top of an uncited one grounds the file as it
// now stands. A whitespace-only commit (or an empty trailer-only one) grounds nothing
// and is skipped: it can neither lend a citation to an earlier uncited change nor
// take one away. A file whose every commit is whitespace-only is judged by the commit
// that last changed it. A file no commit is known to have changed has no citations.
func (cs Changeset) ForFile(f File) []Citation {
	if len(f.Commits) == 0 {
		return nil
	}
	tip := f.Commits[len(f.Commits)-1]
	if len(f.Substantive) > 0 {
		tip = f.Substantive[len(f.Substantive)-1]
	}
	var out []Citation
	for _, c := range cs.Citations {
		if slices.Contains(c.Commits, tip) {
			out = append(out, c)
		}
	}
	return out
}

// ForSubject is the citations that ground a subject: those that ground each of
// its files (see ForFile), without repeats. A subject naming no known file has none.
func (cs Changeset) ForSubject(s Subject) []Citation {
	var out []Citation
	seen := map[string]bool{}
	for _, f := range cs.Files {
		if !slices.Contains(s.Files, f.Path) {
			continue
		}
		for _, c := range cs.ForFile(f) {
			if key := c.Quote + "\x00" + strings.Join(c.Commits, ","); !seen[key] {
				seen[key] = true
				out = append(out, c)
			}
		}
	}
	return out
}

// Plain is the citations as an event carries them, without where they came from.
func Plain(cites []Citation) []transcript.Citation {
	out := make([]transcript.Citation, 0, len(cites))
	for _, c := range cites {
		out = append(out, c.Citation)
	}
	return out
}
