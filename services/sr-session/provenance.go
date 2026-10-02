package main

import "github.com/sloprail/sloprail/internal/checkrun"

// LaunchedByEnv names the guardrails whose hooks a session is running underneath: see
// checkrun.LaunchedByEnv for the recursion it ends.
const LaunchedByEnv = checkrun.LaunchedByEnv

func launchedBy(getenv func(string) string) []string { return checkrun.LaunchedBy(getenv) }

// isLaunchedBy reports whether this session is running underneath the named guardrail's own
// hook: that guardrail is not enforced inside it, every other one is.
func isLaunchedBy(getenv func(string) string, guardrail string) bool {
	return checkrun.IsLaunchedBy(getenv, guardrail)
}

// appendLaunchedBy is the value LaunchedByEnv should carry inside one guardrail's hook.
func appendLaunchedBy(getenv func(string) string, guardrail string) string {
	return checkrun.AppendLaunchedBy(getenv, guardrail)
}
