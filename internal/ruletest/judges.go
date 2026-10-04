package ruletest

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/sloprail/sloprail/internal/dispatch"
)

// JudgeCall is one judge the engine asked during a case, and how the case answered.
type JudgeCall struct {
	// Key is `<nature>/<name>/<judge file>`.
	Key     string `json:"key"`
	Stubbed bool   `json:"stubbed"`
	Pass    bool   `json:"pass"`
	// MissingFromPrompt are the `prompt_contains` substrings the rendered prompt lacked.
	MissingFromPrompt []string `json:"missingFromPrompt,omitempty"`
}

// JudgeTable is a case's canned judges and the record of what was asked of them.
type JudgeTable struct {
	// Stubs is keyed by `<nature>/<name>/<judge file>`.
	Stubs map[string]JudgeStub `json:"stubs"`

	mu    sync.Mutex
	calls []JudgeCall
}

// ExpandJudgeKeys keys a case's `judges:` by `<nature>/<name>/<file>`: a bare file
// name is the rule under test's own.
func ExpandJudgeKeys(r Rule, judges map[string]JudgeStub) map[string]JudgeStub {
	out := make(map[string]JudgeStub, len(judges))
	for k, v := range judges {
		if strings.Count(k, "/") >= 2 {
			parts := strings.SplitN(k, "/", 3)
			out[JudgeKey(parts[0], parts[1], parts[2])] = v
			continue
		}
		out[JudgeKey(string(r.Nature), r.Name, k)] = v
	}
	return out
}

// Stub is the dispatch hook that answers judges from the table.
func (t *JudgeTable) Stub() dispatch.JudgeStub {
	return func(inv dispatch.JudgeInvocation) dispatch.JudgeAnswer {
		dir := filepath.Clean(inv.Dir)
		key := JudgeKey(filepath.Base(filepath.Dir(dir)), filepath.Base(dir), inv.Template)
		t.mu.Lock()
		defer t.mu.Unlock()
		stub, ok := t.Stubs[key]
		call := JudgeCall{Key: key, Stubbed: ok, Pass: stub.Pass}
		if ok {
			for _, want := range stub.PromptContains {
				if !strings.Contains(inv.Prompt, want) {
					call.MissingFromPrompt = append(call.MissingFromPrompt, want)
				}
			}
		}
		t.calls = append(t.calls, call)
		return dispatch.JudgeAnswer{Stubbed: ok, Pass: stub.Pass, Reasoning: stub.Reasoning}
	}
}

// Calls is what the engine asked, in order.
func (t *JudgeTable) Calls() []JudgeCall {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]JudgeCall(nil), t.calls...)
}

// Record adds calls made in another process (the replayed session).
func (t *JudgeTable) Record(calls []JudgeCall) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.calls = append(t.calls, calls...)
}

// Problems are what the case got wrong about its judges once it has run: a judge
// reached with no stub, a stub whose prompt lacked what the case required, and a
// stub never reached (unless optional).
func (t *JudgeTable) Problems() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []string
	reached := map[string]bool{}
	for _, c := range t.calls {
		reached[c.Key] = true
		if !c.Stubbed {
			out = append(out, fmt.Sprintf("the engine asked judge %s, which this case does not stub (a case never calls a model: add it under `judges:`)", c.Key))
			continue
		}
		for _, m := range c.MissingFromPrompt {
			out = append(out, fmt.Sprintf("the prompt judge %s was shown does not contain %q", c.Key, m))
		}
	}
	for key, stub := range t.Stubs {
		if !reached[key] && !stub.Optional {
			out = append(out, fmt.Sprintf("judge %s is stubbed but was never asked: the rule no longer reaches it here (mark it `optional: true` if that is expected)", key))
		}
	}
	return out
}
