package dispatch

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/aisbergg/gonja/pkg/gonja/errors"
	"github.com/aisbergg/gonja/pkg/gonja/exec"
	"github.com/aisbergg/gonja/pkg/gonja/parse"
)

// This gonja does not fail cleanly on a filter or test it cannot apply:
//
//   - a filter name it does not have renders as the error object's name
//     (`<*errors.errorString>`) with no error returned — also when the name is an
//     ARGUMENT, as in `map("nosuch")`, where the error lands inside the list;
//   - a few of its own filters (slice, sum, unique, urlize) RETURN an error as
//     their value instead of raising it, with the same result;
//   - a test name it does not have (`is nosuchtest`, `select("nosuchtest")`)
//     panics with a value that panics again when printed, which takes the whole
//     process down.
//
// In a judge prompt the first two are a fail-open (a typo hands the model
// garbage and the judge rules on it anyway) and the third wedges the hook. So a
// literal filter or test name is checked before rendering (CheckTemplate), an
// error a filter returns — at any depth of the value — is raised as a render
// error (raiseFilterErrors), and a panic during rendering is recovered into one
// (renderTemplate). All of them refuse, fail-closed, naming what failed.

// CheckTemplate reports whether a judge template can be rendered by this engine:
// it parses, and every filter and test it names — in an expression, a
// `{% filter %}` block, a `{% set %}`, an `is` test, or as the literal name
// map/select/reject/selectattr/rejectattr apply; in a branch that runs or one
// that does not — is one this engine has. The load check runs it on every judge
// template, and every render runs it first, so a template that cannot render is
// refused with its own positions, before anything is rewritten.
func CheckTemplate(src string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("template: parse: %s", panicText(r))
		}
	}()
	env := newTemplateEnv()
	tpl, err := env.FromString(src)
	if err != nil {
		return fmt.Errorf("template: parse: %w", err)
	}
	var unknown []string
	seen := map[string]bool{}
	for _, n := range namedCalls(tpl.Root) {
		known := env.Filters.Exists(n.name) && !reservedFilters[n.name]
		if n.test {
			known = env.Tests.Exists(n.name)
		}
		if known || seen[n.kind()+n.name] {
			continue
		}
		seen[n.kind()+n.name] = true
		unknown = append(unknown, fmt.Sprintf("%s %q (line %d)", n.kind(), n.name, n.line))
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return fmt.Errorf("template: unknown %s — this engine has no such filter or test, so the template cannot be rendered; fix the name",
		strings.Join(unknown, ", "))
}

// reservedFilters are the engine's own, applied by context and never by a
// template: attrescape wraps every value in a quoted attribute
// (escapeAttributeValues), and a template naming it too would escape twice. To
// a template it is an unknown filter.
var reservedFilters = map[string]bool{"attrescape": true}

// namedCall is one filter or test a template names, and the line it sits on.
type namedCall struct {
	name string
	test bool
	line int
}

func (n namedCall) kind() string {
	if n.test {
		return "test"
	}
	return "filter"
}

// argNamed says which filters take the name of another filter or test as an
// argument: the position of that argument, the keyword it may be passed by, and
// whether it names a test.
var argNamed = map[string]struct {
	pos     int
	keyword string
	test    bool
}{
	"map":        {0, "filter", false},
	"select":     {0, "", true},
	"reject":     {0, "", true},
	"selectattr": {1, "", true},
	"rejectattr": {1, "", true},
}

var (
	filterCallType = reflect.TypeOf(parse.FilterCall{})
	testCallType   = reflect.TypeOf(parse.TestCall{})
	stringNodeType = reflect.TypeOf(parse.StringNode{})
)

// namedCalls finds every filter and test a parsed template names. gonja's own
// Walk visits only a template's top-level wrappers, and a statement keeps its
// parts in unexported fields (a `{% filter %}` block's chain), so this walks the
// node graph by reflection — reading, never writing, visiting each pointer once.
// A name passed as a variable cannot be known here; renderTemplate catches it
// when it runs.
func namedCalls(root any) []namedCall {
	var out []namedCall
	seen := map[uintptr]bool{}
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() || seen[v.Pointer()] {
				return
			}
			seen[v.Pointer()] = true
			walk(v.Elem())
		case reflect.Interface:
			if !v.IsNil() {
				walk(v.Elem())
			}
		case reflect.Struct:
			switch v.Type() {
			case filterCallType:
				fc := namedCall{name: v.FieldByName("Name").String(), line: tokenLine(v.FieldByName("Token"))}
				out = append(out, fc)
				if spec, ok := argNamed[fc.name]; ok {
					if arg, ok := literalArg(v, spec.pos, spec.keyword); ok && arg != "" {
						out = append(out, namedCall{name: arg, test: spec.test, line: fc.line})
					}
				}
			case testCallType:
				out = append(out, namedCall{name: v.FieldByName("Name").String(), test: true, line: tokenLine(v.FieldByName("Token"))})
			}
			for i := 0; i < v.NumField(); i++ {
				walk(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		case reflect.Map:
			iter := v.MapRange()
			for iter.Next() {
				walk(iter.Key())
				walk(iter.Value())
			}
		}
	}
	walk(reflect.ValueOf(root))
	return out
}

// literalArg is a call's argument at pos (or passed by keyword), when it is a
// string literal.
func literalArg(call reflect.Value, pos int, keyword string) (string, bool) {
	var arg reflect.Value
	if args := call.FieldByName("Args"); pos < args.Len() {
		arg = args.Index(pos)
	} else if keyword != "" {
		if kw := call.FieldByName("Kwargs"); !kw.IsNil() {
			arg = kw.MapIndex(reflect.ValueOf(keyword))
		}
	}
	for arg.IsValid() && (arg.Kind() == reflect.Interface || arg.Kind() == reflect.Pointer) && !arg.IsNil() {
		arg = arg.Elem()
	}
	if !arg.IsValid() || arg.Kind() != reflect.Struct || arg.Type() != stringNodeType {
		return "", false
	}
	return arg.FieldByName("Val").String(), true
}

// tokenLine is the line of a *parse.Token field, or 0 without one.
func tokenLine(tok reflect.Value) int {
	if !tok.IsValid() || tok.IsNil() {
		return 0
	}
	return int(tok.Elem().FieldByName("Line").Int())
}

// raiseFilterErrors wraps every filter in fs so an error it returns — as its
// value, or as an element directly inside the list or map it returns — is raised
// instead: a render error naming the filter, never the error object's name
// printed into the prompt.
func raiseFilterErrors(fs *exec.FilterSet) {
	for name, fn := range *fs {
		name, fn := name, fn
		(*fs)[name] = func(e *exec.Evaluator, in exec.Value, params *exec.VarArgs) exec.Value {
			checkNamedArgument(e, name, params)
			out := fn(e, in, params)
			if err := errorIn(out); err != nil {
				errors.ThrowTemplateRuntimeError("filter %q failed: %s", name, err)
			}
			return out
		}
	}
}

// checkNamedArgument raises a runtime error, before the filter runs, when a
// filter that applies another filter or test by name (map, select, reject,
// selectattr, rejectattr) is handed a name that does not exist — one passed as
// a variable, which CheckTemplate cannot see. gonja's own error for an unknown
// test garbles its message to just the name (its throw passes the format as
// the function name) and panics with it; this says what it is.
func checkNamedArgument(e *exec.Evaluator, filter string, params *exec.VarArgs) {
	spec, ok := argNamed[filter]
	if !ok || params == nil {
		return
	}
	var arg exec.Value
	if spec.pos < len(params.Args) {
		arg = params.Args[spec.pos]
	} else if spec.keyword != "" && params.HasKwarg(spec.keyword) {
		arg = params.GetKwarg(spec.keyword)
	}
	if arg == nil || arg.IsNil() || !arg.IsString() || arg.String() == "" {
		return
	}
	named := arg.String()
	if spec.test && !e.Tests.Exists(named) {
		errors.ThrowTemplateRuntimeError("unknown test %q (passed to %s)", named, filter)
	}
	if !spec.test && !e.Filters.Exists(named) {
		errors.ThrowTemplateRuntimeError("unknown filter %q (passed to %s)", named, filter)
	}
}

// errorIn returns an error a filter returned: the value itself, or one element
// directly inside it. That depth is enough, and keeps the check linear in what
// the filter returned rather than in everything it holds: an error value only
// ever comes from a filter — raised at the top of that filter's own return, by
// this wrapper — or from map applying a filter by a name that does not exist,
// which puts the error one level down, in map's own list. Anything deeper was
// already some filter's output when it was produced, and was checked then.
func errorIn(v exec.Value) error {
	top := valueOf(v)
	if err, ok := top.(error); ok {
		return err
	}
	rv := reflect.ValueOf(top)
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		for i := 0; i < rv.Len(); i++ {
			if err := elementError(rv.Index(i)); err != nil {
				return err
			}
		}
	case reflect.Map:
		iter := rv.MapRange()
		for iter.Next() {
			if err := elementError(iter.Value()); err != nil {
				return err
			}
		}
	}
	return nil
}

// elementError is the error an element holds, directly or as a gonja Value.
func elementError(rv reflect.Value) error {
	if !rv.IsValid() || !rv.CanInterface() {
		return nil
	}
	switch x := rv.Interface().(type) {
	case error:
		return x
	case exec.Value:
		if err, ok := valueOf(x).(error); ok {
			return err
		}
	}
	return nil
}

// valueOf is what a gonja Value holds. Some cannot be turned into an interface
// at all (they panic); those hold no error.
func valueOf(v exec.Value) (out any) {
	defer func() {
		if recover() != nil {
			out = nil
		}
	}()
	if v == nil {
		return nil
	}
	return v.Interface()
}

// panicText is a recovered panic value as text, even when printing it panics
// too: gonja's unknown-test panic is an error whose Error() dereferences a
// position it was never given, so its message is read from the error's own
// `msg` field instead.
func panicText(r any) (text string) {
	defer func() {
		if recover() != nil {
			text = fmt.Sprintf("%T", r)
			if msg := messageField(reflect.ValueOf(r), 0); msg != "" {
				text = msg
			}
		}
	}()
	if err, ok := r.(error); ok {
		return err.Error()
	}
	return fmt.Sprint(r)
}

// messageField finds a string field named msg in v or a struct it embeds.
func messageField(v reflect.Value, depth int) string {
	for depth < 8 && v.IsValid() && (v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface) && !v.IsNil() {
		v = v.Elem()
	}
	if depth >= 8 || !v.IsValid() || v.Kind() != reflect.Struct {
		return ""
	}
	if f := v.FieldByName("msg"); f.IsValid() && f.Kind() == reflect.String {
		return f.String()
	}
	for i := 0; i < v.NumField(); i++ {
		if v.Type().Field(i).Anonymous {
			if msg := messageField(v.Field(i), depth+1); msg != "" {
				return msg
			}
		}
	}
	return ""
}
