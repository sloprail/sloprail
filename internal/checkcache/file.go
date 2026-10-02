package checkcache

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// File is a Cache kept as an append-only JSON-lines file: the local, single-machine
// store until the git-ref store (an orphan branch of content-addressed segments)
// replaces it behind the same interface. Appending a line is atomic enough for several
// processes writing at once, and a reader resolves duplicates as Cache.Lookup says.
type File struct{ path string }

// OpenFile returns the cache kept at path. The file is created on the first Put.
func OpenFile(path string) *File { return &File{path: path} }

func (f *File) Lookup(keys []Key) (map[string]Result, error) {
	want := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		want[k.ID()] = struct{}{}
	}
	out := make(map[string]Result, len(keys))
	fh, err := os.Open(f.path)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, fmt.Errorf("checkcache: %w", err)
	}
	defer fh.Close()
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var r Result
		if err := json.Unmarshal(line, &r); err != nil {
			return nil, fmt.Errorf("checkcache: unreadable result in %s: %w", f.path, err)
		}
		id := r.Key.ID()
		if _, ok := want[id]; !ok {
			continue
		}
		if prior, ok := out[id]; ok && !Newer(r, prior) {
			continue
		}
		out[id] = r
	}
	return out, sc.Err()
}

func (f *File) Put(results []Result) error {
	if len(results) == 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil {
		return fmt.Errorf("checkcache: %w", err)
	}
	var buf bytes.Buffer
	for _, r := range results {
		b, err := json.Marshal(r)
		if err != nil {
			return fmt.Errorf("checkcache: %w", err)
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	fh, err := os.OpenFile(f.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("checkcache: %w", err)
	}
	if _, err := fh.Write(buf.Bytes()); err != nil {
		fh.Close()
		return fmt.Errorf("checkcache: %w", err)
	}
	return fh.Close()
}
