# deterministic-refactoring

A guardrail that makes "I moved this code" a checkable claim.

When an agent says it split a file or lifted a function out, the honest version
and the dishonest one produce diffs that read the same. The dishonest one
regenerated the code from memory of what it did, and the behaviour that changed
in the process is found later by whoever believed the word "move".

This rule catches that, with no model and no judgement call. The agent declares
where the code came from, in a header on the file it is creating; the hook reads
those lines off disk and diffs them against the bytes about to be written. Same
bytes, or same bytes after renames the agent declared, and the write proceeds.
Anything else is refused, with the diff.

## Copying it into your project

Copy the guardrail folder:

    cp -R examples/deterministic-refactoring/.sloprail/guardrails/deterministic-refactoring \
          your-project/.sloprail/guardrails/

The hook must be executable — `chmod +x verify-move.sh` if your copy did not
preserve the bit. A hook that cannot run refuses rather than permits, so a
missing bit shows up as everything being blocked, not as the rule going quiet.

Nothing else is needed. There is no configuration and no state: the rule reads
one event and the file it names.

## Using it

Open a file carrying moved code with its origin:

    // sloprail:moved-from src/big.go:9-11
    func Beta(n int) string {
    	return fmt.Sprintf("beta-%d", n)
    }

Renaming as you move is allowed if you say so:

    // sloprail:moved-from src/big.go:9-11
    // sloprail:rename Beta=Gamma

`GUARDRAIL.md` is the full rule, including a section on what it cannot catch.
Read that section before relying on it — the short version is that it checks
files being CREATED that DECLARE an origin, so it makes a declared move honest
rather than making declaration mandatory.

## What proves it works

`tests/e2e/examples/deterministic_refactoring` drives these exact files through
the real plugin, in both directions: a byte-identical move lands, a move with a
declared rename lands, an ordinary write is untouched, and only the writes whose
bytes disagree with their declared source are stopped. The tests read the
guardrail out of this directory rather than carrying a copy, so they cannot keep
passing after the shipped example breaks.
