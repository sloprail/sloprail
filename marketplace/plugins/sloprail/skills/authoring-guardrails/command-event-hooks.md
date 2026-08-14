# Command event hooks

The command module has **one** kind, fired before a shell command line runs. Get
its name and fields from the load check — this page is what the names do not
tell you.

One kind, not one per program or one per shell construct. What a rule asks is
whether the agent is about to run something, and that is the same question
whether the something sits in a pipeline or behind a `sudo`.

There is no `Post` counterpart. A file has a settled state a diff can establish
afterwards; a command that already ran has no equivalent — what it changed shows
up as the file module's Post events, which is where a rule about consequences
belongs.

## What the parser resolves

One command line is rarely one program. A pipeline, an `&&` chain, a subshell, a
`sudo` or an `xargs` each nest invocations inside a single string. The module
walks that structure once and emits **every invocation it finds, flattened**, so
no rule has to recurse through shell syntax itself — and so nesting an
invocation one level deeper does not defeat a rule written against it.

So match the invocations list, not the raw string:

```
any(invocations, .bin == "curl")
!any(invocations, .bin == "npm")
len(invocations) > 1
```

The event also carries the raw line. It is what a refusal quotes back — telling
an author that `npm` was refused is less use than showing them the line they
actually wrote — and it is the wrong thing to match on, because matching a
substring of the raw line is what the flattening exists to save you from.

## Inside one invocation

Each invocation carries the program, its argument vector, and its parsed flags.
The program name and the argument vector have **declared element shapes**, so a
mistyped key inside a predicate is refused at load with the real keys named:

```
any(invocations, .bin == "rm" && any(.argv, # == "-rf"))
```

**Flags are left open, and the asymmetry is deliberate.** A flag name belongs to
the command being run, not to this module, so there is no vocabulary to
enumerate — a closed type would refuse a real npm flag because the engine has
not heard of npm. The consequence is that a key read off the flags map is
verified against nothing: a mistyped one compiles, loads, and evaluates false
forever. Cause the command and watch it fire before believing a flags matcher.

## The resolution floor

A program named by a variable, a payload decoded and piped to a shell, splitting
that depends on the runtime `IFS`: none of these can be known without running
them, and running them is exactly what a guardrail must not do. What can be seen
is emitted; what cannot is left alone rather than guessed at.

This is a correctness aid, never a security boundary. A rule written on the
assumption that every way of invoking a program is visible here is a rule that
believes more than the parser promises.
