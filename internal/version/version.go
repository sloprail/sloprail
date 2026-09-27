// Package version holds the one build-time version string every sr* binary
// reports through Cobra's own --version flag.
//
// Version is a bare semver (0.2.1, no leading v) so it reads the same as
// plugin.json's version field and can be compared against it directly — the
// plugin's installation check does exactly that, deciding whether an already
// -installed binary is below the plugin's own required minimum.
//
// Set via -ldflags at build time (see the Makefile's build and release
// targets: -X github.com/sloprail/sloprail/internal/version.Version=$(VERSION)).
// A binary built any other way — `go build ./services/sr-session`, `go run`,
// a test binary — gets "dev", which is deliberately not a valid semver: a
// version-comparison caller must treat it as "no known version" rather than
// as older or newer than any real release.
package version

// Version is overwritten by -ldflags at release build time.
var Version = "dev"
