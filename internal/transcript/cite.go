package transcript

import (
	"encoding/json"
	"strings"
)

// Turning a remembered quote into a resolvable citation. An agent grounding a
// written claim in the user's own words has the TEXT it remembers, not a line —
// this closes that gap: given a substring of what the user said, find the single
// line it sits on in the trajectory.
//
// What counts as "the user's own words" is the whole subtlety, and it is two
// things, not one:
//
//   - a plain user message — content that is a string, or a list of text blocks;
//   - the answer the user selected to an AskUserQuestion tool call, which does
//     NOT arrive as a plain message. It lands as a `user` entry carrying a
//     tool_result whose content reads `The user answered: "<question>"="<answer>".
//     ...`, and the answer is extracted from that envelope.
//
// A prompted answer is the person's own words the same as a spontaneous message,
// so a quote landing on either resolves. Nothing else does — not the agent's own
// prior output, and not the text of an ordinary tool_result that merely happens
// to contain the substring.

// CitationMatch is one resolved citation: the user's own words the quote landed
// on, and where they sit. Rendered on stdout as `<path>:<line>`, carried as a
// value so the path and line are named rather than left for a reader to split.
type CitationMatch struct {
	// Path is the absolute path of the trajectory file the match sits in.
	Path string

	// Line is the 1-based physical line of the matched entry in that file.
	Line int
}

// Cite finds where quote sits in the user's own words within the trajectory at
// path, returning one match per entry whose user-words contain the substring.
//
// The search is over USER entries only, and within each over the text that is
// genuinely the user's: the message content, and any AskUserQuestion answer
// carried in a tool_result envelope on that entry. An assistant turn is never
// searched — grounding a claim in the agent's own prior output is precisely what
// this must not let happen — and an ordinary tool_result that is not an answer
// envelope contributes nothing even if the substring appears inside it.
//
// A match is per ENTRY, not per occurrence: an entry whose user-words contain
// the substring twice is one candidate, because the citation resolves to the
// line, and the line is the same both times. The caller decides what one, many,
// or no matches mean — this only finds them, in file order, so the rendered
// candidates read top-to-bottom.
//
// An empty quote is treated as matching nothing rather than everything: a
// citation of "" resolves nowhere useful, and returning every user line for it
// would turn a mistake into an ambiguous flood. The path having no readable
// record is the caller's error to surface; here a read failure propagates.
func Cite(path, quote string) ([]CitationMatch, error) {
	if quote == "" {
		return nil, nil
	}
	entries, err := ReadLines(path)
	if err != nil {
		return nil, err
	}
	var matches []CitationMatch
	for _, e := range entries {
		if e.Type != EntryUser {
			continue
		}
		if userWordsContain(e.Entry, quote) {
			matches = append(matches, CitationMatch{Path: path, Line: e.Line})
		}
	}
	return matches, nil
}

// userWordsContain reports whether quote appears in the user's own words on a
// user entry — its message text, or an AskUserQuestion answer carried in a
// tool_result on it.
//
// Both sources are searched because both are the user speaking. The message is
// what they typed; the answer envelope is what they chose. A rule grounding a
// change in "you said this" and one grounding it in "you picked this option" are
// equally legitimate, so a quote landing on either is a match.
func userWordsContain(e Entry, quote string) bool {
	for _, text := range userWords(e) {
		if strings.Contains(text, quote) {
			return true
		}
	}
	return false
}

// userWords returns the searchable strings that are genuinely the user's on a
// user entry: the plain message text, plus any answer extracted from an
// AskUserQuestion tool_result envelope.
//
// Split out from the search so the extraction is testable on its own — what
// counts as the user's words is the delicate part, and a test naming the
// envelope shapes it must reach is worth more than one that only asks whether a
// substring was found.
func userWords(e Entry) []string {
	var words []string
	words = append(words, messageText(e.Message)...)
	words = append(words, answerText(e.Message)...)
	return words
}

// userContentBlock is one block of a user message's content list, in the fields
// that carry text a person may have written. A block is either plain text (a
// `text` block, or a bare string the harness sometimes writes) or a tool_result
// whose content holds the answer envelope.
type userContentBlock struct {
	Type    string          `json:"type"`
	Text    string          `json:"text"`
	Content json.RawMessage `json:"content"`
}

// userMessage is the envelope a user entry's message sits in. content is either
// a bare string (what the harness writes for a typed message) or a list of
// blocks (what it writes when the turn carries tool results or structured text),
// so it is decoded as raw and dispatched on its JSON shape.
type userMessage struct {
	Content json.RawMessage `json:"content"`
}

// messageText returns the plain text a user typed in an entry's message —
// the string content, or the text of each text block in a content list.
//
// It does NOT reach into tool_result blocks: those are handled by answerText,
// which knows the answer envelope and extracts only the answer from it. Keeping
// the two apart is what stops an ordinary tool_result's body from being searched
// as if it were the user's words — only the answer inside an answer envelope is.
func messageText(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var msg userMessage
	if json.Unmarshal(raw, &msg) != nil || len(msg.Content) == 0 {
		return nil
	}
	// content as a bare string: the ordinary typed message.
	var s string
	if json.Unmarshal(msg.Content, &s) == nil {
		return []string{s}
	}
	// content as a list of blocks: keep the text of the text blocks, and the
	// bare strings some harnesses write as block elements.
	var blocks []json.RawMessage
	if json.Unmarshal(msg.Content, &blocks) != nil {
		return nil
	}
	var out []string
	for _, b := range blocks {
		var bs string
		if json.Unmarshal(b, &bs) == nil {
			out = append(out, bs)
			continue
		}
		var blk userContentBlock
		if json.Unmarshal(b, &blk) != nil {
			continue
		}
		if blk.Type == "text" && blk.Text != "" {
			out = append(out, blk.Text)
		}
	}
	return out
}

// answerPrefix is what a harness writes at the head of an AskUserQuestion answer
// envelope. The text after it opens with the question and answer as
// `"<question>"="<answer>"`; see answerText for what is pulled out.
const answerPrefix = `The user answered:`

// answerText returns the answers the user selected to any AskUserQuestion tool
// call recorded on a user entry — extracted from the tool_result envelopes on it.
//
// The envelope is a tool_result whose content reads
// `The user answered: "<q1>"="<a1>", "<q2>"="<a2>". Read the answers carefully ...`.
// One AskUserQuestion call routinely asks SEVERAL questions, and the harness
// writes every pair into ONE string, so an envelope carries a LIST of answers,
// not a single one. What is the USER's word is each <answer>, so those are what
// is extracted and returned; the questions are the agent's, and the trailing
// instruction is the harness's. Returning the whole envelope, or any question
// text, would let a quote match on words the agent wrote — the exact false
// citation this command exists not to mint.
//
// A tool_result whose content is not an answer envelope contributes nothing: its
// body is a tool's output, not the user's words, and must not be searched as if
// it were. That is the line between this and an ordinary user entry carrying a
// command's result.
func answerText(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var msg userMessage
	if json.Unmarshal(raw, &msg) != nil || len(msg.Content) == 0 {
		return nil
	}
	var blocks []userContentBlock
	if json.Unmarshal(msg.Content, &blocks) != nil {
		return nil
	}
	var out []string
	for _, b := range blocks {
		if b.Type != "tool_result" || len(b.Content) == 0 {
			continue
		}
		for _, envelope := range toolResultStrings(b.Content) {
			out = append(out, extractAnswers(envelope)...)
		}
	}
	return out
}

// toolResultStrings returns the text a tool_result's content carries, whichever
// shape it takes. The content is usually a bare string (the answer envelope in
// the measured corpus is always one), but a tool_result may also carry a list of
// {type:"text", text:...} blocks, so both are read — a harness that moved the
// envelope into a text block would otherwise silently stop resolving.
func toolResultStrings(raw json.RawMessage) []string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []string{s}
	}
	var blocks []userContentBlock
	if json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	var out []string
	for _, b := range blocks {
		if b.Text != "" {
			out = append(out, b.Text)
		}
	}
	return out
}

// pairJoin is the `"="` that joins a question to its answer inside an envelope:
// the question's closing quote, an equals, the answer's opening quote. It is the
// one anchor that is structural rather than content — a question the agent wrote
// is always FOLLOWED by it, which is what lets the walk find where each answer
// begins even when a question contains its own quotes (real questions do: they
// quote identifiers and prior phrases).
const pairJoin = `"="`

// answerTrailer is the sentence the harness appends after the LAST answer, and
// the reliable end-of-pairs anchor: every one of the 308 real envelopes measured
// carries it verbatim, and none carries a `"` inside it. Cutting the pair list
// here removes the boilerplate cleanly, so the last answer's own closing quote is
// the last `"` of what remains rather than something hidden in the trailer.
const answerTrailer = `. Read the answers`

// extractAnswers pulls every <answer> out of an AskUserQuestion envelope, which
// carries one Q/A pair per question asked:
//
//	The user answered: "<q1>"="<a1>", "<q2>"="<a2>". Read the answers carefully ...
//
// It returns ONLY the answers — never a question, never the trailing boilerplate.
// The earlier version found the first `"="` and cut at the last `"` on the line,
// which on a two-question envelope returned the single blob `a1", "q2"="a2` and so
// made the agent's SECOND question text citable. That is the defect this replaces:
// the agent's own words must never resolve, and a multi-question call is the
// common case (44% of the measured envelopes ask more than one), not an edge.
//
// The parse works off two anchors, both structural rather than content:
//
//   - the trailer `. Read the answers ...` ends the pair list; everything before
//     it is pairs, and a truncated envelope with no trailer is pairs to its end;
//   - the `"="` join opens each answer. A pair is read as: the question up to the
//     next `"="` (discarded — how it is quoted does not matter), then the answer
//     that follows, closed at the `"` that begins a REAL next pair (`", "` then a
//     question terminated by its own `"="`) or, for the last answer, at the final
//     `"` before the trailer.
//
// The `"="` lookahead on a candidate boundary is the crux: `", "` alone cannot be
// trusted as a pair boundary because an answer may contain that very sequence, so
// a boundary counts only when the text after it is itself a `"="`-terminated
// question. That keeps an answer with commas, quotes or `="` in it whole, and —
// the property that matters most — never lets the text between two pairs (a
// question) be kept as if it were an answer.
//
// Not an envelope — no prefix, or no `"="` join at all — returns nothing, and the
// caller treats the tool_result as output rather than the user's words.
func extractAnswers(envelope string) []string {
	i := strings.Index(envelope, answerPrefix)
	if i < 0 {
		return nil
	}
	pairs := envelope[i+len(answerPrefix):]
	// Drop the trailing boilerplate so the last answer's close is the last quote
	// of what remains. Absent (a truncated line) leaves the whole tail as pairs.
	if t := strings.Index(pairs, answerTrailer); t >= 0 {
		pairs = pairs[:t]
	}

	var answers []string
	for {
		// Each pair opens with a question ending at the next `"="`; that join is
		// what locates the answer, and the question itself is dropped.
		j := strings.Index(pairs, pairJoin)
		if j < 0 {
			return answers
		}
		answerStart := j + len(pairJoin)
		rel, isLast := answerEnd(pairs[answerStart:])
		answers = append(answers, pairs[answerStart:answerStart+rel])
		if isLast {
			return answers
		}
		// Advance to the next question: the `"` that opens it sits at the end of
		// the `", "` boundary, len(`", "`)-1 == 3 bytes past the answer's close.
		pairs = pairs[answerStart+rel+len(`", `):]
	}
}

// answerEnd finds where the answer occupying the front of s ends, and reports
// whether it is the LAST answer in the pair list. s begins at the answer's first
// byte, just past a `"="` join.
//
// The answer closes at the first `"` that begins a genuine pair boundary — `", "`
// followed by a question that is itself `"="`-terminated. A `"` inside the answer
// is stepped over, because what follows it is not such a question. When no
// boundary is found the answer is the last one: it ends at its own closing quote,
// which is the LAST `"` in s (the trailer having already been removed) — or at
// the end of s when the envelope was truncated before that quote was written.
func answerEnd(s string) (end int, isLast bool) {
	from := 0
	lastQuote := -1
	for {
		q := strings.IndexByte(s[from:], '"')
		if q < 0 {
			// No further quote. This is the last answer: it ends at its own
			// closing quote if one was seen, otherwise the line was truncated
			// mid-answer and the whole remainder is the answer.
			if lastQuote >= 0 {
				return lastQuote, true
			}
			return len(s), true
		}
		closeAt := from + q
		if beginsPair(s[closeAt:]) {
			return closeAt, false
		}
		lastQuote = closeAt
		from = closeAt + 1
	}
}

// beginsPair reports whether s — which starts at a candidate answer-closing quote
// — is followed by a genuine next pair: the boundary `", "` and then a question
// terminated by its own `"="`.
//
// The `"="` requirement is what separates a real boundary from a `", "` that
// merely sits inside an answer: only a following question closed by `"="` makes
// this quote an answer's end.
func beginsPair(s string) bool {
	const boundary = `", "`
	if !strings.HasPrefix(s, boundary) {
		return false
	}
	// From the opening quote of the next question (the last byte of the boundary),
	// there must be a `"="` closing it.
	return strings.Contains(s[len(boundary)-1:], pairJoin)
}
