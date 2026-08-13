// Package registryauth is the compile-time fence around building a registry.
//
// It holds nothing but a type whose zero value is the permission to construct
// one. The value is worthless; the mechanism is who can obtain one at all.
//
// That takes TWO rules, and the fence is only as good as the weaker of them.
//
//  1. The import path. This package sits under internal/module/internal/, which
//     Go lets only packages rooted at internal/module/ import — internal/module
//     itself, and internal/module/modules, and nothing else in the repo. So a
//     hook point under services/ cannot write the name registryauth.Token, and
//     cannot call Grant.
//
//  2. The unexported field on Token. Rule 1 governs who may NAME the type; on
//     its own it says nothing about who may construct a value of the same
//     shape, and Go assignability is structural for unnamed types. While Token
//     was a bare `struct{}` the fence was decorative: see the note on Token.
//
// With both, a package outside the subtree can neither name the type nor forge
// one, so it cannot call module.NewRegistry, so it cannot assemble a module
// list of its own. Rule 1 alone was believed sufficient here for a while, and
// stating it that way is what let the hole survive review — the claim was
// about the import path when it needed to be about the set of reachable
// values.
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
//
// Being the only such mechanism is why it is rule 1. It is not why it is
// enough — identifier case, the mechanism it is usually contrasted with, is
// what rule 2 turned out to still need.
package registryauth

// Token is the permission to construct a registry. Its zero value is valid —
// there is nothing to get right, and nothing to forge, because possessing the
// type at all requires being inside the fence.
//
// The unexported field is load-bearing, and the fence does not hold without
// it. Go assignability is structural for unnamed types: if this were a bare
// `struct{}`, an untyped composite literal `struct{}{}` would be assignable to
// it, and a caller outside the fence would never have to NAME the type at all —
//
//	module.NewRegistry(struct{}{}, modules.All()[:1]...)
//
// compiled clean, vetted clean, and gave a hook point a divergent module list
// with no new type anywhere for discovery to find. The `internal` rule scopes
// who may write `registryauth.Token`; it says nothing about who may write a
// value that happens to have the same shape. A field named `_` — unexported,
// so unwritable and unnameable from another package — makes the two rules line
// up: the only way to obtain a Token is Grant, and the only way to reach Grant
// is to be inside the fence.
//
// Both halves are pinned in internal/module/modules/modules_test.go, by two
// different witnesses, because go/types sees the assignability rule and only
// the go command sees the `internal` rule.
type Token struct{ _ struct{} }

// Grant returns the token. A function rather than an exported variable so that
// no caller can hold a reference to a shared one and none is allocated.
func Grant() Token { return Token{} }
