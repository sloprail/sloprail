package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// ErrBadHarnessArgs is returned when a `--<harness>-args` value is not a JSON
// object of scalars.
var ErrBadHarnessArgs = errors.New("invalid harness arguments")

// ErrWrongHarnessArgs is returned when one harness's args flag is given while a
// different harness is running.
var ErrWrongHarnessArgs = errors.New("harness arguments for the wrong harness")

// ParseHarnessArgs turns a `--claude-args` JSON object into flag/value pairs.
//
// The value is decoded as JSON rather than scanned for a key pattern. That is
// not fussiness: a previous command in this repo read JSON by looking for the
// literal `"key":"` and worked only because Go's encoder emits exactly that
// spacing — every hand-written object, and every object from any other
// producer, would have silently yielded nothing. A real decoder accepts
// whatever is valid JSON, which is the only thing the flag promises.
//
// Values are decoded into `any` so that numbers and booleans are accepted
// alongside strings — a caller writing `{"max-budget-usd": 5}` wrote valid JSON
// and meant something obvious. They are rendered back to strings for the
// command line, since that is what an argv holds. Nested objects and arrays are
// refused: there is no unambiguous way to render one as a flag value, and
// guessing would pass the harness something the author did not write.
//
// Keys are returned sorted, so one input always produces one argv. An argv that
// varied run to run would make the command unreproducible and its tests
// order-dependent.
func ParseHarnessArgs(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}

	// json.Number keeps the literal the author wrote. Decoding into float64
	// would turn `5` into `5` but `1e3` into `1000`, and a large integer into
	// scientific notation — passing the harness a number the author never
	// typed.
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()

	var obj map[string]any
	if err := dec.Decode(&obj); err != nil {
		return nil, fmt.Errorf(
			"%w: expected a JSON object like '{\"permission-mode\":\"plan\"}', got %s",
			ErrBadHarnessArgs, err)
	}
	// A trailing token means the value was more than one JSON document —
	// `{} {}` decodes the first and would silently drop the rest.
	if dec.More() {
		return nil, fmt.Errorf(
			"%w: expected a single JSON object, got more than one value", ErrBadHarnessArgs)
	}
	if obj == nil {
		return nil, fmt.Errorf(
			"%w: expected a JSON object, got null", ErrBadHarnessArgs)
	}

	keys := make([]string, 0, len(obj))
	for key := range obj {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	args := make([]string, 0, len(keys)*2)
	for _, key := range keys {
		if key == "" {
			return nil, fmt.Errorf("%w: empty key", ErrBadHarnessArgs)
		}
		value, err := scalarString(obj[key])
		if err != nil {
			return nil, fmt.Errorf("%w: key %q: %s", ErrBadHarnessArgs, key, err)
		}
		args = append(args, "--"+key, value)
	}
	return args, nil
}

// scalarString renders one JSON scalar as a command-line value.
func scalarString(value any) (string, error) {
	switch v := value.(type) {
	case string:
		return v, nil
	case json.Number:
		return v.String(), nil
	case bool:
		return strconv.FormatBool(v), nil
	case nil:
		return "", errors.New("null is not a value a flag can carry")
	default:
		return "", fmt.Errorf(
			"only strings, numbers and booleans can be flag values, got %T", value)
	}
}

// CheckHarnessArgs reports a harness-args flag given while a different harness
// is running.
//
// This is an ERROR rather than something ignored. The author passed settings
// they expected to take effect; dropping them silently means the agent runs
// configured differently than the command says it is, and nothing anywhere
// says so. Naming both harnesses tells them exactly what happened.
func CheckHarnessArgs(flagName string, forHarness Harness, running harnessSpec) error {
	if forHarness == running.name {
		return nil
	}
	return fmt.Errorf(
		"%w: %s applies to %s, but %s is running. "+
			"Those settings would not take effect; remove the flag or pass --harness %s",
		ErrWrongHarnessArgs, flagName, forHarness, running.name, forHarness)
}

// Invocation is the command line sr-agent will run.
type Invocation struct {
	// Binary is the harness executable.
	Binary string

	// Args is its full argument list, prompt included.
	Args []string
}

// String renders the invocation for diagnostics. Arguments containing spaces
// are quoted so a printed command can be pasted back into a shell and mean the
// same thing — a prompt is almost always such an argument.
func (inv Invocation) String() string {
	parts := make([]string, 0, len(inv.Args)+1)
	parts = append(parts, inv.Binary)
	for _, arg := range inv.Args {
		if strings.ContainsAny(arg, " \t\n\"'") {
			parts = append(parts, strconv.Quote(arg))
			continue
		}
		parts = append(parts, arg)
	}
	return strings.Join(parts, " ")
}

// BuildInvocation assembles the harness command line.
//
// The prompt goes LAST and positionally, following the harnesses themselves:
// both `claude` and `cursor-agent` take it that way. Last matters — a prompt
// beginning with a dash would otherwise be read as a flag, and putting it after
// every flag is what keeps a question like "--model isn't resolving, why?" a
// question rather than a parse error.
//
// `-p` is always passed. sr-agent exists to be called from a hook, and a hook
// has no terminal: an interactive session started there would hang holding the
// whole guardrail open. There is no flag to turn it off because there is no
// caller who wants it off.
func BuildInvocation(spec harnessSpec, model string, harnessArgs []string, prompt string) Invocation {
	args := make([]string, 0, len(harnessArgs)+4)
	args = append(args, "-p", "--model", model)
	args = append(args, harnessArgs...)
	args = append(args, prompt)
	return Invocation{Binary: spec.binary, Args: args}
}
