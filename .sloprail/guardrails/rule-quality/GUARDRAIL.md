---
hooks:
  PreFileCreate:
    - matcher: path endsWith "RULE.md"
      hooks:
        - type: command
          command: ./judge-rule.sh
  PreFileUpdate:
    - matcher: path endsWith "RULE.md"
      hooks:
        - type: command
          command: ./judge-rule.sh
  PostFileCreate:
    - matcher: path endsWith "RULE.md"
      hooks:
        - type: command
          command: ./judge-rule.sh
  PostFileUpdate:
    - matcher: path endsWith "RULE.md"
      hooks:
        - type: command
          command: ./judge-rule.sh
---

# The rules must themselves be high-signal

sloprail's guardrails are made of rules, and a rule is read once by an author
deciding whether their hook has the shape it warns about. A padded rule is not a
harmless one: it is skimmed, the shape is missed, and the hook ships inert. The
four rules under `marketplace/plugins/sloprail/guardrails/authoring-slop/rules/`
set the standard — each names a mistake, shows it, gives the fix, and stops.
This guardrail holds new ones to that.

It lives in the engine repo's own `.sloprail/`, not in the distributed plugin.
It is how this project judges its own rules, not something shipped to projects
that install sloprail; what a project's rules should say is that project's
business.

## The rubric is assembled, not written

This is the structural difference from the sibling judge in the
executive-memory repo, where `RUBRIC.md` *is* the standard.

Here `RUBRIC.md` holds only the **frame** — the judge's job, the conservatism
instruction, the answer shape, and a `<<<META_RULES>>>` marker. The standard
lives in `rules/<name>/RULE.md`, one meta-rule per directory, and
`judge-rule.sh` splices them in at judge time.

So **adding a meta-rule is adding a directory, and never editing a prompt.**
That matters because the alternative — one growing rubric file — makes every
addition a diff against a document holding all the others, where a meta-rule
cannot be pointed at, cannot carry its own enforcement status, and cannot be
removed without touching text that belongs to something else. It is the same
argument `authoring-slop/GUARDRAIL.md` makes for one-rule-one-file, applied to
the rubric a judge assembles rather than to a list a matcher checks.

It also makes the two failure modes separate, as the doc/rubric split did:
editing the frame changes how every rule is judged, editing one `rules/` entry
changes exactly one criterion, and a diff says which happened.

Today there is exactly one meta-rule: `rules/high-signal/` — the minimum text
that still carries the meaning, no noise.

### Ordering

Lexical by directory name, which is what the glob yields. Not mtime, not a
manifest: the prompt must be a pure function of the tree's contents, so two
checkouts produce the same prompt and a diff of `rules/` is a complete account
of what changed. Meta-rules are independent findings, so order carries no
meaning past determinism — a manifest would add a second place to edit and a way
for a rule to be present but silently unlisted.

### Why `enforced:` and not mere presence

Each `RULE.md` carries `enforced: true|false`, and only `true` enters the
prompt. Presence is deliberately not enough.

The four rules in `authoring-slop/` already prove why: two of them
(`a-hook-that-could-not-run-has-not-permitted`, and half of
`content-may-be-unresolvable`) are undecidable by their checker and say so in
their own bodies. A rule worth writing down for the next author is not always a
rule a judge can apply. Without the flag, the only way to record such a
meta-rule would be to keep it out of the repo — losing the writing to save the
judging.

The flag also gives a reversible off switch. Parking a meta-rule that turns out
to fire on taste is a one-word edit, visible in a diff, in the file it belongs
to. The alternative — deleting the directory — throws away the reasoning along
with the enforcement.

### Nothing to judge against is a REFUSAL, not a fail-open

If `rules/` holds no `enforced: true` meta-rule, `judge-rule.sh` **exits 1**.
This is the one place it departs from the fail-open discipline below, and the
departure is the point of composing a rubric at all.

Every other failure here is machinery breaking around a standard that still
exists. An empty `rules/` is the standard being *absent* — the guardrail is
declared, it fires, it loads, and it judges against nothing. A judge with no
criteria cannot find a violation, so permitting in that state is
indistinguishable from a permanent clean bill of health: the inert-but-official
rule sloprail exists to prevent. Refusing is loud and the fix is a decision
someone makes on purpose — add a meta-rule, or disable the guardrail.

A lost `<<<META_RULES>>>` marker fails **open**, not closed, and the asymmetry is
deliberate: an empty `rules/` is a project-state error the author must resolve,
while a mangled frame is this guardrail's own plumbing breaking, which is the
category the fail-open override exists for. It says so on stderr.

## Recursion, and why it terminates

The meta-rules are themselves `RULE.md` files, so writing one fires the rule
that judges them. That is correct and wanted — this guardrail's own rules are
held to the standard they define, and `rules/high-signal/RULE.md` was written
knowing it would be judged by itself.

It terminates because **the judge is not an agent working in this repo.** The
`claude` child runs with `--settings '{"hooks":{},"mcpServers":{},"enabledPlugins":{}}'`
and from `/tmp`, so the sloprail plugin is not loaded inside it and no hook
point exists there to dispatch from. Its one permitted tool is `Write`, and the
only path it is given is a `/tmp` verdict file — which does not match
`path endsWith "RULE.md"`, and is outside the workspace the engine diffs. The
child cannot produce an event this guardrail binds.

So the depth is exactly one, structurally rather than by a counter or a
path exclusion: editing a `RULE.md` causes one judge call, and that call causes
none. The recursion is cut by isolation, which is the same mechanism that stops
the sibling judge recursing from a `Stop` hook, and it holds here for the
stronger reason that the child has no file-writing reach into the repo at all.

**Not excluded by path.** The obvious alternative — a matcher skipping
`.sloprail/guardrails/rule-quality/rules/` — was rejected: it would exempt
exactly the rules that define the standard from the standard, which is the
credibility the guardrail runs on. They are judged like any other.

## Both Pre and Post, and why both

`Pre` prevents: a bloated `RULE.md` is refused before it lands, which is the
useful moment. But a `PreFileUpdate` only carries the pending bytes when the
engine could derive them (`resultKnown`), and `PreFileCreate` is not emitted at
all when the result is unknowable — so an edit made by a script, a `sed`, or a
`git` operation reaches `Pre` with nothing to judge.

Per `rules/content-may-be-unresolvable`, the strategy is written down rather
than guessed: **when the result is not derivable, this rule defers to the Post
kind**, which sees what actually landed on disk whatever produced it. `Pre`
catches the common case before the write; `Post` is the backstop that no
unusual writer slips past. The cost is honest — a `Post` refusal cannot undo the
write, it tells the agent the cycle is not finished.

The script reads the bytes from the **event** on `Pre` and from **disk** on
`Post`. Reading disk on a `Pre` update would judge the pre-edit content and pass
a bloated rewrite of a clean file.

## Scope

Every `RULE.md` in the repo — `path endsWith "RULE.md"`, with no directory
prefix, so a rule written anywhere is caught, not only the four under
`authoring-slop/rules/` that exist today. A prefix matcher would go stale the
first time a guardrail is added somewhere new, and go stale *silently*, which is
the failure `prefer-file-events-over-trajectory` is about.

## Failing open

**This rule permits when its own machinery fails, deliberately overriding
sloprail's fail-closed default**, for the reason the sibling judge records: the
machinery is a network call to a model, model calls time out and return
malformed output, and none of that is evidence about the file. Under
fail-closed one flake refuses a turn the agent cannot fix by changing anything,
and every retry can flake again.

**The verdict fails closed — a flagged rule is refused. The plumbing fails
open.** These `exit 0`, each printing to stderr which path it took so a rule
that has silently stopped judging is visible:

- `claude` not on `PATH`, or the call times out
- `RUBRIC.md` unreadable, or missing its `<<<META_RULES>>>` marker
- the verdict file is missing, empty, or unparseable
- `has_issues` not readably `true`

Two things refuse instead, because neither is a model failure: an event carrying
no `path`, and a payload carrying no `guardrailDir` — that one means the payload
did not come from the engine, and permitting on it is how a hand-made test
looks like a pass while judging nothing.

**To restore the engine's default**, change the `exit 0` lines under the
`FAIL-OPEN` comments in `judge-rule.sh` to `exit 1`. Do not do this while the
judge is a single un-retried model call; make it retry first.

## Timeout

`timeout 25`, not the 60 the sibling judge uses. The engine kills a hook's
process group at `hookTimeout = 30s`
(`services/sr-session/session_pre_tool.go`), so a 60s bound would never be
reached — the engine would kill the judge first, and the fail-open branch that
explains itself on stderr would never run. 25s leaves room to print and exit
before that.
