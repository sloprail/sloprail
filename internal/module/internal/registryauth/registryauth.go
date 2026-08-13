// Package registryauth is the compile-time fence around building a registry.
//
// It holds nothing but a type whose zero value is the permission to construct
// one. The value is worthless; the IMPORT PATH is the whole mechanism. This
// package sits under internal/module/internal/, which Go lets only packages
// rooted at internal/module/ import — internal/module itself, and
// internal/module/modules, and nothing else in the repo. A hook point under
// services/, or any package outside that subtree, cannot name this type, so it
// cannot call module.NewRegistry, so it cannot assemble a module list of its
// own.
//
// This exists because of a real hole. A hook point that called
//
//	module.NewRegistry(append(modules.All(), sneaky.New())...)
//
// enforced against a vocabulary `guardrail help` never printed and no test
// could see: only the help command's registry was observable from outside the
// binary, so a divergent one at any other hook point was undetectable. Making
// every registry observable would mean a test per hook point, each of which the
// next hook point could forget. Making a divergent one impossible to write is
// one fence that no future hook point can forget to stand behind.
//
// Go's `internal` rule is the only visibility mechanism in the language that
// scopes by importing package rather than by identifier case, which is why the
// fence is a directory rather than a lint or a naming convention. A convention
// is what internal/module/registry.go already argues against: collisions are
// caught at registration "rather than a naming convention they would both have
// to remember to follow." The same standard applies to this.
package registryauth

// Token is the permission to construct a registry. Its zero value is valid —
// there is nothing to get right, and nothing to forge, because possessing the
// type at all requires being inside the fence.
type Token struct{}

// Grant returns the token. A function rather than an exported variable so that
// no caller can hold a reference to a shared one and none is allocated.
func Grant() Token { return Token{} }
