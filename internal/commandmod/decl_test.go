package commandmod

import (
	"testing"

	"github.com/sloprail/sloprail/internal/module"
)

// This file holds the check that the sixteen-times failure keeps reappearing
// as: a declaration that says one thing and an extractor that emits another.
//
// The declaration is what a rule is validated against at load. If it names a
// key the events never carry, a predicate reading that key loads and evaluates
// false on every command — a rule that reads as satisfied because nothing it
// names exists. If it OMITS a key the events do carry, a correct predicate is
// refused at load. Both are silent in opposite directions, and neither is
// caught by any test that only inspects one side.

// declaredInvocationKeys reads the element shape off the module's own
// declaration, so this test cannot drift from what the engine actually loads.
func declaredInvocationKeys(t *testing.T) map[string]module.FieldType {
	t.Helper()

	kinds := New().Kinds()
	if len(kinds) != 1 || kinds[0].Name != KindPreInvoke {
		t.Fatalf("Kinds() = %+v, want exactly %s", kinds, KindPreInvoke)
	}

	var invocations *module.FieldDecl
	for i := range kinds[0].Fields {
		if kinds[0].Fields[i].Name == FieldInvocations {
			invocations = &kinds[0].Fields[i]
		}
	}
	if invocations == nil {
		t.Fatalf("no %s field declared", FieldInvocations)
	}
	if invocations.Type != module.TypeList {
		t.Fatalf("%s declared as %q, want %q", FieldInvocations, invocations.Type, module.TypeList)
	}
	// The element shape is the part that was missing and let `.bni` through.
	// Its absence is the defect, so its absence is what fails here.
	if invocations.Elem == nil {
		t.Fatalf("%s declares no Elem — a predicate over it goes unchecked, which is "+
			"how `any(invocations, .bni == \"npm\")` loads and never fires", FieldInvocations)
	}
	if len(invocations.Elem.Fields) == 0 {
		t.Fatalf("%s.Elem declares no fields — same silence, one level down", FieldInvocations)
	}

	keys := map[string]module.FieldType{}
	for _, f := range invocations.Elem.Fields {
		if _, dup := keys[f.Name]; dup {
			t.Errorf("%s.Elem declares %q twice", FieldInvocations, f.Name)
		}
		keys[f.Name] = f.Type
	}
	return keys
}

// TestDecl_ElementShapeMatchesWhatIsEmitted compares the declaration against a
// real emitted event, key for key, in both directions.
//
// A command line rich enough to populate every key: a wrapper so there are
// several invocations, a path so bin differs from argv[0], and a flag so the
// flags map is not empty.
func TestDecl_ElementShapeMatchesWhatIsEmitted(t *testing.T) {
	declared := declaredInvocationKeys(t)

	ev := ExtractCommand(`sudo -u root /usr/local/bin/npm publish --tag=next`).Event()

	list, ok := ev.Fields[FieldInvocations].([]any)
	if !ok || len(list) == 0 {
		t.Fatalf("%s = %#v, want a non-empty list", FieldInvocations, ev.Fields[FieldInvocations])
	}

	for i, item := range list {
		emitted, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("invocation %d is %T, want map[string]any — a matcher reads it as a map", i, item)
		}

		// Every key the event carries must be declared, or a rule naming it is
		// refused at load for a field that genuinely exists.
		for key := range emitted {
			if _, found := declared[key]; !found {
				t.Errorf("invocation %d emits key %q, which the declaration does not name — "+
					"a predicate reading it would be refused at load", i, key)
			}
		}

		// Every key the declaration names must be carried, or a rule naming it
		// loads and evaluates false on every command. This is the direction the
		// `.bni` failure took.
		for key := range declared {
			if _, found := emitted[key]; !found {
				t.Errorf("invocation %d does not carry declared key %q — a predicate "+
					"reading it would load and silently never fire", i, key)
			}
		}
	}
}

// TestDecl_ElementTypesMatchWhatIsEmitted: a key declared with the wrong type
// is checked wrongly in both directions — `.bin == "npm"` refused if bin were a
// list, `.flags` compared as a string if it were declared one.
func TestDecl_ElementTypesMatchWhatIsEmitted(t *testing.T) {
	declared := declaredInvocationKeys(t)

	ev := ExtractCommand(`sudo -u root /usr/local/bin/npm publish --tag=next`).Event()
	list := ev.Fields[FieldInvocations].([]any)

	for i, item := range list {
		for key, value := range item.(map[string]any) {
			want, found := declared[key]
			if !found {
				continue // reported by the test above
			}
			var got module.FieldType
			switch value.(type) {
			case string:
				got = module.TypeString
			case bool:
				got = module.TypeBool
			case []any:
				got = module.TypeList
			case map[string]any:
				got = module.TypeMap
			case int, int64:
				got = module.TypeInt
			default:
				t.Errorf("invocation %d key %q holds %T, which is not a declarable type", i, key, value)
				continue
			}
			if got != want {
				t.Errorf("invocation %d key %q is declared %q but holds a %q (%#v)", i, key, want, got, value)
			}
		}
	}
}

// TestDecl_TopLevelFieldsMatchWhatIsEmitted: the same check for the kind's own
// fields, not just the list element.
func TestDecl_TopLevelFieldsMatchWhatIsEmitted(t *testing.T) {
	declared := map[string]module.FieldType{}
	for _, f := range New().Kinds()[0].Fields {
		declared[f.Name] = f.Type
	}

	ev := ExtractCommand(`npm publish`).Event()

	if ev.Kind != KindPreInvoke {
		t.Errorf("kind = %q, want %q", ev.Kind, KindPreInvoke)
	}
	for name := range ev.Fields {
		if _, found := declared[name]; !found {
			t.Errorf("event carries undeclared field %q", name)
		}
	}
	for name := range declared {
		if _, found := ev.Fields[name]; !found {
			t.Errorf("declared field %q is not carried — a rule reading it never fires", name)
		}
	}
	if _, ok := ev.Fields[FieldRaw].(string); !ok {
		t.Errorf("%s holds %T, want a string as declared", FieldRaw, ev.Fields[FieldRaw])
	}
}

// TestDecl_EmptyInvocationsStillCarriesTheDeclaredShape: a line that resolves to
// nothing must still carry `invocations` as an empty LIST, not as nil.
//
// A nil there reads differently from an empty list to a matcher, and
// `any(invocations, ...)` over a missing field is not the same question as over
// an empty one — a rule could be refused, or throw, on exactly the lines the
// module promises to report honestly.
func TestDecl_EmptyInvocationsStillCarriesTheDeclaredShape(t *testing.T) {
	for _, src := range []string{`$NPM publish`, `# comment`, `"unterminated`, `FOO=1`} {
		t.Run(src, func(t *testing.T) {
			ev := ExtractCommand(src).Event()
			list, ok := ev.Fields[FieldInvocations].([]any)
			if !ok {
				t.Fatalf("%s = %#v (%T), want an empty []any", FieldInvocations, ev.Fields[FieldInvocations], ev.Fields[FieldInvocations])
			}
			if len(list) != 0 {
				t.Errorf("%s = %v, want empty", FieldInvocations, list)
			}
			if ev.Fields[FieldRaw] != src {
				t.Errorf("%s = %q, want %q — the raw line rides along even when nothing resolves", FieldRaw, ev.Fields[FieldRaw], src)
			}
		})
	}
}

// TestDecl_ArgvAndFlagsAreWireTypes: the conversion flattens to plain `any`
// containers. A []string or a map[string]string reaching a matcher is a shape
// its type switch does not know, and the rule silently does not match.
func TestDecl_ArgvAndFlagsAreWireTypes(t *testing.T) {
	ev := ExtractCommand(`npm publish --tag=next`).Event()
	inv := ev.Fields[FieldInvocations].([]any)[0].(map[string]any)

	argv, ok := inv[KeyArgv].([]any)
	if !ok {
		t.Fatalf("%s is %T, want []any", KeyArgv, inv[KeyArgv])
	}
	for i, a := range argv {
		if _, ok := a.(string); !ok {
			t.Errorf("argv[%d] is %T, want string", i, a)
		}
	}

	flags, ok := inv[KeyFlags].(map[string]any)
	if !ok {
		t.Fatalf("%s is %T, want map[string]any", KeyFlags, inv[KeyFlags])
	}
	for k, v := range flags {
		vs, ok := v.([]any)
		if !ok {
			t.Errorf("flags[%q] is %T, want []any", k, v)
			continue
		}
		for i, item := range vs {
			if _, ok := item.(string); !ok {
				t.Errorf("flags[%q][%d] is %T, want string", k, i, item)
			}
		}
	}
}

// TestEvent_RoundTrips: what FromEvent reads back is what ExtractCommand
// produced. The module reads its own events, so a conversion that loses a field
// in one direction is a rule matching on something the reader cannot see.
func TestEvent_RoundTrips(t *testing.T) {
	const src = `sudo -u root /usr/local/bin/npm publish --tag=next -f`
	want := ExtractCommand(src)

	got, err := FromEvent(want.Event())
	if err != nil {
		t.Fatal(err)
	}
	if got.Raw != want.Raw {
		t.Errorf("raw = %q, want %q", got.Raw, want.Raw)
	}
	if len(got.Invocations) != len(want.Invocations) {
		t.Fatalf("got %d invocations, want %d", len(got.Invocations), len(want.Invocations))
	}
	for i := range want.Invocations {
		w, g := want.Invocations[i], got.Invocations[i]
		if g.Bin != w.Bin {
			t.Errorf("invocation %d bin = %q, want %q", i, g.Bin, w.Bin)
		}
		if !equal(g.Argv, w.Argv) {
			t.Errorf("invocation %d argv = %q, want %q", i, g.Argv, w.Argv)
		}
		if len(g.Flags) != len(w.Flags) {
			t.Errorf("invocation %d flags = %v, want %v", i, g.Flags, w.Flags)
		}
		for k, v := range w.Flags {
			if !equal(g.Flags[k], v) {
				t.Errorf("invocation %d flags[%q] = %q, want %q", i, k, g.Flags[k], v)
			}
		}
	}
}

// TestEvent_FromEventRejectsAnEventWithNoRawLine: every event this module emits
// was produced from a command line, so one without it did not come from here.
// Returning a zero value would let a caller judge a line nobody ran.
func TestEvent_FromEventRejectsAnEventWithNoRawLine(t *testing.T) {
	ev := ExtractCommand(`npm publish`).Event()
	delete(ev.Fields, FieldRaw)

	if _, err := FromEvent(ev); err == nil {
		t.Error("FromEvent accepted an event carrying no raw command line")
	}

	// An empty invocation list is NOT an error — a line whose programs could not
	// be resolved is exactly what this module promises to report honestly.
	if _, err := FromEvent(ExtractCommand(`$NPM publish`).Event()); err != nil {
		t.Errorf("FromEvent rejected a line that legitimately resolved to nothing: %v", err)
	}
}

// TestExtract_GatesOnTheToolName pins the CORRECTED, current contract in
// place of what this test used to assert (see git history: extraction used
// to key off the arguments carrying a `command`, deliberately never off the
// tool's name, on the reasoning that a harness renaming Bash or adding a
// second shell tool must not silently stop being watched).
//
// See harnesstools.go for the argument behind the reversal this project
// chose: `Bash`, the one name on HarnessCommandTools, still works exactly as
// before. Every other spelling — including one that looks like an obvious
// rename or an alias — now produces nothing, by design, with no shape
// fallback.
func TestExtract_GatesOnTheToolName(t *testing.T) {
	m := New()

	t.Run("Bash is on the list", func(t *testing.T) {
		evs, err := m.Extract(module.Input{
			module.InputPhase:   module.PhasePre,
			module.InputPayload: pending{tool: "Bash", args: `{"command":"npm publish"}`},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(evs) != 1 {
			t.Fatalf("got %d events, want 1 — Bash is on HarnessCommandTools", len(evs))
		}
		list, _ := evs[0].Fields[FieldInvocations].([]any)
		if len(list) != 1 {
			t.Errorf("invocations = %v, want one npm", list)
		}
	})

	for _, tool := range []string{
		"bash", "BashTool", "Shell", "run_command", "Execute",
		"terminal", "", "SomeFutureHarnessShell",
	} {
		t.Run(tool, func(t *testing.T) {
			evs, err := m.Extract(module.Input{
				module.InputPhase:   module.PhasePre,
				module.InputPayload: pending{tool: tool, args: `{"command":"npm publish"}`},
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(evs) != 0 {
				t.Fatalf("tool %q: got %d events, want 0 — not on HarnessCommandTools, "+
					"and there is no shape fallback", tool, len(evs))
			}
		})
	}

	// The converse still holds: a tool NAMED like a shell but carrying no
	// command line yields nothing. It is the payload that decides, in both
	// directions.
	evs, err := m.Extract(module.Input{
		module.InputPhase:   module.PhasePre,
		module.InputPayload: pending{tool: "Bash", args: `{"file_path":"a.txt"}`},
	})
	if err != nil || len(evs) != 0 {
		t.Errorf("got %d events, err %v; want none from a payload with no command", len(evs), err)
	}
}

// TestModule_RegistersUnderItsOwnName: the name is how the engine reports what
// produced an event and how the module is switched off.
func TestModule_RegistersUnderItsOwnName(t *testing.T) {
	if got := New().Name(); got != Name {
		t.Errorf("Name() = %q, want %q", got, Name)
	}
	if _, err := module.NewRegistryForTest(New()); err != nil {
		t.Errorf("the module does not register cleanly: %v", err)
	}
}
