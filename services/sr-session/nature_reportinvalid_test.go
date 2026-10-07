package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The "reported" half of broken_declaration_is_not_silent, for the NEW nature
// format, pinned where the channel can be read.
//
// The e2e suite (tests/e2e/pre_tool/013_broken_declaration_is_not_silent) proves
// the agent-visible half — a malformed file-guard.yaml blocks NOTHING and does not
// disarm a sound rule beside it. What that suite cannot see is the REPORT: measured
// through the e2e mock, a permitted action carries neither stdout nor stderr back to
// the agent, and a broken rule is exactly the permitted case. So the report's own
// delivery is asserted here, driving the same pre-tool dispatch runSessionPreTool
// with stderr captured — the new-format counterpart of
// TestPreTool_ABrokenRuleIsReportedOnlyForItsOwnKind (which pins the OLD format's
// reportBroken). Without a test on this channel, "a broken declaration is not
// silently ignored" would be a claim no test reaches.

// writeFileGuardYAML places a file-guard.yaml (and any sibling scripts) into a
// project's new-format tree, committed so the guard's own files are part of the
// baseline rather than a change the cycle appears to have made.
func writeFileGuardYAML(t *testing.T, proj, name, yaml string, scripts map[string]string) {
	t.Helper()
	dir := filepath.Join(proj, ".sloprail", "file-guard", name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "file-guard.yaml"), []byte(yaml), 0o644))
	for file, body := range scripts {
		require.NoError(t, os.WriteFile(filepath.Join(dir, file), []byte(body), 0o755))
	}
	runGit(t, proj, "add", "-f", ".sloprail")
	runGit(t, proj, "commit", "-m", "file-guard "+name)
}

// TestPreTool_MalformedFileGuardIsReportedAndDoesNotDeny.
//
// A file-guard whose match names a field the file scope does not carry (`marker`,
// singular — the scope exposes `markers`) cannot load. At the pre-tool dispatch it
// must be REPORTED (reportNatureInvalid's "declaration <x> not loaded", naming the
// guard) and must NOT deny — the two halves of "an invalid guardrail blocks nothing
// but is not silently ignored". Both are asserted on the channels that carry them:
// the report on stderr, the non-denial on stdout.
// sr:proves gates/broken-declaration-denies-nothing
func TestPreTool_MalformedFileGuardIsReportedAndDoesNotDeny(t *testing.T) {
	proj := initRepo(t)
	writeFileGuardYAML(t, proj, "typo", `match: marker.kind == "endpoint"
checks:
  - script: ./refuse.sh
`, map[string]string{"refuse.sh": "#!/bin/sh\ncat >/dev/null\nexit 1\n"})
	t.Chdir(proj)

	stdout, stderr := preToolIn(t, proj, "Write",
		`{"file_path":`+jsonString(filepath.Join(proj, "any.md"))+`,"content":"hi"}`)

	// Reported, and by name — an author fixing the rule can find it.
	assert.Contains(t, stderr, "not loaded",
		"a malformed file-guard must be reported as not loaded, or a broken declaration is silently ignored")
	assert.Contains(t, stderr, "typo",
		"the report must name which declaration did not load, or nobody can find it")

	// And it carries the way out — the origin-aware remedy (D1). This is a
	// PROJECT's own rule, so the remedy is theirs to fix; it must NOT tell them the
	// rule is not theirs or send them to disable a plugin they do not have. The
	// remedy wording matrix itself is pinned in internal/declaration/remedy_test.go;
	// here we only prove the report actually carries it on this channel.
	assert.Contains(t, stderr, "fix the file-guard \"typo\"",
		"the report must offer the project author a way to fix their own broken rule")
	assert.NotContains(t, stderr, "not yours to fix",
		"a project's own rule must not be reported as a plugin's rule to disable")

	// And it did not deny — a rule that could not load blocks nothing.
	assert.NotContains(t, stdout, `"permissionDecision":"deny"`,
		"a file-guard that could not load must not refuse the action")
}

// TestPreTool_UnparseableFileGuardIsReportedAndDoesNotDeny.
//
// The other route to a disqualified declaration: a file the YAML parser cannot get
// a declaration out of at all. It names no bindings, so the engine cannot scope a
// refusal — and it must not respond by refusing everything (the fail-closed this
// design removed). It is still reported as not loaded.
// sr:proves gates/broken-declaration-denies-nothing
func TestPreTool_UnparseableFileGuardIsReportedAndDoesNotDeny(t *testing.T) {
	proj := initRepo(t)
	writeFileGuardYAML(t, proj, "unreadable", ":this is not: valid yaml: at all\n  - [unbalanced\n", nil)
	t.Chdir(proj)

	stdout, stderr := preToolIn(t, proj, "Write",
		`{"file_path":`+jsonString(filepath.Join(proj, "any.md"))+`,"content":"hi"}`)

	assert.Contains(t, stderr, "not loaded",
		"an unparseable file-guard must be reported as not loaded")
	assert.NotContains(t, stdout, `"permissionDecision":"deny"`,
		"an unparseable declaration must not refuse the action")
}
