package transcript

import (
	"fmt"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
)

// A hook script needs answers, not a log. Left to read the record itself, every
// rule would reimplement the same traversal — telling a sub-agent's work from
// the main line, finding where this cycle's work begins — and each would do it
// slightly differently, against a format that is not ours to keep stable. So
// the traversal happens once, here.

// Query narrows which entries an answer contains.
type Query struct {
	// Where is an expression over an entry, in the same language a guardrail's
	// matcher uses, so a rule author learns one syntax rather than two. Empty
	// admits everything the rest of the query allows.
	Where string

	// IncludeSidechains keeps entries belonging to sub-agents. Off by default:
	// a rule asking what the agent did usually means the main line of work, and
	// delegated work would otherwise answer for it.
	IncludeSidechains bool
}

// Filter applies a query to entries.
//
// Asking about something that never happened returns nothing rather than an
// error. Absence is a legitimate finding, and often the one a rule is looking
// for.
//
// An expression that no entry could be evaluated against at all IS an error,
// and the distinction is the point. One entry of another shape is ordinary and
// silently not a match (see runWhere). But an expression that failed on EVERY
// entry it was offered has told us nothing about the session — and returning
// "nothing matched" for it would report no violations, and exit successfully,
// for a rule that never ran. That is the silently-vacuous rule this product
// exists to prevent, and it would be this product producing it.
//
// Judged on whether any entry evaluated, not on what any of them decided: a
// working rule that legitimately matches nothing is the ordinary case, and must
// stay distinguishable from a broken one that could not look.
func Filter(entries []Entry, q Query) ([]Entry, error) {
	program, err := compileWhere(q.Where)
	if err != nil {
		return nil, err
	}

	out := make([]Entry, 0, len(entries))
	offered, evaluated := 0, 0
	for _, e := range entries {
		if e.IsSidechain && !q.IncludeSidechains {
			continue
		}
		offered++
		admitted, ok, err := runWhere(program, e)
		if err != nil {
			return nil, fmt.Errorf("transcript: where %q: %w", q.Where, err)
		}
		if !ok {
			continue // this entry is not the shape the expression asks about
		}
		evaluated++
		if admitted {
			out = append(out, e)
		}
	}

	if offered > 0 && evaluated == 0 && program != nil {
		return nil, fmt.Errorf("transcript: where %q: %w: not one of the %d entries could be evaluated against it, so the answer would report no violations without having looked",
			q.Where, ErrExpressionNeverRan, offered)
	}
	return out, nil
}

func compileWhere(src string) (*vm.Program, error) {
	if src == "" {
		return nil, nil
	}
	// Compiled against the environment, not bare. `type` is also one of the
	// expression language's own builtins, so without the environment declaring
	// it as a name the builtin wins and `type == "assistant"` — the most
	// obvious question anyone would ask of an entry — does not compile at all.
	// Declaring the names also means an expression naming a field that does not
	// exist is refused here rather than silently matching nothing later.
	program, err := expr.Compile(src, expr.Env(envNames), expr.AsBool())
	if err != nil {
		return nil, fmt.Errorf("transcript: where %q: %w", src, err)
	}
	return program, nil
}

// runWhere evaluates one entry against the expression. It reports whether the
// entry is admitted, and separately whether it could be evaluated at all.
//
// The two are reported apart because "not a match" and "could not look" are
// different facts that look identical in an answer, and only the caller —
// seeing every entry — can tell an ordinary shape mismatch from an expression
// that never ran. Collapsing them here is what let a rule failing on every
// entry report no violations.
//
// An entry the expression cannot be evaluated against is not a match, and that
// is ordinary rather than exotic. A user's turn carries its content as a
// string; an assistant's carries a list of blocks. So
// `message.content[0].name == "Bash"` — a fair question, and close to the most
// common one a rule will ask — indexes a string on every user turn and reaches
// for a field of a byte. If that alone were fatal the command would fail on
// every real session, and the rule author's only recourse would be to write the
// shape-guarding themselves in every expression: precisely the per-rule
// reimplementation of the traversal this command exists to absorb.
//
// A non-boolean result IS an error, because that is the expression being wrong
// about itself rather than the entry being a different shape.
func runWhere(program *vm.Program, e Entry) (admitted, evaluated bool, err error) {
	if program == nil {
		return true, true, nil
	}
	out, runErr := expr.Run(program, env(e))
	if runErr != nil {
		return false, false, nil // see above — the caller decides if this is everything
	}
	b, ok := out.(bool)
	if !ok {
		return false, false, fmt.Errorf("%w: produced %T, not a boolean", ErrExpressionNotBoolean, out)
	}
	return b, true, nil
}

// env is what an expression may read: the entry's own fields, under the names
// the canonical shape gives them.
//
// message and toolUseResult are decoded here rather than handed over as raw
// bytes, because an expression asking what a tool returned wants to reach
// inside it, and text it would have to parse itself is not an answer. What is
// inside stays the harness's shape: this package does not model it.
func env(e Entry) map[string]any {
	return map[string]any{
		"type":              string(e.Type),
		"uuid":              e.UUID,
		"parentUuid":        e.ParentUUID,
		"logicalParentUuid": e.LogicalParentUUID,
		"timestamp":         e.Timestamp,
		"isSidechain":       e.IsSidechain,
		"message":           decode(e.Message),
		"toolUseResult":     decode(e.ToolUseResult),
	}
}

// envNames is the environment an expression is compiled against: the same
// names, so an expression using one that does not exist is refused at compile
// time rather than quietly evaluating to nothing.
//
// It is a map rather than a struct deliberately. A map's values are dynamic, so
// an expression may reach into a message's contents — which this package does
// not model and should not, since modelling them would be modelling the
// harness's own message format, exactly what normalising into Entry avoided.
// A struct would type those two fields as the bytes they are and reject the
// reaching.
//
// The two carry a placeholder rather than the nil a blank entry decodes to,
// because nil is not a type an expression can be checked against — it compiles
// to "this has no members" and refuses every expression reaching into a
// message. What is there at RUN time is whatever the entry actually held,
// including nothing.
var envNames = func() map[string]any {
	names := env(Entry{})
	names["message"] = map[string]any{}
	names["toolUseResult"] = map[string]any{}
	return names
}()
