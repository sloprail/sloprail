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

// This gonja does not fail on a filter it cannot apply. A filter name it does
// not have renders as the error object's name (`<*errors.errorString>`) with no
// error returned, and a few of its own filters (slice, sum, unique, urlize)
// RETURN an error as their value instead of raising it, with the same result. In
// a judge prompt that is a fail-open: a typo hands the model garbage and the
// judge rules on it anyway. So an unknown filter name is caught before rendering
// (CheckTemplate), and an error a filter returns is raised as a render error
// (raiseFilterErrors) — both refuse, fail-closed, like any other render error.

// CheckTemplate reports whether a judge template can be rendered by this engine:
// it parses, and every filter it names — in an expression, a `{% filter %}`
// block or a `{% set %}`, in a branch that runs or one that does not — is one
// this engine has. The load check runs it on every judge template, and every
// render runs it first, so a template that cannot render is refused with its own
// positions, before anything is rewritten.
func CheckTemplate(src string) error {
	env := newTemplateEnv()
	tpl, err := env.FromString(src)
	if err != nil {
		return fmt.Errorf("template: parse: %w", err)
	}
	var unknown []string
	seen := map[string]bool{}
	for _, fc := range filterCalls(tpl.Root) {
		if env.Filters.Exists(fc.name) || seen[fc.name] {
			continue
		}
		seen[fc.name] = true
		unknown = append(unknown, fmt.Sprintf("%q (line %d)", fc.name, fc.line))
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return fmt.Errorf("template: unknown filter %s — this engine has no such filter, so the template cannot be rendered; fix the name",
		strings.Join(unknown, ", "))
}

// filterCall is one filter a template names, and the line it sits on.
type filterCall struct {
	name string
	line int
}

var filterCallType = reflect.TypeOf(parse.FilterCall{})

// filterCalls finds every parse.FilterCall in a parsed template. gonja's own
// Walk visits only a template's top-level wrappers, and a statement keeps its
// filters in unexported fields (a `{% filter %}` block's chain), so this walks
// the node graph by reflection — reading, never writing, and visiting each
// pointer once.
func filterCalls(root any) []filterCall {
	var out []filterCall
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
			if v.Type() == filterCallType {
				fc := filterCall{name: v.FieldByName("Name").String()}
				if tok := v.FieldByName("Token"); !tok.IsNil() {
					fc.line = int(tok.Elem().FieldByName("Line").Int())
				}
				out = append(out, fc)
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

// raiseFilterErrors wraps every filter in fs so an error it returns as its
// value is raised instead — a render error naming the filter, never the error
// object's name printed into the prompt.
func raiseFilterErrors(fs *exec.FilterSet) {
	for name, fn := range *fs {
		name, fn := name, fn
		(*fs)[name] = func(e *exec.Evaluator, in exec.Value, params *exec.VarArgs) exec.Value {
			out := fn(e, in, params)
			if err, ok := errorValue(out); ok {
				errors.ThrowTemplateRuntimeError("filter %q failed: %s", name, err)
			}
			return out
		}
	}
}

// errorValue reports whether v holds an error. Some gonja values cannot be
// turned into an interface at all (they panic); those hold no error.
func errorValue(v exec.Value) (err error, ok bool) {
	defer func() {
		if recover() != nil {
			err, ok = nil, false
		}
	}()
	if v == nil {
		return nil, false
	}
	err, ok = v.Interface().(error)
	return err, ok
}
