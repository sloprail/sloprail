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
// The input part covers exactly what the check is given — the files (paths, statuses, both
// contents, both marker sets, diffs), the others, the commits' subjects, bodies
// and trailers, the citations, the subject, and the context — and NEVER a commit
// SHA. A rebase or an amend changes every SHA and none of the content; keyed on
// SHAs, the same input would be judged again, and a fail that was terminal would
// be re-judged into a different answer. Base, Head and every commit's SHA are
// therefore blanked before hashing.
//
// It deliberately covers nothing more. a10n folded extra context into its
// fingerprint and got cascades of re-judging from changes that could not have
// altered the verdict. The transcript path is left out for the same reason: it
// names where the record is, not what the check reads, and it is constant for
// the session the verdict is stored in.
//
// extra is whatever else the check is handed that is not in the payload: what a
// `prepare` step inlined, the rendered prompt. Each part is length-prefixed, so
// two parts cannot be re-cut into another pair with the same concatenation.
func Fingerprint(p Payload, ruleHash, model string, extra ...string) (string, error) {
	view := p
	view.TranscriptPath = ""
	view.Changeset.Base, view.Changeset.Head = "", ""
	view.Changeset.Commits = make([]Commit, len(p.Changeset.Commits))
	for i, c := range p.Changeset.Commits {
		c.SHA = ""
		view.Changeset.Commits[i] = c
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
