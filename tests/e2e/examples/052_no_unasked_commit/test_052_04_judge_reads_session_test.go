package e2e

// The judge half cannot rule from the quote alone: "looks good, commit it"
// means nothing until you know what "it" is. The shipped template hands the
// judge WHERE to read (the session record's path, and the exact line the
// citation resolved to) and tells it what to read there and in the tree. This
// pins that wiring on the shipped template: a template that stopped carrying
// the record's path or the cited line would leave the judge nothing to read
// but the quote, and still load and still "pass".

import (
	"fmt"
	"strings"
	"testing"
)

// T052_11: the rendered judge prompt carries the session record's path, the
// cited line as <record>:<line>, the command, and the instructions to read the
// cited message, the reply it answered, the agent's edits, and the working
// tree — and to fail a commit that sweeps in changes the user did not approve.
func TestT052_11_JudgePromptPointsAtTheSessionRecord(t *testing.T) {
	e := newEnv(t)
	proj := nucProject(t, e)
	// The installed rule is the baseline, not this session's change — otherwise
	// the plugin's own authoring-slop judge reviews it at Stop and its prompt,
	// asked last, is the one the capture keeps.
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-q", "-m", "baseline")
	e.InstallJudgeClaudeCapturing(proj, "judge-prompt.txt", `{"pass": true, "reasoning": "read it"}`)

	prompt := "please commit this fix"
	res := e.Run(proj, "s-052-11", prompt, Turns("done",
		Write("w1", "src/parser.py", "def parse():\n    return None\n"),
		Bash("b1", `git add -A`),
		Bash("b2", `sr-session trajectory cite `+shq(prompt)+` && git commit -m "fix null check"`),
	))
	if res.Refused() {
		t.Fatalf("the cited commit was refused under a passing judge:\n%s", res.Output)
	}

	got := e.JudgePrompt(proj, "judge-prompt.txt")
	if got == "" {
		t.Fatalf("the judge was never asked:\n%s", res.Output)
	}
	record := findTranscriptPath(t, got)
	for _, want := range []string{
		"The whole session record is `" + record + "`",
		`<citation source="` + record + ":",
		`git commit -m "fix null check"`,
		"you must READ the session",
		"What the user was answering",
		"What the agent changed",
		"What this command will commit",
		"git status --porcelain",
		"swept in by `git add -A`",
		// The staged-files test: every file in the commit must have been reported in
		// the reply the user's "commit it" answered, and git runs bare (a `cd … &&`
		// or `git -C` call is blocked in the judge's sandbox).
		"The staged-files test",
		"git diff --cached --name-only",
		"EACH of those files",
		"reply the user's \"commit it\" answered",
		"Run git BARE",
		"`git -C <dir> ...`",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the judge prompt lacks %q:\n%s", want, got)
		}
	}
}

// findTranscriptPath pulls the session record's path out of the rendered
// prompt, and checks it names a .jsonl — the file the judge is told to read.
func findTranscriptPath(t *testing.T, prompt string) string {
	t.Helper()
	const marker = "The whole session record is `"
	i := strings.Index(prompt, marker)
	if i < 0 {
		t.Fatalf("the judge prompt names no session record:\n%s", prompt)
	}
	rest := prompt[i+len(marker):]
	j := strings.Index(rest, "`")
	if j < 0 || !strings.HasSuffix(rest[:j], ".jsonl") {
		t.Fatalf("the session record path is not a .jsonl: %q", fmt.Sprint(rest[:min(j+1, len(rest))]))
	}
	return rest[:j]
}
