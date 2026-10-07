package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/internal/harness"
)

func agentRunArgv(t *testing.T, name Harness, resume bool) []string {
	t.Helper()
	spec, ok := lookupSpec(name)
	if !ok {
		t.Fatalf("no spec for %s", name)
	}
	run, err := spec.forAgentRun(resume)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return BuildInvocation(run, "m", nil, "do it", func(string) string { return "" }).Args
}

// The agent under test runs with its project's hooks live and unattended, and a later
// turn resumes the session, in each harness's own spelling.
func TestForAgentRun_PerHarnessArgv(t *testing.T) {
	for _, c := range []struct {
		name         Harness
		first, later string
		never        []string
	}{
		{ClaudeCode, "-p --model m --permission-mode bypassPermissions -- do it", "-p --continue --model m --permission-mode bypassPermissions -- do it", []string{"--settings"}},
		{Codex, "exec -m m --dangerously-bypass-approvals-and-sandbox --skip-git-repo-check -- do it", "exec resume --last -m m --dangerously-bypass-approvals-and-sandbox --skip-git-repo-check -- do it", []string{"--disable", "--ignore-user-config", "--ephemeral"}},
		{Cursor, "-p --model m --trust --force -- do it", "-p --continue --model m --trust --force -- do it", nil},
	} {
		if got := strings.Join(agentRunArgv(t, c.name, false), " "); got != c.first {
			t.Errorf("%s first turn:\n got %s\nwant %s", c.name, got, c.first)
		}
		if got := strings.Join(agentRunArgv(t, c.name, true), " "); got != c.later {
			t.Errorf("%s later turn:\n got %s\nwant %s", c.name, got, c.later)
		}
		for _, bad := range c.never {
			if slices.Contains(agentRunArgv(t, c.name, false), bad) {
				t.Errorf("%s: the agent under test must not carry judge isolation %s", c.name, bad)
			}
		}
	}
}

// A spec without agent-run args is refused, not run as a judge.
func TestForAgentRun_RefusesASpecWithNone(t *testing.T) {
	if _, err := (harnessSpec{name: "bare", binary: "bare"}).forAgentRun(false); err == nil {
		t.Fatal("a harness that cannot run unattended must be refused")
	}
}

// The binary a spec runs is the one its harness package names, so the sandbox's PATH
// (which asks the harness package) and sr-agent (which runs the spec) never disagree.
func TestSpecBinary_IsTheProvisionersBinary(t *testing.T) {
	for _, spec := range harnesses {
		h, ok := harness.Lookup(string(spec.name))
		if !ok {
			t.Fatalf("%s is in sr-agent's registry but not the harness registry", spec.name)
		}
		p, err := harness.ProvisionerOf(h)
		if err != nil {
			t.Fatal(err)
		}
		if p.Binary() != spec.binary {
			t.Errorf("%s: spec runs %q, provisioner says %q", spec.name, spec.binary, p.Binary())
		}
	}
}

// The agent under test is not a confined judge: Codex's grant adds no --sandbox (which
// `exec resume` would refuse, and which would fight the bypass flag).
func TestAgentRun_CodexGrantAddsNoSandbox(t *testing.T) {
	spec, _ := lookupSpec(Codex)
	args, err := harnessGrant(spec, accessGrant{AgentRun: true})
	if err != nil || slices.Contains(args, "--sandbox") {
		t.Fatalf("got %v, %v", args, err)
	}
}
