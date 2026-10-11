package changeset

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ParseSubjects reads what a rule's `subjects:` script printed: a JSON array of
// {"id": "...", "files": ["..."], "fingerprint": "...", "batch": "..."} (fingerprint and
// batch optional; batch groups subjects, see Batches). Each id must be
// unique and non-empty. A subject is a FILE subject when it names files (every one of them a
// file the changeset selected: the verdict is keyed by their content, plus the fingerprint
// when there is one) or any other subject (an FQN, say), which names no file and must then
// carry a fingerprint: it is the whole of what its verdict is about, and a subject with
// neither would be keyed by nothing, so it is an error (fail closed), never an empty key. An
// empty list is an error: a rule that selected files has something to judge, and a script
// that names nothing must not read as a pass.
// sr:invariant fileguard/subject-contract
func ParseSubjects(stdout []byte, cs Changeset) ([]Subject, error) {
	var raw []struct {
		ID          string   `json:"id"`
		Files       []string `json:"files"`
		Fingerprint string   `json:"fingerprint"`
		Batch       *string  `json:"batch"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(stdout))), &raw); err != nil {
		return nil, fmt.Errorf("printed something that is not a JSON array of {id, files, fingerprint, batch}: %w", err)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("named no subject, though the rule selected %d file(s)", len(cs.Files))
	}
	selected := map[string]bool{}
	for _, f := range cs.Files {
		selected[f.Path] = true
	}
	seen := map[string]bool{}
	out := make([]Subject, 0, len(raw))
	for i, r := range raw {
		switch {
		case r.ID == "":
			return nil, fmt.Errorf("subject %d has no id", i)
		case seen[r.ID]:
			return nil, fmt.Errorf("subject id %q is named twice", r.ID)
		case len(r.Files) == 0 && r.Fingerprint == "":
			return nil, fmt.Errorf("subject %q names no file and has no fingerprint, so nothing says what its verdict is about", r.ID)
		case r.Batch != nil && *r.Batch == "":
			// An empty id is a bug in the script, not "no batch": omit the key for that.
			return nil, fmt.Errorf("subject %q names an empty batch; omit \"batch\" to hand it alone", r.ID)
		}
		seen[r.ID] = true
		for _, f := range r.Files {
			if !selected[f] {
				return nil, fmt.Errorf("subject %q names %q, which the rule did not select", r.ID, f)
			}
		}
		sub := Subject{ID: r.ID, Files: r.Files, Context: map[string]any{}, Fingerprint: r.Fingerprint}
		if r.Batch != nil {
			sub.Batch = *r.Batch
		}
		out = append(out, sub)
	}
	return out, nil
}
