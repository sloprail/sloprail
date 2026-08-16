package e2e

import (
	"strings"
	"testing"
)

// changes_command: a plugin that owns its own PreToolUse hook and learns what
// the turn is about to do from `sr-file changes`.
//
// The subject is `sloprail-secrets`, a real plugin in marketplace/plugins/. It
// declares NO guardrail — the engine dispatches nothing on its behalf. Its
// hooks.json reaches PreToolUse directly, exactly as any Claude Code plugin
// does, and its script decides. That is the arrangement the command exists for.
//
// Why not a guardrail hook: by the time the engine dispatches one, the payload
// it hands over is ALREADY the parsed event — `{"event":{"kind":...}}` — so the
// parsing is done and the command has nothing to add. It was written as a
// guardrail first and measured to be inert for exactly that reason. The command
// earns its place one level up, where the payload is still the harness's own and
// the tool arguments are raw.
//
// Everything is driven through the mock, installed the way a consumer installs
// it: the plugin named in enabledPlugins, resolved from the in-repo marketplace.
// Nothing here writes a hook into the project or constructs a payload by hand.

// installed is the plugin under test, as it appears in marketplace.json.
const installed = "sloprail-secrets"

// T029_01: the headline. A plugin with no guardrail at all refuses a write,
// having learned the path from the command.
func TestT029_01_APluginWithNoGuardrailRefusesFromWhatChangesReported(t *testing.T) {
	e := New(t)
	proj := e.ProjectWith(installed)

	got := e.Run(proj, "s-029-01", "write a secret", Turns("done",
		Write("w1", "secrets.md", "token"),
	))

	if !got.Refused() {
		t.Fatalf("the plugin did not refuse, so its own hook never fired or the command "+
			"reported nothing:\n%s", got.Output)
	}
	if !strings.Contains(got.Output, "SECRETS:") {
		t.Errorf("the refusal is not this plugin's — something else blocked the write:\n%s", got.Output)
	}
	// The command's row reached the message, which is what proves the decision
	// was made from the reported change rather than from the raw arguments.
	if !strings.Contains(got.Output, "PreFileCreate secrets.md") {
		t.Errorf("the refusal does not carry the kind and path the command reported:\n%s", got.Output)
	}
	if e.Exists(proj, "secrets.md") {
		t.Errorf("the write landed despite the refusal — the work was not prevented")
	}
}

// T029_02: the negative control. Same plugin, same project, a path its selector
// does not match — permitted.
//
// Without this, T029_01 would pass against a plugin whose hook refused
// everything it was handed, including one where `sr-file changes` printed
// nothing and the script failed closed by accident.
func TestT029_02_APathTheSelectorMissesIsPermitted(t *testing.T) {
	e := New(t)
	proj := e.ProjectWith(installed)

	got := e.Run(proj, "s-029-02", "write an ordinary note", Turns("done",
		Write("w1", "notes.md", "ordinary"),
	))

	if got.Refused() {
		t.Fatalf("a path the selector does not match was refused, so the plugin is not reading "+
			"the reported rows at all:\n%s", got.Output)
	}
	if !e.Exists(proj, "notes.md") {
		t.Errorf("the permitted write did not land")
	}
}

// T029_03: the case that cannot be done without the command — a SHELL write
// whose target the engine resolved.
//
// This is the whole argument. A hook pattern-matching the raw payload sees the
// string `printf 'token' > secrets.md`; it does not know the shell would create
// secrets.md, and a regex over the argument blob matches a path merely MENTIONED
// as readily as one written. The engine parses the line and reports the file it
// resolved, and the plugin reads that.
func TestT029_03_AShellWriteIsReportedLikeAToolWrite(t *testing.T) {
	e := New(t)
	proj := e.ProjectWith(installed)

	got := e.Run(proj, "s-029-03", "write via the shell", Turns("done",
		Bash("b1", "printf 'token' > secrets.md"),
	))

	if !got.Refused() {
		t.Fatalf("a shell write was not reported, so a plugin reading the command is blind to "+
			"everything not done through a file tool:\n%s", got.Output)
	}
	if !strings.Contains(got.Output, "PreFileCreate secrets.md") {
		t.Errorf("the shell write was not classified as a create on the resolved path:\n%s", got.Output)
	}
}

// T029_04: a turn touching no file is permitted.
//
// The command prints nothing and exits 0 there, and this pins that a plugin
// built on it does not refuse by accident on the ordinary case — most of what
// happens in a session concerns files not at all, and a rule blocking those is
// switched off the same day.
func TestT029_04_ATurnTouchingNoFileIsPermitted(t *testing.T) {
	e := New(t)
	proj := e.ProjectWith(installed)

	got := e.Run(proj, "s-029-04", "look around", Turns("done",
		Bash("b1", "echo hello"),
	))

	if got.Refused() {
		t.Fatalf("a turn that touched no file was refused:\n%s", got.Output)
	}
}

// T029_05: the plugin is not installed, so nothing refuses.
//
// The control for the whole group: it proves the refusals above come from the
// INSTALLED plugin rather than from anything the harness does on its own. Same
// project shape, same write, one line of settings different.
func TestT029_05_WithoutThePluginTheSameWriteIsPermitted(t *testing.T) {
	e := New(t)
	proj := e.Project() // base plugin only

	got := e.Run(proj, "s-029-05", "write a secret", Turns("done",
		Write("w1", "secrets.md", "token"),
	))

	if got.Refused() {
		t.Fatalf("a write was refused with the plugin uninstalled, so the group's refusals are "+
			"not evidence about it:\n%s", got.Output)
	}
	if !e.Exists(proj, "secrets.md") {
		t.Errorf("the write did not land in a project with nothing guarding it")
	}
}
