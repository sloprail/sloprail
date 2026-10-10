# Keeping state across cycles

`sr-session state` is a key-value store a rule writes in one cycle and reads in another. A rule
needs it when what it must check is not in the event in front of it: something seen in one
cycle and answered in the next, or facts gathered from several events and judged once at the
end.

```sh
prev=$(sr-session state get seen 2>/dev/null || echo 0)
sr-session state set seen "$((prev + 1))"
```

A script passes only the key. The rule and the session come from the environment
(`SR_GUARDRAIL`, `SR_SESSION_ID`; [script-checks.md](script-checks.md#the-environment)), so a
rule reads and writes only its own records in this session. Run outside a rule, `state` says
there is no rule in scope. See `sr-session state --help` for the subcommands.

## A context records, a Stop gate judges

A gate on `Stop` gets its subject from a context ([events.md](events.md#stop-a-cycle-ended)). The
usual shape is two rules:

- a **context** whose `enter` runs on the events where something happened (a tag was written,
  a file landed) and records each one, one key per subject, reaching no verdict;
- a **Stop gate** that reads those records back and decides.

The recording side should exit 0 on every path, its own failures included. What it records has
already happened, so there is nothing for it to block. When it cannot record, it says so on
stderr; the gate then finds no evidence for the claim and refuses it, which is the safe
direction.

### Reading another rule's records

The gate reads the context's records, not its own, with `--owner`:

```sh
entries="$(sr-session state list --owner tag-declared 2>/dev/null)"
tags="$(printf '%s' "$entries" | jq -s -r '[.[] | select(.key | startswith("tag:"))] | .[].key | ltrimstr("tag:")')"
```

- **Pair it with `require: [{context: tag-declared}]`**, so the records are this cycle's
  ([gate.md](gate.md#require-preconditions)).
- **`list` prints one JSON object per line**, not an array. Slurp it with `jq -s` before
  treating it as a list: `jq '[.[] | …]'` on the raw lines comes back empty, and the gate then
  refuses every correct turn.
- Each value is stored as a string; read it with `jq -r .value`, and parse JSON inside it with
  a second `jq`.

`--owner` works only with `list`, only reads, and only within this session: a rule can never
write another rule's records.

## Scoping the evidence to the turn

Records outlive the cycle that wrote them, by design. So a recorder that stores `wrote: yes`
lets a write made in turn 3 satisfy a claim made in turn 9, and the rule looks like it works
while it checks nothing.

**Store the turn, not a flag.** Keep one key per subject, whose value identifies the turn that
last touched it. The judge counts a subject only when its value equals the current turn.

Identify a turn as `<count>:<uuid>`: how many real user messages the transcript holds, and the
uuid of the last one. A real user message is an entry with `type == "user"` whose
`message.content` is a string; tool results are also `type: "user"` but carry an array, and
they arrive several times within one turn. The count matters as well as the uuid, because the
same uuid can appear twice when a refusal is handed back to the agent.

Compute it in one script both rules source. If the recorder and the judge ever disagree about
which turn it is, every claim misses its evidence or finds someone else's.

## Do not clear records as you judge

A refused Stop is judged again on the next cycle, so the agent can fix what was refused. A
judge that deleted records as it read them would face that retry with its evidence gone, and
refuse a write the agent really made. A stamped record needs no clearing: once its turn is
past it no longer counts.

The stamp is needed even though most events already belong to one turn. Rewriting a file with
the same bytes it already held produces no event at all, so without the stamp an earlier
turn's record would satisfy the later turn's claim.

## Fail closed on the logic, open on the plumbing

A Stop gate runs every cycle, so a fault in it blocks every turn. Keep the two directions
apart:

- **The logic fails closed.** A claim with no matching evidence is refused; that is the rule.
- **The plumbing fails open**, saying why on stderr: no transcript, a query that returns
  nothing, output that does not parse, and above all a turn that cannot be identified. Without
  the turn no record can be matched to it, so every claim would look unproven and correct work
  would be refused.
