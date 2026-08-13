module github.com/sloprail/sloprail

// Single root module, following the same reasoning a10n records: the binaries
// under services/* are main packages sharing internal/*, and what passes
// between them is exec rather than import. A module per service would add
// replace directives and separate tidies without buying isolation the exec
// boundary does not already give.

go 1.24
