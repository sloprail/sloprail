package e2e

import (
	"strings"
	"testing"
)

// order_within_binding: within one binding, hooks run in the order they are
// declared.
//
// A rule may sequence its own hooks — one writing what the next reads — and the
// list it wrote them in is the only order it can express. The failure this
// guards against is a map iteration or a concurrent dispatch somewhere in the
// chain, which is invisible until a rule depends on the order and then fails
// intermittently.

// Four hooks in one binding, each appending its own name. Four rather than two:
// two hooks in the wrong order are still in one of two orders, and a shuffle
// has an even chance of looking right. Four make an accidental pass unlikely
// and a stable-but-wrong order obvious.
const orderedHooks = `---
hooks:
  PreFileCreate:
    - hooks:
        - type: command
          command: ./a.sh
        - type: command
          command: ./b.sh
        - type: command
          command: ./c.sh
        - type: command
          command: ./d.sh
---

# Four hooks that each record having run

Declared a, b, c, d. That list is the only way a rule can say one hook runs
before another, so it has to be the order they run in.
`

// step is a hook that records its name and permits, so the next one runs.
func step(name string) string {
	return "#!/bin/sh\ncat >/dev/null\necho " + name + " >> \"$PWD/order\"\nexit 0\n"
}

var orderedScripts = map[string]string{
	"a.sh": step("a"),
	"b.sh": step("b"),
	"c.sh": step("c"),
	"d.sh": step("d"),
}

// T010_01: the hooks of one binding run in the order the declaration lists them.
func TestT010_01_HooksRunInDeclaredOrder(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "sequenced", orderedHooks, orderedScripts)

	e.Run(proj, "s-010-01", "write a note", Turns("done",
		Write("w1", "some/notes.md", "hello"),
	))

	got := e.Ledger(proj, "sequenced", "order")
	want := []string{"a", "b", "c", "d"}

	// Every hook must have run before the order means anything. A binding that
	// ran one hook and stopped would otherwise satisfy a prefix comparison.
	if len(got) != len(want) {
		t.Fatalf("want all %d hooks to run, %d did: %v", len(want), len(got), got)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("hooks ran out of declared order:\n got: %v\nwant: %v", got, want)
	}
}

// T010_02: a hook refusing stops the ones declared after it.
//
// The other half of what an order means. If the later hooks ran anyway, the
// order would be a reporting detail rather than a sequence — a rule could not
// use an early hook as a precondition for a later one, which is the whole
// reason the order is promised.
func TestT010_02_RefusalStopsLaterHooks(t *testing.T) {
	e := New(t)
	proj := e.Project()

	scripts := map[string]string{
		"a.sh": step("a"),
		// b refuses. c and d must not run.
		"b.sh": "#!/bin/sh\ncat >/dev/null\necho b >> \"$PWD/order\"\necho 'stopped at b' >&2\nexit 1\n",
		"c.sh": step("c"),
		"d.sh": step("d"),
	}
	e.Guardrail(proj, "sequenced", orderedHooks, scripts)

	res := e.Run(proj, "s-010-02", "write a note", Turns("done",
		Write("w1", "some/notes.md", "hello"),
	))

	if !res.Saw("stopped at b") {
		t.Fatalf("the refusal never reached the agent:\n%s", res.Output)
	}

	got := e.Ledger(proj, "sequenced", "order")
	if strings.Join(got, ",") != "a,b" {
		t.Fatalf("a refusal did not stop the hooks declared after it: ran %v, want [a b]", got)
	}
}
