# Cycle event hooks

The cycle module has **one** kind, fired once when a work cycle ends. Get its
name from the load check — this page is what the name does not tell you.

It is dispatched at the end of a cycle, from the `Stop` and `SubagentStop` hook
points, last and unconditionally: it fires whether or not anything changed,
which is what a rule about the turn as a whole wants.

## It carries no fields at all

That absence is the declaration, not an oversight. The end of a cycle is about
the cycle rather than about one file, which is why what changed is reported as
the file module's own per-file events instead of a list this one carries.

Two consequences:

**A matcher has nothing to narrow on.** Naming any field on this kind is refused
when the guardrail loads, with the same message a misspelled field on any other
kind gets — rather than loading, evaluating against nothing, and never firing.
So a binding to this kind is written without a matcher, and the hook runs every
cycle.

**A rule has to establish its own subject.** Either by asking `sr-session query`
about the transcript, or by having per-file rules record what they saw into
per-guardrail state for this one to read. That second pattern is the usual one
and it has a trap in it — evidence recorded in one cycle satisfying a claim made
in a later one: [state-management.md](state-management.md).

## A refusal here

It is reported to the agent as a blocking error on the cycle, and the cycle's
read mark does not advance — so the next `Stop` judges the same span again, and
a rule that stays unsatisfied stays reported rather than scrolling away.

That re-judging is also why a hook bound to this kind must not clear the state it
read; see the state page.

Because this kind fires on every cycle, a rule bound to it can wedge a session
wholesale rather than for one file. Refuse on the rule's logic, and permit when
the rule's own plumbing fails.
