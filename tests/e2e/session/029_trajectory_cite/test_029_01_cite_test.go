package e2e

import (
	"fmt"
	"strings"
	"testing"
)

// T029_01: a quote in exactly one user message resolves to <path>:<line>, exit 0.
//
// The success case the whole command exists for. The line is the 1-based physical
// line of the matched entry, assembled with the path into the citation an agent
// writes.
func TestT029_01_CiteUniqueMatchExitsZero(t *testing.T) {
	e := New(t)
	path := writeTranscript(t,
		userMsg("u1", "please refactor the auth module carefully"), // line 1
		assistantText("a1", "u1", "on it"),                         // line 2
		userMsg("u2", "and add tests"),                             // line 3
	)

	res := cite(e, dirOf(path), path, "auth module")
	if res.Code != 0 {
		t.Fatalf("a unique match exited %d, want 0:\n%s", res.Code, res.Output)
	}
	want := fmt.Sprintf("%s:%d", path, 1)
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
func TestT029_02_CiteAmbiguousExitsTwo(t *testing.T) {
	e := New(t)
	path := writeTranscript(t,
		userMsg("u1", "the WIDGET needs work"),                    // line 1 — candidate
		assistantText("a1", "u1", "I will change the WIDGET now"), // line 2 — must NOT be a candidate
		userMsg("u2", "yes the WIDGET again"),                     // line 3 — candidate
	)

	res := cite(e, dirOf(path), path, "WIDGET")
	if res.Code != 2 {
		t.Fatalf("an ambiguous match exited %d, want 2:\n%s", res.Code, res.Output)
	}
	lines := nonEmptyLines(res.Output)
	if len(lines) != 2 {
		t.Fatalf("want two candidate lines, got %d:\n%s", len(lines), res.Output)
	}
	if lines[0] != fmt.Sprintf("%s:1", path) || lines[1] != fmt.Sprintf("%s:3", path) {
		t.Fatalf("candidates = %v, want the two user lines %s:1 and %s:3 (not the assistant line 2)",
			lines, path, path)
	}
}

// T029_03: a quote in nothing the user said prints nothing and exits 1 —
// including a quote that appears only in the agent's own output.
//
// The no-match case. Silent on stdout is the contract: a script tests the exit
// code, and printing a candidate here would be a false citation. The substring
// SECRETMARKER is present in the transcript — but only in an assistant turn — so
// the correct answer is "not the user's words", exit 1.
func TestT029_03_CiteNoMatchExitsOneAndIsSilent(t *testing.T) {
	e := New(t)
	path := writeTranscript(t,
		userMsg("u1", "do the thing"),
		assistantText("a1", "u1", "using the SECRETMARKER internally"),
	)

	res := cite(e, dirOf(path), path, "SECRETMARKER")
	if res.Code != 1 {
		t.Fatalf("a no-match exited %d, want 1:\n%s", res.Code, res.Output)
	}
	if strings.TrimSpace(res.Output) != "" {
		t.Fatalf("a no-match printed to stdout, which must be silent:\n%q", res.Output)
	}
}

// T029_04: a quote landing on an AskUserQuestion ANSWER resolves — the prompted
// answer is the user's own words the same as a typed message.
//
// The answer case the spec singles out. The answer lands not as a plain message
// but as a user entry carrying a tool_result envelope, `The user answered:
// "<question>"="<answer>". ...`; the answer text is extracted from that envelope
// and searched. A quote from the answer resolves to the line the envelope sits on.
func TestT029_04_CiteMatchesAnAskUserQuestionAnswer(t *testing.T) {
	e := New(t)
	path := writeTranscript(t,
		userMsg("u1", "here is the task"),       // line 1
		assistantText("a1", "u1", "let me ask"), // line 2
		// line 3: the answer envelope. The user CHOSE "go with the second option".
		answerEnvelope("u2", "a1", "which approach?", "go with the second option"),
	)

	res := cite(e, dirOf(path), path, "second option")
	if res.Code != 0 {
		t.Fatalf("citing a prompted answer exited %d, want 0:\n%s", res.Code, res.Output)
	}
	want := fmt.Sprintf("%s:3", path)
	if strings.TrimSpace(res.Output) != want {
		t.Fatalf("stdout = %q, want %q (the line the answer envelope sits on)", strings.TrimSpace(res.Output), want)
	}
}

// T029_05: the QUESTION text of an AskUserQuestion is NOT citable — only the
// user's selected answer is.
//
// The other half of the answer case, and the one that proves the envelope is
// parsed rather than searched whole. The question is the agent's words; a quote
// from it must not resolve, or "you said this" would be satisfied by what the
// agent asked rather than by what the person chose.
func TestT029_05_CiteDoesNotMatchTheQuestion(t *testing.T) {
	e := New(t)
	path := writeTranscript(t,
		userMsg("u1", "here is the task"),
		answerEnvelope("u2", "u1", "should I use the FROBNICATE strategy?", "no, keep it simple"),
	)

	// FROBNICATE appears only in the QUESTION, which is not the user's words.
	res := cite(e, dirOf(path), path, "FROBNICATE")
	if res.Code != 1 {
		t.Fatalf("citing the question text exited %d, want 1 (the question is not the user's words):\n%s",
			res.Code, res.Output)
	}
	if strings.TrimSpace(res.Output) != "" {
		t.Fatalf("citing the question printed something, must be silent:\n%q", res.Output)
	}
}

// T029_06: cite defaults to the payload's transcript when --path is absent, and
// with no path and no payload it refuses.
//
// The default-resolution wiring, and the refusal when there is nothing to search.
// A resolved-but-empty search is exit 1 (no match); a trajectory that could not
// be resolved at all is a refusal on stderr, a different failure a script must
// not read as "the quote is not there".
func TestT029_06_CiteDefaultAndNoTrajectory(t *testing.T) {
	e := New(t)
	path := writeTranscript(t, userMsg("u1", "cite this exact phrase here"))

	// Default: no --path, transcript on the payload.
	payload := `{"transcript_path":"` + path + `","cwd":"` + dirOf(path) + `"}`
	res := e.CLIDirectStdin(dirOf(path), payload, "sr-session", "trajectory", "cite", "exact phrase")
	if res.Code != 0 {
		t.Fatalf("cite from a payload exited %d, want 0:\n%s", res.Code, res.Output)
	}
	if !strings.Contains(res.Output, path+":1") {
		t.Fatalf("cite from a payload did not resolve to the right line:\n%s", res.Output)
	}

	// No path, no payload: a refusal, not a no-match.
	res = e.CLIDirectStdin(dirOf(path), `{}`, "sr-session", "trajectory", "cite", "anything")
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
