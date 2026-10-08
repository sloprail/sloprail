# Gate

A gate is a checkpoint on an event. It wakes on the events its `on:` names, checks its
preconditions and its checks, and refuses the event if any of them refuses. Then it is done.

```yaml
# .sloprail/gate/require-topic-skill/gate.yaml
# A write under memories/topics/ waits until the document-topic skill was loaded.
on:
  - event: PreFileWrite
    match: 'event.path startsWith "memories/topics/"'
require:
  - skill: document-topic
```

```yaml
# .sloprail/gate/artifact-produced/gate.yaml
# When a tag was declared this turn, the turn may not end without its artifact.
on:
  - event: Stop
    match: 'context["tag-declared"].active'
require:
  - context: tag-declared
checks:
  - script: ./verify-artifact.sh
```

| key | |
|---|---|
| `on` | the triggers: each an `event` kind and an optional `match` over `event.*` and `context` ([matchers.md](matchers.md)) |
| `require` | preconditions that must already hold ([below](#require-preconditions)) |
| `checks` | the [scripts](script-checks.md) and [judges](judge-checks.md) that decide, in order |
| `enabled` | `false` makes the gate ship off until a project switches it on ([SKILL.md](SKILL.md#turning-a-rule-off)) |

A gate can trigger on any event before an action (a file write or delete, a command, a tool
call) and on `Stop` ([events.md](events.md)).

## Preventing a write or a delete

Bind a gate to `PreFileWrite` (a create or an update) or `PreFileDelete`; the two are separate,
so name both when a delete is a change too. The gate refuses before the bytes land or the file
goes:

```yaml
on:
  - event: PreFileWrite
    match: 'event.path startsWith "spec/"'
  - event: PreFileDelete
    match: 'event.path startsWith "spec/"'
checks:
  - script: ./check.sh
```

- **Once per file.** A call that changes several files (`rm a b`) wakes the gate once for each,
  and is refused whole if any file is refused.
- **What it sees.** A create carries the new content, an update both old and new, a delete the
  bytes about to be lost ([events.md](events.md#file-events)). A recursive `rm -r` or `mv` of a
  folder becomes one delete per file inside it; a delete the parser cannot see, such as
  `find -delete` or a script, reaches only the committed changeset.
- **Keep it cheap.** A gate on writes holds a `require` and scripts. Put a judge in a
  file-guard of the same name, which judges what was committed, and keep both: the gate stops
  the common case before it lands, and the file-guard catches what the gate could not see. A
  script both need lives once in the file-guard's folder, as a library each side sources
  (`. "$(dirname "$0")/../../file-guard/<name>/<script>-lib.sh"`).

Start the gate's script from [gate-check-template.sh](gate-check-template.sh).

### An unknown result is yours to refuse

When sloprail cannot work out what a write will leave, such as a `sed -i`, a `git apply` or a
notebook edit, the event carries `resultKnown: false` and an empty `newContent`. That reads
exactly like a write that empties the file, and nothing refuses it for you. A gate whose
decision reads the content must refuse it, or the write lands unchecked. The template does
this; to refuse in the trigger instead:

```yaml
on:
  - event: PreFileWrite
    match: 'event.path startsWith "spec/" and not event.resultKnown'
checks:
  - script: ./cannot-verify.sh   # refuses: "write the file content directly"
```

On a `PreFileDelete`, `oldContentKnown` plays the same part for the bytes being lost.

## Ending a turn: the Stop gate

`Stop` fires once at the end of every cycle, whether or not anything changed. It is the
trigger for a rule about the result of a turn: "the turn promised an artifact; did it produce
one?" `Stop` carries no fields, so a Stop gate finds its subject in what a context recorded
([state-management.md](state-management.md)) or in the transcript.

A refused Stop is handed back to the agent, and its next reply is judged the same way, until
one passes. `.sloprail/config.yaml` caps how many refusals in a row a turn takes:

```yaml
stop_hook_block_cap: 8   # the default; 0 for no cap
```

A Stop gate runs on every cycle, so a fault in it blocks every turn. Refuse on the rule's own
logic, and let the turn through when its machinery fails
([state-management.md](state-management.md#fail-closed-on-the-logic-open-on-the-plumbing)).

## Gating a command

A `PreCommandInvoke` gate matches the programs the command line runs, flattened, rather than
the raw line, so a pipe, a subshell or a `sudo` cannot hide one ([events.md](events.md#precommandinvoke-a-command-line-about-to-run)):

```yaml
on:
  - event: PreCommandInvoke
    match: 'any(event.invocations, .bin == "npm" and "next" in .flags.tag)'
```

## `require`: preconditions

A gate refuses when a requirement is unmet, before its checks run, so `require` can be the
whole rule:

```yaml
require:
  - skill: document-topic             # the skill was loaded this session
  - skill: authoring-guardrails       # and these pages of it were read
    files: [file-guard.md]
  - context: tag-declared             # the context is active, and entered this cycle first
  - citation: {source_types: [user]}  # the action cites the user's words
```

- **`skill`** holds the gate until the named skill was loaded. Loading a skill reads only its
  `SKILL.md`; add `files` to require pages it links to. `require` applies to the whole gate, so
  each pairing of a path and a skill is its own gate.
- **`context`** holds the gate until the context has run its `enter` this cycle, so whatever
  the context recorded is current when the check reads it. A gate that reads a context's
  records without this reads the previous cycle's ([state-management.md](state-management.md#reading-another-rules-records)).
- **`citation`** holds the action until it cites what the user said (`user`) or a tool's output
  (`tool_result`). Only a gate on a command or a file event may require one; it fails to load
  on `Stop` or `PreToolUse` ([grounding.md](grounding.md)).

In the Stop example above, `match: 'context["tag-declared"].active'` skips the gate when the
context never activated, and `require` makes sure the context ran first. A sibling gate with
`match: 'not context["tag-declared"].active'` and no `require` catches the turn with no tag.
