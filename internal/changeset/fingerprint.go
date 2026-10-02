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
// A judge is pure: it judges the slice it is rendered, and its verdict is a function of
// the prompt it is given. So the fingerprint is the sha256 of that prompt, FULLY
// RENDERED (the template with the subject's slice and prepare's additionalContext
// folded in), plus the two things that are not in it:
//
//   - prepareFP: the "fingerprint" string a prepare step may return, for whatever
//     the judge's verdict depends on that is not in the prompt (a file the judge
//     opens with its own tools). A judge that reads more than it is shown without
//     declaring it is a stale-verdict bug in the rule, not in the key.
//   - citations: for a `require: citation` rule only, CitationPart: the commit messages and
//     the citations' quotes, which a prompt may not render.
//
// Nothing volatile may enter: the caller normalises the snapshot's temp path out of the
// prompt before it gets here, and no run id, timestamp or session id is part of it.
// Parts are length-prefixed, so two parts cannot be re-cut into another pair.
func JudgeFingerprint(renderedPrompt, prepareFP, citations string) string {
	var buf []byte
	for _, part := range []string{renderedPrompt, prepareFP, citations} {
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
