package transcript

import (
	"encoding/json"
	"regexp"
	"strings"
)

// Turning a remembered quote into a resolvable citation. An agent grounding a
// written claim in the user's own words has the TEXT it remembers, not a line —
// this closes that gap: given a substring of what the user said, find the single
// line it sits on in the trajectory.
//
// What counts as "the user's own words" is the whole subtlety, and it is three
// things, not one:
//
//   - a plain user message — content that is a string, or a list of text blocks;
//   - the answer the user selected to an AskUserQuestion tool call, which does
//     NOT arrive as a plain message. It lands as a `user` entry carrying a
//     tool_result whose content reads `The user answered: "<question>"="<answer>".
//     ...`, and the answer is extracted from that envelope.
//   - a message the person sends WHILE a turn is already running — Claude Code
//     folds it into the running turn instead of opening a new one, and records
//     it as an `attachment` entry (`attachment.type: "queued_command"`) rather
//     than a `type:"user"` message. See queuedCommandAttachment for how a
//     genuinely human one is told apart from the same attachment shape the
//     harness uses for its own background-task notices.
//
// A prompted answer, and a message sent mid-turn, are the person's own words the
// same as a spontaneous message, so a quote landing on any of the three resolves.
// Nothing else does — not the agent's own prior output, and not the text of an
// ordinary tool_result that merely happens to contain the substring.
//
// # Two searchable pools, selected by SourceType
//
// That last exclusion is the DEFAULT, not the only mode. There is a second,
// distinct thing a caller may legitimately want to ground: the RESULT a tool
// produced — a test that came back green, a build whose output IS the evidence.
// That is not the user's words, and grounding a task's ASK in it would be the
// exact false citation this command exists to refuse; but grounding a task's
// DELIVERY claim in it is precisely right, and nothing else in the record proves
// a command ran and what it returned. The two live in two different pools of the
// SAME user entry, which a harness writes as: the person's typed text (and the
// AskUserQuestion answer) in `message.content` text/string, and a tool's result
// in a `message.content` block of `type: "tool_result"`.
//
// SourceType names which pool(s) a quote is resolved against, in ONE walk:
//
//   - SourceUser        the user's own words — the default, and the only thing
//                       task-body-is-human-authored ever cites. messageText plus
//                       the answer envelope, with the harness-injected filter.
//   - SourceToolResult  the raw body of a tool_result block — the tool's output,
//                       which SourceUser deliberately excludes. This is what a
//                       delivery OBSERVATION cites: proof the work happened.
//
// A caller passing both accepts a match in either. The mirror is exact: the same
// tool_result block that SourceUser reads ONLY for an answer envelope (the user's
// selected words), SourceToolResult reads as the tool's output — so a quote of a
// command's result grounds under SourceToolResult and is refused under SourceUser,
// and a quote of the user's ask grounds under SourceUser and is refused under
// SourceToolResult.

// SourceType is one pool of text within a user entry that a quote may be resolved
// against. It is a string so an unknown value travels under its own name rather
// than being silently coerced, the same reasoning as EntryType.
type SourceType string

const (
	// SourceUser is the user's own words: the typed message text, a message sent
	// mid-turn (a human-typed queued_command attachment), and the answer
	// selected to an AskUserQuestion. Harness-injected user-role messages
	// (<system-reminder>, <task-notification>, a slash-command envelope) are
	// excluded, and an ordinary tool_result's body is not searched — grounding a
	// claim on either would be a false citation of the person.
	SourceUser SourceType = "user"

	// SourceToolResult is the raw text a tool_result block carries — the output an
	// action produced. This is the pool SourceUser refuses: a delivery observation
	// grounds a "the work happened" claim here (a green test, a command's result),
	// where the user's own words could never prove a command ran.
	SourceToolResult SourceType = "tool_result"
)

// ParseSourceType maps a source-type name to its SourceType, reporting whether it
// is one this package knows. The `cite` command parses its --source-types flag
// through here so an unknown name is refused with the value the caller wrote,
// rather than silently searching nothing.
func ParseSourceType(name string) (SourceType, bool) {
	switch SourceType(name) {
	case SourceUser:
		return SourceUser, true
	case SourceToolResult:
		return SourceToolResult, true
	default:
		return "", false
	}
}

// wants reports whether sources names s. An empty set is treated as SourceUser
// only, so a caller (or a back-compat path) that passes nothing gets today's
// behaviour rather than a search that matches nothing.
func wants(sources []SourceType, s SourceType) bool {
	if len(sources) == 0 {
		return s == SourceUser
	}
	for _, want := range sources {
		if want == s {
			return true
		}
	}
	return false
}

// CitationMatch is one resolved citation: the user's own words the quote landed
// on, and where they sit. Rendered on stdout as `<path>:<line>`, carried as a
// value so the path and line are named rather than left for a reader to split.
//
// It is `<path>:<line>` and nothing more, deliberately. cite's whole job is to
// turn a remembered quote into a resolvable location; what SITS at that location
// — the whole AskUserQuestion envelope a judge might want to read, question and
// answer both — is a separate concern with a separate home. A change grounded in
// an answer wants the question beside it before a judge can weigh it, but that
// fetch belongs in the judge-prepare piece, which is handed a citation's path and
// line and reads the envelope back with EnvelopeAt — not in the citation itself.
// Keeping the match to its two fields is what keeps cite's stdout a stable
// `<path>:<line>` contract and the envelope-fetch a reusable transcript helper
// any consumer can call, rather than a payload cite must always assemble.
type CitationMatch struct {
	// Path is the absolute path of the trajectory file the match sits in.
	Path string

	// Line is the 1-based physical line of the matched entry in that file.
	Line int
}

// Cite finds where quote sits in the user's own words within the trajectory at
// path — the SourceUser pool alone. It is CiteWithSources pinned to today's
// default, kept so every existing caller (and every caller that only ever wants
// the person's words) reads the same one-argument shape it always did.
func Cite(path, quote string) ([]CitationMatch, error) {
	return CiteWithSources(path, quote, []SourceType{SourceUser})
}

// CiteWithSources finds where quote sits within the trajectory at path, searching
// the pools named in sources and returning one match per ENTRY that contains the
// substring in any requested pool.
//
// The search is over USER entries only — a tool_result is written on a user entry,
// and the user's own words are on a user entry, so no other entry type carries
// either pool. An assistant turn is never searched: grounding a claim in the
// agent's own prior output is precisely what this must not let happen, whichever
// pools are requested. Within each user entry, sources selects what is read:
//
//   - SourceUser        the message text and any AskUserQuestion answer envelope,
//     with harness-injected user-role messages excluded. An
//     ordinary tool_result's body contributes nothing here.
//   - SourceToolResult  the raw body of each tool_result block that is a GENUINE
//     tool output — the tool's own result. An answer envelope
//     is a tool_result block too, but its body is the user's
//     selected words, so it belongs to SourceUser and is
//     EXCLUDED here (genuineToolResultText drops it): the two
//     pools are disjoint, and under this pool the caller is
//     asking about tool output, not the person.
//
// A match is per ENTRY, not per occurrence or per pool: an entry whose text
// contains the substring — twice, or in both pools — is one candidate, because
// the citation resolves to the line, and the line is the same every time. The
// caller decides what one, many, or no matches mean; this only finds them, in
// file order, so the rendered candidates read top-to-bottom.
//
// An empty quote is treated as matching nothing rather than everything: a citation
// of "" resolves nowhere useful, and returning every line for it would turn a
// mistake into an ambiguous flood. An empty sources is treated as SourceUser only,
// so a nil or unset selection is today's behaviour, not a search that matches
// nothing. The path having no readable record is the caller's error to surface;
// here a read failure propagates.
func CiteWithSources(path, quote string, sources []SourceType) ([]CitationMatch, error) {
	if strings.TrimSpace(quote) == "" {
		return nil, nil
	}
	entries, err := ReadLines(path)
	if err != nil {
		return nil, err
	}
	others := otherToolUses(entries)
	var matches []CitationMatch
	for _, e := range entries {
		switch e.Type {
		case EntryUser:
			if entryContains(e.Entry, quote, sources, others) {
				matches = append(matches, CitationMatch{Path: path, Line: e.Line})
			}
		case EntryAttachment:
			// A queued-command attachment is the ONLY attachment kind that is ever
			// the user's own words (see queuedCommandText); every other attachment
			// — environment, model identity, a completed task's notification — is
			// the harness's own bookkeeping, the same reasoning harnessInjected
			// applies to a `user`-typed record. It only ever belongs to SourceUser:
			// there is no tool_result concept on an attachment record for
			// SourceToolResult to read.
			if wants(sources, SourceUser) && queuedCommandContains(e.Entry, quote) {
				matches = append(matches, CitationMatch{Path: path, Line: e.Line})
			}
		}
	}
	return matches, nil
}

// containsWords reports whether quote appears in text with every run of
// whitespace treated as one space. A message wraps where the person's editor or
// terminal wrapped it, and a pasted block reflows; a quote remembered from it
// breaks lines in different places or not at all. The words and their order
// must still match exactly.
func containsWords(text, quote string) bool {
	return strings.Contains(strings.Join(strings.Fields(text), " "), strings.Join(strings.Fields(quote), " "))
}

// entryContains reports whether quote appears in any of the requested pools on a
// user entry. The pools are consulted in order and the walk short-circuits on the
// first hit — a match is per entry, so which pool found it does not change the
// resolved line.
func entryContains(e Entry, quote string, sources []SourceType, others map[string]bool) bool {
	if own, ok := ownWords(e, others); ok && wants(sources, SourceUser) && userWordsContain(own, quote) {
		return true
	}
	if wants(sources, SourceToolResult) && toolResultContain(e, quote) {
		return true
	}
	return false
}

// askUserQuestion is the harness tool whose result carries the user's answer.
const askUserQuestion = "AskUserQuestion"

// otherToolUses returns the ids of every tool_use in the trajectory that names
// a tool other than AskUserQuestion. A tool_result answering one of them is that
// tool's output however it reads — a Bash call can print `The user answered:
// "q"="a"` as easily as the harness writes it — so it is never the user's words.
// A result whose tool_use is not in the file at all (a fixture, a record split
// across a restart) is left to the envelope test alone.
func otherToolUses(entries []LinedEntry) map[string]bool {
	others := map[string]bool{}
	for _, e := range entries {
		if e.Type != EntryAssistant || len(e.Message) == 0 {
			continue
		}
		var msg assistantContent
		if json.Unmarshal(e.Message, &msg) != nil {
			continue
		}
		var blocks []assistantContentBlock
		if json.Unmarshal(msg.Content, &blocks) != nil {
			continue
		}
		for _, b := range blocks {
			if b.Type == "tool_use" && b.ID != "" && b.Name != askUserQuestion {
				others[b.ID] = true
			}
		}
	}
	return others
}

// ownWords is e as far as it can carry the person's own words, and false when
// it cannot carry them at all: an isMeta entry is the harness writing (a Stop
// hook's feedback, which may quote the agent back; a skill's body), and an
// isSidechain one is a sub-agent's, whose "user" is the parent agent's
// dispatch. Otherwise it is e minus the tool_result blocks answering a tool
// other than AskUserQuestion (otherToolUses).
func ownWords(e Entry, others map[string]bool) (Entry, bool) {
	if e.IsMeta || e.IsSidechain {
		return Entry{}, false
	}
	if len(others) == 0 || len(e.Message) == 0 {
		return e, true
	}
	var msg map[string]json.RawMessage
	var blocks []json.RawMessage
	if json.Unmarshal(e.Message, &msg) != nil || json.Unmarshal(msg["content"], &blocks) != nil {
		return e, true
	}
	kept := blocks[:0:0]
	for _, raw := range blocks {
		var b struct {
			Type      string `json:"type"`
			ToolUseID string `json:"tool_use_id"`
		}
		if json.Unmarshal(raw, &b) == nil && b.Type == "tool_result" && others[b.ToolUseID] {
			continue
		}
		kept = append(kept, raw)
	}
	if len(kept) == len(blocks) {
		return e, true
	}
	content, err := json.Marshal(kept)
	if err != nil {
		return Entry{}, false
	}
	msg["content"] = content
	if e.Message, err = json.Marshal(msg); err != nil {
		return Entry{}, false
	}
	return e, true
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
		if containsWords(text, quote) {
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
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Content   json.RawMessage `json:"content"`
	ToolUseID string          `json:"tool_use_id"`
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
		if harnessInjected(s) {
			return nil
		}
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
			if !harnessInjected(bs) {
				out = append(out, bs)
			}
			continue
		}
		var blk userContentBlock
		if json.Unmarshal(b, &blk) != nil {
			continue
		}
		if blk.Type == "text" && blk.Text != "" && !harnessInjected(blk.Text) {
			out = append(out, blk.Text)
		}
	}
	return out
}

// harnessInjected reports whether a user-role message's text was written by the
// HARNESS, not typed by the person — so it must never be cited as the user's own
// words. These arrive on `user` entries with plain string content, the same shape
// a real typed message has, which is why cite has to tell them apart by content.
//
// Claude Code injects several such messages: a background <task-notification>
// (and its "[SYSTEM NOTIFICATION - NOT USER INPUT]" preamble), a <system-reminder>,
// and the slash-command envelope a <command-name>/<command-message>/<command-args>
// or <local-command...> carries. A written claim grounded on any of these is
// grounded on something the user never said — the exact false citation cite
// exists not to mint (it already excludes tool_result output for the same reason).
// The AskUserQuestion answer envelope is NOT here: that is genuinely the user's
// selected words and is searched via answerText.
func harnessInjected(text string) bool {
	t := strings.TrimSpace(text)
	for _, marker := range []string{
		"<task-notification>",
		"[SYSTEM NOTIFICATION - NOT USER INPUT]",
		"<system-reminder>",
		"<local-command",
		"<command-name>",
		"<command-message>",
		"<command-args>",
	} {
		if strings.HasPrefix(t, marker) {
			return true
		}
	}
	return false
}

// queuedCommandAttachment is the fields of a `queued_command` attachment that
// decide whether its prompt is genuinely the person's own words.
//
// Claude Code writes a message the person sends WHILE a turn is already running
// this way — not as a `type:"user"` record, but as an `attachment` one — because
// it is folded into the running turn rather than opened as a new one. Left out
// of the search, a rule grounding a claim in the user's words silently refuses
// everything the person typed mid-turn, which is the bug this type exists to
// close.
//
// Not every `queued_command` is the person typing, though: Claude Code queues a
// background task's completion notice — a <task-notification> — through the
// exact same attachment shape, and that is the harness speaking, not the person.
// Across the real transcripts this was checked against (~1,900 queued_command
// attachments spanning several projects), the two are told apart cleanly by
// CommandMode alone:
//
//   - CommandMode == "prompt" is every genuinely human-typed message observed —
//     ordinary prose, a message that happens to start with "/" (which is just
//     text the person typed, not a slash-command invocation — those arrive as
//     the <command-name>/<command-message> envelope harnessInjected already
//     excludes), non-English text, image attachments. Origin.Kind, when
//     present, is always "human" on these and never present on the other mode —
//     confirming, not deciding, the classification.
//   - CommandMode == "task-notification" is every <task-notification> observed,
//     with no other CommandMode value seen for it.
//
// HumanTurn is NOT the gate: it is `true` on a healthy fraction of genuine
// "prompt" records but absent on plenty of others (older client versions did
// not write it), so requiring it would silently drop real mid-turn messages
// from otherwise-identical sessions. Origin is similarly present only
// sometimes. CommandMode is the one field written on every queued_command
// record in the corpus and never ambiguous, so it is the sole gate; the other
// two are read only to confirm the classification within tests, not to narrow
// it further.
type queuedCommandAttachment struct {
	Type        string          `json:"type"`
	Prompt      json.RawMessage `json:"prompt"`
	CommandMode string          `json:"commandMode"`
}

// isHumanQueuedCommand reports whether raw — an EntryAttachment's Attachment
// field — is a `queued_command` the person actually typed, as against one the
// harness queued on its own behalf (a background task's <task-notification>).
// See queuedCommandAttachment for how CommandMode decides this.
func isHumanQueuedCommand(raw json.RawMessage) (queuedCommandAttachment, bool) {
	if len(raw) == 0 {
		return queuedCommandAttachment{}, false
	}
	var att queuedCommandAttachment
	if json.Unmarshal(raw, &att) != nil {
		return queuedCommandAttachment{}, false
	}
	if att.Type != "queued_command" || att.CommandMode != "prompt" {
		return queuedCommandAttachment{}, false
	}
	return att, true
}

// queuedCommandText returns the plain text of a human-typed queued-command
// attachment's prompt — empty for anything else (not a queued_command, queued by
// the harness rather than the person, or a non-text prompt such as an image
// attachment, which carries a block list rather than a string and has no text to
// search).
func queuedCommandText(raw json.RawMessage) string {
	att, ok := isHumanQueuedCommand(raw)
	if !ok {
		return ""
	}
	var text string
	if json.Unmarshal(att.Prompt, &text) != nil {
		return ""
	}
	return text
}

// queuedCommandContains reports whether quote appears in a human-typed
// queued-command attachment's prompt text. The mirror, at attachment shape, of
// userWordsContain at user-entry shape: both ask "is this the person's own
// words", each against the field the two record types actually carry it in.
func queuedCommandContains(e Entry, quote string) bool {
	text := queuedCommandText(e.Attachment)
	return text != "" && containsWords(text, quote)
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

// toolResultContain reports whether quote appears in the raw body of any
// tool_result block on a user entry — the SourceToolResult pool.
//
// This is the mirror of userWordsContain: where that reads a tool_result ONLY for
// an answer envelope (extracting the user's selected words and nothing else), this
// reads the WHOLE body — the tool's actual output, which is what a delivery
// observation cites. The two never overlap in intent: a quote of a command's
// result grounds here and is refused by userWordsContain; a quote of the user's
// ask grounds there and is refused here, because a tool's output is not the user's
// words and this pool holds nothing but tool output.
func toolResultContain(e Entry, quote string) bool {
	// genuineToolResultText, not toolResultText: an AskUserQuestion answer envelope
	// is a tool_result block whose body is the user's own answer, and it belongs to
	// the SourceUser pool (userWordsContain reads it), not here. Searching it under
	// SourceToolResult too would let a quote of the user's answer ground as "the
	// tool's output" — the same substitution ToolResultAt guards against on the
	// line-based path. Excluding answer envelopes keeps the two pools disjoint and
	// SourceToolResult meaning exactly "the tool's output", as its doc says.
	for _, text := range genuineToolResultText(e.Message) {
		if containsWords(text, quote) || containsWords(withoutLineNumbers(text), quote) {
			return true
		}
	}
	return false
}

// lineNumber is the `cat -n` prefix the Read tool puts on every line it returns.
var lineNumber = regexp.MustCompile(`(?m)^[ \t]*\d+\t`)

// withoutLineNumbers drops Read's line-number prefixes, so a quote of the file's
// own text matches across the lines it spans; left in, a number sits between the
// last word of one line and the first of the next.
func withoutLineNumbers(text string) string { return lineNumber.ReplaceAllString(text, "") }

// toolResultText returns the raw text of every tool_result block on a user entry's
// message — the output a tool produced, whichever shape the block's content takes.
//
// It is deliberately the UNFILTERED body: unlike answerText, which reaches into a
// tool_result only to pull the <answer> out of an AskUserQuestion envelope, this
// keeps the block's content verbatim, because the thing a delivery observation
// grounds on IS that content — the lines a test printed, the status a build
// returned. An entry with no tool_result block contributes nothing, which is the
// honest answer for a plain typed message: there is no tool output on it to cite.
//
// The top-level `toolUseResult` field an entry may also carry (transcript.Entry:
// "where evidence of what an action actually produced lives") is NOT read here.
// That field is a structured artifact payload a different consumer reads whole
// (see envelope.go / the action-proof prepare); a cite is a substring search for
// a line a person remembers, and what they remember seeing is the tool_result
// block's rendered content, which is where this looks.
func toolResultText(raw json.RawMessage) []string {
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
		out = append(out, toolResultStrings(b.Content)...)
	}
	return out
}

// ToolResultAt reports whether the entry at physical line of the transcript at path
// is a tool_result the session produced, and returns that result's text.
//
// This is the LINE-oriented counterpart to the SourceToolResult pool: a delivery
// OBSERVATION is a `<abs-jsonl>:<ranges>` citation into the transcript, so what the
// deterministic check asks is not "does a quote resolve" but "is the entry at THIS
// LINE a tool_result" — the exact gap the old guardrail named (its
// cite_check_user_message could refuse a user message, but had no "this line is a
// real tool-call result" check). It is built on the SAME toolResultText pool cite
// searches, so "is a tool_result" means precisely what the tool_result source type
// means — one authority, not a second hand-rolled entry-type test.
//
// isToolResult is true iff the entry at that line is a user entry carrying at least
// one tool_result block (toolResultText non-empty); text is that block content
// joined by newlines, the bytes a reviewer sees as the result. A line that is a
// user message, an assistant turn, an answer envelope with no ordinary result, or
// no entry at all is NOT a tool_result — returning false, so the caller refuses an
// observation that points at the agent's prose rather than a produced result.
//
// A line past the end, or one that is not an entry (a preamble line, an unparseable
// one), yields ("", false, nil): "not a tool_result" is the honest answer, not an
// error, so the caller can name the citation rather than crash on a bad range.
func ToolResultAt(path string, line int) (text string, isToolResult bool, err error) {
	entries, err := ReadLines(path)
	if err != nil {
		return "", false, err
	}
	for _, e := range entries {
		if e.Line != line {
			continue
		}
		if e.Type != EntryUser {
			return "", false, nil
		}
		// A GENUINE produced result, not an AskUserQuestion answer envelope. An
		// answer re-enters the transcript as a tool_result block too (its body is
		// `The user answered: ...`), so toolResultText alone would classify the
		// user's own answer as a delivery observation — the exact substitution this
		// check exists to refuse (the user's words standing in for proof of work).
		// genuineToolResultText drops the answer-envelope blocks, so a line that
		// carries ONLY an answer envelope is not a tool_result, matching this
		// function's contract above.
		results := genuineToolResultText(e.Message)
		if len(results) == 0 {
			return "", false, nil
		}
		return strings.Join(results, "\n"), true, nil
	}
	return "", false, nil
}

// genuineToolResultText is toolResultText minus the AskUserQuestion answer
// envelopes: the bodies of a user entry's tool_result blocks EXCEPT those that are
// an answer envelope (a body extractAnswers can read a `The user answered:` pair
// out of). A block that is an answer envelope carries the user's words, not a
// tool's output, so it is not delivery evidence — the same line answerText draws
// between the user's answer and a command's result, applied here to the whole
// block. A line carrying both a real result and an answer envelope keeps the real
// result only; a line carrying only an answer envelope yields nothing.
func genuineToolResultText(raw json.RawMessage) []string {
	var out []string
	for _, r := range genuineToolResults(raw) {
		out = append(out, r.body)
	}
	return out
}

// genuineToolResultIDs is the tool_use_id of each block genuineToolResultText
// reads a body from, once per block — which call produced the output.
func genuineToolResultIDs(raw json.RawMessage) []string {
	var out []string
	for _, r := range genuineToolResults(raw) {
		if len(out) == 0 || out[len(out)-1] != r.id {
			out = append(out, r.id)
		}
	}
	return out
}

// genuineToolResult is one body of a tool_result block and the call it answers.
type genuineToolResult struct{ id, body string }

func genuineToolResults(raw json.RawMessage) []genuineToolResult {
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
	var out []genuineToolResult
	for _, b := range blocks {
		if b.Type != "tool_result" || len(b.Content) == 0 {
			continue
		}
		for _, body := range toolResultStrings(b.Content) {
			// An answer envelope contributes nothing: its body is the user's
			// selected answer, not a produced result.
			if len(extractAnswers(body)) > 0 {
				continue
			}
			// Nor does a hook's refusal: the tool never ran, and the body is the
			// harness's message — which may quote the agent's own words back (an
			// unresolved --cite: quote), and must not then ground them.
			if isHookRefusal(body) {
				continue
			}
			out = append(out, genuineToolResult{id: b.ToolUseID, body: body})
		}
	}
	return out
}

// isHookRefusal reports whether a tool_result body is a hook blocking the call
// ("PreToolUse:Bash hook error: …") rather than anything the tool produced.
func isHookRefusal(body string) bool {
	return strings.HasPrefix(body, "PreToolUse:") && strings.Contains(body, " hook error: ")
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
