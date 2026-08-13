---
name: implement-cli-command
description: Use when adding a new Cobra subcommand — covers two contexts: proxy command in root internal/cli/ (forwards to a sub-binary) or service command in services/{service}/ (actual implementation with Viper wiring)
---

# Implement CLI Command

## Overview

**Input:** Command spec (name, flags, behavior) + context (proxy or service)

There are two distinct CLI contexts:

| Context | Location | Purpose |
|---|---|---|
| **Proxy** | `internal/cli/` | Root `a10n` binary — thin proxy that execs a sub-binary |
| **Service** | `services/{service}/` | Service binary — real implementation with Viper + domain calls |

## Proxy CLI (root `internal/cli/`)

The `a10n` binary delegates feature groups to sub-binaries via `execSubBinary`. Add a proxy command when a new service needs to be reachable via `a10n <group> <subcmd>`.

```
internal/
  cli/
    root.go          # PRE-BUILT: root command + persistent flags + Viper setup
    exec.go          # PRE-BUILT: execSubBinary + findSubBinary helpers
    index.go         # proxy: a10n index <args> → execs a10n-index-server
    server.go        # proxy: a10n server <args> → execs a10n-server
```

**Proxy pattern** — one file per service group, no Viper, no flags:

```go
// internal/cli/index.go
package cli

import "github.com/spf13/cobra"

func newIndexCmd() *cobra.Command {
    return &cobra.Command{
        Use:                "index",
        Short:              "Index management — proxied to a10n-index-server",
        DisableFlagParsing: true,   // pass all args through verbatim
        RunE: func(cmd *cobra.Command, args []string) error {
            return execSubBinary(cmd, "a10n-index-server", args...)
        },
    }
}
```

Register in `root.go`:
```go
root.AddCommand(newIndexCmd())
```

`execSubBinary` finds the sub-binary in: (1) same dir as the running `a10n` binary, (2) `~/.a10n/bin/`. Stdin/stdout/stderr are inherited.

## Service CLI (`services/{service}/`)

Service binaries (`a10n-index-server`, etc.) own their own commands as top-level files — no `internal/cli/` subdirectory.

```
services/index-server/
  main.go        # root command + Viper setup + PersistentPreRunE
  build.go       # a10n-index-server build
  rebuild.go     # a10n-index-server rebuild
  status.go      # a10n-index-server status
  search.go      # a10n-index-server search
  mcp.go         # a10n-index-server mcp
```

One file per leaf command. Register in `main.go`'s `newRoot()`.

**Service command pattern:**

```go
// services/index-server/status.go
package main

import (
    "encoding/json"
    "fmt"

    "github.com/spf13/cobra"
    "github.com/spf13/viper"

    "github.com/a10n-build/a10n-cli/services/index-server/internal/indexer"
    "github.com/a10n-build/a10n-cli/internal/store"
)

func newIndexStatusCmd() *cobra.Command {
    cmd := &cobra.Command{
        Use:   "status",
        Short: "Show index status for the current source",
        RunE:  runIndexStatus,
    }
    cmd.Flags().Bool("json", false, "Output as JSON")
    return cmd
}

func runIndexStatus(cmd *cobra.Command, args []string) error {
    sourceDir := viper.GetString("index.source")
    indexDir  := viper.GetString("index.dir")
    asJSON, _ := cmd.Flags().GetBool("json")

    db, err := store.Open(indexDir)
    if err != nil {
        return fmt.Errorf("open store: %w", err)
    }
    defer db.Close()

    status, err := indexer.Status(cmd.Context(), db, sourceDir)
    if err != nil {
        return err
    }

    if asJSON {
        return json.NewEncoder(cmd.OutOrStdout()).Encode(status)
    }
    fmt.Fprintln(cmd.OutOrStdout(), status.String())
    return nil
}
```

## Flag → Viper Wiring (service commands)

Persistent flags are bound once in `main.go`'s `newRoot()`. For command-local flags, bind in the command constructor:

```go
// persistent (main.go newRoot() — already done for index-server, do not duplicate)
root.PersistentFlags().String("index-dir", "", "Index storage directory")
_ = viper.BindPFlag("index.dir", root.PersistentFlags().Lookup("index-dir"))
_ = viper.BindEnv("index.dir", "A10N_INDEX_DIR")

// local flag — bind in command constructor
cmd.Flags().Bool("json", false, "Output as JSON")
// no Viper binding needed for output-format flags
```

Always read values from Viper in `RunE`, not from `cmd.Flags()` directly — Viper resolves flag → env → config file → default.

## Registration

Add the new command to its parent in `main.go`'s `newRoot()`:

```go
// main.go
root.AddCommand(newIndexStatusCmd())
```

## Output (both contexts)

- Write to `cmd.OutOrStdout()` — not `fmt.Print`
- Errors to `cmd.ErrOrStderr()` or return `error`
- JSON output: `json.NewEncoder(cmd.OutOrStdout()).Encode(...)`
- Return errors; do not call `os.Exit` — Cobra handles exit code 1

## Common Mistakes

| Mistake | Fix |
|---|---|
| Adding flags to a proxy command | Proxies use `DisableFlagParsing: true` — flags belong in the service command |
| Business logic in service RunE | Delegate to domain package in `services/{svc}/internal/`; RunE only wires and calls |
| Read flags with `cmd.Flags().GetString` in service | Read from `viper.GetString` for full precedence chain |
| Add persistent flags in service command file | Persistent flags belong in `main.go`'s `newRoot()` only |
| Creating `internal/cli/` inside a service | Service commands live directly in `services/{service}/` as `package main` |
