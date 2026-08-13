package main

import "testing"

// getenvFrom builds a lookup over a fixed map, so these tests never mutate the
// process environment — which under -race would race every other test reading
// one.
func getenvFrom(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLaunchedByReadsNothingWhenUnset(t *testing.T) {
	if got := launchedBy(getenvFrom(nil)); got != nil {
		t.Fatalf("want no guardrails, got %v", got)
	}
}

// A value of whitespace is the same as unset. A hook that exported the variable
// empty must not make the engine believe it is inside a launched agent.
func TestLaunchedByIgnoresBlank(t *testing.T) {
	for _, raw := range []string{"", "   ", ":", "::", " : "} {
		if got := launchedBy(getenvFrom(map[string]string{LaunchedByEnv: raw})); got != nil {
			t.Fatalf("%q: want no guardrails, got %v", raw, got)
		}
	}
}

func TestIsLaunchedByMatchesOnlyTheNamedRule(t *testing.T) {
	env := getenvFrom(map[string]string{LaunchedByEnv: "judge-notes"})

	if !isLaunchedBy(env, "judge-notes") {
		t.Fatal("the rule that launched this session should not be enforced in it")
	}
	// The property the whole design turns on: an unrelated rule is untouched.
	if isLaunchedBy(env, "no-secrets") {
		t.Fatal("an unrelated guardrail was disabled inside a launched agent")
	}
}

// The SUB-AGENT case. A launched agent may itself launch one, and the third
// level must decline BOTH rules above it — not merely the nearest.
//
// A single-valued marker would fail exactly here: the second append would
// overwrite the first, and the outer rule would become enforceable again one
// level down, which is the original bug displaced by one turn rather than
// fixed.
func TestIsLaunchedByCoversEveryLevel(t *testing.T) {
	outer := appendLaunchedBy(getenvFrom(nil), "judge-notes")
	inner := appendLaunchedBy(getenvFrom(map[string]string{LaunchedByEnv: outer}), "review-docs")

	env := getenvFrom(map[string]string{LaunchedByEnv: inner})
	for _, name := range []string{"judge-notes", "review-docs"} {
		if !isLaunchedBy(env, name) {
			t.Fatalf("%s is enforceable at the third level; the outer rule can re-enter", name)
		}
	}
	if isLaunchedBy(env, "no-secrets") {
		t.Fatal("a rule that launched nothing was disabled")
	}
}

// Appending the same name twice is a no-op. Enforcement cannot reach it twice,
// but a hook is free to run sloprail itself, and a value that grew unbounded
// down a long chain would eventually be an environment too large to exec.
func TestAppendLaunchedByDoesNotRepeat(t *testing.T) {
	env := getenvFrom(map[string]string{LaunchedByEnv: "judge-notes:review-docs"})
	if got := appendLaunchedBy(env, "judge-notes"); got != "judge-notes:review-docs" {
		t.Fatalf("want the value unchanged, got %q", got)
	}
}

func TestAppendLaunchedByStartsFromNothing(t *testing.T) {
	if got := appendLaunchedBy(getenvFrom(nil), "judge-notes"); got != "judge-notes" {
		t.Fatalf("want %q, got %q", "judge-notes", got)
	}
}

// hookEnv must put the engine's own answer LAST. Go's exec resolves a repeated
// name to the last occurrence, so a value inherited from an outer process must
// not be able to override what the engine says is running.
func TestHookEnvAppendsAfterTheInheritedBlock(t *testing.T) {
	env := hookEnv("judge-notes")

	last := ""
	for _, kv := range env {
		if len(kv) > len(LaunchedByEnv) && kv[:len(LaunchedByEnv)+1] == LaunchedByEnv+"=" {
			last = kv
		}
	}
	if last == "" {
		t.Fatalf("hookEnv set no %s", LaunchedByEnv)
	}
	if last != LaunchedByEnv+"=judge-notes" && !contains(last, "judge-notes") {
		t.Fatalf("the engine's own value did not win: %q", last)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
