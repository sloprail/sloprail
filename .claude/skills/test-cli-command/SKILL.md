---
name: test-cli-command
description: Use when implementing e2e tests for CLI commands or MCP tool handlers — tests that exercise the compiled binary as a subprocess against a sandboxed git repo
---

# Test CLI Command

## Overview

**Input:** Feature or command behavior to verify

**Output:** `tests/e2e/{feature}/{group}/test_{group_num}_{test_num}_{name}_test.go`

**Test ID format:** `T{group_num}_{test_num}` where:
- `group_num`: 3-digit group number (e.g., 001, 002)
- `test_num`: 2-digit sequence within group (e.g., 01, 02)

## Organization Pattern

E2e tests live either in the root `tests/e2e/` tree (for root `a10n` CLI commands) or inside the service directory (for service commands):

```
tests/
  e2e/                                    # root CLI tests (a10n config, a10n server, …)
    main_test.go                          # doc only — no tests here
    config/
      001_set_get/
        main_test.go                      # shim
        test_001_01_roundtrip_test.go

services/
  index-server/
    e2e/                                  # index-server service tests
      001_cold_build/
        main_test.go                      # shim
        test_001_01_empty_repo_build_test.go
      002_incremental/
        main_test.go                      # shim
        test_002_01_incremental_adds_file_test.go
```

**Rule:** If the test exercises a command that lives in `services/{service}/`, put the test in `services/{service}/e2e/`. If it exercises a root `a10n` command (e.g., `a10n config`), put it in `tests/e2e/`.

**Pre-built — do not recreate:**
- `tests/e2etest/e2etest.go` — all helper implementations, `Main(m)` builds both binaries

**Each group dir needs a shim `main_test.go`** (copy verbatim):
```go
package e2e

import (
    "testing"
    "github.com/a10n-build/a10n-cli/tests/e2etest"
)

func TestMain(m *testing.M) { e2etest.Main(m) }

var (
    run              = e2etest.Run
    runWithEnv       = e2etest.RunWithEnv
    initTestRepo     = e2etest.InitTestRepo
    writeFile        = e2etest.WriteFile
    gitCommitAll     = e2etest.GitCommitAll
    isolatedIndexDir = e2etest.IsolatedIndexDir
    headCommit       = e2etest.HeadCommit
)
```

**When to add a group folder:**
- New logical scenario group within a feature (e.g., `001_build`, `002_incremental`)

**When to add a feature folder:**
- New top-level command group (e.g., `index/`, `config/`)

## From Spec to Code

### Setup → Sandboxed Repo

```markdown
**Setup:** Python repo with 3 committed files
```

```go
repo := initTestRepo(t)
writeFile(t, repo, "src/main.py", `def main(): pass`)
writeFile(t, repo, "src/utils.py", `def helper(): pass`)
gitCommitAll(t, repo, "add files")
```

### Steps → Binary Invocation

```markdown
**Steps:** Run `a10n index build --source <repo>`
```

```go
out, code := run(t, "index", "build", "--source", repo)
require.Equal(t, 0, code, "unexpected output: %s", out)
```

### Expected Result → Assertions

```markdown
**Expected Result:** Status shows 2 files indexed, 0 errors
```

```go
out, code = run(t, "index", "status", "--json", "--source", repo)
require.Equal(t, 0, code)
goldie.Assert(t, "index-status-after-build", []byte(out))
```

## Full Example

```go
// services/index-server/e2e/001_cold_build/test_001_01_empty_repo_test.go
package e2e

import (
    "testing"
    "github.com/sebdah/goldie/v2"
    "github.com/stretchr/testify/require"
)

// T001_01: build on empty repo exits 0, status shows 0 files
func TestT001_01_EmptyRepoBuildSucceeds(t *testing.T) {
    repo := initTestRepo(t)

    out, code := run(t, "index", "build", "--source", repo)
    require.Equal(t, 0, code, out)

    out, code = run(t, "index", "status", "--json", "--source", repo)
    require.Equal(t, 0, code, out)
    goldie.New(t).Assert(t, "index-status-empty", []byte(out))
}
```

## Available Helpers (from main_test.go)

```go
run(t, args...)                      // invoke binary, return (output, exitCode)
runWithEnv(t, env, args...)          // same, with extra env vars
initTestRepo(t)                      // git init + empty commit in t.TempDir(), return path
writeFile(t, repo, path, content)    // write file relative to repo
gitCommitAll(t, repo, msg)           // stage all + commit
isolatedIndexDir(t)                  // returns (dir, []string{"A10N_INDEX_DIR=..."})
headCommit(t, repo)                  // returns current HEAD SHA
```

## Golden Files

`github.com/sebdah/goldie` — expected output in `testdata/golden/*.golden`. Update with:
```bash
go test -update ./tests/e2e/...
```

Used for JSON output shapes (`--json` flag). For plain text output, use `assert.Contains`.

## Embedding Fixtures

Semantic search tests load pre-generated vectors instead of calling the API:
```go
loadEmbeddingFixture(t, db, "testdata/embeddings/simple-py.bin")
```
Generation requires `OPENAI_API_KEY` — offline only, not in CI.

## What to Cover

| Scope | Examples |
|---|---|
| Exit codes | 0 on success, non-zero on bad state or missing args |
| Output shape | JSON via goldie, human text via Contains |
| Flag wiring | `--index-dir`, `--embedding-model`, env var overrides (`A10N_*`) |
| Error messages | Meaningful stderr on bad input, not a panic |

## Running

```bash
make e2e                                                                      # all
go test -tags fts5 -race -v ./tests/e2e/config/...                           # root CLI feature
go test -tags fts5 -race -v ./services/index-server/e2e/...                  # index-server all
go test -tags fts5 -race -v ./services/index-server/e2e/001_cold_build/      # one group
```

Run them as is, from inside a Claude Code session too: no `env -u CLAUDECODE ...` prefix. The
harness builds every spawned process's env from `harness.HostEnv()`, which strips the enclosing
session's CLAUDECODE / CLAUDE_CODE_* / CLAUDE_PROJECT_DIR / CLAUDE_PLUGIN_ROOT / CLAUDE_CONFIG_DIR
and any SLOPRAIL_* / SR_* the test did not set. A helper of your own that spawns a process must
start from `harness.HostEnv()`, never a bare `os.Environ()`.

## Common Mistakes

| Mistake | Fix |
|---|---|
| Put test directly in feature folder | Use numbered group subfolder |
| Missing shim in group dir | Each group dir needs its own `main_test.go` shim |
| Assert on stdout for data | Use `--json` + goldie for structured output |
| Recreate helpers in test file | They're re-exported by the shim — use directly |
| Share state between tests | Each test calls `initTestRepo` + `isolatedIndexDir` — fully isolated |
| Forget `isolatedIndexDir` | Without it tests share the real `~/.local/share/a10n/index` |
