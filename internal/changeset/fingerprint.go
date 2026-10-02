package changeset

import (
	"encoding/binary"
	"encoding/json"

	"github.com/sloprail/sloprail/internal/fingerprint"
)

// Fingerprint says what a check's input IS, as one short string.
//
// It is the check's cache key, so it also covers what the verdict depends on
// besides the input: the rule's hash (an edited rubric, script or template
// invalidates every earlier verdict) and the model (a different judge is a
// different verdict). a10n's key left both out and kept serving stale passes.
//
// The input part is the CONTENT of the change: the files (paths, statuses, both
// contents, both marker sets, diffs), the others, the citations' quotes (not where in a
// transcript they were found), the subject
// and the context. It is never HISTORY: no commit SHA, and none of how many commits
// the change was made in or what they said. A rebase or an amend changes every SHA, a
// squash changes the commits and their messages, a revert and a re-apply changes the
// path the content took to get here, and none of that is a new input to a check that
// judges the content. The same net change is the same input, wherever and however it
// was made.
//
// It deliberately covers nothing more. a10n folded extra context into its
// fingerprint and got cascades of re-judging from changes that could not have
// altered the verdict. The transcript path is left out for the same reason: it
// names where the record is, not what the check reads.
//
// TODO: key per subject (the subject's own content) once `subjects:` splits a changeset;
// today the one subject is the whole changeset.
//
// extra is whatever else the check is handed that is not in the payload: what a
// `prepare` step inlined, the rendered prompt. Each part is length-prefixed, so
// two parts cannot be re-cut into another pair with the same concatenation.
func Fingerprint(p Payload, ruleHash, model string, extra ...string) (string, error) {
	view := p
	view.TranscriptPath = ""
	view.Changeset.Base, view.Changeset.Head = "", ""
	view.Changeset.Commits = nil
	view.Changeset.Files = make([]File, len(p.Changeset.Files))
	for i, f := range p.Changeset.Files {
		f.Commits, f.Substantive = nil, nil
		view.Changeset.Files[i] = f
	}
	// A citation is its QUOTE and the pool it resolved in: where in which transcript it was found
	// (path, line, the whole cited message) is only known where the transcript is, and a result
	// found by the author must be found by anyone who sees the same quote in the commit.
	view.Changeset.Citations = make([]Citation, len(p.Changeset.Citations))
	for i, c := range p.Changeset.Citations {
		c.Commits = nil
		c.Citation.Path, c.Citation.Line, c.Citation.Message = "", 0, ""
		view.Changeset.Citations[i] = c
	}
	body, err := json.Marshal(view)
	if err != nil {
		return "", err
	}
	buf := frame(frame(frame(nil, []byte(ruleHash)), []byte(model)), body)
	for _, e := range extra {
		buf = frame(buf, []byte(e))
	}
	return fingerprint.Of(buf), nil
}

func frame(buf, part []byte) []byte {
	buf = binary.BigEndian.AppendUint64(buf, uint64(len(part)))
	return append(buf, part...)
}
