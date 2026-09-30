package gitrepo

import (
	"fmt"
	"strings"
)

// Commit is one commit of a range as a rule sees it.
type Commit struct {
	SHA     string
	Subject string
	// Body is the message after the subject, as git prints it (%b): trailers
	// included, because a judge reads what the author wrote.
	Body string
	// Trailers are the message's trailers in order, keys as written, values
	// unfolded onto one line.
	Trailers []Trailer
}

// Trailer is one `Key: value` line of a commit message's trailer block.
type Trailer struct {
	Key   string
	Value string
}

const (
	fieldSep = "\x1f"
	// A format that puts a NUL after every commit under -z. The fields themselves
	// are separated by US, which a commit message can carry but never does; a
	// record with the wrong field count is an error rather than a guess.
	commitFormat = "%H%x1f%s%x1f%b%x1f%(trailers:only=true,unfold=true)"
)

// CommitsIn lists the commits in base..head, oldest first.
//
// Every commit, merges included: development happens through agents, so there
// are no commits to tell apart and skip.
func CommitsIn(dir, base, head string) ([]Commit, error) {
	rng := base + ".." + head
	if base == EmptyTree {
		rng = head // a range from before the first commit: every commit head reaches
	}
	out, err := run(dir, "log", "--reverse", "-z", "--format="+commitFormat, rng)
	if err != nil {
		return nil, fmt.Errorf("gitrepo: log %s..%s: %w", short(base), short(head), err)
	}
	var commits []Commit
	for _, rec := range strings.Split(out, "\x00") {
		rec = strings.TrimPrefix(rec, "\n") // git separates records with a newline too
		if rec == "" {
			continue
		}
		f := strings.Split(rec, fieldSep)
		if len(f) != 4 || !isObjectName(f[0]) {
			return nil, fmt.Errorf("gitrepo: unreadable commit record %q", rec)
		}
		c := Commit{SHA: f[0], Subject: f[1], Body: strings.TrimSpace(f[2])}
		for _, line := range strings.Split(f[3], "\n") {
			if line = strings.TrimSpace(line); line == "" {
				continue
			}
			key, value, ok := strings.Cut(line, ":")
			if !ok || strings.TrimSpace(key) == "" {
				return nil, fmt.Errorf("gitrepo: unreadable trailer %q on %s", line, short(c.SHA))
			}
			c.Trailers = append(c.Trailers, Trailer{Key: strings.TrimSpace(key), Value: strings.TrimSpace(value)})
		}
		commits = append(commits, c)
	}
	return commits, nil
}
