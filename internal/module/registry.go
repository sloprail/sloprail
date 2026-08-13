package module

import (
	"fmt"
	"sort"
	"strings"
)

// Registry holds the modules this build knows about.
//
// They are registered rather than discovered, for now. Shaping the boundary as
// an interface costs a name and a contract; discovering modules at runtime
// would cost a plugin mechanism, a compatibility promise and a security
// boundary, and nobody has asked for one. This is the seam where that becomes
// possible later without being paid for now.
type Registry struct {
	byName map[string]Module
}

// NewRegistry returns a registry holding the given modules.
func NewRegistry(mods ...Module) (*Registry, error) {
	r := &Registry{byName: make(map[string]Module, len(mods))}
	for _, m := range mods {
		if err := r.add(m); err != nil {
			return nil, err
		}
	}
	return r, nil
}

func (r *Registry) add(m Module) error {
	name := m.Name()
	if name == "" {
		return fmt.Errorf("module: registered with no name")
	}
	if strings.Contains(name, ".") {
		// The dot separates a module from its kinds. A module whose own name
		// contained one would make "a.b.c" ambiguous about where the namespace
		// ends.
		return fmt.Errorf("module %q: name may not contain a dot", name)
	}
	if _, taken := r.byName[name]; taken {
		return fmt.Errorf("module %q: already registered", name)
	}
	r.byName[name] = m
	return nil
}

// Kind returns the fully qualified name of one of a module's kinds.
func Kind(moduleName, kindName string) string {
	return moduleName + "." + kindName
}

// Lookup returns the module that owns a kind, and whether one does. A kind
// nothing owns is a declaration naming an event that will never arrive.
func (r *Registry) Lookup(kind string) (Module, bool) {
	name, _, found := strings.Cut(kind, ".")
	if !found {
		return nil, false
	}
	m, ok := r.byName[name]
	return m, ok
}

// DeclaredKinds returns every kind this registry can produce, qualified, in a
// stable order. Used to tell a rule author which kinds exist when their
// declaration names one that does not.
func (r *Registry) DeclaredKinds() []string {
	var kinds []string
	for name, m := range r.byName {
		for _, k := range m.Kinds() {
			kinds = append(kinds, Kind(name, k.Name))
		}
	}
	sort.Strings(kinds)
	return kinds
}

// KindDeclFor returns the declaration of one kind, and whether it exists. This
// is what a matcher is checked against when a guardrail loads.
func (r *Registry) KindDeclFor(kind string) (KindDecl, bool) {
	moduleName, kindName, found := strings.Cut(kind, ".")
	if !found {
		return KindDecl{}, false
	}
	m, ok := r.byName[moduleName]
	if !ok {
		return KindDecl{}, false
	}
	for _, k := range m.Kinds() {
		if k.Name == kindName {
			return k, true
		}
	}
	return KindDecl{}, false
}

// Needed returns the modules that some binding actually asks for, given the
// kinds bound to across the enabled guardrails.
//
// Producing an event nobody has bound to is work done to be discarded, and it
// is not cheap work: finding the programs a command line runs means walking its
// whole structure, on every command, whether or not any rule asks about
// commands. A project with no rule about commands should pay nothing for the
// fact that commands can be inspected at all.
//
// The prefix is what makes this answerable without asking any module anything —
// a binding names "file.pre_create", the namespace says "file", and that is the
// whole decision.
func (r *Registry) Needed(boundKinds []string) []Module {
	wanted := make(map[string]struct{}, len(boundKinds))
	for _, k := range boundKinds {
		if name, _, found := strings.Cut(k, "."); found {
			wanted[name] = struct{}{}
		}
	}

	var needed []Module
	for name, m := range r.byName {
		if _, ok := wanted[name]; ok {
			needed = append(needed, m)
		}
	}
	sort.Slice(needed, func(i, j int) bool { return needed[i].Name() < needed[j].Name() })
	return needed
}
