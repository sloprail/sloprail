---
enforced: true
---

# A check that could not run must refuse, not permit

**Mistake:** `exit 0` on the script's own internal error — a missing dependency,
an unreadable payload, a `jq` that is not installed. A precondition that permits
when it could not check has consented, and from outside that is
indistinguishable from checking and approving.

**The contract** (`internal/dispatch/exec.go`):

- A **script** check: `exit 0` permits; any non-zero exit refuses. The reason is a
  `{"reason": "..."}` JSON object on **stdout** (preferred), falling back to plain
  text on stderr. So a refusal both exits non-zero AND names why.
- A **judge** verdict: the model emits `{"pass": true|false, "reasoning": "..."}`.
  `pass: false` refuses and `reasoning` is what the agent is shown, so it must
  name the specific problem. The engine's judge substrate fails **closed** on its
  own machinery failure (no model, a timeout, an unparseable verdict) — a judge
  cannot fail open on model flakiness.

**Instead:** on an error path, exit non-zero and name what was missing, so the
wiring is fixed rather than the write. A deliberate fail-open is legitimate — a
model judge must not wedge a session on a timeout — but it is a **local
override**: the body must say so, say why, and say which line restores the
default. An undocumented `exit 0` on an error path is indistinguishable from a
bug.

**Flag** an error path (a missing tool, an empty required field, a failed read)
that reaches `exit 0` — permitting without having checked — unless the script's
own prose declares it a deliberate, explained fail-open. Do NOT flag the ordinary
`exit 0` by which a check permits a clean payload.
