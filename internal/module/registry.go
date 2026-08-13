package module

import (
	"fmt"
	"sort"
	"testing"

	"github.com/sloprail/sloprail/internal/module/internal/registryauth"
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
//
// The token is the fence, not a parameter with a value. It is a type only
// packages under internal/module/ can name, AND one no other package can
// construct without naming it — its unexported field is what makes the second
// half true, and the fence is worth nothing without it. Together they make
// internal/module/modules the only place in the repo that can assemble a module
// list — see internal/module/internal/registryauth for why. Everything else
// takes the registry modules.Registry hands it.
//
// So a hook point cannot quietly enforce against a vocabulary of its own. That
// is not a style preference: `guardrail help` is the only registry observable
// from outside the binary, so a second list anywhere else would be enforced and
// undocumented and unseen by every test at once.
func NewRegistry(_ registryauth.Token, mods ...Module) (*Registry, error) {
	return newRegistry(mods)
}

// NewRegistryForTest builds a registry from arbitrary modules, for tests that
// need a vocabulary of their own.
//
// The loader, the validator and the matcher are checked against modules
// declared in the test rather than against filemod, deliberately: the engine is
// supposed to work from declarations rather than from anything compiled into
// it, and a test borrowing a real module's kinds would stop proving that. Those
// tests need a door through the fence.
//
// It is a door with its name on it. It panics outside a test binary, so a hook
// point reaching for it does not get a second module list — it gets a crash on
// the first invocation, which is the loudest failure available and the opposite
// of the silent divergence the fence exists to stop.
func NewRegistryForTest(mods ...Module) (*Registry, error) {
	if !testing.Testing() {
		panic("module: NewRegistryForTest called outside a test — the shipped build has exactly one module list, and it is modules.Registry()")
	}
	return newRegistry(mods)
}

func newRegistry(mods []Module) (*Registry, error) {
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

// add registers one module, or changes nothing at all.
//
// Validate every kind first, write afterwards. The two loops are the whole
// point: a single loop that wrote as it walked left a module owning the kinds
// it had declared before the bad one while never being recorded under its own
// name — a registry holding half a module, which is a state no caller can
// reason about. NewRegistry discarding the registry on error hid that, but the
// hiding is the caller's doing, and this is a method on a live registry that any
// incremental caller can reach. An operation that fails should leave the thing
// it operated on exactly as it found it.
func (r *Registry) add(m Module) error {
	name := m.Name()
	if name == "" {
		return fmt.Errorf("module: registered with no name")
	}
	if _, taken := r.byName[name]; taken {
		return fmt.Errorf("module %q: already registered", name)
	}

	kinds := m.Kinds()

	// Staged, so a kind this module declares twice is caught here rather than
	// by the loop below reading a half-written registry.
	staged := make(map[string]KindDecl, len(kinds))
	for _, k := range kinds {
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
		if _, twice := staged[k.Name]; twice {
			// A module declaring one kind twice was already refused before
			// staging existed — by the r.owner check above, reading a write
			// this same loop had just made. Staging removes that write, so the
			// check has to be stated outright or the refusal would vanish as a
			// side effect of making the operation atomic.
			return fmt.Errorf("module %q: declared kind %q twice", name, k.Name)
		}
		staged[k.Name] = k
	}

	// Past here nothing can fail, so nothing can be left half-done.
	for kind, decl := range staged {
		r.owner[kind] = m
		r.decl[kind] = decl
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

// ModuleNames returns every registered module, in a stable order, whether or
// not it declared a kind.
//
// Registered rather than owning-a-kind is the distinction that matters. Kind
// ownership was the only thing the help reported, which left a module declaring
// no kinds invisible from outside the binary — it was in the build, it was
// enforcing nothing, and nothing could see either fact. A module with no kinds
// is a defect worth surfacing, not a module worth hiding: it produces no events,
// so anything a project bound to it would sit there looking enforced.
func (r *Registry) ModuleNames() []string {
	names := make([]string, 0, len(r.byName))
	for n := range r.byName {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
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
