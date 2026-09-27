---
name: mock-conformance
description: Use before or after writing code in this repo that emulates a real Claude Code hook event — this project marks such code so its behavior can be checked against Anthropic's own published docs.
---

# Mock Conformance Marking

This repo emulates real Claude Code hook behavior. Any code that implements
or changes how a specific hook event is fired, its payload shape, or its
field names must carry a marker pointing at the real documentation it is
supposed to match.

Add, as its own line in the file, next to the code that implements the hook:

```
// sr:docs <URL>
```

using this repo's own comment syntax (`//` for Go). The `a10n:docs <URL>`
comments across the tree are the older spelling of the same citation; nothing
reads them any more. New or changed code writes `sr:docs` — copying the
`a10n:docs` spelling from nearby code leaves the new code uncited. The URL should point at the specific page of Anthropic's Claude Code hooks
documentation that describes the hook event being implemented.

## Why

Emulating a real system's behavior without a way to check the emulation
against the real thing's current documentation is how a mock silently drifts
out of date — the marker is what lets that be checked later, against
whatever the docs say at the time, not what they said when the code was
written.
