---
name: document-invariant
description: Use when judging or writing an invariant — the atomic domain rules a spec asserts. What a GOOD invariant IS: an always-true, field-anchored statement of a domain truth. Reused by the spec-quality checks AND the authoring agent. For CLI mechanics see `a10n-spec apply --help`.
---

# Document Invariant

An invariant is one always-true rule about the domain's data. Invariants are the "physics" of the
spec: they combine by AND — the spec is consistent only when every one holds at once. An invariant
has a `name` (its id) and a `predicate` (the rule). Nothing else is judged.

## The rules

### An invariant states a domain truth, not an implementation mechanism

The predicate states WHAT is always true about the data, not HOW the code keeps it true. The test:
could someone who has never seen the codebase — only the domain — state and check this rule? If yes,
it's a domain truth. If it only makes sense to someone reading the implementation, it's a mechanism.

Reject a predicate ONLY when its subject is a named code artifact — a specific SQL/ORM operation
(`upsert`, `INSERT … ON CONFLICT`) or a code path (an HTTP route, a named query/function/handler,
"the SELECT"). Everything else is a domain truth. In particular do NOT reject a predicate for how it
phrases the outcome: an action, an actor's role, a state change, retention, visibility, or "hidden
from / found by / listed in / stored as" are all valid ways to state a data fact — they describe
WHAT holds about the data, which any domain expert can check without reading the code.

- ❌ `Re-recording an existing Session leaves its autopilot_enabled unchanged.` (subject is a SQL upsert)
- ❌ `The GET /orders endpoint never returns a cancelled order.` (subject is a code path)
- ✅ `A Session's {@fld:session:Session.autopilot_enabled} changes only by an explicit toggle.`
- ✅ `A User's {@fld:user:User.role} changes only when the acting user is an admin.` (action + actor)
- ✅ `A soft-deleted {@fld:user:User.deletedAt} User is excluded from active-member listings.` (visibility)

### An invariant must be violable and matter

The system must be able to plausibly get it wrong, and it must matter when it does. If negating the
predicate is meaningless, it only restates the schema (an enum value, a foreign key) and is not an
invariant — do not author it.

- ❌ `A SessionFolder points at an existing session.` (a foreign key; "points at a non-existent session" is impossible by construction)
- ❌ `A session's spec clone is the SessionFolder whose role is spec.` (restates the role enum)
- ✅ `A cancelled {@fld:order:Order.status} is reachable only from pending or confirmed.` (the system could get this wrong; it matters)

### An invariant is always-true, not a sequence of steps

State a condition that holds at all times. "When X, Y", "After X, Y", or a value fixed "as of" /
"at" some point (`the total as of order creation`) are all FINE — they state a condition and its
always-true consequence. Reject ONLY a predicate that narrates a runtime ORDERING of internal
operations (`until`, `advances after`, `then step N runs`) with no data condition.

- ✅ `An {@fld:order:Order.total} equals the item sum as of order creation.` (a fixed-point fact, always true)
- ❌ `base_ref advances only after the checks pass, and not until the tree is clean.` (narrates a runtime sequence)

### A predicate must anchor on a real domain construct via a typed mention

Every predicate names at least one real field `{@fld:domain:Entity.field}` or entity
`{@ent:domain:Entity}`. Anchor on a field when the rule is about a value; on an entity when the rule
is about a relationship between entities. Untyped text like `Product.active` is not a reference; the
target must already exist.

- ✅ `An inactive Product {@fld:catalog:Product.active} is not visible to customers.` (a value rule)
- ✅ `Every {@ent:order:Order} has exactly one {@ent:notification:Notification}.` (a relationship rule)
- ❌ `An inactive product is hidden from customers.` (no typed anchor — name the field: `{@fld:catalog:Product.active}`)

### A single-value field bound is a constraint_expr, not an invariant

A length, range, format, or enum membership on one field's own value lives on the field
(`constraint_expr`/`enum`) — see document-entity. Author an invariant only for uniqueness or a rule
spanning MULTIPLE fields/entities that no single field can express.

- ❌ invariant `name is 1..120 chars` → belongs as `constraint_expr: len 1..120` on the field
- ✅ invariant `A Product's {@fld:catalog:Product.reservedInventory} never exceeds its {@fld:catalog:Product.inventory}.` (two fields)
