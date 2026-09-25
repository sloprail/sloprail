---
name: mark-endpoints
description: Use when adding a new HTTP endpoint file under src/endpoints/ in this repo — every endpoint must be marked and follow this project's required stack.
---

# Marking a New Endpoint

Every endpoint file under `src/endpoints/` must:

- Be named `<verb>-<resource>.ts` (e.g. `get-users.ts`, `create-order.ts`).
- Carry a marker comment naming it, right above the route definition:

```
// sr:endpoint "<verb>-<resource>"
```

- Use this project's required stack: Express (`Router` from `express`) for
  the HTTP layer, Prisma (`prisma.<model>.<method>(...)`) for data access
  — not a raw SQL client or a different ORM.
- Route paths are `kebab-case`, request/response field names are
  `camelCase` — matching the existing endpoints in this repo.

See `src/endpoints/get-users.ts` for the shape to follow.
