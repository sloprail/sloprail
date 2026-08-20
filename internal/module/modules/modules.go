// Package modules holds the one list of modules a build ships with.
//
// It sits here rather than in the binary because of who else has to know. The
// hook points and the load check all run inside the binary
// and could have read a list defined there; a test cannot. A test asking which
// kinds exist, with the list unreachable, has no option but to write its own —
// and that copy is the worst one of all, because it is the copy the binary is
// judged against. That is not hypothetical: the help e2e held `filemod` alone,
// and registering `commandmod` broke it. The second list appeared inside the
// test written to prevent second lists.
//
// So the list is exported, and every caller — the binary and the tests that
// check it — reaches the same function. Adding a module is one edit here, and
// nothing can be checked against a vocabulary the engine does not have.
//
// A package of its own rather than internal/module itself, which the modules
// import: a list there would import them back.
//
// This is the only place that may call module.NewRegistry. That is enforced by
// TestOnlyModulesPackageBuildsARegistry rather than by the compiler — see
// module.NewRegistry for why a compile-time fence was tried and dropped.
package modules

import (
	"github.com/sloprail/sloprail/internal/commandmod"
	"github.com/sloprail/sloprail/internal/cyclemod"
	"github.com/sloprail/sloprail/internal/filemod"
	"github.com/sloprail/sloprail/internal/module"
	"github.com/sloprail/sloprail/internal/tagmod"
	"github.com/sloprail/sloprail/internal/tooluse"
)

// All returns the modules this build knows about, in registration order.
//
// The list itself, and the only place it is written down. Registry wraps it in
// the registry every caller actually wants; this is exported for the rare
// caller that wants the modules and not the index over them.
func All() []module.Module {
	return []module.Module{
		filemod.New(),
		commandmod.New(),
		// The tool call itself, before any file or command meaning is derived
		// from it. It DECLARES PreToolUse, the harness-native pre-action moment a
		// gate or context binds to when neither a file nor a command event fits.
		tooluse.New(),
		// Tags. It DECLARES PostTagWrite, the one bulk event carrying every
		// `#tag` the agent wrote this cycle, so a context can bind a tag directly
		// instead of re-grepping the trajectory in its own enter script.
		tagmod.New(),
		// The cycle itself. It extracts nothing — a cycle ending is the hook
		// point rather than anything a harness reports — but it is what
		// DECLARES Stop, and a kind no module declares cannot be bound to:
		// the loader rejects the binding outright. Without this entry the
		// dispatcher fires Stop into a build where no guardrail is permitted
		// to name it.
		cyclemod.New(),
	}
}

// Registry returns a registry over All.
//
// The only registry a build has. Enforced by
// TestOnlyModulesPackageBuildsARegistry, which fails if anything outside this
// package calls module.NewRegistry.
//
// One constructor rather than a list written out wherever a registry is needed.
// The hook points run it to produce events, the load check runs it to tell an
// author which kinds exist, and the load check runs it to decide whether a
// declaration binds to an event that exists — all from the same registry, so
// the help cannot document a vocabulary the engine does not have and a
// guardrail cannot pass validation only to bind to nothing at enforcement.
//
// It returns an error because registration is where a collision is caught: two
// modules claiming one kind, or one registered twice. With the list fixed at
// compile time that error means the build is wrong rather than the input, which
// is why TestRegistry_RegistersCleanly exists to catch it before a caller does.
func Registry() (*module.Registry, error) {
	return module.NewRegistry(All()...)
}
