package transcript

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
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

	// Path is the absolute trajectory path the quote resolved in — a sub-agent's
	// own record for output a sub-agent's tool produced.
	Path string `json:"path"`

	// Line is the 1-based physical line of the resolved entry.
	Line int `json:"line"`

	// Message is the full text of the cited entry in the pool(s) the quote
	// resolved in: the whole user message (for an AskUserQuestion answer, the
	// question with the selected answers), or the whole tool output. A quote is a search key — often a fragment chosen to be
	// unique or to avoid awkward characters — so a judge weighs it against the
	// message it came from. Capped at maxCitedMessage bytes.
	Message string `json:"message"`

	// Call is, for a quote that resolved in the tool_result pool, the tool call
	// that produced the output — `Bash: <command>`, `Read: <file_path>`, or the
	// tool's name and input — one line per result on the entry. Output alone
	// does not say where it came from: `echo 'all tests passed'` prints exactly
	// what a test run does, so a judge weighs the output against the call that
	// printed it. Empty in the user pool.
	Call string `json:"call,omitempty"`
}

// maxCitedCall caps each call Citation.Call renders: a command is context for
// the output, and a heredoc can carry a whole file.
const maxCitedCall = 2 << 10

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

// ResolutionError is a citation that was read and did not resolve: its quote is
// in no entry of the pools searched, in more than one, or the user's words were
// asked of a sub-agent whose session is not found. Anything else ResolveCitation
// returns is a request that could not be read or a record that could not be.
type ResolutionError struct{ Msg string }

func (e *ResolutionError) Error() string { return e.Msg }

// ResolveCitation grounds one request in the session whose record is at path.
//
// Each pool is searched in the records it may draw on (see CiteInSession): the
// user pool in the session's ROOT record alone — never a sub-agent's, whose
// "user" message is the parent agent's dispatch — and the tool_result pool in
// the root record AND every sub-agent record beneath it, since a sub-agent's
// tool output is recorded only in its own file.
//
// Each named pool is searched on its own, so the Citation records which pools
// the quote actually landed in. The quote must land on exactly ONE entry across
// every pool and every record searched: none is "not said", several — in one
// record or in two — is ambiguous, the same outcomes cite's exit codes name,
// reported as errors here so a caller can surface them verbatim. The Citation's
// Path, Line, Message and Call point into the record the quote resolved in.
func ResolveCitation(path string, req CitationRequest) (Citation, error) {
	if req.Quote == "" {
		return Citation{}, fmt.Errorf("an empty quote grounds nothing")
	}
	if len(req.SourceTypes) == 0 {
		return Citation{}, fmt.Errorf("citation %s names no source type", req)
	}

	var hits []CitationMatch
	var pools []SourceType
	for _, st := range req.SourceTypes {
		matches, err := CiteInSession(path, req.Quote, []SourceType{st})
		if errors.Is(err, ErrNoSessionRoot) {
			return Citation{}, &ResolutionError{Msg: fmt.Sprintf("citation %s cannot resolve in a sub-agent's trajectory (%s) whose session record is not found: its user messages are the parent agent's dispatch, not the end user's own words. %s", req, path, SubagentUserAdvice)}
		}
		if err != nil {
			return Citation{}, fmt.Errorf("citation %s: %w", req, err)
		}
		for _, m := range matches {
			if !slices.Contains(hits, m) {
				hits = append(hits, m)
			}
		}
		if len(matches) > 0 {
			pools = append(pools, st)
		}
	}
	switch {
	case len(hits) == 0:
		msg := fmt.Sprintf("citation %s does not resolve in %s: the quote is not there word for word in that pool (only whitespace may differ)", req, path)
		if wants(req.SourceTypes, SourceUser) {
			if hint := UnresolvedUserHint(path, req.Quote); hint != "" {
				msg += ". " + hint
			}
		}
		return Citation{}, &ResolutionError{Msg: msg}
	case len(hits) > 1:
		where := make([]string, len(hits))
		for i, h := range hits {
			where[i] = fmt.Sprintf("%s:%d", h.Path, h.Line)
		}
		return Citation{}, &ResolutionError{Msg: fmt.Sprintf("citation %s is ambiguous: it matches more than one entry (%s) — extend the quote until it lands on exactly one", req, strings.Join(where, ", "))}
	}
	at := hits[0]
	message, err := entryText(at.Path, at.Line, pools)
	if err != nil {
		return Citation{}, fmt.Errorf("citation %s: %w", req, err)
	}
	c := Citation{Quote: req.Quote, SourceTypes: pools, Path: at.Path, Line: at.Line, Message: message}
	if wants(pools, SourceToolResult) {
		if c.Call, err = entryCalls(at.Path, at.Line); err != nil {
			return Citation{}, fmt.Errorf("citation %s: %w", req, err)
		}
	}
	return c, nil
}

// CiteInSession finds where quote sits within the SESSION whose record is at
// path — CiteWithSources over every record each named pool may draw on — and
// returns one match per entry, the session's root record first and each
// record's matches in file order.
//
//   - SourceUser        the session's ROOT record alone: the end user's own
//     conversation. path may name a sub-agent's record; the root it was
//     dispatched from is searched instead, and ErrNoSessionRoot is returned
//     when that root is not on disk. No sub-agent's record is ever searched
//     for the user's words: its "user" message is the parent agent's dispatch.
//   - SourceToolResult  the root record AND every sub-agent record beneath it
//     (DescendantSubagentPaths). A sub-agent's tool output is genuine tool
//     output of this session, and it is recorded only in the sub-agent's own
//     file. For an orphaned sub-agent record, that record and its own
//     sub-agents.
//
// An empty sources is SourceUser only, as for CiteWithSources.
func CiteInSession(path, quote string, sources []SourceType) ([]CitationMatch, error) {
	userIn, toolIn, err := citationRecords(path)
	if err != nil {
		return nil, err
	}
	var out []CitationMatch
	add := func(ms []CitationMatch) {
		for _, m := range ms {
			if !slices.Contains(out, m) {
				out = append(out, m)
			}
		}
	}
	if wants(sources, SourceUser) {
		if userIn == "" {
			return nil, ErrNoSessionRoot
		}
		ms, err := CiteWithSources(userIn, quote, []SourceType{SourceUser})
		if err != nil {
			return nil, err
		}
		add(ms)
	}
	if wants(sources, SourceToolResult) {
		for _, r := range toolIn {
			ms, err := CiteWithSources(r, quote, []SourceType{SourceToolResult})
			if err != nil {
				return nil, err
			}
			add(ms)
		}
	}
	// Record order (root first), then line: the candidates read top-to-bottom.
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := slices.Index(toolIn, out[i].Path), slices.Index(toolIn, out[j].Path)
		if ri != rj {
			return ri < rj
		}
		return out[i].Line < out[j].Line
	})
	return out, nil
}

// subagentUserRule is what a sub-agent must know about citing the user: it
// never sees the user's messages, only the parent agent's dispatch prompt.
const subagentUserRule = "your prompt is the parent agent's, not the user's. " +
	"--cite:user resolves only against the user's messages in the main conversation — " +
	"quote them exactly as the user wrote them (the parent must pass them to you verbatim), " +
	"or cite --cite:tool_result for a tool's output."

// SubagentUserAdvice is said to a sub-agent whose citation of the user's words
// does not resolve.
const SubagentUserAdvice = "You are a sub-agent: " + subagentUserRule

// UnresolvedUserHint is what to add when quote does not resolve in the user
// pool of the session whose record is at path, or "" when there is nothing
// specific to say.
//
// A sub-agent never sees the user's messages — only the prompt its parent
// wrote — so its commonest miss is quoting that prompt as the user. When quote
// is in a sub-agent's dispatch prompt, the hint says so; when path is a
// sub-agent's own record (the caller is known to be one), it says how
// --cite:user works for a sub-agent. From the root's record the caller is not
// known, so a dispatch-prompt match is named without assuming who is asking.
func UnresolvedUserHint(path, quote string) string {
	sub := IsSubagentTranscript(path)
	inPrompt := dispatchPromptContains(path, quote)
	switch {
	case inPrompt && sub:
		return "That quote is from your dispatch prompt, written by the parent agent. " + SubagentUserAdvice
	case inPrompt:
		return "That quote is from a sub-agent's dispatch prompt, written by the parent agent, not by the user. If you are that sub-agent: " + subagentUserRule
	case sub:
		return SubagentUserAdvice
	}
	return ""
}

// dispatchPromptContains reports whether quote is in the words a parent agent
// wrote to one of the session's sub-agents: a user message in a sub-agent's
// record, all of which the parent wrote (the dispatch prompt, and any message
// it sent the sub-agent later). An unreadable record answers false; this only
// shapes a message.
func dispatchPromptContains(path, quote string) bool {
	top := SessionRootOf(path)
	var records []string
	if top == "" {
		top = path
		records = append(records, path)
	}
	subs, err := DescendantSubagentPaths(top)
	if err != nil {
		return false
	}
	for _, r := range append(records, subs...) {
		entries, err := ReadLines(r)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.Type != EntryUser || e.IsMeta {
				continue
			}
			for _, text := range messageText(e.Message) {
				if containsWords(text, quote) {
					return true
				}
			}
		}
	}
	return false
}

// citationRecords is where a citation into the session whose record is at path
// may resolve: userIn, the one record the user pool is searched in (the
// session's root; "" when path is a sub-agent's record whose root is not on
// disk), and toolIn, the records the tool_result pool is searched in (the root
// and every sub-agent record beneath it — or, for an orphaned sub-agent record,
// that record and its own sub-agents).
func citationRecords(path string) (userIn string, toolIn []string, err error) {
	userIn = SessionRootOf(path)
	top := userIn
	if top == "" {
		top = path
	}
	subs, err := DescendantSubagentPaths(top)
	if err != nil {
		return "", nil, err
	}
	return userIn, append([]string{top}, subs...), nil
}

// entryCalls renders the tool call behind each genuine tool_result on the entry
// at line, found by its tool_use_id among the record's assistant entries. A
// result whose call is not in the file (a fixture, a record split across a
// restart) renders as unknown rather than being dropped, so its absence shows.
func entryCalls(path string, line int) (string, error) {
	entries, err := ReadLines(path)
	if err != nil {
		return "", err
	}
	calls := map[string]assistantContentBlock{}
	citable := citableResults(entries)
	var ids []string
	for _, e := range entries {
		switch {
		case e.Type == EntryAssistant && len(e.Message) > 0:
			var msg assistantContent
			var blocks []assistantContentBlock
			if json.Unmarshal(e.Message, &msg) == nil && json.Unmarshal(msg.Content, &blocks) == nil {
				for _, b := range blocks {
					if b.Type == "tool_use" && b.ID != "" {
						calls[b.ID] = b
					}
				}
			}
		case e.Line == line:
			ids = genuineToolResultIDs(e.Message, citable)
		}
	}
	var out []string
	for _, id := range ids {
		b, ok := calls[id]
		if !ok {
			out = append(out, "(the call that produced this output is not in the record)")
			continue
		}
		out = append(out, renderCall(b))
	}
	return strings.Join(out, "\n"), nil
}

// renderCall is one tool call as a line a judge reads: the shell command for a
// tool that ran one, the file for one that read one, otherwise the raw input.
func renderCall(b assistantContentBlock) string {
	var in struct {
		Command  string `json:"command"`
		FilePath string `json:"file_path"`
	}
	_ = json.Unmarshal(b.Input, &in)
	arg := string(b.Input)
	switch {
	case in.Command != "":
		arg = in.Command
	case in.FilePath != "":
		arg = in.FilePath
	}
	return b.Name + ": " + clipText(arg, maxCitedCall)
}

// clipText cuts s to at most max bytes on a rune boundary, marking the cut.
func clipText(s string, max int) string {
	if len(s) <= max {
		return s
	}
	n := max
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "[... truncated]"
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
			if own, ok := ownWords(e.Entry, otherToolUses(entries)); ok && wants(pools, SourceUser) {
				// The typed message, and — for an AskUserQuestion answer — the whole
				// envelope, so the question the user was answering comes with it.
				parts = append(parts, messageText(own.Message)...)
				parts = append(parts, answerEnvelopes(own.Message)...)
			}
			if wants(pools, SourceToolResult) {
				parts = append(parts, genuineToolResultText(e.Message, citableResults(entries))...)
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
