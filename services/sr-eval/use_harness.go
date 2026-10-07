package main

// The service's harness: Claude Code is the only one. Importing the implementation
// registers it (internal/harness.Register); only service mains choose.
import _ "github.com/sloprail/sloprail/internal/harness/claudecode"

// Cursor is registered too: which one a process runs is decided by harness.Current
// (the plugin names it with SLOPRAIL_HARNESS, else detection, else Claude Code).
import _ "github.com/sloprail/sloprail/internal/harness/cursor"
