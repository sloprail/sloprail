# Routing note: the one line `impl/hook-env` must take

`impl/hook-env` is unmerged and owns `services/sloprail/hookenv.go`. This branch
adds a provenance variable to the hook's environment, which is the same
mechanism that file exists for. Both branches assign `c.Env` on the same line of
`runHooks`, so this is a real collision and not a textual one.

Nothing on that branch was merged here. This note is the whole handoff.

## What this branch added

`services/sloprail/provenance.go` — `SLOPRAIL_LAUNCHED_BY`, a colon-separated
list of the guardrails whose hooks the current process is running underneath,
plus `launchedBy` / `isLaunchedBy` / `appendLaunchedBy` / `hookEnv`.

Against the MERGED shape, `runHooks` gained:

```go
c := exec.Command("sh", "-c", h.Command)
c.Dir = d.Dir
c.Env = hookEnv(d.Name)          // ← added here
c.Stdin = strings.NewReader(string(payload))
```

## What `impl/hook-env` should take instead

`hookEnv` is this branch's own `append(os.Environ(), ...)`, which is exactly
what `hookScope.env` already does on that branch. Do NOT keep both — two
functions appending to `os.Environ()` for the same exec is how one silently
drops the other's variable.

Fold the provenance variable into `hookScope.env`, and delete `hookEnv` from
`provenance.go`. Everything else in `provenance.go` is independent of that
branch and transfers unchanged.

```go
func (s hookScope) env(guardrail string) []string {
	env := append(os.Environ(),
		GuardrailEnv+"="+guardrail,
		SessionEnv+"="+s.SessionID,
		// Which rules this process is running underneath. Inherited across the
		// exec into a launched agent's own hooks, which is what lets the engine
		// one level down decline to enforce the rule that launched it. Appended
		// after the inherited block for the same reason the other two are: Go's
		// exec resolves a repeated name to the last occurrence, so a value from
		// an outer process cannot override the engine's own answer.
		LaunchedByEnv+"="+appendLaunchedBy(os.Getenv, guardrail),
	)
	return append(env, s.workspaceEnv()...)
}
```

The call site then keeps that branch's line unchanged:

```go
c.Env = scope.env(d.Name)
```

## Why the ordering argument already covers this

That branch's `env` doc explains why the engine's own variables go after
`os.Environ()`: a repeated name resolves to the last occurrence, so an inherited
`SR_GUARDRAIL` cannot override the engine. `SLOPRAIL_LAUNCHED_BY` needs the
identical property for a sharper reason — the value is inherited BY DESIGN, and
the engine must be the only thing that extends it. Appending in the same block
gets that for free, and is why this belongs inside `env` rather than beside it.

## The skip itself does not collide

The other half of this branch is in the enforcement loop, guarded per
declaration:

```go
if isLaunchedBy(os.Getenv, d.Name) {
	continue
}
```

`impl/hook-env` does not touch that loop body, so it merges as ordinary text.
