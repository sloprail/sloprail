package guardrail

import (
	"fmt"
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
//
// Because the read can no longer be nil, a rule that asks whether it is — `==
// nil`, `!= nil`, or a `??` default of its own — would silently answer the
// same way for every command (a gate on `.flags.tag == nil` would never wake).
// Such a rule is refused when it loads instead (Err), with the spelling that
// asks the question.
type absentListIsEmpty struct {
	made map[ast.Node]bool
	Err  error
}

func newAbsentListIsEmpty() *absentListIsEmpty {
	return &absentListIsEmpty{made: map[ast.Node]bool{}}
}

// sr:invariant matching/flag-values-are-lists
func (v *absentListIsEmpty) Visit(node *ast.Node) {
	switch n := (*node).(type) {
	case *ast.BinaryNode:
		if v.made[n] {
			return
		}
		_, nilLeft := n.Left.(*ast.NilNode)
		_, nilRight := n.Right.(*ast.NilNode)
		switch {
		case (n.Operator == "==" || n.Operator == "!=") && (v.made[n.Left] && nilRight || v.made[n.Right] && nilLeft),
			n.Operator == "??" && v.made[n.Left]:
			if v.Err == nil {
				v.Err = fmt.Errorf("%q asks whether a flag is absent, but an absent flag reads as an empty list ([]), never nil; "+
					"write `not (\"tag\" in .flags)` for \"not passed\", and `\"tag\" in .flags` for \"passed\"", n.String())
			}
		}
	case *ast.MemberNode:
		if n.Method {
			return
		}
		nat := n.Node.Nature()
		if nat == nil || nat.TypeData == nil || nat.Strict || nat.DefaultMapValue == nil {
			return
		}
		if t := nat.DefaultMapValue.Type; t == nil || t.Kind() != reflect.Slice {
			return
		}
		patched := &ast.BinaryNode{Operator: "??", Left: n, Right: &ast.ArrayNode{}}
		v.made[patched] = true
		ast.Patch(node, patched)
		v.made[*node] = true
	}
}
