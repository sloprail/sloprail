package changeset

import (
	"fmt"
	"slices"

	"github.com/sloprail/sloprail/internal/transcript"
)

// Resolver grounds one quote in the transcripts on disk. Bound to a session and
// a project by the caller — see transcript.ResolveCitationAcrossSessions.
type Resolver func(transcript.CitationRequest) (transcript.Citation, error)

// Unresolved is a citation trailer that did not become a citation.
type Unresolved struct {
	// Commit is the SHA of the commit that carried it.
	Commit  string
	Trailer string
	Quote   string
	Err     error
}

func (u Unresolved) String() string {
	return fmt.Sprintf("%s: %q on %s: %v", u.Trailer, u.Quote, short(u.Commit), u.Err)
}

// ResolveCitations turns the range's `Sloprail-Cites-User:` and
// `Sloprail-Cites-Tool:` trailers into citations, exactly as `sr-file --cite`
// would: the quote must match exactly one real entry of its pool, model text is
// never citable, and a quote that resolves nowhere is not a citation.
//
// The unresolved are returned rather than dropped, because "the commit says it
// was asked, and the words are not in the record" is what a refusal should be
// able to say. A resolution that fails for a reason other than the quote not
// resolving (an unreadable record) is in the unresolved too, and is never
// promoted to a citation.
//
// Citations accumulate across the range's commits, in commit order, and one
// quote cited twice is one citation carried by both commits: a rule's base does
// not move until it passes, so the range grows and its grounding grows with it.
// Which files a citation grounds is AttributeFiles's to say, once the files are known.
// sr:invariant citations/pool-is-not-borrowed
// sr:invariant citations/unresolved-trailers-are-reported-not-dropped
func ResolveCitations(commits []Commit, resolve Resolver) ([]Citation, []Unresolved) {
	cites := []Citation{}
	var unresolved []Unresolved
	seen := map[string]int{} // quote → index in cites, or -1 when it did not resolve
	for _, c := range commits {
		for _, t := range []struct {
			key   string
			pools []transcript.SourceType
		}{
			{TrailerCitesUser, []transcript.SourceType{transcript.SourceUser}},
			{TrailerCitesTool, []transcript.SourceType{transcript.SourceToolResult}},
		} {
			for _, quote := range c.Trailers[t.key] {
				req := transcript.CitationRequest{Quote: quote, SourceTypes: t.pools}
				id := t.key + "\x00" + quote
				if i, done := seen[id]; done {
					// The same quote on another commit is the same citation, carried by
					// both: either commit grounds the files it changed.
					if i >= 0 {
						cites[i].Commits = append(cites[i].Commits, c.SHA)
					}
					continue
				}
				seen[id] = -1
				cite, err := resolve(req)
				if err != nil {
					unresolved = append(unresolved, Unresolved{Commit: c.SHA, Trailer: t.key, Quote: quote, Err: err})
					continue
				}
				if !slices.Contains(cite.SourceTypes, t.pools[0]) {
					// A resolver that answered from another pool has not grounded
					// THIS trailer; taking it would let a tool output stand in for
					// the user's words.
					unresolved = append(unresolved, Unresolved{Commit: c.SHA, Trailer: t.key, Quote: quote, Err: fmt.Errorf("resolved in %v, not %v", cite.SourceTypes, t.pools)})
					continue
				}
				seen[id] = len(cites)
				cites = append(cites, Citation{Citation: cite, Commits: []string{c.SHA}, Files: []string{}})
			}
		}
	}
	return cites, unresolved
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// AttributeFiles fills each citation's Files: the selected files changed by the
// commits that carry it.
func AttributeFiles(cites []Citation, files []File) {
	for i := range cites {
		cites[i].Files = []string{}
		for _, f := range files {
			if slices.ContainsFunc(f.Commits, func(sha string) bool { return slices.Contains(cites[i].Commits, sha) }) {
				cites[i].Files = append(cites[i].Files, f.Path)
			}
		}
	}
}
