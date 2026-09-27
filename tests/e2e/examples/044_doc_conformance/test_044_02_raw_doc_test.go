package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The judge reads the doc's RAW text, not WebFetch's summary of it. In real
// precompact-support runs (2026-09-27) judges that WebFetched the hooks page
// quoted fields and rules the page never states — a `compact_reason` field,
// "PreCompact cannot block" — and the agent rewrote correct code to match them.
// So the rule tells the judge to curl the page's `.md` form once and cut the
// section out with grep/sed/head, and grants exactly those tools. These tests
// pin the two halves of that wiring: the instructions reach the judge's prompt,
// and the tool grant reaches the harness intact.

// T044_06: the rendered prompt tells the judge how to fetch the raw doc — the
// `.md` form of the marker's URL, piped into grep/sed — and to judge the CHANGE
// against it, quoting the doc line for any refusal.
func TestT044_06_PromptTellsTheJudgeToReadTheRawDoc(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj)
	e.InstallJudgeClaudeCapturing(proj, "judge-prompt.txt", `{"pass": true, "reasoning": ""}`)

	e.Run(proj, "s-044-06", "write a marked mock", Turns("done",
		Write("w1", "internal/mock/hooks.go", markedMock(otherDocURL+"#precompact", "func Fire() string { return `{\"trigger\":\"auto\"}` }")),
	))

	prompt := e.JudgePrompt(proj, "judge-prompt.txt")
	if prompt == "" {
		t.Fatalf("the judge never ran, so nothing about the wiring can be concluded")
	}
	for _, want := range []string{
		otherDocURL + "#precompact",                // the marker's own URL, anchor included
		"append `.md` to the path",                 // how to get the raw page
		"curl -sL <url>.md | sed -n",               // fetch piped into a section cut
		"<change path=\"internal/mock/hooks.go\">", // the diff being judged
		"QUOTE the doc line",                       // a refusal must quote the doc
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the judge prompt lacks %q:\n%s", want, prompt)
		}
	}
	if !strings.Contains(prompt, `{\"trigger\":\"auto\"}`) && !strings.Contains(prompt, `{"trigger":"auto"}`) {
		t.Errorf("the change's own content did not reach the <change> diff:\n%s", prompt)
	}
}

// T044_07: the rule's allowed_tools reach the harness intact — each scoped rule
// whole (`Bash(curl:*)` is one rule, not split at its colon), awk not among them
// (measured to write files under Bash(awk:*)), WebFetch kept as the fallback.
func TestT044_07_ScopedToolsReachTheHarnessIntact(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj)
	argvFile := filepath.Join(t.TempDir(), "claude-argv.txt")
	e.InstallJudgeClaudeRecordingArgv(argvFile, `{"pass": true, "reasoning": ""}`)

	e.Run(proj, "s-044-07", "write a marked mock", Turns("done",
		Write("w1", "internal/mock/hooks.go", markedMock(otherDocURL+"#precompact", "func Fire() {}")),
	))

	argv, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("the recording shim captured no claude argv (was the judge invoked?): %v", err)
	}
	var granted []string
	lines := strings.Split(string(argv), "\n")
	for i, l := range lines {
		if l == "--allowed-tools" && i+1 < len(lines) {
			for _, v := range lines[i+1:] {
				if strings.HasPrefix(v, "--") {
					break
				}
				// A single argument may carry several space-separated tools.
				granted = append(granted, strings.Fields(v)...)
			}
		}
	}
	has := func(tool string) bool {
		for _, g := range granted {
			if g == tool {
				return true
			}
		}
		return false
	}
	for _, want := range []string{"Bash(curl:*)", "Bash(grep:*)", "Bash(sed:*)", "Bash(head:*)", "WebFetch"} {
		if !has(want) {
			t.Errorf("the judge was not granted %s intact; --allowed-tools carried %q", want, granted)
		}
	}
	for _, not := range []string{"Bash(awk:*)", "Bash", "Bash(curl"} {
		if has(not) {
			t.Errorf("the judge was granted %s, which the rule does not name; --allowed-tools carried %q", not, granted)
		}
	}
}
