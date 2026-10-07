package harnessmock

import (
	"fmt"
	"os"
	"path/filepath"
)

// Harness is an agent harness a project or plugin can target.
type Harness string

// The harnesses.
const (
	Claude Harness = "claude"
	Codex  Harness = "codex"
	Cursor Harness = "cursor"
)

// entry is one row of the selection table: the mock for a harness, its pinned version ("" while the
// mock is not released) and the directory markers that say a project or plugin targets the harness.
type entry struct {
	harness  Harness
	binary   string
	version  string
	released bool
	markers  []string
}

// table is THE place where a harness maps to its mock. Order is the detection priority.
var table = []entry{
	{harness: Claude, binary: "a10n-claude-mock", version: Version, released: true, markers: []string{".claude-plugin"}},
	{harness: Codex, binary: "a10n-codex-mock", markers: []string{".codex-plugin", ".codex"}},
	{harness: Cursor, binary: "a10n-cursor-mock", markers: []string{".cursor-plugin", ".cursor"}},
}

// Mock is the selected mock.
type Mock struct {
	Harness Harness
	Binary  string // the mock's binary name
	Version string // its pinned version
	Path    string // where it was found (Select only)
}

// Detect names the harness a folder targets: the first table row (Claude, Codex, Cursor) one of whose
// marker directories exists in dir. A folder with none targets Claude, the default.
func Detect(dir string) Harness {
	for _, e := range table {
		for _, m := range e.markers {
			if fi, err := os.Stat(filepath.Join(dir, m)); err == nil && fi.IsDir() {
				return e.harness
			}
		}
	}
	return Claude
}

// Resolve picks the mock for a harness without looking for the binary: override ("claude", "codex",
// "cursor"; "" detects from dir). A harness whose mock is not released is an error naming it.
func Resolve(override, dir string) (Mock, error) {
	h := Harness(override)
	if override == "" {
		h = Detect(dir)
	}
	for _, e := range table {
		if e.harness != h {
			continue
		}
		if !e.released {
			return Mock{}, fmt.Errorf("%s not released yet (the %s harness has no mock to run a case against)", e.binary, e.harness)
		}
		return Mock{Harness: e.harness, Binary: e.binary, Version: e.version}, nil
	}
	return Mock{}, fmt.Errorf("unknown harness %q: use claude, codex or cursor", override)
}

// Select is Resolve plus locating the binary (see Path).
func Select(override, dir string) (Mock, error) {
	m, err := Resolve(override, dir)
	if err != nil {
		return Mock{}, err
	}
	if m.Path, err = Path(); err != nil {
		return Mock{}, err
	}
	return m, nil
}
