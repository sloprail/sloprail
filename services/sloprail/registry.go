package main

import (
	"github.com/sloprail/sloprail/internal/commandmod"
	"github.com/sloprail/sloprail/internal/filemod"
	"github.com/sloprail/sloprail/internal/module"
)

// registry returns the modules this build knows about.
//
// One constructor rather than a list written out wherever a registry is needed.
// The hook points run it to produce events, and `guardrail help` runs it to tell
// an author which kinds exist — from the same registry, so the help cannot
// document a vocabulary the engine does not actually have. A second list would
// be the copy an author trusts, and the one nobody remembers to update when a
// module is added.
func registry() (*module.Registry, error) {
	return module.NewRegistry(filemod.New(), commandmod.New())
}
