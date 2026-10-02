package changeset

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
)

// GuardFingerprint says what a guard's verdict over one subject is about, as one short string:
// the cache key's last part (the rest — rule, rule hash, the guard's fixed step id, subject id —
// is the checkcache key's own, and the rule hash covers every script and template of the rule).
//
// It is the sha256 of three parts, none of which depends on the session or the history:
//
//   - files: FilesPart, the path and content of the subject's files, ALWAYS, whether or not any check
//     reads them: the verdict is about those bytes.
//   - subjectFP: the "fingerprint" the rule's `subjects:` script gave this subject, for whatever
//     the verdict depends on beyond the files (a file a check opens with its own tools). It
//     must be session-independent. Empty without `subjects:`.
//   - citations: for a `require: citation` rule only, CitationPart: the commit messages and
//     the citations' quotes.
//
// No commit SHA, run id, timestamp, session id or path of a snapshot is part of it. Parts
// are length-prefixed, so two parts cannot be re-cut into another pair.
func GuardFingerprint(files, subjectFP, citations string) string {
	var buf []byte
	for _, part := range []string{files, subjectFP, citations} {
		buf = binary.BigEndian.AppendUint64(buf, uint64(len(part)))
		buf = append(buf, part...)
	}
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:])
}

// CitationPart is what a `require: citation` rule's verdict additionally depends on: the
// commits' subjects, bodies and trailers (citations are read from the trailers, so a
// reword is a new input) and the citations' quotes and pools. Never a commit SHA (a
// rebase or amend changes every SHA and none of the content), and never where in a
// transcript a quote was found: the path, line, cited message and the tool call that
// printed it (the volatile Call field) are blanked.
func CitationPart(p Payload) (string, error) {
	view := struct {
		Commits   []Commit
		Citations []Citation
	}{
		Commits:   make([]Commit, len(p.Changeset.Commits)),
		Citations: make([]Citation, len(p.Changeset.Citations)),
	}
	for i, c := range p.Changeset.Commits {
		c.SHA = ""
		view.Commits[i] = c
	}
	for i, c := range p.Changeset.Citations {
		c.Commits = blankSHAs(c.Commits)
		c.Citation.Path, c.Citation.Line, c.Citation.Message, c.Citation.Call = "", 0, "", ""
		view.Citations[i] = c
	}
	body, err := json.Marshal(view)
	return string(body), err
}

// blankSHAs keeps how many commits there were and drops which.
func blankSHAs(shas []string) []string {
	if shas == nil {
		return nil
	}
	return make([]string, len(shas))
}

// FilesPart is the path and content of the subject's matched files, in the subject's order:
// the bytes the verdict is about, and where they are (the same bytes at a new path have never
// been judged there: a rule's prompt and its match are about the path too), keyed whether or
// not the template renders them. A deleted file is marked as such; no SHA, no base.
func FilesPart(p Payload) string {
	byPath := make(map[string]File, len(p.Changeset.Files))
	for _, f := range p.Changeset.Files {
		byPath[f.Path] = f
	}
	paths := p.Subject.Files
	if len(paths) == 0 {
		for _, f := range p.Changeset.Files {
			paths = append(paths, f.Path)
		}
	}
	var buf []byte
	for _, path := range paths {
		f := byPath[path]
		content := f.NewContent
		if f.Status == "D" {
			content = "\x00deleted"
		}
		buf = binary.BigEndian.AppendUint64(buf, uint64(len(path)))
		buf = append(buf, path...)
		buf = binary.BigEndian.AppendUint64(buf, uint64(len(content)))
		buf = append(buf, content...)
	}
	return string(buf)
}
