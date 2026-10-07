package declaration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/module"
	"github.com/sloprail/sloprail/internal/module/modules"
)

// The loader is exercised here against declarations written into a temporary
// `.sloprail` root, not against real fixture directories on disk — one test can
// then state exactly the declaration it is about, valid or malformed, next to the
// assertion, and a table row is one rule and its expected verdict. The one test
// that DOES read the real examples/ tree is in examples_test.go, and its job is
// the opposite: to prove the shipped examples reconcile with this loader.

// testRegistry builds the same module vocabulary the shipped build has, so a
// trigger's `match` is compiled against the REAL kind field declarations — the
// same `event.path`, `event.invocations`, `event.tags` a shipped gate/context
// trigger sees. It feeds modules.All (the one shipped list) to NewRegistryForTest,
// the sanctioned way for a test to hold a vocabulary without being the one place
// allowed to build the shipped registry. Building from the real list rather than a
// hand-picked subset is what makes these tests prove the loader against the engine
// the examples will actually run under, not a stand-in.
func testRegistry(t *testing.T) *module.Registry {
	t.Helper()
	reg, err := module.NewRegistryForTest(modules.All()...)
	require.NoError(t, err)
	return reg
}

// write puts one declaration file at rel under a fresh `.sloprail` root and
// returns the root. rel is nature-relative (e.g. "gate/foo/gate.yaml"), so a test
// reads like the on-disk layout it is building.
func writeDecl(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		path := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		writeDeclFile(t, path, content)
	}
	return root
}

// loadOK loads and asserts nothing was invalid, returning the Loaded for further
// assertions. The common shape of a "this declaration is valid" test.
func loadOK(t *testing.T, files map[string]string) Loaded {
	t.Helper()
	root := writeDecl(t, files)
	loaded, err := New(root).Load(testRegistry(t))
	require.NoError(t, err)
	require.Empty(t, loaded.Invalid, "expected every declaration to load; invalid: %v", invalidReasons(loaded))
	return loaded
}

// loadOneInvalid loads, asserts exactly one declaration was invalid, and returns
// it. The common shape of a "this declaration is refused" test.
func loadOneInvalid(t *testing.T, files map[string]string) Invalid {
	t.Helper()
	root := writeDecl(t, files)
	loaded, err := New(root).Load(testRegistry(t))
	require.NoError(t, err)
	require.Len(t, loaded.Invalid, 1, "expected exactly one invalid declaration")
	return loaded.Invalid[0]
}

func invalidReasons(l Loaded) []string {
	out := make([]string, 0, len(l.Invalid))
	for _, iv := range l.Invalid {
		out = append(out, iv.Qualified()+": "+iv.Reason)
	}
	return out
}

// hasKind reports whether an Invalid carries a problem of the given sentinel
// kind, so a refusal test asserts on the CLASS of fault rather than its wording.
func hasKind(iv Invalid, kind error) bool {
	for _, p := range iv.Problems {
		if p.Is(kind) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Empty / absent
// ---------------------------------------------------------------------------

// A project with no `.sloprail` directory has no declarations — the ordinary
// state of a project that has not adopted any, not an error.
// sr:proves loading/rule-discovery
func TestLoad_NoDotDir(t *testing.T) {
	root := filepath.Join(t.TempDir(), "does-not-exist")
	loaded, err := New(root).Load(testRegistry(t))
	require.NoError(t, err)
	assert.Empty(t, loaded.FileGuards)
	assert.Empty(t, loaded.Gates)
	assert.Empty(t, loaded.Contexts)
	assert.Empty(t, loaded.Structures)
	assert.Empty(t, loaded.Invalid)
}

// An empty `.sloprail` directory is the same: nothing adopted, nothing wrong.
// sr:proves loading/rule-discovery
func TestLoad_EmptyDotDir(t *testing.T) {
	loaded := loadOK(t, map[string]string{})
	assert.Empty(t, loaded.FileGuards)
	assert.Empty(t, loaded.Gates)
}

// ---------------------------------------------------------------------------
// File-guard: valid loads
// ---------------------------------------------------------------------------

func TestLoad_FileGuard_Valid(t *testing.T) {
	loaded := loadOK(t, map[string]string{
		"file-guard/pinned/file-guard.yaml": `
match: any(markers, .kind == "invariant")
checks:
  - script: ./check.sh
  - judge: ./judge.md.j2
`,
	})
	require.Len(t, loaded.FileGuards, 1)
	g := loaded.FileGuards[0]
	assert.Equal(t, "pinned", g.Name)
	assert.False(t, g.HadPreventiveKey)
	assert.Len(t, g.Checks, 2)
	assert.True(t, g.Checks[0].isScript())
	assert.True(t, g.Checks[1].isJudge())
	// The folder is recorded so scripts resolve relative to it.
	assert.Equal(t, filepath.Join(g.Dir, "check.sh"), filepath.Join(g.Dir, "check.sh"))
	assert.True(t, filepath.IsAbs(g.Dir) || g.Dir != "")
}

// A bare glob is the shorthand half of the file-match union and must load.
func TestLoad_FileGuard_GlobMatch(t *testing.T) {
	loaded := loadOK(t, map[string]string{
		"file-guard/mdfiles/file-guard.yaml": `
match: "**/*.md"
checks:
  - script: ./check.sh
`,
	})
	require.Len(t, loaded.FileGuards, 1)
	assert.Equal(t, "**/*.md", loaded.FileGuards[0].Match)
}

// ---------------------------------------------------------------------------
// File-guard: refusals
// ---------------------------------------------------------------------------

// sr:proves loading/rule-must-decide-something
func TestLoad_FileGuard_MissingMatch(t *testing.T) {
	iv := loadOneInvalid(t, map[string]string{
		"file-guard/nomarch/file-guard.yaml": `
checks:
  - script: ./check.sh
`,
	})
	assert.Equal(t, NatureFileGuard, iv.Nature)
	assert.True(t, hasKind(iv, ErrMissingField), "a file-guard with no match is refused: %v", iv.Reason)
}

// The singular `marker.kind` the older example vocabulary used is refused — the
// file scope exposes `markers` (list), and `marker` is out of scope.
// sr:proves loading/trigger-match-compiles-at-load
func TestLoad_FileGuard_SingularMarkerRefused(t *testing.T) {
	iv := loadOneInvalid(t, map[string]string{
		"file-guard/bad/file-guard.yaml": `
match: marker.kind == "endpoint"
checks:
  - script: ./check.sh
`,
	})
	assert.True(t, hasKind(iv, ErrBadMatch), "singular marker.kind is not in the file scope: %v", iv.Reason)
	assert.Contains(t, iv.Reason, "marker")

	// The refusal must also NAME the fields the file scope DOES carry, or an
	// author who mistyped one is left guessing at the spelling. The offending
	// name is in front of them; the available names have to be too. (The old
	// GUARDRAIL.md validator proved this via guardrail.Validate's field list; the
	// new file-guard validator carries the same courtesy in its match message.)
	for _, field := range []string{"path", "markers", "trailers"} {
		assert.Containsf(t, iv.Reason, field,
			"the refusal should name %q as an available field on the file scope", field)
	}
}

// The bare `refactoring.active` the older example vocabulary used does NOT read
// as a context reference — a context is reached as `context[<name>]`. Because it
// carries no whitespace or quote, CompileFileMatch's union discriminator reads it
// as a bare GLOB, so it silently compiles to a path match on the literal path
// `refactoring.active` and matches nothing real. This is precisely WHY the example
// reconciliation rewrites it to `context["refactoring"].active`: the old form is
// not refused, it is worse — it loads and never fires. The test pins that
// surprising behaviour so a future change to the discriminator that DID start
// refusing it is noticed here.
func TestLoad_FileGuard_BareDottedTokenIsAGlobNotAContext(t *testing.T) {
	loaded := loadOK(t, map[string]string{
		"file-guard/bad/file-guard.yaml": `
match: refactoring.active
checks:
  - script: ./check.sh
`,
	})
	require.Len(t, loaded.FileGuards, 1)
	assert.Equal(t, "refactoring.active", loaded.FileGuards[0].Match,
		"a bare dotted token is stored verbatim and compiled as a glob, not a context read")
}

// The quoted/spaced form that genuinely reaches for an out-of-scope bare variable
// IS refused — e.g. `refactoring.active == true`, which carries whitespace and so
// routes to the expression parser, where `refactoring` is not in the file scope.
// sr:proves loading/trigger-match-compiles-at-load
func TestLoad_FileGuard_OutOfScopeVariableInExpressionRefused(t *testing.T) {
	iv := loadOneInvalid(t, map[string]string{
		"file-guard/bad/file-guard.yaml": `
match: refactoring.active == true
checks:
  - script: ./check.sh
`,
	})
	assert.True(t, hasKind(iv, ErrBadMatch), "an expression reading a bare out-of-scope variable is refused: %v", iv.Reason)
}

// A file-guard whose whole enforcement is a `require:` precondition is meaningful
// without any checks — the engine evaluates `require` before any check and refuses
// the write when it is unmet — so an absent `checks` LOADS as long as `require` is
// present, the same at-least-one rule a gate carries.
func TestLoad_FileGuard_PureRequire(t *testing.T) {
	loaded := loadOK(t, map[string]string{
		"file-guard/require-topic/file-guard.yaml": `
match: "memories/topics/**/*.md"
require:
  - citation: {source_types: [user]}
`,
	})
	require.Len(t, loaded.FileGuards, 1)
	g := loaded.FileGuards[0]
	assert.Empty(t, g.Checks)
	assert.Len(t, g.Require, 1)
	assert.NotNil(t, g.Require[0].Citation)
}

// A file-guard cannot see the session, so `require: skill` / `require: context`
// is refused at load, and the message carries the gate to write instead.
// sr:proves loading/retired-file-guard-keys-are-refused
func TestLoad_FileGuard_SessionRequireIsRefusedWithTheGate(t *testing.T) {
	iv := loadOneInvalid(t, map[string]string{
		"file-guard/require-topic/file-guard.yaml": `
match: "memories/topics/**/*.md"
require:
  - skill: document-topic
`,
	})
	assert.True(t, hasKind(iv, ErrRetiredKey), "a skill require on a file-guard is refused: %v", iv.Reason)
	assert.Contains(t, iv.Reason, "gate/<name>/gate.yaml")
	assert.Contains(t, iv.Reason, "skill: document-topic")
}

// A file-guard's match has no `context`: an identifier use is refused (with the
// gate advice), while the word inside a string literal still loads.
// sr:proves loading/retired-file-guard-keys-are-refused
func TestLoad_FileGuard_MatchReadingContextIsRefused(t *testing.T) {
	for _, m := range []string{`context["x"].active`, `any(markers, context["x"].active)`, `path contains "a" and context["x"].active`} {
		iv := loadOneInvalid(t, map[string]string{
			"file-guard/ctx/file-guard.yaml": "match: '" + m + "'\nchecks:\n  - script: ./c.sh\n",
		})
		assert.True(t, hasKind(iv, ErrRetiredKey), "%s: %v", m, iv.Reason)
		assert.Contains(t, iv.Reason, "move the condition on the context to a gate", m)
	}
}

func TestLoad_FileGuard_ContextInAStringLiteralLoads(t *testing.T) {
	root := writeDecl(t, map[string]string{
		"file-guard/lit/file-guard.yaml": "match: 'path contains \".sloprail/context/\"'\nchecks:\n  - script: ./c.sh\n",
	})
	loaded, err := New(root).Load(testRegistry(t))
	require.NoError(t, err)
	assert.Empty(t, loaded.Invalid, invalidReasons(loaded))
	assert.Len(t, loaded.FileGuards, 1)
}

// A file-guard with neither require nor checks would select a file and decide
// nothing — refused, the same at-least-one rule the gate has (ErrAtLeastOne, not a
// per-field missing-field fault).
// sr:proves loading/rule-must-decide-something
func TestLoad_FileGuard_NeitherRequireNorChecks(t *testing.T) {
	iv := loadOneInvalid(t, map[string]string{
		"file-guard/nochecks/file-guard.yaml": `
match: "**/*.md"
`,
	})
	assert.True(t, hasKind(iv, ErrAtLeastOne), "a file-guard with neither require nor checks is refused: %v", iv.Reason)
}

// ---------------------------------------------------------------------------
// Check: exactly-one-of script/judge, prepare placement
// ---------------------------------------------------------------------------

func TestLoad_Check_BothScriptAndJudge(t *testing.T) {
	iv := loadOneInvalid(t, map[string]string{
		"file-guard/both/file-guard.yaml": `
match: "**/*.md"
checks:
  - script: ./s.sh
    judge: ./j.md.j2
`,
	})
	assert.True(t, hasKind(iv, ErrExactlyOne), "a check setting both script and judge is refused: %v", iv.Reason)
	assert.Contains(t, iv.Reason, "both")
}

func TestLoad_Check_NeitherScriptNorJudge(t *testing.T) {
	iv := loadOneInvalid(t, map[string]string{
		"file-guard/neither/file-guard.yaml": `
match: "**/*.md"
checks:
  - prepare: ./p.sh
`,
	})
	assert.True(t, hasKind(iv, ErrExactlyOne), "a check setting neither script nor judge is refused: %v", iv.Reason)
	assert.Contains(t, iv.Reason, "neither")
}

// A prepare also loads on a script check: it builds context for the script's payload
// (additionalContext) and is never a refusal. It only builds context, so the check
// itself still has to be a script or a judge.
func TestLoad_Check_PrepareOnScriptLoads(t *testing.T) {
	loaded := loadOK(t, map[string]string{
		"file-guard/prep/file-guard.yaml": `
match: "**/*.md"
checks:
  - script: ./s.sh
    prepare: ./p.sh
`,
		"file-guard/prep/s.sh": "#!/bin/sh\n",
		"file-guard/prep/p.sh": "#!/bin/sh\n",
	})
	require.Len(t, loaded.FileGuards, 1)
	c := loaded.FileGuards[0].Checks[0]
	assert.True(t, c.isScript())
	assert.Equal(t, "./p.sh", c.Prepare)
	assert.Empty(t, loaded.Invalid)
}

// A prepare with neither a script nor a judge is still the exactly-one-of refusal.
func TestLoad_Check_PrepareAloneIsRefused(t *testing.T) {
	iv := loadOneInvalid(t, map[string]string{
		"file-guard/lone/file-guard.yaml": `
match: "**/*.md"
checks:
  - prepare: ./p.sh
`,
	})
	assert.True(t, hasKind(iv, ErrExactlyOne), "%v", iv.Reason)
}

// A prepare ALONGSIDE a judge is the sanctioned shape and loads.
func TestLoad_Check_PrepareWithJudge(t *testing.T) {
	loaded := loadOK(t, map[string]string{
		"file-guard/prep/file-guard.yaml": `
match: "**/*.md"
checks:
  - prepare: ./p.sh
    judge: ./j.md.j2
`,
	})
	require.Len(t, loaded.FileGuards, 1)
	c := loaded.FileGuards[0].Checks[0]
	assert.True(t, c.isJudge())
	assert.True(t, c.Prepare != "")
}

// model/timeout are judge-only. On a script-only check each can only be a
// mistake and is refused, so the author learns
// the field does nothing rather than having it silently ignored.
func TestLoad_Check_StrayModelOnScript(t *testing.T) {
	iv := loadOneInvalid(t, map[string]string{
		"file-guard/straymodel/file-guard.yaml": `
match: "**/*.md"
checks:
  - script: ./s.sh
    model: size-md
`,
	})
	assert.True(t, hasKind(iv, ErrStrayModel), "model on a script-only check is refused: %v", iv.Reason)
}

func TestLoad_Check_StrayTimeoutOnScript(t *testing.T) {
	iv := loadOneInvalid(t, map[string]string{
		"file-guard/straytimeout/file-guard.yaml": `
match: "**/*.md"
checks:
  - script: ./s.sh
    timeout: 45s
`,
	})
	assert.True(t, hasKind(iv, ErrStrayModel), "timeout on a script-only check is refused: %v", iv.Reason)
}

// A judge carrying a well-formed model and timeout is the sanctioned shape and
// loads, with the values preserved on the parsed Check.
func TestLoad_Check_JudgeWithModelAndTimeout(t *testing.T) {
	loaded := loadOK(t, map[string]string{
		"file-guard/judgecfg/file-guard.yaml": `
match: "**/*.md"
checks:
  - judge: ./j.md.j2
    model: size-xl
    timeout: 1m30s
`,
	})
	require.Len(t, loaded.FileGuards, 1)
	c := loaded.FileGuards[0].Checks[0]
	assert.True(t, c.isJudge())
	assert.Equal(t, "size-xl", c.Model)
	assert.Equal(t, "1m30s", c.Timeout)
}

// A comma-separated modelset (a preference list, sr-agent's own --model format)
// loads on a judge — the loader validates the shape without consulting a
// catalogue, exactly as sr-agent classifies entries lexically.
func TestLoad_Check_JudgeWithModelSetList(t *testing.T) {
	loaded := loadOK(t, map[string]string{
		"file-guard/modelset/file-guard.yaml": `
match: "**/*.md"
checks:
  - judge: ./j.md.j2
    model: claude-opus-5,size-md
`,
	})
	require.Len(t, loaded.FileGuards, 1)
	assert.Equal(t, "claude-opus-5,size-md", loaded.FileGuards[0].Checks[0].Model)
}

// A malformed modelset — a stray/trailing comma leaving an empty entry — is
// refused at load, mirroring what sr-agent's own --model parsing refuses, so a
// set that would fail at the judge is caught here instead.
func TestLoad_Check_BadModelSet(t *testing.T) {
	iv := loadOneInvalid(t, map[string]string{
		"file-guard/badmodel/file-guard.yaml": `
match: "**/*.md"
checks:
  - judge: ./j.md.j2
    model: "size-md,"
`,
	})
	assert.True(t, hasKind(iv, ErrBadModel), "a modelset with an empty entry is refused: %v", iv.Reason)
}

// A timeout that does not parse as a Go duration is refused at load.
func TestLoad_Check_BadTimeoutFormat(t *testing.T) {
	iv := loadOneInvalid(t, map[string]string{
		"file-guard/badtimeout/file-guard.yaml": `
match: "**/*.md"
checks:
  - judge: ./j.md.j2
    timeout: "half a minute"
`,
	})
	assert.True(t, hasKind(iv, ErrBadTimeout), "a non-duration timeout is refused: %v", iv.Reason)
}

// A non-positive timeout is refused — a timeout that never fires is not a
// timeout.
func TestLoad_Check_NonPositiveTimeout(t *testing.T) {
	iv := loadOneInvalid(t, map[string]string{
		"file-guard/zerotimeout/file-guard.yaml": `
match: "**/*.md"
checks:
  - judge: ./j.md.j2
    timeout: 0s
`,
	})
	assert.True(t, hasKind(iv, ErrBadTimeout), "a zero timeout is refused: %v", iv.Reason)
}

// allowed_tools on a SCRIPT-only check is a load error, mirroring the stray
// prepare/model rule — the field grants tools to a judge's agent, and a script
// has no agent to grant them to.
func TestLoad_Check_StrayAllowedToolsOnScript(t *testing.T) {
	iv := loadOneInvalid(t, map[string]string{
		"file-guard/straytools/file-guard.yaml": `
match: "**/*.md"
checks:
  - script: ./s.sh
    allowed_tools: [Read]
`,
	})
	assert.True(t, hasKind(iv, ErrStrayAllowedTools), "allowed_tools on a script-only check is refused: %v", iv.Reason)
}

// A judge carrying allowed_tools is the sanctioned shape and loads, with the list
// preserved on the parsed Check.
func TestLoad_Check_JudgeWithAllowedTools(t *testing.T) {
	loaded := loadOK(t, map[string]string{
		"file-guard/judgetools/file-guard.yaml": `
match: "**/*.md"
checks:
  - judge: ./j.md.j2
    allowed_tools: [Read, WebFetch]
`,
	})
	require.Len(t, loaded.FileGuards, 1)
	c := loaded.FileGuards[0].Checks[0]
	assert.True(t, c.isJudge())
	assert.Equal(t, []string{"Read", "WebFetch"}, c.AllowedTools)
}

// An allowed_tools list carrying an empty entry is refused at load — a blank tool
// name grants nothing, mirroring the empty-modelset-entry refusal.
func TestLoad_Check_BadAllowedTools(t *testing.T) {
	iv := loadOneInvalid(t, map[string]string{
		"file-guard/badtools/file-guard.yaml": `
match: "**/*.md"
checks:
  - judge: ./j.md.j2
    allowed_tools: ["Read", ""]
`,
	})
	assert.True(t, hasKind(iv, ErrBadAllowedTools), "an allowed_tools list with an empty entry is refused: %v", iv.Reason)
}

// A scoped rule is ONE entry, spaces and all, and loads as written.
func TestLoad_Check_ScopedToolRulesLoadWhole(t *testing.T) {
	loaded := loadOK(t, map[string]string{
		"file-guard/scoped/file-guard.yaml": `
match: "**/*.md"
checks:
  - judge: ./j.md.j2
    allowed_tools: ["Bash(git show:*)", "WebFetch(domain:code.claude.com)", mcp__srv__tool, mcp__claude-in-chrome__navigate, mcp__my-server, "mcp__srv__*"]
    disallowed_tools: ["Bash(curl * -o *)", "Bash(curl * -d @*)"]
`,
	})
	c := loaded.FileGuards[0].Checks[0]
	assert.Equal(t, []string{"Bash(git show:*)", "WebFetch(domain:code.claude.com)", "mcp__srv__tool",
		"mcp__claude-in-chrome__navigate", "mcp__my-server", "mcp__srv__*"}, c.AllowedTools)
	assert.Equal(t, []string{"Bash(curl * -o *)", "Bash(curl * -d @*)"}, c.DisallowedTools)
}

// disallowed_tools on a script-only check is a load error: it denies tools to a
// judge's agent, and a script has none.
func TestLoad_Check_StrayDisallowedToolsOnScript(t *testing.T) {
	iv := loadOneInvalid(t, map[string]string{
		"file-guard/straydeny/file-guard.yaml": `
match: "**/*.md"
checks:
  - script: ./s.sh
    disallowed_tools: ["Bash(curl * -o *)"]
`,
	})
	assert.True(t, hasKind(iv, ErrStrayDisallowedTools), "disallowed_tools on a script-only check is refused: %v", iv.Reason)
}

// Every malformed shape is refused at load, naming the entry — for a deny,
// silently denying nothing would be worse than refusing to load.
func TestLoad_Check_MalformedToolRulesAreRefused(t *testing.T) {
	for name, entry := range map[string]string{
		"empty":           `""`,
		"two in one item": `"Read WebFetch"`,
		"never closes":    `"Bash(git show:*"`,
		"trailing text":   `"Bash(curl:*) Read"`,
		"no tool name":    `"(curl:*)"`,
		"stray close":     `"Bash)"`,
	} {
		for _, key := range []string{"allowed_tools", "disallowed_tools"} {
			iv := loadOneInvalid(t, map[string]string{
				"file-guard/bad/file-guard.yaml": "match: \"**/*.md\"\nchecks:\n  - judge: ./j.md.j2\n    " + key + ": [" + entry + "]\n",
			})
			want := ErrBadAllowedTools
			if key == "disallowed_tools" {
				want = ErrBadDisallowedTools
			}
			assert.True(t, hasKind(iv, want), "%s %s must be refused: %v", key, name, iv.Reason)
			assert.Contains(t, iv.Reason, "entry 1", "the refusal names the entry (%s %s)", key, name)
		}
	}
}

// A deny on Write or Edit would stop every judge of the rule writing its
// verdict, so it is refused at load.
func TestLoad_Check_DenyingTheVerdictWriteIsRefused(t *testing.T) {
	for _, entry := range []string{"Write", "Edit", `"Edit(//tmp/**)"`} {
		iv := loadOneInvalid(t, map[string]string{
			"file-guard/denywrite/file-guard.yaml": "match: \"**/*.md\"\nchecks:\n  - judge: ./j.md.j2\n    disallowed_tools: [" + entry + "]\n",
		})
		assert.True(t, hasKind(iv, ErrBadDisallowedTools), "%s must be refused: %v", entry, iv.Reason)
		assert.Contains(t, iv.Reason, "verdict", entry)
	}
}

// ---------------------------------------------------------------------------
// Gate: valid + at-least-one + on-kinds
// ---------------------------------------------------------------------------

func TestLoad_Gate_PureRequire(t *testing.T) {
	loaded := loadOK(t, map[string]string{
		"gate/skillgate/gate.yaml": `
on:
  - event: PreFileWrite
    match: event.path startsWith "memories/topics/"
require:
  - skill: document-topic
`,
	})
	require.Len(t, loaded.Gates, 1)
	assert.Len(t, loaded.Gates[0].Require, 1)
	assert.Equal(t, "document-topic", loaded.Gates[0].Require[0].Skill)
}

func TestLoad_Gate_PureChecks(t *testing.T) {
	loaded := loadOK(t, map[string]string{
		"gate/proofgate/gate.yaml": `
on:
  - event: Stop
checks:
  - script: ./verify.sh
`,
	})
	require.Len(t, loaded.Gates, 1)
	assert.Len(t, loaded.Gates[0].Checks, 1)
}

// A gate with neither require nor checks would wake and do nothing — refused.
// sr:proves loading/rule-must-decide-something
func TestLoad_Gate_NeitherRequireNorChecks(t *testing.T) {
	iv := loadOneInvalid(t, map[string]string{
		"gate/empty/gate.yaml": `
on:
  - event: Stop
`,
	})
	assert.True(t, hasKind(iv, ErrAtLeastOne), "a gate with neither require nor checks is refused: %v", iv.Reason)
}

// A gate may not wake on a Post file event — the action already landed.
// sr:proves loading/event-kinds-by-nature
func TestLoad_Gate_PostEventRefused(t *testing.T) {
	iv := loadOneInvalid(t, map[string]string{
		"gate/late/gate.yaml": `
on:
  - event: PostFileCreate
checks:
  - script: ./s.sh
`,
	})
	assert.True(t, hasKind(iv, ErrUnknownEventKind), "a gate on a Post event is refused: %v", iv.Reason)
	assert.Contains(t, iv.Reason, "PostFileCreate")
}

// The PostFileWrite alias is a context's alone — a gate naming it is refused.
// sr:proves loading/event-kinds-by-nature
func TestLoad_Gate_PostFileWriteAliasRefused(t *testing.T) {
	iv := loadOneInvalid(t, map[string]string{
		"gate/late/gate.yaml": `
on:
  - event: PostFileWrite
checks:
  - script: ./s.sh
`,
	})
	assert.True(t, hasKind(iv, ErrUnknownEventKind), "a gate on PostFileWrite is refused: %v", iv.Reason)
}

// A gate naming a kind no nature has is refused with a diagnostic listing what a
// gate admits.
// sr:proves loading/event-kinds-by-nature
func TestLoad_Gate_UnknownKindRefused(t *testing.T) {
	iv := loadOneInvalid(t, map[string]string{
		"gate/typo/gate.yaml": `
on:
  - event: PreFileWirte
checks:
  - script: ./s.sh
`,
	})
	assert.True(t, hasKind(iv, ErrUnknownEventKind), "a gate on a typo'd kind is refused: %v", iv.Reason)
	assert.Contains(t, iv.Reason, "PreFileCreate", "the diagnostic lists the kinds a gate admits")
}

// A gate trigger's match reads the event under `event`; a bare `path` is refused.
// sr:proves loading/trigger-match-compiles-at-load
func TestLoad_Gate_BareEventFieldRefused(t *testing.T) {
	iv := loadOneInvalid(t, map[string]string{
		"gate/bare/gate.yaml": `
on:
  - event: PreFileCreate
    match: path startsWith "x"
checks:
  - script: ./s.sh
`,
	})
	assert.True(t, hasKind(iv, ErrBadMatch), "a gate match reading a bare event field is refused: %v", iv.Reason)
}

// A gate trigger's match reading a misspelled event field is refused at load.
// sr:proves loading/trigger-match-compiles-at-load
// sr:proves matching/checked-at-load
func TestLoad_Gate_MisspelledEventFieldRefused(t *testing.T) {
	iv := loadOneInvalid(t, map[string]string{
		"gate/typo/gate.yaml": `
on:
  - event: PreFileCreate
    match: event.paht startsWith "x"
checks:
  - script: ./s.sh
`,
	})
	assert.True(t, hasKind(iv, ErrBadMatch), "a gate match reading event.paht is refused: %v", iv.Reason)
	assert.Contains(t, iv.Reason, "paht")
}

// A gate comparing a flag's value to a string is refused at load: every
// `.flags.X` is the list of that flag's occurrences, so `.flags.tag == "next"`
// could never match — the gate loaded and permitted `--tag=next` in silence.
// The list spelling of the same rule loads.
// sr:proves loading/trigger-match-compiles-at-load
// sr:proves matching/flag-values-are-lists
func TestLoad_Gate_FlagComparedToAStringRefused(t *testing.T) {
	iv := loadOneInvalid(t, map[string]string{
		"gate/next-tag/gate.yaml": `
on:
  - event: PreCommandInvoke
    match: any(event.invocations, .bin == "npm" and .flags.tag == "next")
checks:
  - script: ./s.sh
`,
	})
	assert.True(t, hasKind(iv, ErrBadMatch), "a gate comparing a list-valued flag to a string is refused: %v", iv.Reason)

	loadOK(t, map[string]string{
		"gate/next-tag/gate.yaml": `
on:
  - event: PreCommandInvoke
    match: any(event.invocations, .bin == "npm" and "next" in .flags.tag)
checks:
  - script: ./s.sh
`,
	})
}

// A gate on Stop is legal (the one non-file/command event a gate carries).
// sr:proves loading/event-kinds-by-nature
func TestLoad_Gate_StopIsValid(t *testing.T) {
	loadOK(t, map[string]string{
		"gate/stopgate/gate.yaml": `
on:
  - event: Stop
checks:
  - script: ./s.sh
`,
	})
}

// ---------------------------------------------------------------------------
// Gate: PreFileWrite alias expansion
// ---------------------------------------------------------------------------

// The PreFileWrite alias must load on a gate, and its match must be checked
// against BOTH kinds it expands to — a match valid for create and update passes.
func TestLoad_Gate_PreFileWriteAliasExpands(t *testing.T) {
	loadOK(t, map[string]string{
		"gate/write/gate.yaml": `
on:
  - event: PreFileWrite
    match: event.path startsWith "memories/"
require:
  - skill: document-topic
`,
	})
}

// The alias expands to create + update, so a match reading a field only ONE of
// them declares is refused — this is what makes the expansion honest. PreFileCreate
// has newContent (required); PreFileUpdate has it optional but still declared, and
// oldContent which PreFileCreate lacks — so a match on event.oldContent is valid
// for update and NOT for create, and must be refused because the alias covers
// create too.
// sr:proves loading/trigger-match-compiles-at-load
// sr:proves matching/checked-at-load
func TestLoad_Gate_PreFileWriteAliasRejectsFieldOnlyOneKindHas(t *testing.T) {
	iv := loadOneInvalid(t, map[string]string{
		"gate/write/gate.yaml": `
on:
  - event: PreFileWrite
    match: event.oldContent startsWith "x"
require:
  - skill: document-topic
`,
	})
	assert.True(t, hasKind(iv, ErrBadMatch),
		"a PreFileWrite match reading a field only PreFileUpdate has (oldContent) is refused, because the alias also covers PreFileCreate: %v", iv.Reason)
	assert.Contains(t, iv.Reason, "PreFileCreate", "the refusal names the kind whose scope rejected it")
}

// ---------------------------------------------------------------------------
// Context: valid + on-kinds + enter/exit + PostFileWrite alias
// ---------------------------------------------------------------------------

func TestLoad_Context_Valid(t *testing.T) {
	loaded := loadOK(t, map[string]string{
		"context/refactoring/context.yaml": `
on:
  - event: PreToolUse
enter: ./enter.sh
exit: ./exit.sh
`,
	})
	require.Len(t, loaded.Contexts, 1)
	assert.Equal(t, "refactoring", loaded.Contexts[0].Name)
	assert.Equal(t, "./enter.sh", loaded.Contexts[0].Enter)
	assert.Equal(t, "./exit.sh", loaded.Contexts[0].Exit)
}

// A context may wake on a Post file event — the whole reason its vocabulary is
// wider than a gate's.
// sr:proves loading/event-kinds-by-nature
func TestLoad_Context_PostEventValid(t *testing.T) {
	loadOK(t, map[string]string{
		"context/people/context.yaml": `
on:
  - event: PostFileCreate
    match: event.path startsWith "people/"
enter: ./enter.sh
exit: ./exit.sh
`,
	})
}

// A context may wake on PostTagWrite, reading event.tags.
// sr:proves loading/event-kinds-by-nature
func TestLoad_Context_PostTagWriteValid(t *testing.T) {
	loadOK(t, map[string]string{
		"context/research/context.yaml": `
on:
  - event: PostTagWrite
    match: any(event.tags, .label == "research")
enter: ./enter.sh
exit: ./exit.sh
`,
	})
}

// Stop is intentionally never a context ENTRY event — a Stop is where exit runs.
// sr:proves loading/event-kinds-by-nature
func TestLoad_Context_StopRefused(t *testing.T) {
	iv := loadOneInvalid(t, map[string]string{
		"context/bad/context.yaml": `
on:
  - event: Stop
enter: ./enter.sh
exit: ./exit.sh
`,
	})
	assert.True(t, hasKind(iv, ErrUnknownEventKind), "a context on Stop is refused: %v", iv.Reason)
	assert.Contains(t, iv.Reason, "Stop")
}

// sr:proves loading/rule-must-decide-something
func TestLoad_Context_MissingEnter(t *testing.T) {
	iv := loadOneInvalid(t, map[string]string{
		"context/noenter/context.yaml": `
on:
  - event: PreToolUse
exit: ./exit.sh
`,
	})
	assert.True(t, hasKind(iv, ErrMissingField), "a context with no enter is refused: %v", iv.Reason)
	assert.Contains(t, iv.Reason, "enter")
}

// sr:proves loading/rule-must-decide-something
func TestLoad_Context_MissingExit(t *testing.T) {
	iv := loadOneInvalid(t, map[string]string{
		"context/noexit/context.yaml": `
on:
  - event: PreToolUse
enter: ./enter.sh
`,
	})
	assert.True(t, hasKind(iv, ErrMissingField), "a context with no exit is refused: %v", iv.Reason)
	assert.Contains(t, iv.Reason, "exit")
}

// sr:proves loading/rule-must-decide-something
func TestLoad_Context_NoTriggers(t *testing.T) {
	iv := loadOneInvalid(t, map[string]string{
		"context/notrig/context.yaml": `
enter: ./enter.sh
exit: ./exit.sh
`,
	})
	assert.True(t, hasKind(iv, ErrMissingField), "a context with no on triggers is refused: %v", iv.Reason)
}

// PostFileWrite is a context alias, expanded to PostFileCreate + PostFileUpdate.
func TestLoad_Context_PostFileWriteAliasExpands(t *testing.T) {
	loadOK(t, map[string]string{
		"context/goaltrack/context.yaml": `
on:
  - event: PostFileWrite
    match: event.path startsWith "goal/" and event.path endsWith "goal.yaml"
enter: ./enter.sh
exit: ./exit.sh
`,
	})
}

// ---------------------------------------------------------------------------
// Prerequisite: exactly-one-of, context resolution
// ---------------------------------------------------------------------------

func TestLoad_Prerequisite_BothSkillAndContext(t *testing.T) {
	iv := loadOneInvalid(t, map[string]string{
		"gate/both/gate.yaml": `
on:
  - event: Stop
require:
  - skill: document-topic
    context: some-context
checks:
  - script: ./s.sh
`,
		"context/some-context/context.yaml": validContextYAML,
	})
	assert.True(t, hasKind(iv, ErrExactlyOne), "a prerequisite setting both skill and context is refused: %v", iv.Reason)
}

func TestLoad_Prerequisite_Empty(t *testing.T) {
	iv := loadOneInvalid(t, map[string]string{
		"gate/empty/gate.yaml": `
on:
  - event: Stop
require:
  - {}
checks:
  - script: ./s.sh
`,
	})
	assert.True(t, hasKind(iv, ErrExactlyOne), "an empty prerequisite is refused: %v", iv.Reason)
}

// A skill prerequisite naming an unknown skill is NOT a load error — skills are a
// runtime fact the loader has no list of.
func TestLoad_Prerequisite_UnknownSkillIsFine(t *testing.T) {
	loadOK(t, map[string]string{
		"gate/skillgate/gate.yaml": `
on:
  - event: PreToolUse
require:
  - skill: some-skill-that-may-not-exist-yet
`,
	})
}

// A files entry alongside skill loads — its contents are a runtime fact
// (whether that page was read), not something the loader checks against disk,
// the same reasoning TestLoad_Prerequisite_UnknownSkillIsFine gives for the
// skill name itself.
func TestLoad_Prerequisite_FilesWithSkillIsFine(t *testing.T) {
	loadOK(t, map[string]string{
		"gate/skillgate/gate.yaml": `
on:
  - event: PreToolUse
require:
  - skill: authoring-guardrails
    files: [script-checks.md, file-guard.md]
`,
	})
}

// files without skill is refused — it names a subpage of a skill, and has no
// meaning without one.
func TestLoad_Prerequisite_FilesWithoutSkillRefused(t *testing.T) {
	iv := loadOneInvalid(t, map[string]string{
		"gate/orphan/gate.yaml": `
on:
  - event: PreToolUse
require:
  - files: [script-checks.md]
checks:
  - script: ./s.sh
`,
	})
	assert.True(t, hasKind(iv, ErrBadFilesEntry), "files without skill is refused: %v", iv.Reason)
}

// files without skill is refused even when a context is set instead — files
// is not one half of the skill/context exactly-one-of pair, so setting it
// alongside context does not satisfy it.
func TestLoad_Prerequisite_FilesWithContextRefused(t *testing.T) {
	iv := loadOneInvalid(t, map[string]string{
		"gate/mixed/gate.yaml": `
on:
  - event: Stop
require:
  - context: goal-tracking
    files: [script-checks.md]
checks:
  - script: ./s.sh
`,
		"context/goal-tracking/context.yaml": validContextYAML,
	})
	assert.True(t, hasKind(iv, ErrBadFilesEntry), "files alongside context (no skill) is refused: %v", iv.Reason)
}

// A files entry must be a plain relative path inside the skill's own
// directory — no absolute path, no escaping with "..".
func TestLoad_Prerequisite_BadFilesEntryRefused(t *testing.T) {
	for name, entry := range map[string]string{
		"empty":    `""`,
		"absolute": `/etc/passwd`,
		"climbs":   `../../etc/passwd`,
	} {
		t.Run(name, func(t *testing.T) {
			iv := loadOneInvalid(t, map[string]string{
				"gate/bad/gate.yaml": `
on:
  - event: PreToolUse
require:
  - skill: authoring-guardrails
    files: [` + entry + `]
`,
			})
			assert.True(t, hasKind(iv, ErrBadFilesEntry), "%s: bad files entry is refused: %v", name, iv.Reason)
		})
	}
}

// A context prerequisite naming a context that resolves loads.
func TestLoad_Prerequisite_KnownContextResolves(t *testing.T) {
	loadOK(t, map[string]string{
		"gate/verify/gate.yaml": `
on:
  - event: Stop
require:
  - context: goal-tracking
checks:
  - script: ./s.sh
`,
		"context/goal-tracking/context.yaml": validContextYAML,
	})
}

// A context prerequisite naming a context nothing declares IS a configuration
// error caught at load — the engine cannot order against a context that does not
// exist.
func TestLoad_Prerequisite_UnknownContextRefused(t *testing.T) {
	iv := loadOneInvalid(t, map[string]string{
		"gate/verify/gate.yaml": `
on:
  - event: Stop
require:
  - context: no-such-context
checks:
  - script: ./s.sh
`,
	})
	assert.True(t, hasKind(iv, ErrUnknownContext), "a prerequisite naming an unknown context is refused: %v", iv.Reason)
	assert.Contains(t, iv.Reason, "no-such-context")
}

// A context whose OWN yaml failed to parse contributes no name, so a prerequisite
// naming it is (correctly) reported unknown — the engine cannot order against a
// context it could not read.
func TestLoad_Prerequisite_UnreadableContextIsUnknown(t *testing.T) {
	root := writeDecl(t, map[string]string{
		"gate/verify/gate.yaml": `
on:
  - event: Stop
require:
  - context: broken
checks:
  - script: ./s.sh
`,
		"context/broken/context.yaml": "this: is: not: valid: yaml: {",
	})
	loaded, err := New(root).Load(testRegistry(t))
	require.NoError(t, err)
	// Two invalids: the broken context (malformed) and the gate (unknown context).
	require.Len(t, loaded.Invalid, 2)
	var gateIv, ctxIv *Invalid
	for i := range loaded.Invalid {
		switch loaded.Invalid[i].Nature {
		case NatureGate:
			gateIv = &loaded.Invalid[i]
		case NatureContext:
			ctxIv = &loaded.Invalid[i]
		}
	}
	require.NotNil(t, ctxIv)
	require.NotNil(t, gateIv)
	assert.True(t, hasKind(*ctxIv, ErrMalformed), "the broken context is malformed")
	assert.True(t, hasKind(*gateIv, ErrUnknownContext), "the gate requiring it reports unknown context")
}

// ---------------------------------------------------------------------------
// Structure gate
// ---------------------------------------------------------------------------

func TestLoad_Structure_Valid(t *testing.T) {
	loaded := loadOK(t, map[string]string{
		"file-guard/structure.yaml": `
allow:
  - glob: "memories/updates/*.md"
  - regex: "^memories/decisions/[0-9]{8}_[a-z0-9-]+/.*\\.md$"
deny:
  - glob: "memories/updates/secret.md"
`,
	})
	sg := loaded.ProjectStructure()
	require.NotNil(t, sg)
	assert.Len(t, sg.Allow, 2)
	assert.Len(t, sg.Deny, 1)
	assert.True(t, sg.Allow[0].isGlob())
	assert.True(t, sg.Allow[1].isRegex())
}

func TestLoad_Structure_EntryBothGlobAndRegex(t *testing.T) {
	iv := loadOneInvalid(t, map[string]string{
		"file-guard/structure.yaml": `
allow:
  - glob: "x/*.md"
    regex: "^x/.*$"
`,
	})
	assert.Equal(t, NatureStructure, iv.Nature)
	assert.True(t, hasKind(iv, ErrExactlyOne), "a structure entry setting both glob and regex is refused: %v", iv.Reason)
}

func TestLoad_Structure_EntryNeither(t *testing.T) {
	iv := loadOneInvalid(t, map[string]string{
		"file-guard/structure.yaml": `
allow:
  - {}
`,
	})
	assert.True(t, hasKind(iv, ErrExactlyOne), "an empty structure entry is refused: %v", iv.Reason)
}

func TestLoad_Structure_EmptyAllowRefused(t *testing.T) {
	iv := loadOneInvalid(t, map[string]string{
		"file-guard/structure.yaml": `
deny:
  - glob: "x/*.md"
`,
	})
	assert.True(t, hasKind(iv, ErrMissingField), "a structure gate with an empty allowlist is refused: %v", iv.Reason)
}

func TestLoad_Structure_MalformedRegexRefused(t *testing.T) {
	iv := loadOneInvalid(t, map[string]string{
		"file-guard/structure.yaml": `
allow:
  - regex: "^[unterminated"
`,
	})
	assert.True(t, hasKind(iv, ErrBadMatch), "a structure entry with a malformed regex is refused: %v", iv.Reason)
}

// The structure gate sits BESIDE the per-guard folders under file-guard/, and
// is not mistaken for a file-guard folder.
func TestLoad_Structure_CoexistsWithFileGuards(t *testing.T) {
	loaded := loadOK(t, map[string]string{
		"file-guard/structure.yaml": `
allow:
  - glob: "memories/*.md"
`,
		"file-guard/pinned/file-guard.yaml": `
match: "**/*.md"
checks:
  - script: ./s.sh
`,
	})
	require.NotNil(t, loaded.ProjectStructure())
	require.Len(t, loaded.FileGuards, 1)
	assert.Equal(t, "pinned", loaded.FileGuards[0].Name)
}

// ---------------------------------------------------------------------------
// Malformed / parse failures
// ---------------------------------------------------------------------------

// sr:proves loading/one-broken-rule-disables-only-itself
func TestLoad_Malformed_UnparseableYAML(t *testing.T) {
	iv := loadOneInvalid(t, map[string]string{
		"gate/bad/gate.yaml": "on: [ this is not valid",
	})
	assert.True(t, hasKind(iv, ErrMalformed), "unparseable YAML is refused: %v", iv.Reason)
}

// A nature folder with no yaml inside is a half-written declaration — refused by
// name rather than silently skipped, so the author sees the folder they meant to
// fill.
// sr:proves loading/one-broken-rule-disables-only-itself
func TestLoad_Malformed_FolderWithoutYAML(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gate", "empty-folder"), 0o755))
	loaded, err := New(root).Load(testRegistry(t))
	require.NoError(t, err)
	require.Len(t, loaded.Invalid, 1)
	assert.True(t, hasKind(loaded.Invalid[0], ErrMalformed), "a gate folder with no gate.yaml is refused: %v", loaded.Invalid[0].Reason)
}

// ---------------------------------------------------------------------------
// One bad declaration does not disarm the others
// ---------------------------------------------------------------------------

// sr:proves loading/one-broken-rule-disables-only-itself
func TestLoad_OneBadDoesNotDisarmOthers(t *testing.T) {
	root := writeDecl(t, map[string]string{
		"gate/good/gate.yaml": `
on:
  - event: Stop
checks:
  - script: ./s.sh
`,
		"gate/bad/gate.yaml": `
on:
  - event: PostFileCreate
checks:
  - script: ./s.sh
`,
	})
	loaded, err := New(root).Load(testRegistry(t))
	require.NoError(t, err)
	require.Len(t, loaded.Gates, 1, "the good gate loaded")
	assert.Equal(t, "good", loaded.Gates[0].Name)
	require.Len(t, loaded.Invalid, 1, "the bad gate is invalid")
	assert.Equal(t, "bad", loaded.Invalid[0].Name)
}

// Every fault is reported, not just the first — an author fixing a declaration
// sees all of it at once.
// sr:proves loading/one-broken-rule-disables-only-itself
func TestLoad_ReportsEveryFault(t *testing.T) {
	iv := loadOneInvalid(t, map[string]string{
		"gate/many/gate.yaml": `
on:
  - event: PostFileCreate
  - event: AlsoBad
checks:
  - script: ./s.sh
    judge: ./j.md.j2
`,
	})
	// Two bad on-kinds + one both-set check = three problems.
	assert.GreaterOrEqual(t, len(iv.Problems), 3, "every fault is reported: %v", iv.Reasons)
}

// ---------------------------------------------------------------------------
// nil registry: parses, skips match checks
// ---------------------------------------------------------------------------

// Without a registry the loader still parses and validates structure — but a
// trigger's match cannot be compiled (no kind fields), so a match that WOULD be
// refused with a registry loads without one. This mirrors guardrail.Load vs
// LoadWith.
func TestLoad_NilRegistry_SkipsMatchCheck(t *testing.T) {
	root := writeDecl(t, map[string]string{
		"gate/g/gate.yaml": `
on:
  - event: PreFileCreate
    match: event.paht startsWith "x"
checks:
  - script: ./s.sh
`,
	})
	loaded, err := New(root).Load(nil)
	require.NoError(t, err)
	// The match typo is not caught (no registry), but the on-kind check still runs
	// (it needs no registry), so this gate loads clean.
	require.Empty(t, loaded.Invalid, "a bad match is not caught without a registry: %v", invalidReasons(loaded))
	require.Len(t, loaded.Gates, 1)
}

// But the on-kind check does NOT need a registry — a Post event on a gate is
// refused even with a nil registry.
// sr:proves loading/event-kinds-by-nature
func TestLoad_NilRegistry_StillChecksOnKinds(t *testing.T) {
	root := writeDecl(t, map[string]string{
		"gate/late/gate.yaml": `
on:
  - event: PostFileCreate
checks:
  - script: ./s.sh
`,
	})
	loaded, err := New(root).Load(nil)
	require.NoError(t, err)
	require.Len(t, loaded.Invalid, 1)
	assert.True(t, hasKind(loaded.Invalid[0], ErrUnknownEventKind))
}

// validContextYAML is a minimal valid context, reused where a test needs a
// context to exist so a prerequisite resolves.
const validContextYAML = `
on:
  - event: PreToolUse
enter: ./enter.sh
exit: ./exit.sh
`

// `preventive:` was removed: a file-guard carrying it — with any value — is
// refused at load, and the refusal says how to split it.
// sr:proves loading/retired-file-guard-keys-are-refused
func TestLoad_FileGuard_PreventiveIsRefusedWithTheSplit(t *testing.T) {
	for _, value := range []string{"true", "false"} {
		iv := loadOneInvalid(t, map[string]string{
			"file-guard/pinned/file-guard.yaml": "match: \"**/*.md\"\npreventive: " + value + "\nchecks:\n  - script: ./check.sh\n",
		})
		assert.True(t, hasKind(iv, ErrRetiredKey), "preventive: %s must be refused as a retired key: %v", value, iv.Problems)
		msg := strings.Join(Messages(iv.Problems), "\n")
		assert.Contains(t, msg, "PreFileWrite")
		assert.Contains(t, msg, "gate")
		assert.Contains(t, msg, "file-guard")
	}
}

// writeDeclFile writes one declaration file. A `.sh` file is a declared script, so
// it gets what the loader demands of one — the execute bit and a shebang —
// unless the test is about exactly that (it then writes the file itself).
func writeDeclFile(t *testing.T, path, content string) {
	t.Helper()
	mode := os.FileMode(0o644)
	if strings.HasSuffix(path, ".sh") {
		mode = 0o755
		if !strings.HasPrefix(content, "#!") {
			content = "#!/bin/sh\n" + content
		}
	}
	require.NoError(t, os.WriteFile(path, []byte(content), mode))
	require.NoError(t, os.Chmod(path, mode))
}

// A declared script that exists but cannot be exec'd directly (no shebang, no
// execute bit, a non-standard interpreter) does NOT drop the rule: it stays loaded
// and enforced (its exec path refuses what it guards), and is reported in Degraded,
// naming the file and the fix. Written with os.WriteFile directly: writeDeclFile
// would repair them.
// sr:proves loading/declared-script-fault-keeps-rule-enforced
func TestLoad_Script_WithoutShebangOrExecBitStaysLoadedAndIsReported(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		mode os.FileMode
		want string
	}{
		"no shebang":     {"exit 0\n", 0o755, "#!/usr/bin/env bash"},
		"not executable": {"#!/bin/sh\nexit 0\n", 0o644, "chmod +x"},
		"local interp":   {"#!/usr/local/bin/bash\nexit 0\n", 0o755, "#!/usr/bin/env bash"},
	} {
		t.Run(name, func(t *testing.T) {
			root := writeDecl(t, map[string]string{
				"file-guard/g/file-guard.yaml": "match: \"**/*.md\"\nchecks:\n  - script: ./c.sh\n",
			})
			p := filepath.Join(root, "file-guard", "g", "c.sh")
			require.NoError(t, os.WriteFile(p, []byte(tc.body), tc.mode))
			require.NoError(t, os.Chmod(p, tc.mode))
			loaded, err := New(root).Load(testRegistry(t))
			require.NoError(t, err)
			assert.Empty(t, loaded.Invalid, "an unrunnable script is not a declaration fault")
			require.Len(t, loaded.FileGuards, 1, "the rule must stay loaded, or it stops refusing until the next report")
			require.Len(t, loaded.Degraded, 1)
			assert.True(t, hasKind(loaded.Degraded[0], ErrBadScript))
			assert.Equal(t, "g", loaded.Degraded[0].Name)
			assert.Contains(t, loaded.Degraded[0].Reason, "c.sh")
			assert.Contains(t, loaded.Degraded[0].Reason, tc.want)
		})
	}
}

// A script that does not exist is not this load check's concern (it is reported
// where it is run), and an inline command is never inspected.
// sr:proves loading/declared-script-fault-keeps-rule-enforced
func TestLoad_Script_MissingFileStillLoads(t *testing.T) {
	loadOK(t, map[string]string{
		"file-guard/g/file-guard.yaml": "match: \"**/*.md\"\nchecks:\n  - script: sr-checks\n  - script: ./nowhere.sh\n",
	})
}

// The same holds for every nature: a gate's check, a context's enter and exit and a rule's
// subjects script stay loaded when the file loses its shebang or execute bit.
// sr:proves loading/declared-script-fault-keeps-rule-enforced
func TestLoad_Script_OtherNaturesStayLoadedAndReported(t *testing.T) {
	root := writeDecl(t, map[string]string{
		"gate/gt/gate.yaml":       "on:\n  - event: PreFileWrite\n    match: event.path endsWith \".md\"\nchecks:\n  - prepare: ./p.sh\n    judge: ./j.md.j2\n",
		"gate/gt/j.md.j2":         "judge\n",
		"context/cx/context.yaml": "on:\n  - event: PostFileWrite\n    match: event.path endsWith \".md\"\nenter: ./enter.sh\nexit: ./exit.sh\n",
	})
	for _, rel := range []string{"gate/gt/p.sh", "context/cx/enter.sh", "context/cx/exit.sh"} {
		p := filepath.Join(root, rel)
		require.NoError(t, os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), 0o644))
		require.NoError(t, os.Chmod(p, 0o644))
	}
	loaded, err := New(root).Load(testRegistry(t))
	require.NoError(t, err)
	assert.Empty(t, loaded.Invalid)
	require.Len(t, loaded.Gates, 1)
	require.Len(t, loaded.Contexts, 1)
	require.Len(t, loaded.Degraded, 2)
	for _, d := range loaded.Degraded {
		assert.True(t, hasKind(d, ErrBadScript))
		assert.Contains(t, d.Reason, "chmod +x")
	}
}

// Degraded is reported in qualified-name order, whatever order the folders were read in.
// sr:proves loading/declared-script-fault-keeps-rule-enforced
func TestLoad_Degraded_IsSortedByQualifiedName(t *testing.T) {
	files := map[string]string{}
	for _, n := range []string{"zeta", "alpha", "mid"} {
		files["file-guard/"+n+"/file-guard.yaml"] = "match: \"**/*.md\"\nchecks:\n  - script: ./c.sh\n"
	}
	root := writeDecl(t, files)
	for _, n := range []string{"zeta", "alpha", "mid"} {
		p := filepath.Join(root, "file-guard", n, "c.sh")
		require.NoError(t, os.WriteFile(p, []byte("exit 0\n"), 0o755))
		require.NoError(t, os.Chmod(p, 0o755))
	}
	loaded, err := New(root).Load(testRegistry(t))
	require.NoError(t, err)
	require.Len(t, loaded.Degraded, 3)
	var got []string
	for _, d := range loaded.Degraded {
		got = append(got, d.Name)
	}
	assert.Equal(t, []string{"alpha", "mid", "zeta"}, got)
}
