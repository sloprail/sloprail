module github.com/sloprail/sloprail

// Single root module, following the same reasoning a10n records: the binaries
// under services/* are main packages sharing internal/*, and what passes
// between them is exec rather than import. A module per service would add
// replace directives and separate tidies without buying isolation the exec
// boundary does not already give.

go 1.25.0

require (
	github.com/expr-lang/expr v1.17.8
	github.com/spf13/cobra v1.10.2
	github.com/stretchr/testify v1.11.1
	gopkg.in/yaml.v3 v3.0.1
	modernc.org/sqlite v1.56.0
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	golang.org/x/sys v0.47.0 // indirect
	modernc.org/libc v1.74.4 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
	mvdan.cc/sh/v3 v3.13.1
)
