package main

// The service's harnesses: importing an implementation registers it
// (internal/harness.Register); only service mains choose which are available, and
// harness.Current picks the one this process runs under (SLOPRAIL_HARNESS, then
// environment detection, then Claude Code).
import (
	_ "github.com/sloprail/sloprail/internal/harness/claudecode"
	_ "github.com/sloprail/sloprail/internal/harness/codex"
)
