package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/sessionstate"
)

// The Post dispatch's fail-open paths.
//
// Everything here is the same failure the pre-tool point was already corrected
// for, alive on the other side of the cycle: a rule that is DECLARED, that the
// project believes is enforcing something, and that contributes no refusal
// because the engine could not run it. On the Pre side each of these refuses.
// On the Post side each of them permitted, and permitting at Stop means the
// turn ends with the violation unreported.
//
// Why the asymmetry is not justifiable by the timing. A Post refusal cannot
// undo the write — that is true and is not the point. What it CAN do is stop
// the turn from ending, which is the entire mechanism by which an
// after-the-fact rule gets anything corrected. Letting the turn end because the
// engine could not evaluate the rule is the engine deciding, on its own
// account, that an unrunnable rule is a satisfied one.

// brokenMatcherDecl binds a Post kind with a matcher naming a field the kind
// does not carry.
//
// `content` is real on PreFileCreate and absent from every Post kind, so this
// is the ordinary author mistake: a matcher moved from a Pre binding to a Post
// one. It fails CompileMatcherFor, which makes the whole declaration Invalid at
// load.
const brokenMatcherDecl = `---
hooks:
  PostFileCreate:
    - matcher: content startsWith "x"
      hooks:
        - type: command
          command: ./refuse.sh
---

# Refuses every created file
`

// alwaysRefuse is a hook that refuses whatever it is given.
const alwaysRefuse = `#!/bin/sh
echo "refused by the rule" >&2
exit 1
`

// TestDispatch_BrokenDeclarationIsNotSilentAtPost.
//
// runPostDispatch discarded the invalid list outright — `decls, _, err :=` —
// so a declaration that could not be loaded contributed nothing at all to the
// cycle: no hook, no objection, and no word to the agent.
//
// That is exactly the defect refuseForBroken was written to close on the Pre
// side, and the argument transfers without modification. An author who wrote
// `content` on a Post binding believes their cycle is guarded. The engine knows
// it is not. Ending the turn tells the author they were right.
//
// The channel argument transfers too. A Stop hook exits 0 and blocks by writing
// {"decision":"block"} on stdout, so its STDERR reaches no agent — measured on
// harness.BlockingErrors. reportInvalid printing to stderr here would therefore
// be the same silence the Pre side already rejected: a line in a log nobody
// reads, beside a turn that ended cleanly.
func TestDispatch_BrokenDeclarationIsNotSilentAtPost(t *testing.T) {
	proj := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(proj, "seed.md"), []byte("seed"), 0o644))
	runGit(t, proj, "add", ".")
	runGit(t, proj, "commit", "-m", "base")

	guardrailDir(t, proj, "typo", brokenMatcherDecl, map[string]string{"refuse.sh": alwaysRefuse})
	store := openStore(t)
	baselineAt(t, store, proj)

	// A file the broken rule was bound to.
	require.NoError(t, os.WriteFile(filepath.Join(proj, "fresh.md"), []byte("new"), 0o644))

	_, ran := dispatchIn(t, proj, store)

	assert.False(t, ran,
		"a cycle whose guardrail could not be loaded must not report itself as completed: "+
			"the rule bound to PostFileCreate did not run, and the file it was about was created")
}

// TestDispatch_BrokenDeclarationBlocksAndNamesItself.
//
// The refusal has to reach the agent and has to name the rule, which is
// refusal_names_guardrail. A block carrying no name leaves the agent unable to
// find what it broke; a diagnostic on stderr is not delivered at all.
func TestDispatch_BrokenDeclarationBlocksAndNamesItself(t *testing.T) {
	proj := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(proj, "seed.md"), []byte("seed"), 0o644))
	runGit(t, proj, "add", ".")
	runGit(t, proj, "commit", "-m", "base")

	guardrailDir(t, proj, "typo", brokenMatcherDecl, map[string]string{"refuse.sh": alwaysRefuse})
	store := openStore(t)
	baselineAt(t, store, proj)
	require.NoError(t, os.WriteFile(filepath.Join(proj, "fresh.md"), []byte("new"), 0o644))

	stdout, _ := dispatchOut(t, proj, store)

	assert.Contains(t, stdout, `"decision":"block"`,
		"a broken declaration bound to this cycle's events must block the turn")
	assert.Contains(t, stdout, "typo",
		"the block must name the guardrail that could not be loaded")
}

// unreadableDecl has no frontmatter fence at all, so it cannot be parsed and
// names no bindings. On the Pre side refuseForUnreadable refuses EVERY action
// for it, because "no evidence of what it guarded" is not "evidence it guarded
// nothing".
const unreadableDecl = `hooks:
  PostFileCreate:
    - hooks:
        - type: command
          command: ./refuse.sh
`

// TestDispatch_UnreadableDeclarationBlocksTheTurn.
//
// The same argument refuseForUnreadable makes, at the other hook point. A file
// the project keeps as a guardrail and that the engine cannot read says nothing
// about what it was guarding — and the engine deciding on its own that it
// guarded nothing is the fail-open in its purest form.
//
// It is not scoped to a kind because there is no kind to scope to. A
// declaration that did not parse has no bindings to read off it.
func TestDispatch_UnreadableDeclarationBlocksTheTurn(t *testing.T) {
	proj := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(proj, "seed.md"), []byte("seed"), 0o644))
	runGit(t, proj, "add", ".")
	runGit(t, proj, "commit", "-m", "base")

	guardrailDir(t, proj, "unreadable", unreadableDecl, map[string]string{"refuse.sh": alwaysRefuse})
	store := openStore(t)
	baselineAt(t, store, proj)

	stdout, ran := dispatchOut(t, proj, store)

	assert.False(t, ran, "a cycle with an unreadable guardrail has not judged what that guardrail was about")
	assert.Contains(t, stdout, `"decision":"block"`,
		"an unreadable declaration must block the turn — nothing says what it was guarding")
	assert.Contains(t, stdout, "unreadable", "the block must name the file that could not be read")
}

// TestDispatch_UnevaluableMatcherDoesNotPermit.
//
// admits() reported false — "does not apply" — for a matcher that could not be
// EVALUATED, and false is indistinguishable from a rule legitimately not
// matching. The Pre side refuses on exactly this and says why; the comment
// there names the family: "a matcher that cannot be evaluated is not the same
// as a rule that was satisfied".
//
// Finding a trigger takes care, because most ways of writing a bad matcher are
// caught at LOAD and so exercise refuseForBroken rather than this branch. The
// checker rejects `path[9999]` ("type string[int] is undefined"), a malformed
// regex literal, and an out-of-range constant index — all before dispatch.
//
// What is needed is an expression that TYPE-CHECKS and then cannot be evaluated
// on the value that actually arrives. `int(path)` is that: the checker knows
// int() returns an int and accepts the comparison, and the vm then meets the
// string "fresh.md" and reports "invalid operation". Measured, not assumed —
// see the compile/run split in internal/guardrail's matcher tests.
//
// It is also not a contrived shape. Coercing a field and comparing it is an
// ordinary thing to write, and the coercion succeeding depends on the value
// rather than on the declaration, which is precisely the class the load check
// cannot cover.
func TestDispatch_UnevaluableMatcherDoesNotPermit(t *testing.T) {
	proj := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(proj, "seed.md"), []byte("seed"), 0o644))
	runGit(t, proj, "add", ".")
	runGit(t, proj, "commit", "-m", "base")

	// Compiles: int() is declared to return an int, so the comparison checks.
	// Fails at run time on a path that is not a number, which every path is.
	const runtimeFaultDecl = `---
hooks:
  PostFileCreate:
    - matcher: int(path) > 0
      hooks:
        - type: command
          command: ./refuse.sh
---

# A matcher that compiles and cannot be evaluated
`
	guardrailDir(t, proj, "unevaluable", runtimeFaultDecl, map[string]string{"refuse.sh": alwaysRefuse})
	store := openStore(t)
	baselineAt(t, store, proj)
	require.NoError(t, os.WriteFile(filepath.Join(proj, "fresh.md"), []byte("new"), 0o644))

	stdout, ran := dispatchOut(t, proj, store)

	assert.False(t, ran,
		"a matcher that could not be evaluated left the engine unable to say whether the rule applied; "+
			"ending the turn reads that as the rule being satisfied")
	assert.Contains(t, stdout, `"decision":"block"`)
	assert.Contains(t, stdout, "unevaluable", "the block must name the guardrail whose matcher could not be evaluated")
}

// TestDispatch_HookThatCannotStartDoesNotPermit.
//
// dispatchAll's runHooks-error branch `continue`d, which is the third member of
// the family and the one whose own comment on the Pre side says outright that a
// hook that could not be started is "not something to proceed through either".
//
// A NUL byte in the command is the reachable trigger, and it is the same one
// T014_06 drives on the Pre side: it is a valid double-quoted YAML scalar,
// checkExecutable does not judge it because `echo` is not a path reference, so
// the declaration loads SOUND — and exec then fails with "invalid argument"
// before any process exists. Not an ExitError, so none of the exit-status
// handling sees it.
func TestDispatch_HookThatCannotStartDoesNotPermit(t *testing.T) {
	proj := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(proj, "seed.md"), []byte("seed"), 0o644))
	runGit(t, proj, "add", ".")
	runGit(t, proj, "commit", "-m", "base")

	// The NUL is inside a double-quoted YAML scalar, which is where it is legal.
	const nulDecl = `---
hooks:
  PostFileCreate:
    - hooks:
        - type: command
          command: "echo hi\x00there"
---

# A hook the OS will not launch
`
	guardrailDir(t, proj, "cannotstart", nulDecl, nil)
	store := openStore(t)
	baselineAt(t, store, proj)
	require.NoError(t, os.WriteFile(filepath.Join(proj, "fresh.md"), []byte("new"), 0o644))

	stdout, ran := dispatchOut(t, proj, store)

	assert.False(t, ran,
		"the hook never ran, so nothing judged this file; ending the turn reads a failed exec as approval")
	assert.Contains(t, stdout, `"decision":"block"`)
	assert.Contains(t, stdout, "cannotstart", "the block must name the guardrail whose hook could not be started")
}

// TestDispatch_UnknownHookTypeDoesNotPermit.
//
// The same branch by the other route. A hook naming a mechanism this engine
// does not have is caught at LOAD as a declaration fault, so this reaches
// dispatch only through the invalid list — which makes it a second reading on
// the same fix rather than a separate one. Kept because the two arrive by
// different paths and a fix that closed only one would leave the other.
func TestDispatch_UnknownHookTypeDoesNotPermit(t *testing.T) {
	proj := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(proj, "seed.md"), []byte("seed"), 0o644))
	runGit(t, proj, "add", ".")
	runGit(t, proj, "commit", "-m", "base")

	const badTypeDecl = `---
hooks:
  TurnEnd:
    - hooks:
        - type: script
          command: ./refuse.sh
---

# A mechanism this engine does not have
`
	guardrailDir(t, proj, "badtype", badTypeDecl, map[string]string{"refuse.sh": alwaysRefuse})
	store := openStore(t)
	baselineAt(t, store, proj)

	stdout, ran := dispatchOut(t, proj, store)

	assert.False(t, ran)
	assert.Contains(t, stdout, `"decision":"block"`)
	assert.Contains(t, stdout, "badtype")
}

// TestDispatch_BrokenDeclarationDoesNotBlockUnrelatedCycles.
//
// The other half of the scoping the Pre side keeps, and the reason a fix here
// must not be "refuse everything". A typo in a rule about COMMANDS must not
// block a cycle no rule was written about — otherwise the only way out is
// deleting the rule, which is the mistake guardrail.Fault warns about one door
// along.
//
// PreCommandInvoke is a Pre kind and no Post dispatch ever produces it, so a
// declaration broken only there affects no cycle. TurnEnd fires regardless and
// nothing is bound to it here, so this cycle has nothing outstanding.
func TestDispatch_BrokenDeclarationDoesNotBlockUnrelatedCycles(t *testing.T) {
	proj := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(proj, "seed.md"), []byte("seed"), 0o644))
	runGit(t, proj, "add", ".")
	runGit(t, proj, "commit", "-m", "base")

	const preOnlyBroken = `---
hooks:
  PreCommandInvoke:
    - matcher: nosuchfield == "x"
      hooks:
        - type: command
          command: ./refuse.sh
---

# Broken, and about commands only
`
	guardrailDir(t, proj, "cmdtypo", preOnlyBroken, map[string]string{"refuse.sh": alwaysRefuse})
	store := openStore(t)
	baselineAt(t, store, proj)
	require.NoError(t, os.WriteFile(filepath.Join(proj, "fresh.md"), []byte("new"), 0o644))

	stdout, ran := dispatchOut(t, proj, store)

	assert.True(t, ran,
		"a rule broken only on a Pre command binding has nothing to say about a cycle that changed a file")
	assert.NotContains(t, stdout, `"decision":"block"`)
}

// TestDispatch_DisabledBrokenGuardrailStaysInert.
//
// disabled_guardrail_inert, against the new refusal path. Validate returns
// nothing for a disabled declaration, so it never becomes Invalid and must not
// reach any of the refusals above — turning a rule off has to actually turn it
// off, including when the rule is also wrong.
func TestDispatch_DisabledBrokenGuardrailStaysInert(t *testing.T) {
	proj := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(proj, "seed.md"), []byte("seed"), 0o644))
	runGit(t, proj, "add", ".")
	runGit(t, proj, "commit", "-m", "base")

	const disabledBroken = `---
enabled: false
hooks:
  PostFileCreate:
    - matcher: content startsWith "x"
      hooks:
        - type: command
          command: ./refuse.sh
---

# Off, and also wrong
`
	guardrailDir(t, proj, "parked", disabledBroken, map[string]string{"refuse.sh": alwaysRefuse})
	store := openStore(t)
	baselineAt(t, store, proj)
	require.NoError(t, os.WriteFile(filepath.Join(proj, "fresh.md"), []byte("new"), 0o644))

	stdout, ran := dispatchOut(t, proj, store)

	assert.True(t, ran, "a disabled guardrail contributes no refusals, whatever else is wrong with it")
	assert.NotContains(t, stdout, `"decision":"block"`)
	assert.False(t, strings.Contains(stdout, "parked"))
}

// dispatchOut is dispatchIn, returning STDOUT instead of stderr.
//
// Stdout is the only channel that reaches the agent from a Stop hook: the
// command exits 0 and blocks by writing {"decision":"block"} there, so a claim
// about what the agent was told has to be read off this stream and not the
// other. dispatchIn returns stderr, which is why it cannot answer any question
// in this file.
func dispatchOut(t *testing.T, proj string, store sessionstate.Store) (string, bool) {
	t.Helper()
	var stdout bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetOut(&stdout)
	ran := runPostDispatch(cmd, store, HookPayload{Cwd: proj})
	return stdout.String(), ran
}
