package main

import (
	"github.com/sloprail/sloprail/internal/commandmod"
	"github.com/sloprail/sloprail/internal/filemod"
	"github.com/sloprail/sloprail/internal/module"
)

// registry returns the modules this build knows about.
//
// One constructor rather than a list written out wherever a registry is needed.
// The hook points run it to produce events, `guardrail help` runs it to tell an
// author which kinds exist, and the load check runs it to decide whether a
// declaration binds to an event that exists — all from the same registry, so
// the help cannot document a vocabulary the engine does not have and a
// guardrail cannot pass validation only to bind to nothing at enforcement. A
// second list would be the copy an author trusts, and the one nobody remembers
// to update when a module is added.
func registry() (*module.Registry, error) {
	return module.NewRegistry(filemod.New(), commandmod.New())
}
