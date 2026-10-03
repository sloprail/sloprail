package changeset

import (
	"net/textproto"
	"regexp"
	"strings"

	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/transcript"
)

// Trailer keys the engine reads.
const (
	TrailerCitesUser = "Sloprail-Cites-User"
	TrailerCitesTool = "Sloprail-Cites-Tool"
)

// TrailerFor is the trailer a citation in these pools is written as: the tool
// trailer when tool output is the only source accepted, the user's otherwise.
func TrailerFor(pools []transcript.SourceType) string {
	if len(pools) == 1 && pools[0] == transcript.SourceToolResult {
		return TrailerCitesTool
	}
	return TrailerCitesUser
}

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
		addBodyCitations(trailers, c.Body)
		out = append(out, Commit{SHA: c.SHA, Subject: c.Subject, Body: c.Body, Trailers: trailers})
	}
	return out
}

// citeLine is a citation trailer written as a line of the message body: at the
// start of the line, one of our own two keys.
var citeLine = regexp.MustCompile(`(?mi)^(` + TrailerCitesUser + `|` + TrailerCitesTool + `):[ \t]*(.*\S)[ \t]*$`)

// addBodyCitations adds the citation trailers git did not parse. git reads only the
// LAST paragraph of a message as trailers, and an agent that writes
// `-m 'Sloprail-Cites-User: …' -m 'Co-Authored-By: …'` puts its citation in a
// paragraph of its own, in front of another. The line is plainly ours (anchored,
// our key, nothing else loosened), so it is read from any paragraph. One git already
// parsed — its value is the same, or the same unfolded — is not added twice.
func addBodyCitations(trailers map[string][]string, body string) {
	for _, m := range citeLine.FindAllStringSubmatch(body, -1) {
		key, value := textproto.CanonicalMIMEHeaderKey(m[1]), strings.TrimSpace(m[2])
		if continuesAny(trailers[key], value) {
			continue
		}
		trailers[key] = append(trailers[key], value)
	}
}

// CiteLine is one Sloprail-Cites-* line of a message: the canonical trailer key and its quote.
type CiteLine struct{ Key, Quote string }

// CiteLines is every citation line of a commit message not yet made, in order, read as
// addBodyCitations reads a made one.
func CiteLines(msg string) []CiteLine {
	var out []CiteLine
	for _, m := range citeLine.FindAllStringSubmatch(msg, -1) {
		out = append(out, CiteLine{Key: textproto.CanonicalMIMEHeaderKey(m[1]), Quote: strings.TrimSpace(m[2])})
	}
	return out
}

// continuesAny reports whether one of the values is value or continues it (a folded
// trailer's first line is value, git having unfolded the rest onto it).
func continuesAny(values []string, value string) bool {
	for _, v := range values {
		if strings.HasPrefix(v, value) {
			return true
		}
	}
	return false
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
