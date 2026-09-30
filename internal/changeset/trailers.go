package changeset

import (
	"net/textproto"

	"github.com/sloprail/sloprail/internal/gitrepo"
)

// Trailer keys the engine reads.
const (
	TrailerCitesUser = "Sloprail-Cites-User"
	TrailerCitesTool = "Sloprail-Cites-Tool"
)

// commitsOf converts git's commits to the payload's, with trailer keys in
// canonical case. Git trailer keys are case-insensitive, and a rule writing
// `trailers["Sloprail-Refactor"]` should not miss `sloprail-refactor:`.
func commitsOf(in []gitrepo.Commit) []Commit {
	out := make([]Commit, 0, len(in))
	for _, c := range in {
		trailers := map[string][]string{}
		for _, t := range c.Trailers {
			key := textproto.CanonicalMIMEHeaderKey(t.Key)
			trailers[key] = append(trailers[key], t.Value)
		}
		out = append(out, Commit{SHA: c.SHA, Subject: c.Subject, Body: c.Body, Trailers: trailers})
	}
	return out
}

// TrailerScope is the `trailers` a rule's match sees: each trailer key mapped to
// its values across every commit of the range, in commit order.
func TrailerScope(commits []Commit) map[string][]string {
	out := map[string][]string{}
	for _, c := range commits {
		for key, values := range c.Trailers {
			out[key] = append(out[key], values...)
		}
	}
	return out
}
