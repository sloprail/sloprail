package main

// The service's harness: Claude Code is the only one. Importing the implementation
// registers it (internal/harness.Register); only service mains choose.
import _ "github.com/sloprail/sloprail/internal/harness/claudecode"
