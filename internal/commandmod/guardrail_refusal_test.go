package commandmod_test

// This file is the end-to-end proof of what the unwrapping is FOR, and it is a
// separate external test package on purpose: it goes through the real matcher
// rather than reading a bin list, so nothing here can pass because the test
// knew what to look for.
//
// The claim the module makes to a rule author is that a rule about npm fires on
// every line that runs npm. `internal/commandmod`'s own tests establish that
// `sh -c "npm publish"` reports npm; that is a fact about a slice. What an
// author actually relies on is that the RULE refuses, which is a fact about the
// matcher evaluating a real event built from a real command line. Those are the
// same thing only if the event carries the invocations in the shape the matcher
// reads — so this asserts the whole path, end to end.
//
// The two cases are the two halves of the same judgement:
//
//	sh -c "npm publish"   MUST be refused. Not refusing it is the failure the
//	                      product exists to prevent: a rule that silently never
//	                      fires looks exactly like a rule being satisfied.
//	sh -c "$CMD"          MUST be admitted. Refusing it would mean the module
//	                      guessed at a payload it cannot read, and a guardrail
//	                      that invents what a command does is worse than one
//	                      that admits it cannot tell.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/commandmod"
	"github.com/sloprail/sloprail/internal/guardrail"
)

// refuses reports whether a rule fires on a command line, going through the
// real extraction and the real matcher.
//
// The polarity is worth stating because it inverts. A matcher returns whether
// the event is ADMITTED by the expression; a rule of this shape names what is
// forbidden, so a match is a refusal.
func refuses(t *testing.T, rule, command string) bool {
	t.Helper()
	m, err := guardrail.CompileMatcher(rule)
	require.NoError(t, err, "rule %q must compile", rule)

	matched, err := m.Match(commandmod.ExtractCommand(command).Event())
	require.NoError(t, err, "matching %q", command)
	return matched
}

// TestGuardrail_RefusesNpmPublishThroughAnInterpreter is the case the whole
// task exists for.
//
// A rule forbidding `npm publish` was defeated by one of the most ordinary
// things an agent writes — running it through a shell. `sh -c` is how a harness
// packs a compound command into a single tool call, and a harness that wraps
// everything in `bash -lc` made EVERY command rule cover nothing at all.
// sr:proves events/command-nesting-flattened
func TestGuardrail_RefusesNpmPublishThroughAnInterpreter(t *testing.T) {
	const rule = `any(invocations, .bin == "npm")`

	// The direct form, which always worked. Here as the control: if this ever
	// fails, the failures below mean nothing.
	assert.True(t, refuses(t, rule, `npm publish`),
		"the control case must refuse, or the rest of this test proves nothing")

	for _, command := range []string{
		`sh -c "npm publish"`,
		`sh -c 'npm publish'`,
		`bash -c "npm publish"`,
		`bash -lc "npm publish"`,
		`bash -lc 'cd /x && npm publish'`,
		`bash -euxc "npm publish"`,
		`zsh -c 'npm publish'`,
		`dash -c 'npm publish'`,
		`sh -cx "npm publish"`,

		// Behind a wrapper, which is how it is written when the agent also
		// needs privileges or an environment.
		`sudo sh -c 'npm publish'`,
		`xargs sh -c 'npm publish'`,
		`env FOO=1 bash -lc 'npm publish'`,
		`timeout 60 sh -c 'npm publish'`,

		// Nested one deeper — a harness wrapping the agent's own `sh -c`,
		// which is the case the task names.
		`bash -lc 'sh -c "npm publish"'`,

		// The interpreter spelled as a path.
		`/bin/sh -c 'npm publish'`,

		// The payload buried in shell structure the module already flattens.
		`sh -c 'npm publish' | tee log`,
		`(sh -c 'npm publish')`,
		`if true; then sh -c 'npm publish'; fi`,

		// The other tables' entries, which reach npm by the same fix.
		`su -c "npm publish"`,
		`flock /tmp/lock -c "npm publish"`,
		`setsid npm publish`,
		`exec npm publish`,
		`command npm publish`,
	} {
		t.Run(command, func(t *testing.T) {
			assert.True(t, refuses(t, rule, command),
				"a rule forbidding npm must refuse %q — it runs npm. A rule that "+
					"silently never fires is worse than one that will not load: "+
					"the first looks like a rule being satisfied.", command)
		})
	}
}

// TestGuardrail_DoesNotGuessAtAnUnreadablePayload is the other half, and it is
// the half that keeps the fix honest.
//
// Every case here RUNS something this module cannot read. The tempting answer
// is to assume the payload is whatever it resolves to under an empty
// environment, which would refuse several of these — and would be a guardrail
// reporting a program nobody can show the line invokes.
//
// `sh -c "np${X}m publish"` is the sharpest of them: it resolves to exactly the
// string `npm publish`, character for character identical to the case above, and
// is still not knowable. Only literalness separates the two, which is why
// literalness is the test rather than the resolved value.
// sr:proves events/command-undecidable-not-guessed
func TestGuardrail_DoesNotGuessAtAnUnreadablePayload(t *testing.T) {
	const rule = `any(invocations, .bin == "npm")`

	for _, tc := range []struct {
		command string
		why     string
	}{
		{`sh -c "$CMD"`, "the payload is a parameter; its value needs the runtime environment"},
		{`sh -c $CMD`, "same, unquoted"},
		{`bash -lc "$CMD"`, "same, through the clustered spelling"},
		{`sh -c "np${X}m publish"`,
			"it resolves to `npm publish` ONLY because the empty environment assumed ${X} was empty"},
		{`sh -c "npm publish --tag $TAG"`,
			"any non-literal part makes the whole payload uncertain"},
		{`sh -c "$(cat run.sh)"`, "the payload does not exist until cat runs"},
		{`eval "$CMD"`, "eval's payload is a parameter; its value needs the runtime environment"},
		{`eval "npm publish --tag $TAG"`, "eval joins its words: one non-literal word makes the whole payload uncertain"},
		{`echo bnBtIHB1Ymxpc2g= | base64 -d | sh`, "the decoded payload does not exist until base64 runs"},
		{`sh deploy.sh`, "the payload is a file; its contents are not in the command line"},
		{`sh -s "npm publish"`, "-s reads the script from stdin; the string is $0, not code"},
		{`python -c "import os; os.system('npm publish')"`, "the payload is Python, not shell"},
	} {
		t.Run(tc.command, func(t *testing.T) {
			assert.False(t, refuses(t, rule, tc.command),
				"a rule about npm must NOT refuse %q — %s. Refusing means the module "+
					"guessed, and a guardrail that invents what a command does is worse "+
					"than one that admits it cannot tell.", tc.command, tc.why)
		})
	}
}

// TestGuardrail_RuleAboutTheInterpreterStillFires is the regression the fix
// itself could cause.
//
// Unwrapping ADDS what a payload runs. An implementation that replaced the
// interpreter with its payload would close the npm gap and open an identical
// one on `sh` — a rule about shelling out would stop firing on the lines that
// shell out. Fixing one rule by breaking another is not a fix.
// sr:proves events/command-nesting-flattened
func TestGuardrail_RuleAboutTheInterpreterStillFires(t *testing.T) {
	const shellRule = `any(invocations, .bin == "sh" || .bin == "bash")`

	for _, command := range []string{
		`sh -c "npm publish"`,
		`bash -lc 'cd /x && npm publish'`,
		`sudo sh -c 'npm publish'`,
		`sh -c "$CMD"`,
		`sh -c 'sh -c "npm publish"'`,
	} {
		t.Run(command, func(t *testing.T) {
			assert.True(t, refuses(t, shellRule, command),
				"a rule about the interpreter must still fire on %q — unwrapping adds "+
					"to what is reported, it does not replace the wrapper", command)
		})
	}
}

// TestGuardrail_ArgumentsOfTheUnwrappedProgramReachTheRule: a rule is rarely
// just a program name. `npm publish` is forbidden where `npm ping` is fine, and
// that distinction lives in argv — so the payload's arguments have to arrive
// with it, not just its program word.
func TestGuardrail_ArgumentsOfTheUnwrappedProgramReachTheRule(t *testing.T) {
	const rule = `any(invocations, .bin == "npm" && "publish" in .argv)`

	assert.True(t, refuses(t, rule, `sh -c "npm publish"`),
		"the payload's arguments must reach the rule, not only its program name")
	assert.True(t, refuses(t, rule, `bash -lc 'cd /x && npm publish --tag next'`))

	// The same rule must not fire on a different npm subcommand reached the
	// same way — otherwise the argv it received was not really the payload's.
	assert.False(t, refuses(t, rule, `sh -c "npm ping"`),
		"a rule about `npm publish` must not fire on `npm ping` run through a shell")

	// Flags parsed out of the payload reach the rule too.
	const flagRule = `any(invocations, .bin == "npm" && "tag" in .flags)`
	assert.True(t, refuses(t, flagRule, `sh -c "npm publish --tag=next"`))
	assert.False(t, refuses(t, flagRule, `sh -c "npm publish"`))
}
