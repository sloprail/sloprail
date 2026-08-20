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
// `The user answered: "<question>"="<answer>". Read the answers carefully ...`.
// What is the USER's word is the <answer>, so that is what is extracted and
// returned; the question is the agent's, and the trailing instruction is the
// harness's. Returning the whole envelope would let a quote match on the question
// or the boilerplate, which are not the person's words — the point of the command
// is to cite what the user actually chose.
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
			if answer, ok := extractAnswer(envelope); ok {
				out = append(out, answer)
			}
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

// extractAnswer pulls the <answer> out of an AskUserQuestion envelope of the form
// `The user answered: "<question>"="<answer>". ...`, reporting whether the string
// was such an envelope at all.
//
// The answer is the text inside the quotes AFTER the `=` that follows the
// question. The parse is deliberately literal rather than a regexp: it finds the
// prefix, then the `"="` that joins question to answer, then the closing quote of
// the answer. An answer may itself contain escaped quotes, and the closing quote
// is the one before the `.` that ends the sentence — so the LAST `"` on the line
// after the join is taken as the close, which is correct for the single-answer
// envelope the harness writes and errs toward including too much rather than
// truncating the user's words.
//
// Not an envelope — no prefix, or no `"="` join — reports false, and the caller
// treats it as an ordinary tool_result that is not the user's words.
func extractAnswer(envelope string) (string, bool) {
	i := strings.Index(envelope, answerPrefix)
	if i < 0 {
		return "", false
	}
	rest := envelope[i+len(answerPrefix):]
	// The join between question and answer is `"="`. Everything after it, up to
	// the closing quote of the answer, is what the user chose.
	j := strings.Index(rest, `"="`)
	if j < 0 {
		return "", false
	}
	answerStart := j + len(`"="`)
	answer := rest[answerStart:]
	// Trim the closing quote and whatever the harness appended after it. The
	// answer text runs from answerStart to its closing quote; that quote is the
	// last one before the trailing `. Read the answers ...` sentence, so cutting
	// at the final quote keeps an answer that itself contains quotes intact.
	if k := strings.LastIndex(answer, `"`); k >= 0 {
		answer = answer[:k]
	}
	return answer, true
}
