package transcript

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Fetching the whole tool_result envelope that sits at a citation's location.
//
// `cite` resolves a remembered quote to a `<path>:<line>` and stops there: the
// location is all an agent writing a citation needs, and all cite's stdout
// contract carries. But a later consumer — the judge-prepare piece that assembles
// what a change's judge sees — wants MORE than the location for an answer-grounded
// change. A change grounded in an AskUserQuestion answer cites the line the answer
// envelope sits on, and the answer ALONE ("the second option") is not something a
// judge can weigh: it cannot tell what was asked, nor which of several sibling
// answers the user meant, without the question beside it.
//
// So the whole envelope has to be fetchable from a citation's path and line, and
// this file is where that fetch lives — deliberately APART from cite. cite mints
// the location; EnvelopeAt reads back what sits there. Keeping them separate is
// the layering the reviewer asked for (PR review on cite.go): cite's CitationMatch
// stays `{Path, Line}` and its stdout stays `<path>:<line>`, while the
// envelope-fetch is a reusable transcript helper any consumer can call with a
// citation it already holds — rather than a payload cite must always assemble onto
// every match whether or not anyone reads it.

// ErrNoEntryAtLine: the given line names no entry in the transcript — it is past
// the end of the file, or it falls on a line that is not an entry (a preamble or
// bookkeeping line Read skips, or one that will not parse). A citation's line
// always names a real entry, so this is a caller holding a line that did not come
// from Cite, or a file that changed under it.
var ErrNoEntryAtLine = errors.New("no entry at the given line")

// EnvelopeAt returns the whole AskUserQuestion answer envelope(s) sitting on the
// entry at the given 1-based physical line of the transcript at path — the full
// `The user answered: "<question>"="<answer>". ...` string, question text and
// trailing instruction included, not the extracted answer alone.
//
// This is what the judge-prepare piece calls with a citation's path and line. cite
// hands back `<path>:<line>` and nothing more; when the change being judged was
// grounded in an AskUserQuestion answer, the prepare/judge-input assembly passes
// that same path and line here to recover the whole envelope, so the judge sees
// WHAT WAS ASKED beside what was answered. (The consuming wiring is NOT in this
// package and is not built here: it lives in the judge-prepare slice — unit 17's
// Wave-2 slice, in internal/dispatch — which this helper exists to serve. This
// package only provides the reusable fetch.)
//
// The envelope is returned UNCUT, which is the whole point of fetching it rather
// than reusing what cite searched. cite's own search extracts only each <answer>,
// because a quote must never resolve to the agent's question — but a judge needs
// the question to make the answer mean anything, and the sibling answers to know
// which one "the second option" was. One AskUserQuestion call routinely asks
// several questions and the harness writes every pair into one envelope, and one
// user turn may carry several such tool_result blocks, so this returns a LIST — in
// the order the envelopes sit on the entry.
//
// Only genuine answer envelopes are returned. A tool_result whose content does not
// open with the answer prefix is an ordinary tool's output — a command's result
// that merely sits on the same user turn — and is skipped, the same line cite's
// own answer/output distinction draws. So a line naming a plain typed message, or
// a user turn carrying only ordinary tool results, yields (nil, nil): there is no
// envelope to show, which is the normal case for a message-grounded citation and
// not an error.
//
// A line that names no entry is ErrNoEntryAtLine, and a file that cannot be read
// propagates its read error — both distinct from the empty-but-fine result above,
// so the consumer can tell "nothing to add for this citation" from "this citation
// does not point where you said".
func EnvelopeAt(path string, line int) ([]string, error) {
	entries, err := ReadLines(path)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.Line == line {
			return answerEnvelopesOf(e.Message), nil
		}
	}
	return nil, fmt.Errorf("transcript: envelope at %s:%d: %w", path, line, ErrNoEntryAtLine)
}

// answerEnvelopesOf returns the whole AskUserQuestion answer envelope(s) carried
// on a user entry's message — every tool_result whose content is a genuine answer
// envelope, uncut.
//
// It is the whole-envelope counterpart to cite's answerText, and the two draw the
// same line in the same place. answerText pulls only each <answer> because that is
// what the user SAID and is what a quote may resolve to; this keeps the whole
// string because its consumer is a judge, which needs the question the answer
// answered. Both skip a tool_result that is not an answer envelope — the answer
// prefix absent means it is a tool's output, not the user's prompted answer — so
// neither ever treats an ordinary command's body as the user's words.
func answerEnvelopesOf(raw json.RawMessage) []string {
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
			// The same "is this an answer envelope" test extractAnswers makes: the
			// answer prefix present means it is the user's prompted answer and the
			// whole string is worth carrying; absent means it is a tool's output and
			// is not.
			if i, _ := answerPrefixAt(envelope); i >= 0 {
				out = append(out, envelope)
			}
		}
	}
	return out
}
