package changeset

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
)

// GuardFingerprint says what a guard's verdict over one subject is about, as one short string:
// the cache key's last part (the rest, the rule's name, the guard's fixed step id and the subject
// id, is the checkcache key's own).
//
// It is the sha256 of two parts, neither of which depends on the session or the history:
//
//   - files: FilesPart, the path and content of the subject's files, ALWAYS, whether or not any check
//     reads them: the verdict is about those bytes.
//   - subjectFP: the "fingerprint" the rule's `subjects:` script gave this subject, for whatever
//     the verdict depends on beyond the files (a file a check opens with its own tools). It
//     must be session-independent. Empty without `subjects:`.
//
// Citations are NOT part of it: a stored pass says the content was fine, and a citation is only
// a gate, which the engine checks afresh on every run (#291, #298).
//
// No commit SHA, run id, timestamp, session id or path of a snapshot is part of it. Parts
// are length-prefixed, so two parts cannot be re-cut into another pair.
func GuardFingerprint(files, subjectFP string) string {
	var buf []byte
	for _, part := range []string{files, subjectFP} {
		buf = binary.BigEndian.AppendUint64(buf, uint64(len(part)))
		buf = append(buf, part...)
	}
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:])
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
