---
name: implement-package
description: Use when adding a new internal package, a new file to an existing package, or a helper function — covers DDD boundaries, provider pattern, functional file decomposition, interfaces, error types, and external library isolation
---

# Implement Package

## Overview

**Input:** Package or helper spec (responsibility, dependencies, consumers)

**Output:** `internal/{domain}/{file}.go` or `services/{service}/internal/{domain}/{file}.go`

## Placement Decision: Global vs Service-Local

The monorepo has two `internal/` trees:

| Tree | Path | Used by |
|---|---|---|
| **Global** | `internal/` | All binaries — `a10n`, all services |
| **Service-local** | `services/{service}/internal/` | That service only |

Put the package where it belongs by consumer scope:

- `internal/store` — shared by root CLI and any service that reads the index
- `internal/config` — shared; XDG paths + AppConfig
- `services/index-server/internal/indexer` — only index-server needs this
- `services/index-server/internal/parser` — only index-server needs this
- `services/index-server/internal/embed` — only index-server needs this

When adding a new domain that only one service will ever use, put it in `services/{service}/internal/`.

## Architecture Philosophy

### Domains own their resource

Each package owns exactly one external resource or concern. Nothing else touches it.

```
internal/store   → SQLite (only package that imports mattn/go-sqlite3)
services/index-server/internal/parser  → tree-sitter
services/index-server/internal/embed   → embedding APIs
internal/config  → XDG + config.yaml
```

Interface layers (`services/{svc}/internal/mcp/`, service command files) own nothing — they wire and delegate.

### Many small files, not one big file

Split by responsibility within a package. Each file does one thing.

```
store/
  db.go          # connection init, extension loading
  migrate.go     # schema versioning
  files.go       # files + files_fts table ops
  chunks.go      # chunks + chunk_embeddings vec0 ops
  symbols.go     # symbols + calls + imports ops
  overlay.go     # ATTACH-based overlay merge queries
```

Not `store/store.go` with everything. If a file grows past ~150 lines, it's doing too much — split it.

### Provider pattern for multiple implementations

When a domain has multiple implementations (different APIs, local vs remote), define one interface and one file per provider:

```
services/index-server/internal/embed/
  client.go      # Client interface + Config struct
  openai.go      # openAIClient — OpenAI-compatible HTTP
  nomic.go       # nomicClient — local model runner
```

```go
// services/index-server/internal/embed/client.go
type Client interface {
    Embed(ctx context.Context, texts []string) ([][]float32, error)
}

type Config struct {
    Provider string // "openai" | "nomic"
    BaseURL  string
    APIKey   string
    Model    string
    Dims     int
}

func Open(cfg Config) (Client, error) {
    switch cfg.Provider {
    case "openai":
        return newOpenAIClient(cfg)
    case "nomic":
        return newNomicClient(cfg)
    default:
        return nil, fmt.Errorf("embed: unknown provider %q", cfg.Provider)
    }
}
```

Consumers call `embed.Open(cfg)` and get back `embed.Client` — never know the concrete type.

## Placement Decision

**New package** — the thing has a clear domain boundary, owns a resource, other packages import it.

**New file in existing package** — extends a domain, shares its types, not imported independently. Same `package X` declaration.

## API Design

Constructor returns interface, not concrete type:

```go
// ✅ returns interface
func Open(cfg Config) (Client, error)

// ❌ leaks concrete type
func Open(cfg Config) (*openAIClient, error)
```

Export the minimum. Start unexported, promote only when a consumer requires it.

## Error Types

Sentinel errors for expected failures, wrapped errors for unexpected:

```go
var (
    ErrEmptyInput  = errors.New("embed: input cannot be empty")
    ErrRateLimited = errors.New("embed: rate limited")
)

// callers: errors.Is(err, embed.ErrRateLimited)
// never: strings.Contains(err.Error(), "rate limited")
```

## External Library Isolation

Library imports stay inside the owning package — never leak to consumers:

```go
// internal/store/db.go — only place in the codebase with this import
import _ "github.com/mattn/go-sqlite3"
```

Expose your own types at the boundary, not the library's types.

## SQLite (store package)

New table → new migration + new file:

```
store/
  migrations/
    001_initial.sql
    002_add_symbols.sql   ← new file, never edit existing
  symbols.go              ← ops for the new table
  symbols_test.go
```

Never modify existing migration files. Always bump schema version in `migrate.go`.

## Tree-sitter (parser package)

New language → new file. 6-pass order is fixed: module registry → type defs → functions → calls → imports → exports. Pass 1 must complete before FQN resolution in passes 2–6.

## Common Mistakes

| Mistake | Fix |
|---|---|
| One big file per package | Split by responsibility; ~150 line ceiling per file |
| Concrete type from constructor | Return interface; keep struct unexported |
| Import external library in multiple packages | Isolate to owning package only |
| Match errors by string | Sentinel errors + `errors.Is` |
| Add second implementation to existing file | New file per provider |
| Edit existing migration | Add new migration file |
