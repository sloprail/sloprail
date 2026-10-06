package changeset

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"sort"

	"github.com/sloprail/sloprail/internal/transcript"
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
//   - citations: CitationPart, the quotes of the citations that ground the subject (the only
//     citations its checks receive). A commit touching none of the subject's files is no input.
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
// quotes (with their pools) of the citations that ground the payload's subject, sorted. Nothing
// else of the range: not a commit's subject or body, not a trailer that grounds another
// subject, never a commit SHA, and never where in a transcript a quote was found.
func CitationPart(p Payload) (string, error) {
	type quote struct {
		Pool  []transcript.SourceType
		Quote string
	}
	type grounded struct {
		Path   string
		Quotes []quote
		// Evidence is every quote cited by a commit that changed the file
		// (EvidenceForFile): what a check is handed, beyond what grounds it.
		Evidence []quote
	}
	sorted := func(qs []quote) {
		sort.Slice(qs, func(i, j int) bool {
			a, b := qs[i], qs[j]
			if a.Quote != b.Quote {
				return a.Quote < b.Quote
			}
			return fmt.Sprint(a.Pool) < fmt.Sprint(b.Pool)
		})
	}
	// Per file, not per subject: which file a quote grounds is part of the verdict (a quote
	// that grounds one file of a subject and not another is not the same grounding).
	var out []grounded
	for _, f := range p.Changeset.Files {
		if !slices.Contains(p.Subject.Files, f.Path) {
			continue
		}
		g := grounded{Path: f.Path}
		for _, c := range p.Changeset.ForFile(f) {
			g.Quotes = append(g.Quotes, quote{Pool: c.SourceTypes, Quote: c.Quote})
		}
		for _, c := range p.Changeset.EvidenceForFile(f) {
			g.Evidence = append(g.Evidence, quote{Pool: c.SourceTypes, Quote: c.Quote})
		}
		sorted(g.Quotes)
		sorted(g.Evidence)
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	body, err := json.Marshal(out)
	return string(body), err
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
	// A subject naming no file (an FQN) has no files part: its fingerprint is its content.
	var buf []byte
	for _, path := range p.Subject.Files {
		f := byPath[path]
		content := f.NewContent
		if f.NewBlob != "" {
			// git's own hash of the bytes: the same content has the same id, and nothing was read.
			content = "blob:" + f.NewBlob
		}
		if f.Status == "D" {
			// What was deleted is part of the verdict: deleting a recreated file with other
			// content is another change. The old blob's id (or, with none, its bytes).
			content = "\x00deleted:" + f.OldContent
			if f.OldBlob != "" {
				content = "\x00deleted:blob:" + f.OldBlob
			}
		}
		buf = binary.BigEndian.AppendUint64(buf, uint64(len(path)))
		buf = append(buf, path...)
		buf = binary.BigEndian.AppendUint64(buf, uint64(len(content)))
		buf = append(buf, content...)
	}
	return string(buf)
}
