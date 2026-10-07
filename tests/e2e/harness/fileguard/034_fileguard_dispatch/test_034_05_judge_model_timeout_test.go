package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This file covers a file-guard JUDGE with a per-judge MODEL and TIMEOUT
// (dot-dir-file-store Check model/timeout). It proves the whole config path
// end-to-end: the check's `model` reaches the sr-agent invocation (which builds
// `claude -p --model <resolved> …`, so the alias's harness-native name appears in
// the recorded argv), the custom `timeout` is accepted, and the judge still runs
// to a real verdict that blocks. The precise --model wiring is unit-tested in
// internal/dispatch; this is the end-to-end confirmation through the real
// binaries and the mock.

// judgeGuardModelTimeout is a file-guard judge carrying a custom model and
// timeout. size-lg resolves to `opus` under the Claude Code harness, which is
// what the recorded argv is asserted to carry.
const judgeGuardModelTimeout = `match: memories/**/*.md
checks:
  - judge: ./judge.md.j2
    model: size-lg
    timeout: 90s
`

// T034_11: a file-guard judge with `model: size-lg` and `timeout: 90s` blocks on
// a failing verdict, AND the check's model reached the harness — the recorded
// claude argv carries `--model opus` (size-lg's resolution). The custom timeout
// is generous, so the run completes normally under it.
func TestT034_11_JudgeModelAndTimeoutReachTheInvocation(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "substantive-memory", judgeGuardModelTimeout, map[string]string{"judge.md.j2": judgeGuardTemplate})
	e.CommitAll(proj, "the rule and its scripts")

	// A recording shim: writes the failing verdict AND records the claude argv, so
	// the test can assert the check's model reached the harness invocation.
	argvFile := filepath.Join(proj, "claude-argv.txt")
	e.InstallJudgeClaudeRecordingArgv(argvFile, `{"pass": false, "reasoning": "this memory is one word, not substantive"}`)

	e.Run(proj, "s-034-11", "write a thin memory", Turns("done",
		Write("w1", "memories/note.md", "meh"),
	).ThenCommit("add the memory"))

	// The judge ran to a verdict and blocked — the custom model/timeout did not
	// break the judge path.
	blocks := e.BlockingErrorsFrom(proj, "s-034-11", "Stop")
	if len(blocks) == 0 {
		t.Fatalf("a file-guard judge with a custom model/timeout did not block on a failing verdict")
	}
	joined := strings.Join(blocks, "\n")
	if !containsStr(joined, "not substantive") {
		t.Errorf("the judge's reasoning did not reach the agent:\n%s", joined)
	}

	// The check's model reached the harness: sr-agent resolved size-lg to opus and
	// invoked `claude … --model opus …`, recorded here.
	argv, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("the recording shim did not capture the claude argv (was the judge invoked?): %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(argv)), "\n")
	if !hasAdjacentPair(lines, "--model", "opus") {
		t.Errorf("the judge's model (size-lg) did not reach the harness as `--model opus`; argv was:\n%s", string(argv))
	}
}

// hasAdjacentPair reports whether flag is immediately followed by value in the
// argv lines — `--model` then `opus` as two consecutive arguments, which is how
// sr-agent passes a resolved model to the harness.
func hasAdjacentPair(lines []string, flag, value string) bool {
	for i := 0; i+1 < len(lines); i++ {
		if lines[i] == flag && lines[i+1] == value {
			return true
		}
	}
	return false
}

// judgeGuardScopedTools grants its judge scoped tool rules whose parentheses
// hold spaces and colons — the shape a rule needs to let a judge run one
// command family (`git show`, `curl -sL`) or fetch one domain.
const judgeGuardScopedTools = `match: memories/**/*.md
checks:
  - judge: ./judge.md.j2
    allowed_tools: ["Bash(git show:*)", "Bash(curl -sL:*)", "WebFetch(domain:code.claude.com)", Read]
`

// T034_12: a judge's scoped allowed_tools reach the harness INTACT — each rule
// one --allowed-tools value, spaces and all. They pass through the engine's
// judge command and sr-agent's parser, and a parser that split at every space
// would hand claude `Bash(git` and `show:*)` as two argv values: the first
// grants nothing, the second starts no rule at all.
func TestT034_12_ScopedAllowedToolsReachTheHarnessIntact(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "substantive-memory", judgeGuardScopedTools, map[string]string{"judge.md.j2": judgeGuardTemplate})
	e.CommitAll(proj, "the rule and its scripts")

	argvFile := filepath.Join(proj, "claude-argv.txt")
	e.InstallJudgeClaudeRecordingArgv(argvFile, `{"pass": true, "reasoning": ""}`)

	e.Run(proj, "s-034-12", "write a memory", Turns("done",
		Write("w1", "memories/note.md", "a substantive memory"),
	).ThenCommit("add the memory"))

	argv, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("the recording shim did not capture the claude argv (was the judge invoked?): %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(argv)), "\n")
	var granted []string
	for i, l := range lines {
		if l != "--allowed-tools" {
			continue
		}
		for _, v := range lines[i+1:] {
			if strings.HasPrefix(v, "--") {
				break
			}
			granted = append(granted, v)
		}
	}
	for _, want := range []string{"Bash(git show:*)", "Bash(curl -sL:*)", "WebFetch(domain:code.claude.com)", "Read"} {
		found := false
		for _, g := range granted {
			if g == want {
				found = true
			}
		}
		if !found {
			t.Errorf("the scoped rule %q did not reach the harness as one --allowed-tools value; got %q", want, granted)
		}
	}
	for _, g := range granted {
		if g == "Bash(git" || g == "show:*)" || g == "-sL:*)" {
			t.Errorf("a scoped rule was split at a space: %q in %q", g, granted)
		}
	}
}

// judgeGuardDisallowedTools grants its judge a command family and takes back
// forms of it with disallowed_tools — rules with spaces inside the scope.
const judgeGuardDisallowedTools = `match: memories/**/*.md
checks:
  - judge: ./judge.md.j2
    allowed_tools: ["Bash(curl:*)"]
    disallowed_tools: ["Bash(curl * -o *)", "Bash(curl * -d @*)", WebSearch]
`

// T034_13: a judge's disallowed_tools reach the harness INTACT, as claude's
// --disallowed-tools, each rule one value, in the same group as the readonly
// workspace's own deny — and the granted rule stays in --allowed-tools.
func TestT034_13_DisallowedToolsReachTheHarnessIntact(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "substantive-memory", judgeGuardDisallowedTools, map[string]string{"judge.md.j2": judgeGuardTemplate})
	e.CommitAll(proj, "the rule and its scripts")

	argvFile := filepath.Join(proj, "claude-argv.txt")
	e.InstallJudgeClaudeRecordingArgv(argvFile, `{"pass": true, "reasoning": ""}`)

	e.Run(proj, "s-034-13", "write a memory", Turns("done",
		Write("w1", "memories/note.md", "a substantive memory"),
	).ThenCommit("add the memory"))

	argv, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("the recording shim did not capture the claude argv (was the judge invoked?): %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(argv)), "\n")
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
	deny := values("--disallowed-tools")
	if strings.Count(string(argv), "--disallowed-tools\n") != 1 {
		t.Errorf("the denies must be one --disallowed-tools group; argv:\n%s", argv)
	}
	for _, want := range []string{"Bash(curl * -o *)", "Bash(curl * -d @*)", "WebSearch"} {
		if !containsValue(deny, want) {
			t.Errorf("the deny rule %q did not reach the harness whole; --disallowed-tools carried %q", want, deny)
		}
	}
	if len(deny) == 0 || !strings.HasPrefix(deny[0], "Edit(/") {
		t.Errorf("the readonly workspace's own Edit deny must still lead the group; got %q", deny)
	}
	if !containsValue(values("--allowed-tools"), "Bash(curl:*)") {
		t.Errorf("the granted rule did not reach --allowed-tools; argv:\n%s", argv)
	}
}

// T034_14: a malformed disallowed_tools entry, or one on a script check, is
// refused when the rule loads, named where an author looks.
func TestT034_14_BadDisallowedToolsIsRefusedAtLoad(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.FileGuard(proj, "unclosed", "match: \"memories/**\"\nchecks:\n  - judge: ./judge.md.j2\n    disallowed_tools: [\"Bash(curl * -o *\"]\n",
		map[string]string{"judge.md.j2": judgeGuardTemplate})
	e.FileGuard(proj, "onscript", "match: \"memories/**\"\nchecks:\n  - script: ./check.sh\n    disallowed_tools: [WebSearch]\n",
		map[string]string{"check.sh": "#!/bin/sh\ncat >/dev/null\nexit 0\n"})

	decl := e.CLIDirect(proj, "sr-file", "declarations", proj)
	if decl.Code != 1 {
		t.Fatalf("sr-file declarations should exit 1 on an invalid declaration, got %d:\n%s", decl.Code, decl.Output)
	}
	for _, want := range []string{"file-guard/unclosed", "disallowed_tools", "never closes", "file-guard/onscript", "without a judge"} {
		if !strings.Contains(decl.Output, want) {
			t.Errorf("sr-file declarations did not report %q:\n%s", want, decl.Output)
		}
	}
}

func containsValue(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
