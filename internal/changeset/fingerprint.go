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
//   - citations: for a `require: citation` rule only, CitationPart: the quotes of the
//     citations that ground the subject.
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
	}
	// Per file, not per subject: which file a quote grounds is part of the verdict (a quote
	// that grounds one file of a subject and not another is not the same grounding).
	var out []grounded
	for _, f := range p.Changeset.Files {
		if len(p.Subject.Files) > 0 && !slices.Contains(p.Subject.Files, f.Path) {
			continue
		}
		g := grounded{Path: f.Path}
		for _, c := range p.Changeset.ForFile(f) {
			g.Quotes = append(g.Quotes, quote{Pool: c.SourceTypes, Quote: c.Quote})
		}
		sort.Slice(g.Quotes, func(i, j int) bool {
			a, b := g.Quotes[i], g.Quotes[j]
			if a.Quote != b.Quote {
				return a.Quote < b.Quote
			}
			return fmt.Sprint(a.Pool) < fmt.Sprint(b.Pool)
		})
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
		if f.NewBlob != "" {
			// git's own hash of the bytes: the same content has the same id, and nothing was read.
			content = "blob:" + f.NewBlob
		}
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
