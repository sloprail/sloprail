package e2e

import "github.com/sloprail/sloprail/tests/e2e/harness"

// New stands up an isolated environment: the binary built, this repo's plugin
// enabled from this repo's marketplace, an isolated HOME.
var New = harness.New
