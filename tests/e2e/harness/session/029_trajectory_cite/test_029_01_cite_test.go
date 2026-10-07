package e2e

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T029_01: a quote in the user's own words resolves to <path>:<line>, exit 0.
//
// The success case the whole command exists for. The mock is driven with the
// quote in its PROMPT — the user's own words — and cite resolves that message to
// its 1-based physical line. The prompt's line is derived from the raw file the
// mock wrote (it follows the mock's no-uuid preamble block, so it is not line 1),
// so the expected line is a checked fact rather than an assumption.
func TestT029_01_CiteUniqueMatchExitsZero(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	e.Run(proj, "s-029-01", "please refactor the auth module carefully", Turns("done",
		Bash("b1", "echo on-it > note.md"),
	))
	path := e.TranscriptPath(proj, "s-029-01")

	// The prompt's physical line, read from the file the mock wrote, so the citation
	// below is checked against the record's real layout rather than a hard-coded guess.
	promptLine := physicalLine(t, path, "please refactor the auth module carefully")
	if promptLine == 0 {
		t.Fatalf("the mock did not write the prompt to the transcript\n%s", readFile(t, path))
	}

	res := cite(e, proj, path, "auth module")
	if res.Code != 0 {
		t.Fatalf("a unique match exited %d, want 0:\n%s", res.Code, res.Output)
	}
	want := fmt.Sprintf("%s:%d", path, promptLine)
	if strings.TrimSpace(res.Output) != want {
		t.Fatalf("stdout = %q, want %q", strings.TrimSpace(res.Output), want)
	}
}

// T029_02: a quote in several user messages prints every candidate, one per line,
// exit 2.
//
// The ambiguous case. The substring was not enough to land on one line, so the
// agent is handed all the candidates and exits 2 — distinguishable from both
// success (0) and absence (1) by the code alone. The assistant turn carrying the
// same substring is NOT among the candidates: cite searches the user's words, not
// the agent's.
//
// Driven through the MOCK with TWO same-session-id runs — a genuine multi-human-turn
// transcript. A real second human turn IS a new prompt on the same session, and the
// mock now appends it on resume: the harness drives the FIRST Run with --session-id
// (seeding the prompt as the transcript's parentless root, line 1) and the SECOND Run
// with --resume (the mock appends its prompt as a continuation human record, its own
// uuid and a non-null parentUuid, after the first cycle's records). Both prompts —
// "the WIDGET needs work" and "yes the WIDGET again" — are citable candidates. Run 1's
// `Say` turn puts the agent's own prose ("I will change the WIDGET now") into the
// transcript as a cite-VISIBLE assistant record (it carries a uuid, so a transcript
// reader keeps it), which cite must NEVER cite — that is the "cite skips the agent's
// words" half this case proves. The candidate lines are derived from the file the mock
// wrote (physicalLine), not hardcoded, since the two runs interleave result records
// whose positions this test does not fix.
func TestT029_02_CiteAmbiguousExitsTwo(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	// Run 1 (fresh, --session-id): the prompt is the root human turn; the Say turn is
	// the agent's own WIDGET-carrying prose, persisted as an assistant record cite skips.
	e.Run(proj, "s-029-02", "the WIDGET needs work", Turns("done",
		Say("m1", "I will change the WIDGET now"),
	))
	// Run 2 (repeat id → --resume): the mock appends this prompt as a second human turn.
	e.Run(proj, "s-029-02", "yes the WIDGET again", Turns("done"))
	path := e.TranscriptPath(proj, "s-029-02")

	// The two HUMAN lines, derived from the file the mock wrote so the candidate
	// assertions rest on the real transcript rather than a guess. The assistant's
	// WIDGET prose sits on its own line between them and must NOT be a candidate.
	firstHuman := physicalLine(t, path, "the WIDGET needs work")
	secondHuman := physicalLine(t, path, "yes the WIDGET again")
	if firstHuman == 0 || secondHuman == 0 {
		t.Fatalf("the mock did not write both human turns (first=%d second=%d)\n%s",
			firstHuman, secondHuman, readFile(t, path))
	}
	// The agent's prose must be present as a cite-visible assistant record (a distinct
	// physical line), or this case would not actually exercise "cite skips the agent".
	assistantLine := physicalLine(t, path, "I will change the WIDGET now")
	if assistantLine == 0 {
		t.Fatalf("run 1's Say prose is not in the transcript — the 'cite skips the assistant' "+
			"assertion would be vacuous\n%s", readFile(t, path))
	}
	if assistantLine == firstHuman || assistantLine == secondHuman {
		t.Fatalf("the assistant prose shares a line with a human turn (assistant=%d first=%d second=%d)",
			assistantLine, firstHuman, secondHuman)
	}

	res := cite(e, proj, path, "WIDGET")
	if res.Code != 2 {
		t.Fatalf("an ambiguous match exited %d, want 2:\n%s", res.Code, res.Output)
	}
	lines := nonEmptyLines(res.Output)
	if len(lines) != 2 {
		t.Fatalf("want two candidate lines (the two human turns, not the assistant), got %d:\n%s",
			len(lines), res.Output)
	}
	// The candidates are exactly the two HUMAN lines, in file order, and the assistant
	// line is absent — cite returned the user's words and skipped the agent's.
	wantA := fmt.Sprintf("%s:%d", path, min(firstHuman, secondHuman))
	wantB := fmt.Sprintf("%s:%d", path, max(firstHuman, secondHuman))
	if lines[0] != wantA || lines[1] != wantB {
		t.Fatalf("candidates = %v, want the two human lines %s and %s (never the assistant line %d)",
			lines, wantA, wantB, assistantLine)
	}
	for _, l := range lines {
		if l == fmt.Sprintf("%s:%d", path, assistantLine) {
			t.Fatalf("cite returned the assistant's line %d — it must search the user's words, not the agent's:\n%s",
				assistantLine, res.Output)
		}
	}
}

// T029_03: a quote in nothing the user said prints nothing and exits 1 —
// including a quote that appears only in the agent's own output.
//
// The no-match case. Silent on stdout is the contract: a script tests the exit
// code, and printing a candidate here would be a false citation. Driven through
// the mock: the substring SECRETMARKER is present in the transcript — but only in
// the agent's own `Say` turn — so the correct answer is "not the user's words",
// exit 1. Proving cite ignores the agent's prose is exactly what a mock-produced
// assistant turn lets this assert against the real record.
func TestT029_03_CiteNoMatchExitsOneAndIsSilent(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	e.Run(proj, "s-029-03", "do the thing", Turns("done",
		Say("m1", "using the SECRETMARKER internally"),
	))
	path := e.TranscriptPath(proj, "s-029-03")

	res := cite(e, proj, path, "SECRETMARKER")
	if res.Code != 1 {
		t.Fatalf("a no-match exited %d, want 1:\n%s", res.Code, res.Output)
	}
	// Output carries stdout and stderr together. No candidate may be printed (a
	// false citation); the only thing said is the stderr notice of the miss (#295).
	if strings.Contains(res.Output, path+":") {
		t.Fatalf("a no-match printed a citation, which must be silent on stdout:\n%q", res.Output)
	}
	if !strings.Contains(res.Output, "the quote is not in the user's messages") {
		t.Fatalf("a no-match must say on stderr that the quote is not there:\n%q", res.Output)
	}
}

// T029_04: a quote landing on an AskUserQuestion ANSWER resolves — the prompted
// answer is the user's own words the same as a typed message.
//
// The answer case the spec singles out. The answer lands not as a plain message
// but as a user entry carrying a tool_result envelope, `The user answered:
// "<question>"="<answer>". ...`; the answer text is extracted from that envelope
// and searched. A quote from the answer resolves to the line the envelope sits on.
//
// Driven through the MOCK: a10n-cli#470 taught the mock's scenario validator to
// accept a scenario-authored `user` record carrying a tool_result WITH a
// tool_use_id — the exact envelope an answer lands as — and forward+persist it into
// the transcript (see AnswerQuestion). So the answer envelope is now a mock-produced
// record, not a hand-authored fixture. The prompt is the record on line 1, the
// answer envelope the record AnswerQuestion emits on line 2; a quote from the answer
// resolves to that line. The prompt is chosen NOT to contain the answer substring,
// so the match is unique to the envelope.
func TestT029_04_CiteMatchesAnAskUserQuestionAnswer(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	// The agent asked a question; the person CHOSE "go with the second option",
	// which the mock writes as the answer envelope after the seeded prompt.
	e.Run(proj, "s-029-04", "here is the task", Turns("done",
		harness.AnswerIfAsked(t, "q1", [2]string{"which approach?", "go with the second option"})...,
	))
	path := e.TranscriptPath(proj, "s-029-04")

	// Without an answer record (no question tool) the words were never said: they are
	// not citable, while the person's prompt, in the same record, is.
	if !harness.HasCap(t, harness.CapAskUserQuestion) {
		requireNotSaid(t, e, proj, path, "here is the task", "second option")
		return
	}

	// The envelope's physical line, read from the file the mock wrote — it follows the
	// mock's no-uuid preamble block and the root prompt, so it is not a hard-coded line.
	answerLine := physicalLine(t, path, "go with the second option")
	if answerLine == 0 {
		t.Fatalf("the mock did not write the answer envelope to the transcript\n%s", readFile(t, path))
	}

	res := cite(e, proj, path, "second option")
	if res.Code != 0 {
		t.Fatalf("citing a prompted answer exited %d, want 0:\n%s", res.Code, res.Output)
	}
	want := fmt.Sprintf("%s:%d", path, answerLine)
	if strings.TrimSpace(res.Output) != want {
		t.Fatalf("stdout = %q, want %q (the line the answer envelope sits on)", strings.TrimSpace(res.Output), want)
	}
}

// T029_05: no QUESTION text of an AskUserQuestion is citable — only the user's
// selected answers are — and this holds for a MULTI-question call, where every
// pair is written into one string.
//
// The other half of the answer case, and the one that proves the envelope is
// parsed pair-by-pair rather than searched whole. The fixture asks TWO questions
// deliberately: the reviewer caught that the old parser mangled a two-question
// envelope into one blob carrying the SECOND question, so a quote from that
// question resolved — a false citation grounding a claim in the agent's own
// words. Both questions must be non-citable; a single-question fixture would not
// exercise that path at all.
//
// Driven through the MOCK, like T029_04: the multi-question answer envelope is the
// record AnswerQuestion emits, now that #470 lets the mock forward a tool_result
// user record. The prompt is chosen NOT to contain either question's distinctive
// token, so the only place FROBNICATE/BAZQUX appear is inside the (mock-written)
// question text — which must stay non-citable.
func TestT029_05_CiteDoesNotMatchAnyQuestion(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	e.Run(proj, "s-029-05", "here is the task", Turns("done",
		harness.AnswerIfAsked(t, "q1",
			[2]string{"should I use the FROBNICATE strategy?", "no, keep it simple"},
			[2]string{"and which BAZQUX mode?", "the fast one"},
		)...,
	))
	path := e.TranscriptPath(proj, "s-029-05")

	// Without an answer record neither the questions nor the answers are in the record
	// at all: nothing of them is citable, and the prompt still is.
	if !harness.HasCap(t, harness.CapAskUserQuestion) {
		requireNotSaid(t, e, proj, path, "here is the task", "FROBNICATE", "BAZQUX", "keep it simple", "the fast one")
		return
	}

	// The envelope's physical line, read from the file the mock wrote (it follows the
	// mock's no-uuid preamble block and the root prompt).
	envLine := physicalLine(t, path, "no, keep it simple")
	if envLine == 0 {
		t.Fatalf("the mock did not write the answer envelope to the transcript\n%s", readFile(t, path))
	}

	// Positive control: an ANSWER from the envelope resolves to the line the envelope
	// sits on. Without this the question-negatives below could pass vacuously — a run
	// where the mock never wrote the envelope would also make every question "not
	// found". This proves the envelope IS there and its answers ARE citable, so the
	// negatives are about the parse, not an empty file.
	if res := cite(e, proj, path, "keep it simple"); res.Code != 0 ||
		strings.TrimSpace(res.Output) != fmt.Sprintf("%s:%d", path, envLine) {
		t.Fatalf("the answer envelope's own answer did not resolve to its line %d (code %d, out %q) — "+
			"the question-negatives below would be vacuous", envLine, res.Code, strings.TrimSpace(res.Output))
	}

	// FROBNICATE is only in the FIRST question; BAZQUX only in the SECOND. Neither
	// is the user's words — and the SECOND is the one the old parser leaked.
	for _, q := range []string{"FROBNICATE", "BAZQUX"} {
		res := cite(e, proj, path, q)
		if res.Code != 1 {
			t.Fatalf("citing question text %q exited %d, want 1 (a question is not the user's words):\n%s",
				q, res.Code, res.Output)
		}
		if strings.Contains(res.Output, path+":") {
			t.Fatalf("citing question %q printed a citation, must be silent on stdout:\n%q", q, res.Output)
		}
	}
}

// T029_07: in a MULTI-question AskUserQuestion, EACH answer is citable, resolving
// to the one line the envelope sits on.
//
// The positive half of the multi-question fix: both the first and the second
// answer resolve (exit 0) to the envelope's line. Paired with T029_05's negative
// half — no question resolves — this pins the pair-by-pair parse from both sides,
// which is the regression the reviewer flagged. Driven through the MOCK, like the
// other answer cases now that #470 forwards the tool_result envelope.
func TestT029_07_CiteMatchesEachAnswerInAMultiQuestion(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	// A three-question envelope, written by the mock after the seeded prompt.
	e.Run(proj, "s-029-07", "kick it off", Turns("done",
		harness.AnswerIfAsked(t, "q1",
			[2]string{"where should it live?", "under the dotdir store"},
			[2]string{"required or optional?", "make it required"},
			[2]string{"backfill existing?", "yes backfill everything"},
		)...,
	))
	path := e.TranscriptPath(proj, "s-029-07")

	// Without an answer record none of the answers was given: none is citable.
	if !harness.HasCap(t, harness.CapAskUserQuestion) {
		requireNotSaid(t, e, proj, path, "kick it off", "under the dotdir store", "make it required", "yes backfill everything")
		return
	}

	// The envelope's physical line, read from the file the mock wrote (it follows the
	// mock's no-uuid preamble block and the root prompt).
	envLine := physicalLine(t, path, "under the dotdir store")
	if envLine == 0 {
		t.Fatalf("the mock did not write the answer envelope to the transcript\n%s", readFile(t, path))
	}

	// Each answer resolves to the ONE envelope line; no answer is ambiguous, since one
	// envelope is one line however many pairs it carries.
	for _, a := range []string{"under the dotdir store", "make it required", "yes backfill everything"} {
		res := cite(e, proj, path, a)
		if res.Code != 0 {
			t.Fatalf("citing answer %q exited %d, want 0:\n%s", a, res.Code, res.Output)
		}
		want := fmt.Sprintf("%s:%d", path, envLine)
		if strings.TrimSpace(res.Output) != want {
			t.Fatalf("citing answer %q gave %q, want %q", a, strings.TrimSpace(res.Output), want)
		}
	}
}

// T029_08: an answer in a multi-question envelope that itself contains an inner
// quote is extracted whole and remains citable.
//
// The brittle bit the reviewer named: an answer may contain a `"`, and the parse
// must not truncate at it. Here the SECOND answer carries a quoted word; a quote
// spanning that inner quote must still resolve, proving the inner quote did not
// end the answer early — while the question, as ever, stays non-citable. Driven
// through the MOCK, like the other answer cases now that #470 forwards the envelope
// (and the inner `"` survives jsonStr's escaping into the transcript intact).
func TestT029_08_CiteAnswerWithInnerQuoteInMultiQuestion(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	e.Run(proj, "s-029-08", "start", Turns("done",
		harness.AnswerIfAsked(t, "q1",
			[2]string{"first thing?", "keep it plain"},
			[2]string{"what label?", `call it "draft" for now`},
		)...,
	))
	path := e.TranscriptPath(proj, "s-029-08")

	// Without an answer record the inner-quoted answer was never given: not citable.
	if !harness.HasCap(t, harness.CapAskUserQuestion) {
		requireNotSaid(t, e, proj, path, "start", `call it "draft" for now`, "keep it plain")
		return
	}

	// The envelope's physical line, read from the file the mock wrote.
	envLine := physicalLine(t, path, `keep it plain`)
	if envLine == 0 {
		t.Fatalf("the mock did not write the answer envelope to the transcript\n%s", readFile(t, path))
	}

	// A substring spanning the inner-quoted word: only resolvable if the whole
	// answer, inner quote and all, was kept.
	res := cite(e, proj, path, `call it "draft" for now`)
	if res.Code != 0 {
		t.Fatalf("citing an answer with an inner quote exited %d, want 0:\n%s", res.Code, res.Output)
	}
	if !strings.Contains(res.Output, fmt.Sprintf("%s:%d", path, envLine)) {
		t.Fatalf("the inner-quote answer did not resolve to its line:\n%s", res.Output)
	}
}

// T029_06: cite defaults to the payload's transcript_path when --path is absent,
// and with no path, no payload and no session id it refuses.
//
// The default-resolution wiring, and the refusal when there is nothing to search
// at all. A resolved-but-empty search is exit 1 (no match); a trajectory that could
// not be resolved by any source is a refusal on stderr, a different failure a script
// must not read as "the quote is not there". The default half reads a mock-produced
// transcript; the refusal half needs none.
//
// The refusal half clears CLAUDE_CODE_SESSION_ID: cite's environment fallback would
// otherwise resolve the current session from it, which is exactly the T029_15
// behavior — so "nothing to resolve" means an empty payload AND no session id in the
// environment. A developer runs this suite inside a real session whose own
// CLAUDE_CODE_SESSION_ID is on os.Environ(), so the empty assignment wins over it and
// the test means the same thing locally and in CI.
func TestT029_06_CiteDefaultAndNoTrajectory(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	e.Run(proj, "s-029-06", "cite this exact phrase here", Turns("done",
		Bash("b1", "echo done > done.md"),
	))
	path := e.TranscriptPath(proj, "s-029-06")
	// The prompt's physical line, read from the file the mock wrote (it follows the
	// mock's no-uuid preamble block).
	promptLine := physicalLine(t, path, "cite this exact phrase here")
	if promptLine == 0 {
		t.Fatalf("the mock did not write the prompt to the transcript\n%s", readFile(t, path))
	}

	// Default: no --path, transcript_path on the payload.
	payload := `{"transcript_path":"` + path + `","cwd":"` + proj + `"}`
	res := e.CLIDirectStdin(proj, payload, "sr-session", "trajectory", "cite", "exact phrase")
	if res.Code != 0 {
		t.Fatalf("cite from a payload exited %d, want 0:\n%s", res.Code, res.Output)
	}
	if !strings.Contains(res.Output, fmt.Sprintf("%s:%d", path, promptLine)) {
		t.Fatalf("cite from a payload did not resolve to the right line:\n%s", res.Output)
	}

	// No path, no payload, no session id: a refusal, not a no-match.
	res = e.CLIDirectStdinEnv(proj, `{}`, []string{"CLAUDE_CODE_SESSION_ID="},
		"sr-session", "trajectory", "cite", "anything")
	if res.Code == 0 {
		t.Fatalf("cite with no trajectory exited 0, want non-zero:\n%s", res.Output)
	}
	if !res.Saw("no trajectory to read") {
		t.Fatalf("the refusal did not say why:\n%s", res.Output)
	}
}

// dirOf is the directory a fixture transcript sits in — the working directory to
// run the binary from. Nothing about cite depends on it (the path is explicit),
// but CLIDirect wants a directory to run in.
func dirOf(path string) string {
	i := strings.LastIndexByte(path, '/')
	if i < 0 {
		return "."
	}
	return path[:i]
}

// nonEmptyLines splits output into its non-blank lines.
func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// readFile returns the file at path as a string, for including the transcript in a
// failure message when a line-derivation did not find what it expected.
func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("(could not read %s: %v)", path, err)
	}
	return string(b)
}

// physicalLine returns the 1-based physical line of the first record in the file
// at path whose bytes contain substr, or 0 when none does.
//
// An independent witness of where the mock put a record — a plain scan of the
// file, owing nothing to the command under test — so a citation assertion can be
// checked against the transcript's real layout rather than a hard-coded guess.
func physicalLine(t *testing.T, path, substr string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read transcript %s: %v", path, err)
	}
	for i, line := range strings.Split(string(b), "\n") {
		if strings.Contains(line, substr) {
			return i + 1
		}
	}
	return 0
}
