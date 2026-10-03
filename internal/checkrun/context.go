package checkrun

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/natures"
	"github.com/sloprail/sloprail/internal/sessionstate"
)

// ContextsGuardrailKey is the reserved sessionstate keyspace the context[] map is persisted
// under: per-guardrail state under a name no real rule can have (a context's folder name
// cannot contain '!' or ':'), one `context:<name>` entry each, JSON-encoded ContextState.
const ContextsGuardrailKey = "!sloprail:contexts"

// ContextStatePrefix keys each context's state under the reserved keyspace, so ListState
// with this prefix reads the whole map back.
const ContextStatePrefix = "context:"

// LoadContextMap reads the context[] state map from the session store, seeded so EVERY
// declared context has an entry (inactive by default): `not context["x"].active` must be a
// usable match, and a declared-but-never-entered context must read as present-and-inactive,
// not as absent (indexing it would error). A nil store, or one that cannot be read, yields
// the seeded-inactive map: losing the store costs the cross-cycle memory, not the gating.
func LoadContextMap(errw io.Writer, store sessionstate.Store, contexts []declaration.Context) map[string]natures.ContextState {
	out := map[string]natures.ContextState{}
	for _, c := range contexts {
		out[c.Name] = natures.ContextState{Active: false, Payload: map[string]any{}}
	}
	if store == nil {
		return out
	}
	entries, err := store.ListState(ContextsGuardrailKey, ContextStatePrefix)
	if err != nil {
		fmt.Fprintf(errw, "sloprail: context state unavailable, treating all contexts as inactive: %v\n", err)
		return out
	}
	for _, e := range entries {
		name := e.Key[len(ContextStatePrefix):]
		var st natures.ContextState
		if json.Unmarshal([]byte(e.Value), &st) != nil {
			continue
		}
		// A stored payload of null unmarshals to a nil map; keep it a non-nil map so a
		// reader indexing `.payload.<key>` gets a clean miss, not an error.
		if st.Payload == nil {
			st.Payload = map[string]any{}
		}
		out[name] = st
	}
	return out
}

// ContextMatchValue is the context map in the wire form a matcher's expression reads
// (lowercase keys: `context["x"].active`, `context["x"].payload.<key>`). Every declared
// context is already present in the map, so an inactive one reads as
// `{active:false, payload:{}}` rather than an absent key that would error.
func ContextMatchValue(contextMap map[string]natures.ContextState) map[string]any {
	out := make(map[string]any, len(contextMap))
	for name, st := range contextMap {
		payload := st.Payload
		if payload == nil {
			payload = map[string]any{}
		}
		out[name] = map[string]any{
			"active":  st.Active,
			"payload": payload,
		}
	}
	return out
}
