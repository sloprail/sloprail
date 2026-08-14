# State across cycles

`sr-session state get|set|list` is a key-value store a guardrail can write in
one cycle and read in another. It is what a rule uses when the thing it must
check is not visible in the event in front of it — a trigger seen in one cycle
and answered in the next, or a fact assembled from several per-file events and
judged once at the end.

```sh
prev=$(sr-session state get seen 2>/dev/null || echo 0)
sr-session state set seen "$((prev + 1))"
```

Neither the guardrail nor the session is an argument. Both come from the
environment the engine sets on every hook it runs — `SR_GUARDRAIL`,
`SR_SESSION_ID`, `SR_WORKSPACE` — so a hook calls it with nothing but a key, and
a hook cannot read a rule it was never told about or reach into another session.
Outside a hook there is no guardrail in scope and it says so rather than
guessing. Run `sr-session state --help` for the subcommands.

## The two-halves pattern

A cycle-wide rule has no subject: the cycle kind carries no fields, so a hook
bound to it knows only that a cycle ended. State is how it gets one.

**Per-file hooks RECORD; a cycle hook JUDGES.** The file events are where the
engine has already decided that a path really changed — it parsed the tool call,
resolved the shell command and diffed the tree — so the recording half writes
down what it was told and reaches no verdict. The cycle half reads what was
recorded and decides.

The ordering this depends on is guaranteed rather than hoped for: the cycle
event is dispatched last and unconditionally, after the per-file events, so a
hook reading what one of them wrote finds the write already made. Unconditional
matters too — a cycle that changed no files still ends, and that is exactly the
cycle a rule of this shape usually has to refuse.

The recording half should exit 0 on every path, including its own failures. It
reaches no verdict, and the write it is being told about has already happened,
so there is nothing left to block. When it cannot record, it says so on stderr
and the cycle loses that evidence — the judge then refuses a claim it cannot
corroborate, which is the fail-closed direction and the right one.

## Scoping the evidence to the cycle

This is the part that fails in the **permissive** direction, which is why it is
worth the space: a rule that gets it wrong looks like it is working and has in
fact stopped checking anything.

State survives across cycles by design. So a naive `state set wrote:updates yes`
in the recorder and a `state get` in the judge lets a write made in cycle 3
satisfy a claim made in cycle 9.

**Store the turn, not a flag.** One key per subject, whose value is the
identifier of the turn that last touched it. The judge counts the subject as
satisfied only when the stored value **equals the current turn**.

### What identifies a turn

How many real user messages the record holds, and the uuid of the last one —
`<count>:<uuid>`.

A real user message is `type == "user"` whose `message.content` is a **string**:
tool-result entries are also `type: "user"` but carry an array, so keying on the
type alone takes the last tool result as the boundary, which moves several times
within one turn.

The uuid alone is not enough, and this was measured rather than reasoned. Across
8,570 transcripts holding 14,638 real user messages, one file carries a
duplicate real-user-message uuid — a stop-hook feedback re-injection, a refusal
handed back to the agent as a user message, written twice with the same uuid,
the same parent and the same timestamp. A cycle-refusing rule is one of the
things that produces those. With the uuid alone, two turns wearing one uuid are
one turn to the rule, and the second is permitted having written nothing.

The count fixes it without depending on the harness minting anything unique: the
record only grows, so the number of real user messages is strictly increasing
across turns and constant within one. The uuid is kept alongside it because the
count alone would make two different sessions' turn 7 the same string.

Compute it in **one sourced helper** that both halves call, never as two copies
of a pipeline. If the recorder and the judge ever disagreed about what the
current turn is, every claim would fail to find its evidence — or find someone
else's.

### Key on the subject, not on the turn

A key per turn accumulates one entry per subject per turn for the life of the
session, and nothing ever collects them. Keying on the subject bounds the
keyspace at one entry per subject, each carrying the last turn that touched it,
and the only transition that ever has to happen is an older turn's value being
overwritten by a newer one.

## Do not clear state as you judge

The obvious alternative — the judge deleting the keys once it has read them — is
wrong, and the reason is the refusal path.

A refusal does not advance the read mark. The engine re-judges the same span on
the next cycle, deliberately, so the agent can fix what was refused. A judge
that cleared as it went would bring a cycle refused for some *other* reason back
round with its recorded evidence already erased, and refuse it a second time for
a write it genuinely made. The agent is then told to write a file it has already
written.

Stamping has no such path. A stale entry is inert, because it names a turn that
is no longer current — nothing has to run for the evidence to expire, which
means nothing can fail to run.

## Revalidation is why the stamp is not redundant

It would be easy to measure the ordinary cases, find the events already
turn-scoped, and drop the stamp. They mostly are: a quiet cycle reports no event
for a file an earlier cycle wrote, and an unrelated write does not re-report it
either.

The case that decides it is revalidation, which is keyed on **content**. If a
later cycle rewrites a file with bytes identical to what an earlier one left
there, **no event fires at all**. Without the stamp, the earlier cycle's
evidence is still sitting in state and satisfies the later cycle's claim.

Refusing there is the strict answer and the right one: a rewrite that changes
nothing has recorded nothing, and a claim asserts something was recorded.

## Fail closed on the logic, open on the plumbing

A rule bound to the cycle runs on every cycle, so it can wedge a session
wholesale rather than for one file. Split the two directions deliberately.

The **logic** fails closed: a claim with no matching evidence is a refusal. That
is the whole rule.

The **plumbing** fails open, each path saying so on stderr — no transcript to
read, the query unavailable or returning nothing, the analysis not parsing, and
above all **the current turn not being identifiable**. Without a turn id no
recorded write can be attributed to this cycle, so every claim would look
unsatisfied and the rule would refuse every cycle: a plumbing failure refusing
correct work.
