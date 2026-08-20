package declaration

import "sort"

// This file is the per-nature event-kind vocabulary and the alias expansion the
// spec defines for a trigger's `on` list.
//
// A gate and a context do not admit the same event kinds. The events spec
// (sloprail-service/events/main.tsp) splits them into two unions: GateEventKind
// is the pre-action subset (a gate that already happened is too late to gate),
// and ContextEventKind is wider (a context sometimes must recognise itself from a
// file's SETTLED content, so it adds the Post file events and PostTagWrite). A
// declaration's `on:` must name only kinds its nature admits, and the loader
// REFUSES at load if it does not — mirroring how the old engine refuses binding
// to an unknown kind rather than silently never firing.
//
// The kind names here are the same strings the modules declare (filemod's
// KindPre*/KindPost*, commandmod's KindPreInvoke, tooluse's KindPreToolUse,
// tagmod's KindPostTagWrite, cyclemod's KindStop) and the same the events spec's
// EventKind enum names. They are written out rather than imported from the
// modules because this package sits alongside internal/guardrail, BELOW the
// modules (filemod's own tests import guardrail), and importing a module here
// would risk the same cycle scopes.go avoids by stating the marker shape inline.
// The set is pinned faithful to the spec by TestEventKinds_MatchTheSpecUnions
// rather than by a shared symbol.

// The concrete event kinds, spelled exactly as the modules declare them and the
// events spec's EventKind enum names them. Named constants so the union sets and
// the alias expansions below cannot drift on a spelling.
const (
	KindPreFileCreate    = "PreFileCreate"
	KindPreFileUpdate    = "PreFileUpdate"
	KindPreFileDelete    = "PreFileDelete"
	KindPreCommandInvoke = "PreCommandInvoke"
	KindPreToolUse       = "PreToolUse"
	KindPostFileCreate   = "PostFileCreate"
	KindPostFileUpdate   = "PostFileUpdate"
	KindPostFileDelete   = "PostFileDelete"
	KindPostTagWrite     = "PostTagWrite"
	KindStop             = "Stop"
)

// The config-level aliases a trigger's `event` may name in place of a concrete
// kind. Not events the engine emits — a shorthand in `on` the loader expands on
// read, because a file written under a prefix arrives as either a create or an
// update, and repeating the same `match` on two triggers is the duplication the
// alias removes.
const (
	// AliasPreFileWrite expands to PreFileCreate + PreFileUpdate. Admitted on both
	// a gate and a context (both may wake pre-write).
	AliasPreFileWrite = "PreFileWrite"

	// AliasPostFileWrite expands to PostFileCreate + PostFileUpdate. Admitted on a
	// CONTEXT only — a gate does not wake on settled content, so `PostFileWrite`
	// is not a gate alias.
	AliasPostFileWrite = "PostFileWrite"
)

// gateEventKinds is the set of concrete kinds a gate may wake on — the spec's
// GateEventKind union: the pre-action file/command/tool events plus Stop, and no
// Post event.
var gateEventKinds = map[string]bool{
	KindPreFileCreate:    true,
	KindPreFileUpdate:    true,
	KindPreFileDelete:    true,
	KindPreCommandInvoke: true,
	KindPreToolUse:       true,
	KindStop:             true,
}

// contextEventKinds is the set of concrete kinds a context may wake on to enter —
// the spec's ContextEventKind union: every pre-action kind a gate has EXCEPT Stop
// (TurnEnd/Stop is intentionally never a context entry event — a Stop covers the
// end of a cycle, and a context's exit, not its enter, is what runs then), plus
// the Post file events and PostTagWrite.
var contextEventKinds = map[string]bool{
	KindPreFileCreate:    true,
	KindPreFileUpdate:    true,
	KindPreFileDelete:    true,
	KindPreCommandInvoke: true,
	KindPreToolUse:       true,
	KindPostFileCreate:   true,
	KindPostFileUpdate:   true,
	KindPostFileDelete:   true,
	KindPostTagWrite:     true,
}

// gateAliases maps a gate's admitted aliases to the concrete kinds they expand
// to. Only PreFileWrite — a gate has no PostFileWrite alias.
var gateAliases = map[string][]string{
	AliasPreFileWrite: {KindPreFileCreate, KindPreFileUpdate},
}

// contextAliases maps a context's admitted aliases to the concrete kinds they
// expand to. Both the Pre and Post write aliases, since a context may wake on
// settled content.
var contextAliases = map[string][]string{
	AliasPreFileWrite:  {KindPreFileCreate, KindPreFileUpdate},
	AliasPostFileWrite: {KindPostFileCreate, KindPostFileUpdate},
}

// expandGateEvent resolves one gate trigger's `event` to the concrete kinds it
// names, and reports whether the name was understood at all.
//
// A concrete GateEventKind resolves to itself; the PreFileWrite alias resolves to
// its pair. Anything else — a name no gate admits, whether a typo, a Post event a
// gate cannot wake on, or the PostFileWrite alias that is a context's alone — is
// UNKNOWN, and the caller refuses the declaration naming it. This is the "refuse
// at load, do not silently never fire" contract for the `on:` list.
func expandGateEvent(name string) (kinds []string, known bool) {
	if expanded, ok := gateAliases[name]; ok {
		return expanded, true
	}
	if gateEventKinds[name] {
		return []string{name}, true
	}
	return nil, false
}

// expandContextEvent resolves one context trigger's `event` the same way
// expandGateEvent does, against the context vocabulary and aliases.
func expandContextEvent(name string) (kinds []string, known bool) {
	if expanded, ok := contextAliases[name]; ok {
		return expanded, true
	}
	if contextEventKinds[name] {
		return []string{name}, true
	}
	return nil, false
}

// gateEventNames lists every name a gate's `on` may carry — concrete kinds and
// aliases together, sorted — for a diagnostic that has to tell an author which
// kinds a gate admits when theirs is refused.
func gateEventNames() []string { return names(gateEventKinds, gateAliases) }

// contextEventNames lists every name a context's `on` may carry, for the same
// kind of diagnostic.
func contextEventNames() []string { return names(contextEventKinds, contextAliases) }

// names collects the concrete kinds and the alias names of a nature into one
// sorted list, so a "you named X; a <nature> admits …" message enumerates every
// legal spelling, aliases included.
func names(kinds map[string]bool, aliases map[string][]string) []string {
	out := make([]string, 0, len(kinds)+len(aliases))
	for k := range kinds {
		out = append(out, k)
	}
	for a := range aliases {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}
