package checkrun

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/gitrepo"
)

// whenWorkers is how many `when` scripts run side by side.
func whenWorkers() int { return max(2, min(8, runtime.NumCPU())) }

// whenCache remembers what a requirement's `when` script said about a subject of a staged change, so
// a second evaluation of the same candidate (the gate asks twice: once as `when`, once as `check`;
// and an agent retries the same commit) does not run the scripts again. A verdict is keyed by what
// the script can read: the script and its folder's files (and the shared _lib above the rule), the
// candidate's TREE, the range's base and the subject. The candidate commit's own sha is not part of
// it: it is new on every evaluation. Best effort: any trouble reading or writing means a miss.
type whenCache struct {
	dir string
	key string
}

func newWhenCache(root string, g declaration.FileGuard, when string, rng gitrepo.Range) *whenCache {
	c := &whenCache{}
	common := gitLine(root, "rev-parse", "--path-format=absolute", "--git-common-dir")
	tree := gitLine(root, "rev-parse", rng.Head+"^{tree}")
	if common == "" || tree == "" {
		return c
	}
	h := sha256.New()
	h.Write([]byte(when + "\x00" + g.Name + "\x00" + tree + "\x00" + rng.Base + "\x00"))
	for _, d := range []string{g.Dir, filepath.Join(g.Dir, "..", "..", "_lib")} {
		if !hashDir(h, d) && d == g.Dir {
			return c
		}
	}
	c.dir = filepath.Join(common, "sloprail", "when-cache")
	c.key = hex.EncodeToString(h.Sum(nil))[:32]
	return c
}

func hashDir(h interface{ Write([]byte) (int, error) }, dir string) bool {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return false
		}
		h.Write([]byte(e.Name() + "\x00"))
		h.Write(b)
		h.Write([]byte{0})
	}
	return true
}

func (c *whenCache) path(subject string) string {
	if c.dir == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(c.key + "\x00" + subject))
	return filepath.Join(c.dir, hex.EncodeToString(sum[:16]))
}

func (c *whenCache) get(subject string) (applies, ok bool) {
	p := c.path(subject)
	if p == "" {
		return false, false
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return false, false
	}
	switch string(b) {
	case "1":
		return true, true
	case "0":
		return false, true
	}
	return false, false
}

func (c *whenCache) put(subject string, applies bool) {
	p := c.path(subject)
	if p == "" || os.MkdirAll(c.dir, 0o755) != nil {
		return
	}
	v := "0"
	if applies {
		v = "1"
	}
	tmp := fmt.Sprintf("%s.%d.tmp", p, os.Getpid())
	if os.WriteFile(tmp, []byte(v), 0o644) == nil {
		_ = os.Rename(tmp, p)
	}
}

func gitLine(dir string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	b, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
