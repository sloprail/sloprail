---
name: write-model-mocked-tests
description: Use before writing or editing a test file under tests/ — this project's tests must never call a real model.
---

# Write Model-Mocked Tests

`models.ALLOW_MODEL_REQUESTS = False` is set globally so a test cannot
accidentally hit a real LLM for cost, latency, and reproducibility. A test
that exercises an `Agent` must replace its model rather than call it for
real.

- **`TestModel`** — the default choice. Calls every tool once, then returns
  either plain text or output matching the agent's schema. No custom
  behavior to write; use it unless the test needs to control what a
  specific tool call returns.
- **`FunctionModel`** — when the test needs specific tool arguments or a
  specific sequence of calls, pass a plain Python function that receives the
  message history and returns a `ModelResponse`.
- Swap the model with `agent.override(model=TestModel())` (or
  `FunctionModel(...)`) as a context manager around the call under test, or
  via a `pytest.fixture` when many tests in a file need the same override.

A test file that never overrides its agent's model, or that asserts on real
model output instead of `TestModel`'s deterministic one, is not exercising
this project's application code reliably — it is exercising API variance.
