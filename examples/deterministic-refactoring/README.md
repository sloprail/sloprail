# deterministic-refactoring

A guardrail that makes "I moved this code" a checkable claim.

When an agent says it split a file or lifted a function out, the honest version
and the dishonest one produce diffs that read the same. The dishonest one
regenerated the code from memory of what it did, and the behaviour that changed
in the process is found later by whoever believed the word "move".

This rule catches that, with no model and no judgement call. The agent marks the
file it is creating with where the code came from — a path, a commit, and a line
range — and the hook reads those lines out of git at that commit and diffs them
against the bytes about to be written. Same bytes, or same bytes after renames
the agent declared, and the write proceeds. Anything else is refused, with the
diff.

## Copying it into your project

Copy the guardrail folder:

    cp -R examples/deterministic-refactoring/.sloprail/guardrails/deterministic-refactoring \
          your-project/.sloprail/guardrails/

The hook must be executable — `chmod +x verify-move.sh` if your copy did not
preserve the bit. A hook that cannot run refuses rather than permits, so a
missing bit shows up as everything being blocked, not as the rule going quiet.

It needs `jq` and `git` on PATH, and the project must be a git repository — the
origin is a commit, and a check that cannot read that commit refuses rather than
falling back to the working tree. There is no configuration and no state: the
rule reads one event and the commit it names.

## Using it

Mark the file carrying moved code with its origin, using `sr-mark`:

    sr-mark apply moved-from --src/big.go@$(git rev-parse HEAD):9-11=src/beta.go:1

which writes:

    // sr:moved-from src/big.go@a1b2c3d:9-11
    func Beta(n int) string {
    	return fmt.Sprintf("beta-%d", n)
    }

The origin is `<path>@<sha>:<start>-<end>` — all four parts in the marker's fqn,
because that is the only field on a marker that can carry them. `line` says
where the marker sits, not how far what it describes extends.

**The commit is not optional.** A range alone names a file as it is *now*, so if
the source is edited after the copy is taken the check compares against the
wrong bytes and says nothing about it. The sha pins what was actually copied,
and lets the check ask git rather than the working tree. A marker with no sha is
refused, and so is one naming a commit this checkout cannot reach — never a
silent fall back to whatever the file says today.

Renaming as you move is allowed if you say so:

    // sr:moved-from src/big.go@a1b2c3d:9-11
    // sr:moved-rename Beta=Gamma

`GUARDRAIL.md` is the full rule, including the argument for where the origin
range lives and what that form cannot express, and a section on what the rule
cannot catch. Read that section before relying on it — the short version is that
it checks files being CREATED that CARRY a marker, so it makes a declared move
honest rather than making declaration mandatory.

## What proves it works

`tests/e2e/examples/deterministic_refactoring` drives these exact files through
the real plugin, in both directions: a byte-identical move lands, a move with a
declared rename lands, an ordinary write is untouched, and only the writes whose
bytes disagree with their declared source are stopped. The tests build a real
git repository and take a real sha, so the commit half of the origin is
exercised rather than described. They read the guardrail out of this directory
rather than carrying a copy, so they cannot keep passing after the shipped
example breaks.
