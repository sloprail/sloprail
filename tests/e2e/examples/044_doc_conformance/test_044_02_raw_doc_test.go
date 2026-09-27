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
// section out with grep/head, and grants exactly those tools. These tests
// pin the two halves of that wiring: the instructions reach the judge's prompt,
// and the tool grant reaches the harness intact.

// T044_06: the rendered prompt tells the judge how to fetch the raw doc — the
// `.md` form of the marker's URL, piped into grep/head — and to judge the CHANGE
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
		"curl -sL <url>.md | grep -n -A60",         // fetch piped into a section cut
		"EXACTLY `curl -sL <url>.md`",              // the one curl form the grant permits
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

// T044_07: the rule's tool grant reaches the harness intact. The curl grant is
// PINNED — one command, one host, one URL — and its deny takes back any extra
// word after the URL, which is what closes curl's writing and uploading
// options (measured; see the rule's file-guard.yaml). Each rule arrives as one
// argv value, spaces included. sed and awk are not granted (both wrote files
// under their Bash(...:*) grants), no unpinned Bash(curl:*) is either, and no
// WebFetch (a summary, not the page; and a way to send the project out).
func TestT044_07_PinnedToolsReachTheHarnessIntact(t *testing.T) {
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
	lines := strings.Split(string(argv), "\n")
	values := func(flag string) []string {
		var out []string
		for i, l := range lines {
			if l != flag {
				continue
			}
			for _, v := range lines[i+1:] {
				if strings.HasPrefix(v, "--") {
					break
				}
				out = append(out, v)
			}
		}
		return out
	}
	has := func(vs []string, want string) bool {
		for _, v := range vs {
			if v == want {
				return true
			}
		}
		return false
	}
	allowed, denied := values("--allowed-tools"), values("--disallowed-tools")
	for _, want := range []string{"Bash(curl -sL https://code.claude.com/docs/*)", "Bash(grep:*)", "Bash(head:*)"} {
		if !has(allowed, want) {
			t.Errorf("the judge was not granted %q intact; --allowed-tools carried %q", want, allowed)
		}
	}
	for _, not := range []string{"Bash(curl:*)", "Bash(sed:*)", "Bash(awk:*)", "Bash", "WebFetch"} {
		if has(allowed, not) {
			t.Errorf("the judge was granted %q, which the rule does not name; --allowed-tools carried %q", not, allowed)
		}
	}
	if !has(denied, "Bash(curl -sL https://code.claude.com/docs/* *)") {
		t.Errorf("the pinned curl's any-extra-word deny did not reach --disallowed-tools intact; got %q", denied)
	}
}
