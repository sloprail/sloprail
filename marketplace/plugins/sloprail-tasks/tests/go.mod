// The sloprail-tasks plugin's OWN end-to-end test module — the first instance of
// the "each use-case plugin self-tests" model. It is a SEPARATE Go module from the
// main repo (its own go.mod), living inside the plugin it tests, so the plugin is a
// self-contained unit: its guardrails and the e2e that prove them ship together.
//
// It imports the SHARED harness (github.com/sloprail/sloprail/tests/e2e/harness) —
// the same one tests/e2e/harness/session/* drives the a10n-claude-
// mock through — via a `replace` pointing at the main repo root. The path is
// computed from this tests/ directory up to the repo root that holds the main
// go.mod: marketplace/plugins/sloprail-tasks/tests -> ../../../.. is the root.
//
// The main module is `require`d at a placeholder version; the `replace` makes that
// resolve to the local checkout rather than a published version, so the harness the
// tests build against is exactly the one in this tree (and the one whose engine the
// mock's hooks reach).
module github.com/sloprail/sloprail-tasks/tests

go 1.25.0

require github.com/sloprail/sloprail v0.0.0-00010101000000-000000000000

require (
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/expr-lang/expr v1.17.8 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/klauspost/compress v1.18.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	golang.org/x/sys v0.47.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	modernc.org/libc v1.74.4 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
	modernc.org/sqlite v1.56.0 // indirect
)

replace github.com/sloprail/sloprail => ../../../..
