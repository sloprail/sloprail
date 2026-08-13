---
name: test-package
description: Use when implementing unit tests for domain helpers with complex, encapsulated logic — key derivation, overlay merge queries, FQN resolution, config precedence, and other pure or near-pure functions inside internal/ domain packages
---

# Test Package

## Overview

**Input:** A domain helper function with non-trivial logic

**Output:** `internal/{domain}/{helper}_test.go` — next to the source file

Unit tests cover **domain logic** — not CLI commands or MCP tool handlers. Those are tested via e2e tests.

Write unit tests when:
- Logic has branching, edge cases, or non-obvious correctness requirements
- Function is pure or near-pure (no network, no real filesystem)
- Helper is reused across multiple packages

## File Placement

Test file lives **next to** the source it tests:

```
internal/
  store/
    overlay.go
    overlay_test.go       ← same package
services/index-server/internal/
  indexer/
    keys.go
    keys_test.go          ← same package
  parser/
    python.go
    python_test.go        ← same package
```

Never put unit tests in `tests/` — that's e2e only.

## Implementation Pattern

```go
// services/index-server/internal/indexer/keys_test.go
package indexer

import (
    "testing"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestDeriveBaseKey_ValidInput(t *testing.T) {
    key, err := deriveBaseKey("/repo", "abc123")
    require.NoError(t, err)
    assert.Equal(t, "expected-key-value", key)
}

func TestDeriveBaseKey_EmptyCommit(t *testing.T) {
    _, err := deriveBaseKey("/repo", "")
    assert.ErrorIs(t, err, ErrEmptyCommit)
}

func TestDeriveBaseKey_Deterministic(t *testing.T) {
    key1, _ := deriveBaseKey("/repo", "abc123")
    key2, _ := deriveBaseKey("/repo", "abc123")
    assert.Equal(t, key1, key2)
}
```

Use `require` for fatal failures (stops test immediately), `assert` for non-fatal (collects all failures).

## SQLite-backed Helpers

For store-package helpers that need real SQLite, use in-memory DB — no temp files, no cleanup:

```go
// internal/store/overlay_test.go
package store

import (
    "database/sql"
    "testing"
    "github.com/stretchr/testify/require"
)

func TestOverlayMerge_FilesFromBothDBs(t *testing.T) {
    db := openTestDB(t)
    // populate base and overlay tables
    // call function under test
    // assert merged result
}

func openTestDB(t *testing.T) *sql.DB {
    t.Helper()
    db, err := sql.Open("sqlite3_vec", ":memory:")
    require.NoError(t, err)
    require.NoError(t, runMigrations(db))
    t.Cleanup(func() { db.Close() })
    return db
}
```

Construct test data inline — no fixture files needed.

## What to Cover

| Case | Example |
|---|---|
| Happy path | Valid input returns expected output |
| Edge cases | Empty string, zero, boundary values |
| Error sentinels | Typed errors (`errors.Is`) not string matching |
| Invariants | Deterministic output for same inputs |
| Security | Path traversal, injection (if relevant to helper) |

## Running

```bash
go test -tags fts5 -race ./internal/...                                    # root internal unit tests
go test -tags fts5 -race ./services/index-server/internal/...              # index-server unit tests
go test -tags fts5 -race ./services/index-server/internal/indexer/...      # one package
```
