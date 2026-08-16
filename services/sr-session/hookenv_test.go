package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/guardrail"
)

// resolve reads an environment slice the way exec does: last occurrence of a
// name wins. Asserting on this rather than on slice positions is what makes the
// ordering test about the behaviour instead of about the construction.
func envValue(env []string, name string) (string, bool) {
	value, found := "", false
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if ok && k == name {
			value, found = v, true
		}
	}
	return value, found
}

func TestHookEnv_SetsTheThreeTheEngineOwns(t *testing.T) {
	s := hookScope{SessionID: "sess-1", Workspace: "/w"}

	name, ok := envValue(s.env(guardrail.Declaration{Name: "my-rule"}), GuardrailEnv)
	require.True(t, ok, "the guardrail must reach the hook")
	assert.Equal(t, "my-rule", name)

	session, ok := envValue(s.env(guardrail.Declaration{Name: "my-rule"}), SessionEnv)
	require.True(t, ok)
	assert.Equal(t, "sess-1", session)

	workspace, ok := envValue(s.env(guardrail.Declaration{Name: "my-rule"}), WorkspaceEnv)
	require.True(t, ok)
	assert.Equal(t, "/w", workspace)
}

// The inherit half of the decision. A hook is an ordinary shell command, and
// without PATH `sh -c` cannot find sloprail itself — which, since a hook that
// cannot run is a refusal, would turn every state-using rule into one that
// blocks all work.
func TestHookEnv_InheritsTheParentEnvironment(t *testing.T) {
	t.Setenv("SOME_UNRELATED_VAR", "kept")

	env := hookScope{SessionID: "s", Workspace: "/w"}.env(guardrail.Declaration{Name: "r"})

	got, ok := envValue(env, "SOME_UNRELATED_VAR")
	require.True(t, ok, "the parent environment must be inherited")
	assert.Equal(t, "kept", got)

	_, hasPath := envValue(env, "PATH")
	assert.True(t, hasPath, "PATH must reach the hook or sh -c cannot find sloprail")
}

// The ordering half, and the reason the three are appended rather than
// prepended: an inherited value must not win over the engine's own answer about
// which rule is running.
func TestHookEnv_EngineValuesWinOverInherited(t *testing.T) {
	t.Setenv(GuardrailEnv, "outer-rule")
	t.Setenv(SessionEnv, "outer-session")
	t.Setenv(WorkspaceEnv, "/outer-workspace")

	env := hookScope{SessionID: "inner-session", Workspace: "/inner"}.env(guardrail.Declaration{Name: "inner-rule"})

	guardrail, _ := envValue(env, GuardrailEnv)
	assert.Equal(t, "inner-rule", guardrail,
		"an inherited guardrail name must never override the rule actually running")

	session, _ := envValue(env, SessionEnv)
	assert.Equal(t, "inner-session", session)

	workspace, _ := envValue(env, WorkspaceEnv)
	assert.Equal(t, "/inner", workspace)
}

// The ordering above is only meaningful if exec really resolves duplicates to
// the last one. This pins that assumption against the real thing rather than
// against a belief about it: if Go's behaviour ever differed, appending would
// silently stop overriding and every hook would see the outer value.
func TestHookEnv_LastOccurrenceIsWhatTheProcessSees(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh to run")
	}
	c := exec.Command("sh", "-c", "printf '%s' \"$"+GuardrailEnv+"\"")
	c.Env = append(os.Environ(), GuardrailEnv+"=first", GuardrailEnv+"=last")

	out, err := c.Output()
	require.NoError(t, err)
	assert.Equal(t, "last", string(out),
		"appending is what makes the engine's value win; if this fails, env() must prepend instead")
}

// Each hook is told its own name, so one guardrail's invocation cannot carry
// another's — the accident the variable exists to prevent.
func TestHookEnv_NamesEachGuardrailSeparately(t *testing.T) {
	s := hookScope{SessionID: "s", Workspace: "/w"}

	a, _ := envValue(s.env(guardrail.Declaration{Name: "rule-a"}), GuardrailEnv)
	b, _ := envValue(s.env(guardrail.Declaration{Name: "rule-b"}), GuardrailEnv)

	assert.Equal(t, "rule-a", a)
	assert.Equal(t, "rule-b", b)
}

// An unresolved session is passed through as empty rather than omitted, so the
// CLI reports "no session id" — a diagnosable failure — instead of falling back
// to an inherited value from some outer process and writing to another
// session's record.
func TestHookEnv_EmptySessionOverridesRatherThanInherits(t *testing.T) {
	t.Setenv(SessionEnv, "stale-outer-session")

	env := hookScope{Workspace: "/w"}.env(guardrail.Declaration{Name: "r"})

	session, ok := envValue(env, SessionEnv)
	require.True(t, ok)
	assert.Empty(t, session, "an unresolved session must not silently inherit a stale one")
}

// An unresolved workspace is NOT passed through as empty, and this is the one
// place the two variables are treated differently.
//
// An empty SR_WORKSPACE is not diagnosable the way an empty session id is:
// sessionDBPath reads it as "use the process's own directory", and a hook's
// process directory is the guardrail's own folder. Every rule would silently
// get a database of its own, keyed by where its scripts live rather than by the
// tree being guarded, and nothing anywhere would report it.
func TestHookEnv_UnresolvedWorkspaceIsNotAnEmptyValue(t *testing.T) {
	env := hookScope{SessionID: "s"}.env(guardrail.Declaration{Name: "r"})

	workspace, ok := envValue(env, WorkspaceEnv)
	require.True(t, ok, "the variable must still be set, or an outer one is inherited")
	assert.NotEmpty(t, workspace,
		"an empty workspace makes sessionDBPath fall back to the guardrail's own folder")

	// It must be a value the store refuses, not one it can turn into a path.
	_, err := sessionDBPath(workspace, "s")
	require.Error(t, err, "an unresolved workspace must fail loudly rather than resolve somewhere")
	assert.Contains(t, err.Error(), WorkspaceEnv)
}

// The sentinel also has to survive being handed to a real process. A NUL byte
// would be the tidiest impossible value and is exactly wrong here: exec refuses
// to start a process whose environment contains one, so the hook would not run
// at all — and a hook that cannot run is a refusal, which would turn an
// unresolvable workspace into a block on all work.
func TestHookEnv_UnresolvedWorkspaceStillLetsTheHookRun(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh to run")
	}
	c := exec.Command("sh", "-c", "printf 'ran'")
	c.Env = hookScope{SessionID: "s"}.env(guardrail.Declaration{Name: "r"})

	out, err := c.Output()

	require.NoError(t, err, "the hook must still start; only reaching for state may fail")
	assert.Equal(t, "ran", string(out))
}

// An outer SR_WORKSPACE must not survive into a hook whose workspace the engine
// could not resolve. Leaving the variable unset would let the stale value stand
// and be read as the tree being guarded.
func TestHookEnv_UnresolvedWorkspaceOverridesAnInheritedOne(t *testing.T) {
	t.Setenv(WorkspaceEnv, "/stale-outer-workspace")

	env := hookScope{SessionID: "s"}.env(guardrail.Declaration{Name: "r"})

	workspace, _ := envValue(env, WorkspaceEnv)
	assert.NotEqual(t, "/stale-outer-workspace", workspace,
		"a workspace the engine could not resolve must not fall back to an inherited one")
}

// The record's path reaches the hook, which is what a rule reading the
// trajectory has to have. SR_SESSION_ID cannot stand in for it: that is the
// stable id, the uuid of the conversation's origin record, not the transcript's
// filename.
func TestHookEnv_CarriesTheRecordsPath(t *testing.T) {
	s := hookScope{SessionID: "sess-1", Workspace: "/w", Transcript: "/rec/abc.jsonl"}

	got, ok := envValue(s.env(guardrail.Declaration{Name: "r"}), TranscriptEnv)
	require.True(t, ok, "a rule that reads the trajectory needs the record itself")
	assert.Equal(t, "/rec/abc.jsonl", got)

	session, _ := envValue(s.env(guardrail.Declaration{Name: "r"}), SessionEnv)
	assert.NotEqual(t, got, session,
		"the stable session id is not the transcript's filename; they must not be conflated")
}

// No record means the variable is UNSET, not empty — the opposite of the
// workspace's answer, and deliberately so.
//
// Nothing reads this variable except a rule that chose to, and `test -n
// "$SR_TRANSCRIPT"` is the documented check. Set-but-empty reads as a path in
// every shell idiom that does not test for emptiness first, so a rule would hand
// "" to `sr-session query` and get an error about the file rather than
// about the variable.
func TestHookEnv_NoRecordLeavesTheVariableUnset(t *testing.T) {
	env := hookScope{SessionID: "s", Workspace: "/w"}.env(guardrail.Declaration{Name: "r"})

	_, ok := envValue(env, TranscriptEnv)
	assert.False(t, ok,
		"with no record the variable must be absent, so a rule's -n check reports it")
}

// The rule's own folder reaches the hook. The cwd is this same directory, so a
// sibling is reachable relatively — this exists for the hooks that hand an
// ABSOLUTE path to something else, where a relative one would resolve against
// the callee's directory instead.
func TestHookEnv_CarriesTheGuardrailsOwnDirectory(t *testing.T) {
	s := hookScope{SessionID: "s", Workspace: "/w"}

	dir, ok := envValue(s.env(guardrail.Declaration{Name: "r", Dir: "/plug/guardrails/r"}), GuardrailDirEnv)
	require.True(t, ok, "a hook must be able to find its own folder without parsing the payload first")
	assert.Equal(t, "/plug/guardrails/r", dir)
}

// A SHIPPED rule's assets travel with the plugin, so the hook is told where the
// plugin landed. Without this a shipped rule can only name a path in the
// consumer's tree — a file the consumer never had.
func TestHookEnv_CarriesThePluginRootForAShippedRule(t *testing.T) {
	s := hookScope{SessionID: "s", Workspace: "/w"}
	d := guardrail.Declaration{
		Name:   "task-evidence-resolves",
		Dir:    "/install/sloprail-tasks/guardrails/task-evidence-resolves",
		Origin: guardrail.Origin{Plugin: "sloprail-tasks", Root: "/install/sloprail-tasks"},
	}

	root, ok := envValue(s.env(d), PluginRootEnv)
	require.True(t, ok, "a shipped rule must be told where its plugin was installed")
	assert.Equal(t, "/install/sloprail-tasks", root)
}

// A PROJECT's rule has no plugin, and the variable is UNSET rather than empty.
//
// Set-but-empty is what breaks `${SR_PLUGIN_ROOT:-$SR_WORKSPACE/.sloprail}`: the
// parameter would be SET, so the default never applies, and a project rule using
// that idiom would resolve its schema against the filesystem root. Absent, the
// fallback is selected — which is what lets one rule be developed in a project
// and later shipped without being rewritten.
func TestHookEnv_ProjectRuleHasNoPluginRoot(t *testing.T) {
	s := hookScope{SessionID: "s", Workspace: "/w"}

	env := s.env(guardrail.Declaration{Name: "r", Dir: "/w/.sloprail/guardrails/r"})

	_, ok := envValue(env, PluginRootEnv)
	assert.False(t, ok,
		"a project rule must leave the variable absent so ${SR_PLUGIN_ROOT:-...} selects the fallback")
}

// The fallback idiom itself, exercised through a real shell rather than asserted
// about. This is the line a shipped-and-portable rule actually writes, and the
// test is that it selects the plugin's copy when installed and the project's
// when not — which is the whole point of the variable being unset in one case.
func TestHookEnv_FallbackIdiomSelectsEachLayout(t *testing.T) {
	script := `printf '%s' "${SR_PLUGIN_ROOT:-$SR_WORKSPACE/.sloprail}/schemas/task.cue"`

	shipped := exec.Command("sh", "-c", script)
	shipped.Env = hookScope{SessionID: "s", Workspace: "/w"}.env(guardrail.Declaration{
		Name:   "r",
		Origin: guardrail.Origin{Plugin: "sloprail-tasks", Root: "/install/sloprail-tasks"},
	})
	out, err := shipped.Output()
	require.NoError(t, err)
	assert.Equal(t, "/install/sloprail-tasks/schemas/task.cue", string(out),
		"an installed rule must read the schema that shipped beside it")

	own := exec.Command("sh", "-c", script)
	own.Env = hookScope{SessionID: "s", Workspace: "/w"}.env(guardrail.Declaration{Name: "r"})
	out, err = own.Output()
	require.NoError(t, err)
	assert.Equal(t, "/w/.sloprail/schemas/task.cue", string(out),
		"the same line in a project's own rule must read the project's schema")
}

// The engine's answer must win over a stale value exported by an outer process,
// exactly as it does for the other four. Only the plugin case can be defended
// this way — see pluginRootEnv for why the project case deliberately cannot be,
// and what that costs.
func TestHookEnv_PluginRootOverridesAnInheritedOne(t *testing.T) {
	t.Setenv(PluginRootEnv, "/some/other/plugin")

	env := hookScope{SessionID: "s", Workspace: "/w"}.env(guardrail.Declaration{
		Name:   "r",
		Origin: guardrail.Origin{Plugin: "sloprail-tasks", Root: "/install/sloprail-tasks"},
	})

	root, ok := envValue(env, PluginRootEnv)
	require.True(t, ok)
	assert.Equal(t, "/install/sloprail-tasks", root,
		"the engine's answer about which plugin is running must win over an inherited one")
}
