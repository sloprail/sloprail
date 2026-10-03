package checkrun

import (
	"slices"
	"strings"

	"github.com/sloprail/sloprail/internal/changeset"
	"github.com/sloprail/sloprail/internal/transcript"
)

// recordedQuote is a citation this session already recorded for a file (an
// `sr-file ... --cite:` that landed, by the agent or by a sub-agent sharing its
// tree), as the trailer that carries it in a commit.
type recordedQuote struct {
	Path    string
	Quote   string
	Trailer string
}

// recordedQuotes is the quotes the session's cited changes recorded for these
// files (Params.Recorded), in the pools the requirement accepts, in the order
// given and without repeats. A refusal lists them: the agent that cited a file
// with sr-file has already found its quote, and what it lacks is only that the
// quote has to ride on the COMMIT as a trailer.
func (ev *changesetEvaluation) recordedQuotes(files []string, pools []transcript.SourceType) []recordedQuote {
	recorded := ev.params.Recorded
	if recorded == nil && ev.params.RecordedFn != nil {
		recorded = ev.params.RecordedFn() // built only now that a refusal needs it
	}
	return RecordedQuotes(recorded, files, pools)
}

// RecordedQuote is a recorded citation as the trailer that carries it and its quote.
type RecordedQuote = recordedQuote

// RecordedQuotes is recordedQuotes over a session's recorded citations, for a caller that has
// no evaluation (the commit-time gate hands them back the same way a refusal after the commit does).
func RecordedQuotes(recorded map[string][]transcript.Citation, files []string, pools []transcript.SourceType) []RecordedQuote {
	var out []recordedQuote
	seen := map[string]bool{}
	for _, path := range files {
		for _, c := range recorded[path] {
			q := strings.Join(strings.Fields(c.Quote), " ")
			trailer, ok := trailerOf(c, pools)
			if !ok || q == "" || seen[trailer+"\x00"+q] {
				continue
			}
			seen[trailer+"\x00"+q] = true
			out = append(out, recordedQuote{Path: path, Quote: q, Trailer: trailer})
		}
	}
	return out
}

// trailerOf is the trailer a recorded citation is carried by: the user's when it
// resolved in the user pool and the requirement accepts that pool, else the
// tool's. ok is false when it resolved in none of the pools.
func trailerOf(c transcript.Citation, pools []transcript.SourceType) (string, bool) {
	has := func(p transcript.SourceType) bool {
		return slices.Contains(c.SourceTypes, p) && slices.Contains(pools, p)
	}
	switch {
	case has(transcript.SourceUser):
		return changeset.TrailerCitesUser, true
	case has(transcript.SourceToolResult):
		return changeset.TrailerCitesTool, true
	}
	return "", false
}

// shellQuote single-quotes s for a shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
