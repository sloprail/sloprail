---
name: implement-file-store
description: Use when adding a file-based store — reading or writing typed content at predictable paths under a root directory (XDG data dir, project state dir, .a10n dotdir)
---

# Implement File Store

## Overview

**Input:** Content that needs to persist at a typed path

**Output:** A store struct with typed read/write methods that own all path construction — callers never build paths themselves.

## Path Construction

The store owns the root and constructs all paths from typed identifiers:

```go
// internal/{domain}store/{domain}store.go
package domainstore

import (
    "os"
    "path/filepath"
)

// Store reads and writes domain files under root.
type Store struct {
    root string
}

func New(root string) *Store {
    return &Store{root: root}
}

// pluginsPath returns the absolute path to plugins.yaml.
func (s *Store) pluginsPath() string {
    return filepath.Join(s.root, "plugins.yaml")
}

// hookScriptPath returns the path to a named hook script.
func (s *Store) hookScriptPath(name string) string {
    return filepath.Join(s.root, "task-manager", "hooks", name+".sh")
}
```

## Read / Write Methods

```go
import (
    "encoding/json"
    "fmt"
    "os"

    "gopkg.in/yaml.v3"
)

// ReadPlugins reads plugins.yaml. Returns zero value when absent.
func (s *Store) ReadPlugins() (PluginRegistry, error) {
    data, err := os.ReadFile(s.pluginsPath())
    if os.IsNotExist(err) {
        return PluginRegistry{}, nil
    }
    if err != nil {
        return PluginRegistry{}, fmt.Errorf("domainstore: read plugins: %w", err)
    }
    var reg PluginRegistry
    if err := yaml.Unmarshal(data, &reg); err != nil {
        return PluginRegistry{}, fmt.Errorf("domainstore: parse plugins: %w", err)
    }
    return reg, nil
}

// WritePlugins writes plugins.yaml, creating parent dirs as needed.
func (s *Store) WritePlugins(reg PluginRegistry) error {
    if err := os.MkdirAll(filepath.Dir(s.pluginsPath()), 0o755); err != nil {
        return fmt.Errorf("domainstore: mkdir plugins: %w", err)
    }
    data, err := yaml.Marshal(reg)
    if err != nil {
        return fmt.Errorf("domainstore: marshal plugins: %w", err)
    }
    if err := os.WriteFile(s.pluginsPath(), data, 0o644); err != nil {
        return fmt.Errorf("domainstore: write plugins: %w", err)
    }
    return nil
}
```

## Content Types

| Content | Format | Method |
|---|---|---|
| Config / registry | YAML | `gopkg.in/yaml.v3` |
| Structured data | JSON | `encoding/json` |
| Scripts / text | Plain text | `os.ReadFile` / `os.WriteFile` |
| Binary blobs | Bytes | `os.ReadFile` / `os.WriteFile` |

## Root Resolution

Roots follow XDG conventions — resolve in the caller (config or main), pass into the store:

```go
// Typical root sources:
dataDir := filepath.Join(os.Getenv("XDG_DATA_HOME"), "a10n")      // persistent user data
stateDir := filepath.Join(dataDir, "projects", projectID, "state") // per-project state
dotdirRoot := filepath.Join(repoPath, ".a10n")                     // repo dotdir
```

Never resolve the root inside the store — the store is root-agnostic.

## Common Mistakes

| Mistake | Fix |
|---------|-----|
| Caller constructs paths | Store owns all path construction — caller passes typed identifiers |
| `os.IsNotExist` not handled on read | Return zero value + nil error for absent optional files |
| Missing `MkdirAll` before write | Create parent dirs before writing |
| Root resolved inside the store | Resolve root in the caller; inject into the store constructor |
