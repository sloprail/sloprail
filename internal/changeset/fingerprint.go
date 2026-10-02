package changeset

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
)

// JudgeFingerprint says what a judge was asked, as one short string: the cache key's
// last part (the rest — rule, rule hash, step, subject — is the checkcache key's own).
//
// It is the sha256 of four parts, none of which depends on the session or the history:
//
//   - template: the bytes of the judge template. The rendered prompt is NOT keyed: it may
//     carry prepare's session-derived text, which `verify` in CI cannot reproduce.
//   - files: FilesPart, the content of the subject's matched files, ALWAYS, whether or not
//     the template renders them: the verdict is about those bytes.
//   - prepareFP: the "fingerprint" string a prepare step may return, ADDED to the rest, for
//     whatever the verdict depends on beyond the template and the files (a file the judge
//     opens with its own tools). It must be session-independent.
//   - citations: for a `require: citation` rule only, CitationPart: the commit messages and
//     the citations' quotes.
//
// No commit SHA, run id, timestamp, session id or path of a snapshot is part of it. Parts
// are length-prefixed, so two parts cannot be re-cut into another pair.
func JudgeFingerprint(template, files, prepareFP, citations string) string {
	var buf []byte
	for _, part := range []string{template, files, prepareFP, citations} {
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

// FilesPart is the content of the subject's matched files, in the subject's order: the
// bytes the verdict is about, keyed whether or not the template renders them. Content only
// (a deleted file is marked as such): no SHA, no base, no path.
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
		buf = binary.BigEndian.AppendUint64(buf, uint64(len(content)))
		buf = append(buf, content...)
	}
	return string(buf)
}
