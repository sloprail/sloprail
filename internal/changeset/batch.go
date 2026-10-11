package changeset

// A subject is the unit a verdict is given to and kept under; a batch is the unit a
// rule's checks are run on. A rule's `subjects:` script decides the grouping (each
// subject's `batch`), never the engine: what belongs together is the rule's knowledge.
//
// A subject with no batch is handed alone, in the `subject` payload rules have always
// had. A subject with one, even alone in its batch, is handed in the `batch` payload: a
// rule that names batches has opted in to it, so the payload's shape never depends on
// how many subjects a run happened to leave in a batch.

// Batch is the subjects a check is handed together.
type Batch struct {
	ID string `json:"id"`
	// Subjects are the batch's subjects still to be decided, in the order the
	// `subjects:` script printed them.
	Subjects []Subject `json:"subjects"`
}

// IDs are the batch's subject ids, in order.
func (b Batch) IDs() []string {
	ids := make([]string, len(b.Subjects))
	for i, s := range b.Subjects {
		ids[i] = s.ID
	}
	return ids
}

// Group is one unit of execution: the subjects run together. Batch is "" for a
// subject handed alone (Group then holds exactly one subject).
type Group struct {
	Batch    string
	Subjects []int // indexes into the list Batches was given
}

// Batches groups subjects into units of execution, in order of first appearance: the
// subjects of one batch id together, each subject without one on its own.
func Batches(subjects []Subject) []Group {
	var out []Group
	at := map[string]int{}
	for i, s := range subjects {
		if s.Batch == "" {
			out = append(out, Group{Subjects: []int{i}})
			continue
		}
		if g, ok := at[s.Batch]; ok {
			out[g].Subjects = append(out[g].Subjects, i)
			continue
		}
		at[s.Batch] = len(out)
		out = append(out, Group{Batch: s.Batch, Subjects: []int{i}})
	}
	return out
}

// NewBatchPayload assembles what a check receives for a batch of a changeset: the
// batch in place of the one subject.
func NewBatchPayload(cs Changeset, batch Batch, transcriptPath string) Payload {
	subs := make([]Subject, len(batch.Subjects))
	for i, s := range batch.Subjects {
		if s.Context == nil {
			s.Context = map[string]any{}
		}
		s.Batch = "" // the batch names itself once, not on each of its subjects
		subs[i] = s
	}
	batch.Subjects = subs
	return Payload{
		Event:          Event{Kind: Kind},
		Changeset:      cs,
		Batch:          &batch,
		TranscriptPath: transcriptPath,
	}
}

// EvidenceForBatch is the citations a batch's checks receive: every citation that is
// evidence for one of its subjects (EvidenceForSubject), once, in the range's order.
func (cs Changeset) EvidenceForBatch(b Batch) []Citation {
	var files []string
	for _, s := range b.Subjects {
		files = append(files, s.Files...)
	}
	return cs.EvidenceForSubject(Subject{Files: files})
}
