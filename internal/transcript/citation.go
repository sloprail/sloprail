package transcript

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// A Citation is a resolved grounding: a quote, the pool(s) it resolved in, and
// where it sits. Unlike CitationMatch — cite's own bare `{Path, Line}`, kept to
// the two fields its stdout contract needs — a Citation is the shape carried on
// an EVENT (`event.citations`), so a consumer that never ran cite itself (a
// judge template, a `require: citation` check) sees what was cited and how, not
// only where it resolved.
type Citation struct {
	// Quote is the exact substring the caller asked to ground.
	Quote string `json:"quote"`

	// SourceTypes are the pools the quote actually resolved in — a subset of the
	// pools the request named. A request naming `user,tool_result` whose quote is
	// the user's words records `[user]`, so a requirement for tool_result alone
	// is not satisfied by it.
	SourceTypes []SourceType `json:"sourceTypes"`

	// Path is the absolute trajectory path the quote resolved in.
	Path string `json:"path"`

	// Line is the 1-based physical line of the resolved entry.
	Line int `json:"line"`

	// Message is the full text of the cited entry in the pool(s) the quote
	// resolved in: the whole user message (for an AskUserQuestion answer, the
	// question with the selected answers), or the whole tool output. A quote is a search key — often a fragment chosen to be
	// unique or to avoid awkward characters — so a judge weighs it against the
	// message it came from. Capped at maxCitedMessage bytes.
	Message string `json:"message"`
}

// maxCitedMessage caps Citation.Message: a tool output can run to megabytes, and
// a judge needs the context around a quote, not every byte of a build log.
const maxCitedMessage = 16 << 10

// CitationRequest is one quote to ground and the pools it may ground in — what
// one `--cite:<source-types> <quote>` flag, or one `sr-session trajectory cite`
// call, names before it is resolved.
type CitationRequest struct {
	Quote       string
	SourceTypes []SourceType
}

// String renders the request the way a command line spells it, for a message.
func (r CitationRequest) String() string {
	names := make([]string, len(r.SourceTypes))
	for i, s := range r.SourceTypes {
		names[i] = string(s)
	}
	return fmt.Sprintf("%s %q", strings.Join(names, ","), r.Quote)
}

// ResolveCitation grounds one request against the trajectory at path.
//
// Each named pool is searched on its own, so the Citation records which pools
// the quote actually landed in. The quote must land on exactly ONE line across
// all of them: none is "not said", several is ambiguous — the same outcomes
// cite's exit codes name, reported as errors here so a caller can surface them
// verbatim. A sub-agent's trajectory is refused outright, for cite's reason: its
// "user" messages are the parent agent's dispatch, not the end user's words.
func ResolveCitation(path string, req CitationRequest) (Citation, error) {
	if req.Quote == "" {
		return Citation{}, fmt.Errorf("an empty quote grounds nothing")
	}
	if len(req.SourceTypes) == 0 {
		return Citation{}, fmt.Errorf("citation %s names no source type", req)
	}
	if IsSubagentTranscript(path) {
		return Citation{}, fmt.Errorf("citation %s cannot resolve in a sub-agent's trajectory (%s): its user messages are the parent agent's dispatch, not the end user's own words", req, path)
	}

	line := 0
	var pools []SourceType
	for _, st := range req.SourceTypes {
		matches, err := CiteWithSources(path, req.Quote, []SourceType{st})
		if err != nil {
			return Citation{}, fmt.Errorf("citation %s: %w", req, err)
		}
		for _, m := range matches {
			if line != 0 && m.Line != line {
				return Citation{}, fmt.Errorf("citation %s is ambiguous in %s: it matches more than one entry — extend the quote until it lands on exactly one", req, path)
			}
			line = m.Line
		}
		if len(matches) > 0 {
			pools = append(pools, st)
		}
	}
	if line == 0 {
		return Citation{}, fmt.Errorf("citation %s does not resolve in %s: the quote is not there word for word in that pool (only whitespace may differ)", req, path)
	}
	message, err := entryText(path, line, pools)
	if err != nil {
		return Citation{}, fmt.Errorf("citation %s: %w", req, err)
	}
	return Citation{Quote: req.Quote, SourceTypes: pools, Path: path, Line: line, Message: message}, nil
}

// entryText is the text the entry at line holds in the given pools — the same
// text the quote was searched in — joined by blank lines and capped.
func entryText(path string, line int, pools []SourceType) (string, error) {
	entries, err := ReadLines(path)
	if err != nil {
		return "", err
	}
	var parts []string
	for _, e := range entries {
		if e.Line != line {
			continue
		}
		switch e.Type {
		case EntryUser:
			if wants(pools, SourceUser) {
				// The typed message, and — for an AskUserQuestion answer — the whole
				// envelope, so the question the user was answering comes with it.
				parts = append(parts, messageText(e.Message)...)
				parts = append(parts, answerEnvelopes(e.Message)...)
			}
			if wants(pools, SourceToolResult) {
				parts = append(parts, genuineToolResultText(e.Message)...)
			}
		case EntryAttachment:
			if t := queuedCommandText(e.Attachment); t != "" && wants(pools, SourceUser) {
				parts = append(parts, t)
			}
		}
		break
	}
	text := strings.Join(parts, "\n\n")
	if len(text) > maxCitedMessage {
		n := maxCitedMessage
		for n > 0 && !utf8.RuneStart(text[n]) {
			n--
		}
		text = text[:n] + "\n[... truncated]"
	}
	return text, nil
}

// answerEnvelopes returns the whole body of each AskUserQuestion answer envelope
// on a user entry — question and selected answers together.
func answerEnvelopes(raw json.RawMessage) []string {
	var out []string
	for _, body := range toolResultText(raw) {
		if len(extractAnswers(body)) > 0 {
			out = append(out, body)
		}
	}
	return out
}

// ResolveCitations grounds every request, in order, and fails CLOSED on the
// first that does not resolve: a caller handed fewer citations than it asked for
// would ground its claim on only some of what it cited without noticing.
func ResolveCitations(path string, reqs []CitationRequest) ([]Citation, error) {
	out := make([]Citation, 0, len(reqs))
	for _, r := range reqs {
		c, err := ResolveCitation(path, r)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}
