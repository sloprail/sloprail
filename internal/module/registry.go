package module

import (
	"fmt"
	"sort"
)

// Registry holds the modules this build knows about, and which kinds each owns.
//
// They are registered rather than discovered, for now. Shaping the boundary as
// an interface costs a name and a contract; discovering modules at runtime
// would cost a plugin mechanism, a compatibility promise and a security
// boundary, and nobody has asked for one. This is the seam where that becomes
// possible later without being paid for now.
type Registry struct {
	byName map[string]Module
	// owner maps a kind to the module that declared it. Built once at
	// registration, which is also where a second module claiming a kind is
	// caught — that is what keeps two modules from colliding, rather than a
	// naming convention they would both have to remember to follow.
	owner map[string]Module
	decl  map[string]KindDecl
}

// NewRegistry returns a registry holding the given modules.
func NewRegistry(mods ...Module) (*Registry, error) {
	r := &Registry{
		byName: make(map[string]Module, len(mods)),
		owner:  make(map[string]Module),
		decl:   make(map[string]KindDecl),
	}
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
	if _, taken := r.byName[name]; taken {
		return fmt.Errorf("module %q: already registered", name)
	}

	for _, k := range m.Kinds() {
		if k.Name == "" {
			return fmt.Errorf("module %q: declared a kind with no name", name)
		}
		if prev, taken := r.owner[k.Name]; taken {
			// Two modules answering to one kind means an event nobody can
			// attribute and a rule nobody can predict. Caught here, at
			// registration, rather than left to surface as whichever module
			// happened to be asked first.
			return fmt.Errorf("module %q: kind %q already declared by %q", name, k.Name, prev.Name())
		}
		r.owner[k.Name] = m
		r.decl[k.Name] = k
	}

	r.byName[name] = m
	return nil
}

// Lookup returns the module that owns a kind, and whether one does. A kind
// nothing owns is a declaration naming an event that will never arrive.
func (r *Registry) Lookup(kind string) (Module, bool) {
	m, ok := r.owner[kind]
	return m, ok
}

// KindDeclFor returns the declaration of one kind, and whether it exists. This
// is what a matcher is checked against when a guardrail loads.
func (r *Registry) KindDeclFor(kind string) (KindDecl, bool) {
	k, ok := r.decl[kind]
	return k, ok
}

// DeclaredKinds returns every kind this registry can produce, in a stable
// order. Used to tell a rule author which kinds exist when their declaration
// names one that does not.
func (r *Registry) DeclaredKinds() []string {
	kinds := make([]string, 0, len(r.decl))
	for k := range r.decl {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	return kinds
}

// Needed returns the modules that some binding actually asks for, given the
// kinds bound to across the enabled guardrails.
//
// Producing an event nobody has bound to is work done to be discarded, and it
// is not cheap work: finding the programs a command line runs means walking its
// whole structure, on every command, whether or not any rule asks about
// commands. A project with no rule about commands should pay nothing for the
// fact that commands can be inspected at all.
func (r *Registry) Needed(boundKinds []string) []Module {
	seen := make(map[string]Module)
	for _, k := range boundKinds {
		if m, ok := r.owner[k]; ok {
			seen[m.Name()] = m
		}
	}

	needed := make([]Module, 0, len(seen))
	for _, m := range seen {
		needed = append(needed, m)
	}
	sort.Slice(needed, func(i, j int) bool { return needed[i].Name() < needed[j].Name() })
	return needed
}
