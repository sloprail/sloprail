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

require github.com/aisbergg/gonja v0.0.0-20230529111726-b16083958108

require github.com/klauspost/compress v1.18.0

require (
	golang.org/x/mod v0.37.0 // indirect
	golang.org/x/sync v0.21.0 // indirect
	golang.org/x/tools v0.47.0
)

require (
	cuelang.org/go v0.17.1
	github.com/cockroachdb/apd/v3 v3.2.3 // indirect
	github.com/emicklei/proto v1.14.3 // indirect
	github.com/mitchellh/go-wordwrap v1.0.1 // indirect
	github.com/pelletier/go-toml/v2 v2.3.1 // indirect
	github.com/protocolbuffers/txtpbfmt v0.0.0-20260420112717-c39628bde8b5 // indirect
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/net v0.56.0 // indirect
	golang.org/x/text v0.38.0 // indirect
	google.golang.org/protobuf v1.33.0 // indirect
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/pmezard/go-difflib v1.0.0
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/spf13/pflag v1.0.10
	golang.org/x/sys v0.47.0
	modernc.org/libc v1.74.4 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
	mvdan.cc/sh/v3 v3.13.1
)
