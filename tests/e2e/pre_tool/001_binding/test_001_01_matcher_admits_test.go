package e2e

import "testing"

// One guardrail, bound to a file about to be created, narrowed to a directory.
// Its hook refuses whatever it is shown — so what these tests prove is which
// events reach it, not what it decides once they do.
const refuseUnderGuarded = `---
hooks:
  PreFileCreate:
    - matcher: path startsWith "guarded/"
      hooks:
        - type: command
          command: ./refuse.sh
---

# Refuses any write it is shown

Unconditional on purpose: a test about binding should fail when the wrong event
arrives, not when the right event is judged differently.
`

const refuseScript = `#!/bin/sh
cat >/dev/null
echo '{"decision":"block","reason":"this path is guarded"}'
exit 1
`

// T001_01: the agent tries to write where a rule is bound, and is refused.
//
// The positive half of hook_within_binding. Nothing here calls sloprail: the
// agent writes, the harness fires its PreToolUse hook, the plugin reaches our
// subcommand, and the refusal travels back through the tool result.
func TestT001_01_MatcherAdmitsWrite(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "guarded-dir", refuseUnderGuarded, map[string]string{"refuse.sh": refuseScript})

	got := e.Run(proj, "s-001-01", "write a note", Turns("done",
		Write("w1", "guarded/notes.md", "hello"),
	))

	if !got.Saw("this path is guarded") {
		t.Fatalf("want the refusal to reach the agent, got:\n%s", got.Output)
	}
}

// T001_02: the same agent writing outside the binding is left alone.
//
// The negative half, and the one that catches a matcher that admits
// everything — a guardrail refusing every write looks identical to a correct
// one until something outside its scope is tried.
func TestT001_02_MatcherRejectsOtherPath(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "guarded-dir", refuseUnderGuarded, map[string]string{"refuse.sh": refuseScript})

	got := e.Run(proj, "s-001-02", "write a note", Turns("done",
		Write("w1", "elsewhere/notes.md", "hello"),
	))

	if got.Saw("this path is guarded") {
		t.Fatalf("a path outside the binding was refused:\n%s", got.Output)
	}
}

// T001_03: a project that declares nothing sees no difference.
//
// The engine has no opinion of its own. Every refusal traces to something a
// project declared, and one that declared nothing should be indistinguishable
// from not having installed this.
func TestT001_03_NoDeclarationsPermits(t *testing.T) {
	e := New(t)
	proj := e.Project()

	got := e.Run(proj, "s-001-03", "write a note", Turns("done",
		Write("w1", "guarded/notes.md", "hello"),
	))

	if got.Saw("denied") || got.Saw("blocked") {
		t.Fatalf("a project with no guardrails saw a refusal:\n%s", got.Output)
	}
}
