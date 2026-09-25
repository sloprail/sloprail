// The sloprail-content plugin's OWN end-to-end test module — mirrors
// sloprail-tasks/tests exactly: a SEPARATE Go module (its own go.mod), nested
// inside the plugin it tests, importing the SHARED harness via a `replace`
// pointing at the main repo root so this plugin's guardrails and the e2e that
// prove them ship together as one self-contained unit.
//
// The path is computed from this tests/ directory up to the repo root that
// holds the main go.mod: marketplace/plugins/sloprail-content/tests ->
// ../../../.. is the root.

module github.com/sloprail/sloprail-content/tests

go 1.25.0

replace github.com/sloprail/sloprail => ../../../..

require github.com/sloprail/sloprail v0.0.0-00010101000000-000000000000

require (
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	golang.org/x/sys v0.47.0 // indirect
	modernc.org/libc v1.74.4 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
	modernc.org/sqlite v1.56.0 // indirect
)
