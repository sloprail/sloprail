package e2e

import (
	"strings"
	"testing"
)

// changes_command: a plugin that owns its own PreToolUse hook and calls
// low-level commands directly — `sr-file changes` for what a turn is about to
// do, `sr-session query` for what the agent has already done this session —
// rather than a guardrail the engine dispatches.
//
// The subject is sloprail-require-skill, a real plugin in marketplace/plugins/.
// It declares NO guardrail. Its hooks.json reaches PreToolUse directly, exactly
// as any Claude Code plugin does, and its script decides, driven by the
// consumer's own .sloprail/require-skill/config.yaml.
//
// Everything is driven through the mock, installed the way a consumer installs
// it: the plugin named in enabledPlugins, resolved from the in-repo
// marketplace. Nothing here writes a hook into the project or constructs a
// payload by hand.

const installed = "sloprail-require-skill"

const config = `require:
  - match: "memories/topics/**"
    skills: [document-topic]
`

// T029_01: the headline. No skill loaded, a write under the configured prefix —
// refused, and the message names the missing skill.
func TestT029_01_NoSkillLoadedRefusesTheWrite(t *testing.T) {
	e := New(t)
	proj := e.ProjectWith(installed)
	e.WriteFile(proj, ".sloprail/require-skill/config.yaml", config)

	got := e.Run(proj, "s-029-01", "write a topic with no skill", Turns("done",
		Write("w1", "memories/topics/x/TOPIC.md", "body"),
	))

	if !got.Refused() {
		t.Fatalf("no skill was loaded and the write was not refused:\n%s", got.Output)
	}
	if !strings.Contains(got.Output, "document-topic") {
		t.Errorf("the refusal does not name the missing skill:\n%s", got.Output)
	}
	if e.Exists(proj, "memories/topics/x/TOPIC.md") {
		t.Errorf("the write landed despite the refusal")
	}
}

// T029_02: the skill loaded earlier in the SAME turn's session satisfies the
// precondition, and the write proceeds.
func TestT029_02_TheSkillLoadedEarlierPermitsTheWrite(t *testing.T) {
	e := New(t)
	proj := e.ProjectWith(installed)
	e.WriteFile(proj, ".sloprail/require-skill/config.yaml", config)

	got := e.Run(proj, "s-029-02", "load the skill, then write", Turns("done",
		Skill("k1", "document-topic"),
		Write("w1", "memories/topics/x/TOPIC.md", "body"),
	))

	if got.Refused() {
		t.Fatalf("the skill was loaded and the write was still refused:\n%s", got.Output)
	}
	if !e.Exists(proj, "memories/topics/x/TOPIC.md") {
		t.Errorf("the permitted write did not land")
	}
}

// T029_03: SAYING the skill was read does not satisfy the check — only a real
// Skill tool_use does. This is the case the rule exists to catch: an agent
// stating "I have read the document-topic guidance" in its own text, with no
// tool_use naming it.
func TestT029_03_ClaimingInProseDoesNotSatisfyTheCheck(t *testing.T) {
	e := New(t)
	proj := e.ProjectWith(installed)
	e.WriteFile(proj, ".sloprail/require-skill/config.yaml", config)

	got := e.Run(proj, "s-029-03", "claim to have read it, then write", Turns(
		"I have read the document-topic guidance",
		Write("w1", "memories/topics/x/TOPIC.md", "body"),
	))

	if !got.Refused() {
		t.Fatalf("a prose claim satisfied the check — the rule is trusting what the agent "+
			"SAYS instead of the session's own record:\n%s", got.Output)
	}
}

// T029_04: the negative control. A path outside every configured prefix is
// permitted regardless of what skills were loaded — proves the refusals above
// are about matching the config, not about the plugin refusing everything.
func TestT029_04_APathOutsideTheConfigIsPermitted(t *testing.T) {
	e := New(t)
	proj := e.ProjectWith(installed)
	e.WriteFile(proj, ".sloprail/require-skill/config.yaml", config)

	got := e.Run(proj, "s-029-04", "write an unrelated note", Turns("done",
		Write("w1", "notes.md", "ordinary"),
	))

	if got.Refused() {
		t.Fatalf("a path outside every configured prefix was refused:\n%s", got.Output)
	}
	if !e.Exists(proj, "notes.md") {
		t.Errorf("the permitted write did not land")
	}
}

// T029_05: without the plugin installed, the same write is permitted — the
// group's control, proving the refusals above come from the installed plugin.
func TestT029_05_WithoutThePluginTheSameWriteIsPermitted(t *testing.T) {
	e := New(t)
	proj := e.Project() // base plugin only

	got := e.Run(proj, "s-029-05", "write with no plugin installed", Turns("done",
		Write("w1", "memories/topics/x/TOPIC.md", "body"),
	))

	if got.Refused() {
		t.Fatalf("a write was refused with the plugin uninstalled, so the group's refusals "+
			"are not evidence about it:\n%s", got.Output)
	}
	if !e.Exists(proj, "memories/topics/x/TOPIC.md") {
		t.Errorf("the write did not land in a project with nothing guarding it")
	}
}

// T029_06: with no config file at all, the plugin is silent — installed but
// unconfigured is the ordinary state for a consumer who has not opted into any
// prefix yet, and must not read as "refuse everything".
func TestT029_06_NoConfigFileIsSilent(t *testing.T) {
	e := New(t)
	proj := e.ProjectWith(installed) // no config written

	got := e.Run(proj, "s-029-06", "write with no config", Turns("done",
		Write("w1", "memories/topics/x/TOPIC.md", "body"),
	))

	if got.Refused() {
		t.Fatalf("a write was refused with no config file present:\n%s", got.Output)
	}
}

// T029_07: a shell write under the configured prefix is caught the same as a
// tool write — the case that cannot be done without `sr-file changes`. A hook
// pattern-matching the raw payload sees the command's TEXT; it does not know
// the shell would create the file, and this is exactly the gap the earlier
// sloprail-secrets example measured on `sr-file changes` directly.
func TestT029_07_AShellWriteUnderTheConfiguredPrefixIsCaught(t *testing.T) {
	e := New(t)
	proj := e.ProjectWith(installed)
	e.WriteFile(proj, ".sloprail/require-skill/config.yaml", config)

	got := e.Run(proj, "s-029-07", "write via the shell, no skill loaded", Turns("done",
		Bash("b1", "printf 'body' > memories/topics/x/TOPIC.md"),
	))

	if !got.Refused() {
		t.Fatalf("a shell write under the configured prefix was not caught:\n%s", got.Output)
	}
}
