package record

import "strings"

// QuestionTool is the tool whose result carries the user's answer
// (harness.UserAnswerer).
func (Transcripts) QuestionTool() string { return "AskUserQuestion" }

// IsAnswerEnvelope reports whether resultText carries an answer prefix
// (harness.UserAnswerer).
func (Transcripts) IsAnswerEnvelope(resultText string) bool {
	i, _ := answerPrefixAt(resultText)
	return i >= 0
}

// ExtractAnswers pulls every <answer> out of an AskUserQuestion answer envelope
// (harness.UserAnswerer).
func (Transcripts) ExtractAnswers(envelope string) []string { return extractAnswers(envelope) }

// answerPrefixes are what a harness writes at the head of an AskUserQuestion
// answer envelope, one per wording a Claude Code version has used. The text after
// each opens with the question and answer as `"<question>"="<answer>"`; see
// answerText for what is pulled out. Newer versions write "Your questions have
// been answered:" (measured in a 2026-10 session); a wording missing here leaves
// every answer of that version uncitable.
var answerPrefixes = []string{`The user answered:`, `Your questions have been answered:`}

// answerPrefixAt is where the first answer prefix in envelope starts and how long
// it is, or -1.
func answerPrefixAt(envelope string) (int, int) {
	for _, p := range answerPrefixes {
		if i := strings.Index(envelope, p); i >= 0 {
			return i, len(p)
		}
	}
	return -1, 0
}

// pairJoin is the `"="` that joins a question to its answer inside an envelope:
// the question's closing quote, an equals, the answer's opening quote. It is the
// one anchor that is structural rather than content — a question the agent wrote
// is always FOLLOWED by it, which is what lets the walk find where each answer
// begins even when a question contains its own quotes (real questions do: they
// quote identifiers and prior phrases).
const pairJoin = `"="`

// answerTrailers are the sentences a harness appends after the LAST answer, one
// per wording (see answerPrefixes), and the reliable end-of-pairs anchor: every
// one of the 308 real envelopes first measured carries the first verbatim, and
// none carries a `"` inside it. Cutting the pair list here removes the
// boilerplate cleanly, so the last answer's own closing quote is the last `"` of
// what remains rather than something hidden in the trailer.
var answerTrailers = []string{`. Read the answers`, `. You can now continue with these answers`}

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
	i, n := answerPrefixAt(envelope)
	if i < 0 {
		return nil
	}
	pairs := envelope[i+n:]
	// Drop the trailing boilerplate so the last answer's close is the last quote
	// of what remains. Absent (a truncated line) leaves the whole tail as pairs.
	for _, trailer := range answerTrailers {
		if t := strings.Index(pairs, trailer); t >= 0 {
			pairs = pairs[:t]
			break
		}
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
