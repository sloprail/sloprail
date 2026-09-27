package guardrail

import (
	"reflect"

	"github.com/expr-lang/expr/ast"
)

// absentListIsEmpty is a compile-time patch: a read of a key a map with open
// keys and LIST values does not carry — `.flags.access` on a command that did
// not pass --access — evaluates to an empty list instead of nil.
//
// Without it every natural reading of such a value errors at runtime when the
// key is absent (`len(.flags.access) > 0` is "invalid argument for len",
// `any(.flags.tag, …)` has nothing to range over), and a matcher that errors
// fails CLOSED: a gate on `len(.flags.access) > 0` would refuse every npm call
// that did not pass the flag. An absent list and an empty one mean the same
// thing to every rule that reads it, so the read is `(.flags.access ?? [])`.
//
// Only a map whose values the module declared as a list is patched (the
// checker's nature says so: open keys, a list default), so a map of scalars
// or an undeclared one keeps nil for an absent key.
type absentListIsEmpty struct{}

func (absentListIsEmpty) Visit(node *ast.Node) {
	m, ok := (*node).(*ast.MemberNode)
	if !ok || m.Method {
		return
	}
	n := m.Node.Nature()
	if n == nil || n.TypeData == nil || n.Strict || n.DefaultMapValue == nil {
		return
	}
	if t := n.DefaultMapValue.Type; t == nil || t.Kind() != reflect.Slice {
		return
	}
	ast.Patch(node, &ast.BinaryNode{Operator: "??", Left: m, Right: &ast.ArrayNode{}})
}
