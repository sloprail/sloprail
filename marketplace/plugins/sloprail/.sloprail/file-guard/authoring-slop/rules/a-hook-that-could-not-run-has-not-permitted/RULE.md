---
enforced: false
---

# A hook that could not run has not permitted

**Mistake:** `exit 0` on the hook's own internal error.

A precondition that permits when it could not check has not checked — it has
consented, and from outside that is indistinguishable from checking and
approving. The engine holds the same line from its side: a hook that cannot be
executed at all is a refusal.

**Instead:** exit non-zero and name what was missing, so the wiring gets fixed
rather than the write.

A deliberate fail-open is legitimate — a model judge must not wedge a session on
a timeout — but it is a **local override**: the body must say so, say
why, and say which line to change to restore the default. An undocumented
`exit 0` on an error path is indistinguishable from a bug.

## Why this is not enforced mechanically

`exit 0` is correct on most paths — it is how a hook permits. Telling an error
path from a permit path needs the surrounding logic, and a check that guessed
would fire on every well-written hook. Documented, left to review.
