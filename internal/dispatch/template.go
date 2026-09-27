package dispatch

import (
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/aisbergg/gonja/pkg/gonja"
	"github.com/aisbergg/gonja/pkg/gonja/exec"
)

// This file renders a judge's `.md.j2` prompt template against its JudgeInput,
// through gonja — a real Jinja2 engine for Go (github.com/aisbergg/gonja), the
// same library and construction a10n uses (a10n-cli/internal/jinja/jinja.go).
//
// A judge is "a Jinja2 prompt template (.md.j2)" (dot-dir-file-store/main.tsp),
// rendered against the nature's judge-input model — the payload spread flat plus
// `additionalContext`. The output is the prompt handed to the model. Because the
// input is the SAME flat map for a template variable as for a script's stdin
// (checks.go assembles it once), a template reads `{{ event.newContent }}` and
// `{{ additionalContext.foo }}` as ordinary member accesses.
//
// # Why gonja rather than the hand-rolled subset renderer it replaces
//
// This used to be a ~800-line hand-rolled Jinja2 SUBSET (template.go +
// templateexpr.go): a tokenizer, a parser, and an expression evaluator that
// implemented interpolation, `if`/`for`, and a few operators, and REFUSED
// anything outside that subset. gonja is a full Jinja2, so a template author is
// not boxed into whichever constructs the subset happened to cover, and the
// engine carries a maintained library rather than a bespoke parser. The review
// note was direct: "use gonja (see how a10n did that)".
//
// # FAIL-CLOSED is preserved — including against a gonja that hangs
//
// The whole point of rendering here is that a prompt the engine cannot assemble
// must REFUSE, never render blank or partial and ask the model a different
// question than the author wrote. gonja PARSES strictly (most malformed tags are
// a parse error). Its undefined RESOLUTION is mixed, not uniformly strict: under
// the default UndefinedValue a bare `{{ missing }}` or a present-parent/absent-key
// access (`{{ event.absentKey }}`) renders EMPTY with no error, while a condition
// (`{% if missing %}`), an access off an ABSENT parent (`{{ nope.deep }}`), and
// iterating a `null` all THROW. Any error comes back from Render and runJudgeAgent
// turns it into a fail-closed refusal (judge.go). So fail-closed for the SHIPPED
// templates rests not on strict-undefined but on their inputs always being present:
// every bare interpolation reads a field off a parent (`event`, `additionalContext`,
// `context`) the assembler always supplies, and the fields read (`event.newContent`,
// `event.path`) are declared+present for the kinds a judge fires on. Non-strict is
// also the RIGHT choice here — a genuinely-empty file's `{{ event.newContent }}`
// should render empty, not throw.
//
// But this gonja fork has a sharp edge: some malformed inputs — an UNTERMINATED
// `{{` with no closing `}}` among them — send its lexer into an INFINITE LOOP
// rather than returning a parse error (verified against the pinned version). This
// render runs in the Go process BEFORE the sr-agent shell timeout, so a hang here
// is not caught by that timeout — it would wedge the hook forever, and a wedged
// preventive check is the opposite of fail-closed. So the gonja call is run under
// a WATCHDOG: it renders in a goroutine, and if it does not finish within
// renderTimeout the render is reported as an error (→ refusal). The goroutine may
// leak on a true hang, but the hook process refuses and exits, taking the leaked
// goroutine with it — a bounded, per-invocation cost that keeps the guarantee.
//
// # A fresh environment per call
//
// gonja.DefaultEnv (used by the package-level gonja.FromString) caches by
// template source and would return an earlier call's output for later calls with
// a different scope. Judges render different prompts against different inputs, so
// a shared cache is exactly wrong here — a fresh Environment per call, the same
// thing a10n does and for the same reason.

// renderTimeout bounds how long a single gonja render may run before it is
// treated as a failure. Generous relative to any real template (the shipped ones
// render in well under a millisecond), so it only ever fires on a pathological
// input — a gonja lexer loop — not on a large-but-valid prompt. A refusal, never
// a blank prompt, is the fail-closed outcome when it fires.
//
// A var, not a const, only so a test can lower it to prove the watchdog fires
// without waiting the full production bound; nothing in production writes it.
var renderTimeout = 5 * time.Second

// # Values are escaped by default, cheaply
//
// A judge prompt wraps what it judges in tags (`<message>…</message>`), and the
// wrapped value is written by the agent being judged. The one thing that must not
// survive into the prompt is a value closing the tag it sits in, so every string a
// template renders has `</` broken to `<\/` — before rendering, whatever the
// template does. Nothing else changes: HTML escaping (`&#34;` for every quote)
// would bloat a prompt of code or diffs and make it harder to read, for no gain
// the tags do not already give. `| raw` restores a value a template means to
// embed as markup; `| e` (and `| escape`) is the same cheap escape, a no-op on an
// already-escaped value.

// escapeClose breaks every closing-tag opener in s.
func escapeClose(s string) string { return strings.ReplaceAll(s, "</", "<\\/") }

// escapeStrings returns v with escapeClose applied to every string inside it.
func escapeStrings(v any) any {
	switch t := v.(type) {
	case string:
		return escapeClose(t)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			out[k] = escapeStrings(x)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = escapeStrings(x)
		}
		return out
	}
	return v
}

// renderTemplate renders src against vars and returns the prompt text.
//
// vars is the judge-input as a decoded JSON object (map[string]any) — the flat
// payload plus `additionalContext`. A render error is returned rather than a
// partial string: the caller refuses on it (fail-closed), because a half-rendered
// prompt asks the model a different question than the author wrote. A render that
// does not finish within renderTimeout is likewise reported as an error, so a
// gonja pathology (an unterminated `{{` loops its lexer) refuses rather than
// wedging the hook.
func renderTemplate(src string, vars map[string]any) (string, error) {
	type result struct {
		out string
		err error
	}
	// Buffered so the goroutine can always send and exit even after the watchdog
	// gave up waiting — an unbuffered send would block a would-be-leaked goroutine
	// on a channel no one reads.
	done := make(chan result, 1)
	escaped, _ := escapeStrings(vars).(map[string]any)
	go func() {
		out, err := renderGonja(src, escaped)
		done <- result{out, err}
	}()

	select {
	case r := <-done:
		return r.out, r.err
	case <-time.After(renderTimeout):
		return "", fmt.Errorf(
			"template: render did not finish within %s (a malformed template can loop this renderer); refusing rather than wedging on it",
			renderTimeout)
	}
}

// cheapEscape is `| e`: the same closing-tag break every value already had.
func cheapEscape(e *exec.Evaluator, in exec.Value, params *exec.VarArgs) exec.Value {
	return e.ValueFactory.Value(escapeClose(in.String()))
}

// renderGonja is the actual gonja render: a fresh environment, the two registered
// filters, parse, execute. Split out so renderTemplate can run it under a
// watchdog without the timeout machinery obscuring the mirror of a10n's jinja.go.
func renderGonja(src string, vars map[string]any) (string, error) {
	env := gonja.NewEnvironment()
	// This gonja fork ships no string `.split`, slicing, or `dirname`, so a
	// template cannot derive a path's directory on its own. Register the same two
	// small filters a10n registers, so a judge prompt can name a marker's file or
	// a symbol's short name without a prepare step doing it:
	//   {{ some_path | dirname }}   -> the directory of a path
	//   {{ "e2e.TestFoo" | funcname }} -> "TestFoo"
	env.Filters.Update(exec.FilterSet{
		"e":      cheapEscape,
		"escape": cheapEscape,
		"raw": func(e *exec.Evaluator, in exec.Value, params *exec.VarArgs) exec.Value {
			return e.ValueFactory.Value(strings.ReplaceAll(in.String(), "<\\/", "</"))
		},
		"dirname": func(e *exec.Evaluator, in exec.Value, params *exec.VarArgs) exec.Value {
			return e.ValueFactory.Value(path.Dir(in.String()))
		},
		"funcname": func(e *exec.Evaluator, in exec.Value, params *exec.VarArgs) exec.Value {
			s := in.String()
			if i := strings.LastIndex(s, "."); i >= 0 {
				s = s[i+1:]
			}
			return e.ValueFactory.Value(s)
		},
	})
	tpl, err := env.FromString(src)
	if err != nil {
		return "", fmt.Errorf("template: parse: %w", err)
	}
	out, err := tpl.Execute(vars)
	if err != nil {
		return "", fmt.Errorf("template: render: %w", err)
	}
	return out, nil
}
