package gitrepo

import (
	"bufio"
	"bytes"
	"os/exec"
	"strings"
	"sync"
)

// The commit graph, loaded once.
//
// A Stop that has to decide, for every recorded tip and every rule, whether a rule already
// passed it asks "is X an ancestor of Y" for each of the rule's passed heads: a session with
// two thousand passed runs and twenty tips asked a git process for each pair and took
// minutes. The questions are all about one repository's ancestry, which one
// `git rev-list --parents` answers whole, so this loads it once per process and answers the
// questions in memory. Everything here is exact (nothing is estimated): a commit the graph
// does not hold is one git does not hold either, and so is not an ancestor of anything.

// Graph is the ancestry of every commit reachable from a repository's refs and from the
// extra commits it was loaded with.
type Graph struct {
	parents map[string][]string
	mu      sync.Mutex
	anc     map[string]map[string]struct{}
	desc    map[string]map[string]struct{}
	kids    map[string][]string
	// tried is every extra commit the load was asked to include, held by git or not, so a
	// head git no longer has does not trigger a reload at every question.
	tried map[string]bool
}

var (
	graphMu sync.Mutex
	graphs  = map[string]*Graph{}
)

// maxGraphLines bounds what is loaded: a repository larger than this is asked commit by
// commit, as before.
const maxGraphLines = 3_000_000

// LoadGraph loads (once per directory) the graph of everything reachable from the
// repository's refs plus extra, the commits to include that no ref reaches (a passed head
// whose branch was rewritten away). An extra git does not have is left out. It returns nil
// when the graph cannot be loaded, and the caller asks git commit by commit.
func LoadGraph(dir string, extra ...string) *Graph {
	key := realPath(dir)
	graphMu.Lock()
	defer graphMu.Unlock()
	if g, ok := graphs[key]; ok {
		missing := false
		for _, e := range extra {
			if !g.tried[e] && isObjectName(e) {
				missing = true
				break
			}
		}
		if !missing {
			return g
		}
	}
	args := []string{"rev-list", "--parents", "--all"}
	var tips []string
	seen := map[string]bool{}
	for _, e := range extra {
		if isObjectName(e) && !seen[e] {
			seen[e] = true
			tips = append(tips, e)
		}
	}
	if old, ok := graphs[key]; ok {
		for e := range old.tried {
			if !seen[e] {
				seen[e] = true
				tips = append(tips, e)
			}
		}
	}
	for _, e := range existing(dir, tips) {
		args = append(args, e)
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil
	}
	if err := cmd.Start(); err != nil {
		return nil
	}
	g := &Graph{tried: map[string]bool{}, parents: map[string][]string{}, anc: map[string]map[string]struct{}{}, desc: map[string]map[string]struct{}{}}
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	lines := 0
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 0 {
			continue
		}
		if lines++; lines > maxGraphLines {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return nil
		}
		g.parents[f[0]] = f[1:]
	}
	if err := cmd.Wait(); err != nil || sc.Err() != nil {
		return nil
	}
	for _, e := range tips {
		g.tried[e] = true
	}
	if old, ok := graphs[key]; ok {
		for e := range old.tried {
			g.tried[e] = true
		}
	}
	graphs[key] = g
	return g
}

// existing keeps the commits git has, with one `cat-file --batch-check`.
func existing(dir string, shas []string) []string {
	if len(shas) == 0 {
		return nil
	}
	cmd := exec.Command("git", "cat-file", "--batch-check=%(objectname) %(objecttype)")
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(strings.Join(shas, "\n") + "\n")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil
	}
	var keep []string
	for _, line := range strings.Split(out.String(), "\n") {
		if sha, typ, ok := strings.Cut(strings.TrimSpace(line), " "); ok && typ == "commit" {
			keep = append(keep, sha)
		}
	}
	return keep
}

// Has reports whether git holds commit sha (reachable from a ref or an extra).
func (g *Graph) Has(sha string) bool {
	_, ok := g.parents[sha]
	return ok
}

// Ancestors is every commit reachable from tip, tip included.
func (g *Graph) Ancestors(tip string) map[string]struct{} {
	g.mu.Lock()
	defer g.mu.Unlock()
	if s, ok := g.anc[tip]; ok {
		return s
	}
	s := map[string]struct{}{}
	stack := []string{tip}
	for len(stack) > 0 {
		c := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if _, done := s[c]; done {
			continue
		}
		if _, ok := g.parents[c]; !ok {
			continue
		}
		s[c] = struct{}{}
		stack = append(stack, g.parents[c]...)
	}
	g.anc[tip] = s
	return s
}

// Descendants is every commit that has c in its history, c included.
func (g *Graph) Descendants(c string) map[string]struct{} {
	g.mu.Lock()
	defer g.mu.Unlock()
	if s, ok := g.desc[c]; ok {
		return s
	}
	if g.kids == nil {
		g.kids = make(map[string][]string, len(g.parents))
		for child, ps := range g.parents {
			for _, p := range ps {
				g.kids[p] = append(g.kids[p], child)
			}
		}
	}
	s := map[string]struct{}{}
	if _, ok := g.parents[c]; !ok {
		g.desc[c] = s
		return s
	}
	stack := []string{c}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if _, done := s[n]; done {
			continue
		}
		s[n] = struct{}{}
		stack = append(stack, g.kids[n]...)
	}
	g.desc[c] = s
	return s
}

// IsAncestorFast is IsAncestor answered from the graph when it holds both commits, and from
// git otherwise (a commit it does not hold is not an ancestor of one it does).
func IsAncestorFast(dir string, g *Graph, a, b string) (bool, error) {
	if g == nil || !g.Has(b) {
		return IsAncestor(dir, a, b)
	}
	if !g.Has(a) {
		return false, nil
	}
	_, ok := g.Ancestors(b)[a]
	return ok, nil
}

// AnyDescendant reports whether any of heads has tip in its history (a head is its own
// descendant). The graph answers it when it holds tip; git, one head at a time, otherwise.
func AnyDescendant(dir string, g *Graph, tip string, heads []string) (bool, error) {
	if g != nil && g.Has(tip) {
		d := g.Descendants(tip)
		for _, h := range heads {
			if _, ok := d[h]; ok {
				return true, nil
			}
		}
		return false, nil
	}
	for _, h := range heads {
		if ok, err := IsAncestor(dir, tip, h); err == nil && ok {
			return true, nil
		}
	}
	return false, nil
}
