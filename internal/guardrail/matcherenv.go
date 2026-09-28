package guardrail

import (
	"github.com/expr-lang/expr/types"

	"github.com/sloprail/sloprail/internal/module"
)

// matcherEnv turns a kind's declared fields into the type environment its
// matchers are checked against.
//
// The translation lives here rather than beside the declarations because a
// module's job is to declare what its events carry, and the engine's is to
// decide what that means for the expression language it happens to use. Keeping
// expr on this side of the boundary is what lets the language change without
// every module changing with it — internal/module has no idea an expression
// language exists.
func matcherEnv(k module.KindDecl) types.Map {
	env := make(types.Map, len(k.Fields))
	for _, f := range k.Fields {
		env[f.Name] = fieldType(f)
	}
	return env
}

// fieldType maps one declared field to what the checker should expect of it.
//
// The rule throughout: check what the module actually said, and nothing more.
// A declaration that stops short is a reason to check less, never a reason to
// refuse a matcher for a fault that is ours rather than the author's.
func fieldType(f module.FieldDecl) types.Type {
	switch f.Type {
	case module.TypeString:
		return types.String
	case module.TypeBool:
		return types.Bool
	case module.TypeInt:
		// types.Int is TypeOf(0) — Go's `int`. A module declaring TypeInt must
		// put a Go `int` in the event, not an int32 or an int64: the checker
		// would accept `.line > 10` and the vm would then be comparing a type
		// the check did not describe.
		return types.Int

	case module.TypeList:
		// A declared element shape is what makes `any(invocations, .bin == …)`
		// checkable — without one the collection is checked and the predicate
		// body is not, which is the same silent never-fires one level down.
		if f.Elem != nil {
			return types.Array(fieldType(*f.Elem))
		}
		return types.Array(types.Any)

	case module.TypeMap:
		// Enumerated keys get checked; unenumerated ones stay open. A closed
		// type over keys the module never claimed to know would refuse
		// `meta.whatever` on a declared field — the checker punishing an author
		// for our missing vocabulary rather than for their mistake.
		if len(f.Fields) > 0 {
			return structure(f.Fields)
		}
		if f.Elem != nil {
			// Open keys, declared values: `.flags.<any name>` is checked as the
			// value type, so a comparison the value can never satisfy (a list
			// against a string) is refused at load rather than false forever.
			return types.Map{types.Extra: fieldType(*f.Elem)}
		}
		return types.Any

	default:
		// A type this build does not recognise.
		return types.Any
	}
}

// structure builds a closed type over enumerated fields. Used for a map or a
// list element whose fields the module named — naming them is the module
// asserting these are the fields, which is what makes refusing the others fair.
func structure(fields []module.FieldDecl) types.Type {
	m := make(types.Map, len(fields))
	for _, f := range fields {
		m[f.Name] = fieldType(f)
	}
	return m
}
