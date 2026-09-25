# sloprail

Your agents slop. Take control.

Skills get skipped. Prompts get ignored. Everything drifts into slop and
dies there. sloprail is the layer that holds the line — structure the agent
can't wander out of, checked against what actually happened, not what it
claimed.

![What sloprail is made of](docs/assets/bento.png)

<!-- HERO GIF -->

## Install

```
/plugin install sloprail@sloprail-marketplace
```

sloprail installs as a plugin. Once it's in, every tool call and turn-end
runs through it — you don't run anything by hand.

## Skills aren't enough

A skill is a request, not a guarantee — and it might not even
[load](https://sloprail.com/docs/use-cases/knowledge/acted-without-context).
sloprail is the check that runs no matter what the agent does, and reads the
real result, not the agent's word for it.

## You've seen these happen

<!-- USE-CASE GRID -->

- [The task gets rewritten to match the work](https://sloprail.com/docs/use-cases/tasks/task-rewritten)
- [After enough compaction, it games the score instead of doing the work](https://sloprail.com/docs/use-cases/long-runs/compaction-gaming)
- [A request fell through the cracks](https://sloprail.com/docs/use-cases/tasks/request-fell-through)
- [The same fact, copied into two files, now disagreeing](https://sloprail.com/docs/use-cases/knowledge/duplicated-knowledge)
- [It acted without loading what it needed first](https://sloprail.com/docs/use-cases/knowledge/acted-without-context)
- [The "mechanical" refactor silently rewrote your code](https://sloprail.com/docs/use-cases/coding/refactor-regenerated)

## Docs → [sloprail.com/docs](https://sloprail.com/docs)

## How it works

A project declares guardrails under `.sloprail/guardrails/`; the harness
calls the session hook points, and the engine runs whichever guardrails bind
to what is about to happen.

Full docs, including how to write a guardrail:
[sloprail.com/docs](https://sloprail.com/docs).
