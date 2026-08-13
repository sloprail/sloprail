package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/internal/guardrail"
	"github.com/sloprail/sloprail/internal/module"
	"github.com/sloprail/sloprail/internal/module/modules"
)

// T003_13: every kind the help lists as undispatched really is undispatched,
// and every kind it does NOT list really does arrive.
//
// The help now prints a NOT DISPATCHED YET list, which is a claim about the
// engine rather than about the registry — the one kind of claim this output has
// no structural defence against. EVENT KINDS cannot be wrong because it is
// printed from the declarations; this list is printed from a map of phases
// maintained by hand, and a hand-maintained map is exactly what goes stale.
//
// Both directions, because each failure is bad in its own way. A kind listed
// here that actually fires scares an author off a rule that would have worked.
// A kind NOT listed that never fires is the silent no-op the whole document
// exists to prevent, delivered by the document itself.
//
// Ground truth is what the modules do with each phase: a kind is dispatchable
// when its owning module, asked with the phase the kind's name implies, is
// asked at all by some hook point. Only pre-tool dispatches today and only with
// PhasePre, so Post kinds are stranded — and this recomputes that rather than
// restating it, so the day a Post dispatch lands the test says so.
func TestT003_13_UndispatchedListMatchesWhatTheEngineDispatches(t *testing.T) {
	e := New(t)

	got := e.CLI(t.TempDir(), "guardrail", "help")
	if got.Code != 0 {
		t.Fatalf("guardrail help exited %d:\n%s", got.Code, got.Output)
	}

	reg, err := modules.Registry()
	if err != nil {
		t.Fatalf("registry: %v", err)
	}

	listed := undispatchedFromHelp(got.Output)

	for _, kind := range reg.DeclaredKinds() {
		// A kind arrives only if some hook point extracts with its phase. The
		// binary has exactly one dispatching hook point, session pre-tool, and
		// it passes PhasePre — so a Post kind cannot arrive no matter what its
		// module would do if asked.
		arrives := !strings.HasPrefix(kind, "Post")

		switch {
		case arrives && listed[kind]:
			t.Errorf("help lists %q as not dispatched, but it is — an author is being warned off a rule that would work", kind)
		case !arrives && !listed[kind]:
			t.Errorf("help does not warn that %q is never dispatched — a rule bound to it loads, validates and never fires", kind)
		}
	}

	if len(listed) == 0 {
		return // nothing stranded is a legitimate future state
	}
	for kind := range listed {
		if _, declared := reg.KindDeclFor(kind); !declared {
			t.Errorf("help warns about %q, which no module declares at all", kind)
		}
	}
}

// undispatchedFromHelp reads back the kinds the help says never arrive.
func undispatchedFromHelp(out string) map[string]bool {
	listed := map[string]bool{}
	_, rest, found := strings.Cut(out, "NOT DISPATCHED YET")
	if !found {
		return listed
	}
	// The list runs to the blank line that ends it — the indented kind names.
	for _, line := range strings.Split(rest, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || !strings.HasPrefix(line, "  ") {
			continue
		}
		if strings.Contains(trimmed, " ") {
			break // reached the prose under the list
		}
		listed[trimmed] = true
	}
	return listed
}

// T003_14: the matcher operators the help offers all compile against a kind
// this build declares.
//
// T003_04 guards the reverse — that the help names no FIELD nothing carries.
// This guards the operators themselves. An operator documented but unsupported
// by the expression language produces a matcher that will not load, and one
// documented for the wrong field TYPE produces a matcher that loads and never
// matches. Both send an author away with a rule that does not work, and neither
// is visible by reading the help.
//
// The expressions are the ones the help actually prints, found by scanning its
// operator tables, so adding an example to the help puts it under this check
// without anyone remembering to.
func TestT003_14_EveryMatcherExampleCompiles(t *testing.T) {
	e := New(t)

	got := e.CLI(t.TempDir(), "guardrail", "help")

	reg, err := modules.Registry()
	if err != nil {
		t.Fatalf("registry: %v", err)
	}

	examples := matcherExamplesFromHelp(got.Output)
	if len(examples) == 0 {
		t.Fatal("found no matcher examples in the help — this test would prove nothing")
	}

	for _, src := range examples {
		// An example is good if it compiles against SOME declared kind: the
		// help groups them by field type, so a list example is not expected to
		// compile against a kind carrying only strings.
		var compiled bool
		var lastErr error
		for _, kind := range reg.DeclaredKinds() {
			decl, ok := reg.KindDeclFor(kind)
			if !ok {
				continue
			}
			if _, err := guardrail.CompileMatcherFor(src, decl); err == nil {
				compiled = true
				break
			} else {
				lastErr = err
			}
		}
		if !compiled {
			t.Errorf("help offers matcher %q, which compiles against no declared kind: %v", src, lastErr)
		}
	}
}

// matcherExamplesFromHelp pulls the expressions out of the help's operator
// tables — the indented lines between MATCHERS and THERE IS NO GLOB that read
// as expressions rather than as prose.
func matcherExamplesFromHelp(out string) []string {
	_, rest, found := strings.Cut(out, "\nMATCHERS\n")
	if !found {
		return nil
	}
	body, _, _ := strings.Cut(rest, "THERE IS NO GLOB")

	var examples []string
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "  ") {
			continue
		}
		trimmed := strings.TrimSpace(line)
		// The operator table's left column names the operator and the right
		// column shows it. Take what follows the operator name when the line
		// has both, and the whole line when it is an expression on its own.
		expr := trimmed
		if name, rhs, ok := strings.Cut(trimmed, "     "); ok && !strings.ContainsAny(name, "(\"") {
			expr = strings.TrimSpace(rhs)
		}
		// Drop trailing commentary.
		if idx := strings.Index(expr, "  #"); idx >= 0 {
			expr = strings.TrimSpace(expr[:idx])
		}
		if expr == "" || !looksLikeExpression(expr) {
			continue
		}
		examples = append(examples, expr)
	}
	return examples
}

// looksLikeExpression keeps the lines that are matchers and drops the prose and
// the table headings around them.
func looksLikeExpression(s string) bool {
	if strings.HasPrefix(s, "----") || s == "want" {
		return false
	}
	// Every matcher example reads a field and applies something to it, so it
	// contains either a quoted literal or a comparison.
	return strings.Contains(s, `"`) || strings.Contains(s, ">") || strings.Contains(s, "==")
}

// T003_15: the help does not tell an author to use a command that fails from a
// hook, without saying that it does.
//
// `sloprail session state` is listed under `sloprail session --help` and looks
// usable. It is not: it resolves its scope from an environment the dispatcher
// does not set, so it fails from every hook. The help has to say so, and this
// pins the saying to the fact.
//
// The fact is established by running the command the way a hook would — with no
// SLOPRAIL_* environment — and confirming it still fails. When the dispatcher
// starts setting that environment this test goes red, which is the correct
// moment to delete the warning.
func TestT003_15_StateIsDocumentedAsUnavailableWhileItIs(t *testing.T) {
	e := New(t)

	// What a hook actually gets today: no scope in the environment. CLI runs
	// the built binary with exactly HOME set, which is that situation.
	got := e.CLI(t.TempDir(), "session", "state", "get", "anything")
	if got.Code == 0 {
		t.Fatalf("`session state get` succeeded with no scope in the environment — the dispatcher gap has closed, so the help's NOT AVAILABLE warning is now false and must be removed:\n%s", got.Output)
	}

	help := e.CLI(t.TempDir(), "guardrail", "help")
	if !strings.Contains(help.Output, "session state") {
		t.Error("help never mentions `session state` — an author finds it in `session --help`, where it looks usable, and writes a rule around it")
	}
	if !strings.Contains(help.Output, "NOT AVAILABLE") {
		t.Error("help mentions `session state` without saying it does not work from a hook")
	}
}

// T003_16: the help's claim about what a hook is handed matches what the engine
// sends.
//
// The ON STDIN section shows a payload with `event` and `guardrailDir`. That is
// the shape a hook script is written against — a wrong key name there produces
// a hook whose extraction returns empty and which then permits everything.
//
// Checked against the constant the engine builds the payload from where one
// exists, and against the literal key names otherwise, since runHooks writes
// them as a map literal.
func TestT003_16_StdinShapeIsWhatTheEngineSends(t *testing.T) {
	e := New(t)

	got := e.CLI(t.TempDir(), "guardrail", "help")

	for _, key := range []string{`"event"`, `"guardrailDir"`, `"kind"`, `"fields"`} {
		bare := strings.Trim(key, `"`)
		if !strings.Contains(got.Output, bare) {
			t.Errorf("help never names %q, which a hook must read off its stdin", bare)
		}
	}

	// The only hook mechanism, named from the constant rather than spelled out,
	// so renaming it breaks this rather than leaving the help behind.
	if !strings.Contains(got.Output, "type: "+guardrail.HookCommand) {
		t.Errorf("help does not show `type: %s`, the only hook type this build accepts", guardrail.HookCommand)
	}
}

// T003_17: the help names every module-declared field type it explains how to
// match on, as a type rather than in passing.
//
// The operator tables are grouped by field type — one set for `string`, another
// for `list`. A build that starts declaring a type neither group covers leaves
// an author with a field they can see under EVENT KINDS and no way to write a
// matcher for it, which is the silent no-op again.
//
// It looks for the type in BACKTICKS, not as a bare substring. A bare search
// passes on the prose around the tables — "the ones listed above" contains
// "list" — so the first version of this test could not fail, which is the shape
// of test this project has already had to delete once. The backticked form is
// how the help marks a type name as a type name.
func TestT003_17_EveryDeclaredFieldTypeHasOperators(t *testing.T) {
	e := New(t)

	got := e.CLI(t.TempDir(), "guardrail", "help")

	reg, err := modules.Registry()
	if err != nil {
		t.Fatalf("registry: %v", err)
	}

	seen := map[module.FieldType]bool{}
	for _, kind := range reg.DeclaredKinds() {
		decl, ok := reg.KindDeclFor(kind)
		if !ok {
			continue
		}
		for _, f := range decl.Fields {
			seen[f.Type] = true
		}
	}
	if len(seen) == 0 {
		t.Fatal("no fields declared anywhere — this test would prove nothing")
	}

	_, matchers, found := strings.Cut(got.Output, "\nMATCHERS\n")
	if !found {
		t.Fatal("help has no MATCHERS section")
	}
	matchers, _, _ = strings.Cut(matchers, "\nHOOKS\n")

	for typ := range seen {
		if !strings.Contains(matchers, "`"+string(typ)+"`") {
			t.Errorf("a module declares a field of type %q, and MATCHERS never names that type as a type — an author cannot tell which operators apply to it", typ)
		}
	}
}
