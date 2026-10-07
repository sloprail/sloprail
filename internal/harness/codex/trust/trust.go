// Package trust establishes Codex's trust in sloprail's plugin hooks.
//
// Codex runs a hook only once it is trusted, and skips the rest without a word:
// `codex exec` prints nothing, a TUI shows a "Hooks need review" dialog. A plugin
// that is installed and enabled but whose hooks are untrusted is therefore an
// agent running with no guardrails and no sign of it. Enabling a plugin does not
// trust its hooks (https://developers.openai.com/codex/hooks, "Review and trust
// hooks").
//
// What trust is, from the source of codex 0.160.1 (codex-rs/hooks/src/engine/
// discovery.rs, codex-rs/config/src/hook_config.rs, codex-rs/tui/src/hooks_rpc.rs):
//
//   - one entry per hook handler in the user's config.toml:
//     [hooks.state."<plugin>@<marketplace>:hooks/hooks.json:<event>:<group>:<handler>"]
//     trusted_hash = "sha256:<hex>"
//   - the hash is a SHA-256 of the handler's canonical definition (event, matcher,
//     command, timeout, ...), NOT of the script the command runs. Editing a hook
//     script does not drop trust; changing its definition in hooks.json does (that hook
//     alone becomes "modified" and is skipped again). A plugin upgrade that leaves
//     the definitions alone keeps its trust (the key carries no version).
//   - the hash is Codex's to compute. This package does not reproduce it: it does
//     what Codex's own TUI does, over the app-server protocol (`codex app-server`,
//     JSON lines on stdio): `hooks/list` returns each hook's key and currentHash, and
//     `config/batchWrite` on `hooks.state` records them.
package trust

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

// Hook is one hook handler as `hooks/list` reports it (the fields used here).
type Hook struct {
	Key         string `json:"key"`
	EventName   string `json:"eventName"`
	PluginID    string `json:"pluginId"`
	CurrentHash string `json:"currentHash"`
	TrustStatus string `json:"trustStatus"`
	Enabled     bool   `json:"enabled"`
}

// NeedsReview reports a hook Codex skips until it is trusted: never reviewed, or
// changed since it was.
func (h Hook) NeedsReview() bool {
	return h.TrustStatus == "untrusted" || h.TrustStatus == "modified"
}

// IsSloprail reports a hook that belongs to one of sloprail's plugins (sloprail,
// sloprail-tasks, sloprail-content, from any marketplace).
func (h Hook) IsSloprail() bool {
	name, _, _ := strings.Cut(h.PluginID, "@")
	return name == "sloprail" || strings.HasPrefix(name, "sloprail-")
}

// ErrNoCodex is returned when no codex binary can be started.
var ErrNoCodex = errors.New("codex not found")

// Client talks to one `codex app-server` process.
type Client struct {
	cmd    *exec.Cmd
	in     io.WriteCloser
	out    *bufio.Reader
	nextID int
}

// Start launches `<bin> app-server` in dir (the directory whose project layer
// applies) and performs the protocol's initialize handshake.
func Start(ctx context.Context, bin, dir string) (*Client, error) {
	cmd := exec.CommandContext(ctx, bin, "app-server")
	cmd.Dir = dir
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, fmt.Errorf("%w: %q is not on PATH", ErrNoCodex, bin)
		}
		return nil, err
	}
	c := &Client{cmd: cmd, in: in, out: bufio.NewReader(out)}
	if _, err := c.call("initialize", map[string]any{
		"clientInfo": map[string]any{"name": "sloprail", "version": "0"},
	}); err != nil {
		c.Close()
		return nil, fmt.Errorf("codex app-server initialize: %w", err)
	}
	if err := c.send(map[string]any{"method": "initialized"}); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// Close ends the app-server.
func (c *Client) Close() {
	c.in.Close()
	if c.cmd.Process != nil {
		c.cmd.Process.Kill()
	}
	c.cmd.Wait()
}

func (c *Client) send(msg map[string]any) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	_, err = c.in.Write(append(b, '\n'))
	return err
}

// call sends a request and waits for its response, skipping notifications.
func (c *Client) call(method string, params any) (json.RawMessage, error) {
	c.nextID++
	id := c.nextID
	if err := c.send(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	for {
		line, err := c.out.ReadBytes('\n')
		if err != nil {
			return nil, fmt.Errorf("%s: app-server closed: %w", method, err)
		}
		var m struct {
			ID     *int            `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(line, &m) != nil || m.ID == nil || *m.ID != id {
			continue
		}
		if m.Error != nil {
			return nil, fmt.Errorf("%s: %s", method, m.Error.Message)
		}
		return m.Result, nil
	}
}

// Hooks lists every hook Codex discovers for dir, with its trust status.
func (c *Client) Hooks(dir string) ([]Hook, error) {
	res, err := c.call("hooks/list", map[string]any{"cwds": []string{dir}})
	if err != nil {
		return nil, err
	}
	var r struct {
		Data []struct {
			Hooks []Hook `json:"hooks"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res, &r); err != nil {
		return nil, err
	}
	var all []Hook
	for _, d := range r.Data {
		all = append(all, d.Hooks...)
	}
	return all, nil
}

// Trust records each hook's current hash as trusted (the write Codex's own review
// dialog performs).
func (c *Client) Trust(hooks []Hook) error {
	if len(hooks) == 0 {
		return nil
	}
	state := map[string]any{}
	for _, h := range hooks {
		state[h.Key] = map[string]any{"trusted_hash": h.CurrentHash}
	}
	_, err := c.call("config/batchWrite", map[string]any{
		"edits": []map[string]any{{
			"keyPath": "hooks.state", "value": state, "mergeStrategy": "upsert",
		}},
		"reloadUserConfig": true,
	})
	return err
}

// Pending is sloprail's enabled hooks that Codex would skip.
func Pending(hooks []Hook) []Hook {
	var out []Hook
	for _, h := range hooks {
		if h.IsSloprail() && h.Enabled && h.NeedsReview() {
			out = append(out, h)
		}
	}
	return out
}

// Result is what one Run found and did.
type Result struct {
	Sloprail int    // sloprail hooks Codex knows of
	Pending  []Hook // those it would skip, before this run
	Trusted  bool   // whether this run recorded trust for them
}

// Run lists dir's hooks and, unless checkOnly, trusts sloprail's pending ones.
func Run(ctx context.Context, bin, dir string, checkOnly bool) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	c, err := Start(ctx, bin, dir)
	if err != nil {
		return Result{}, err
	}
	defer c.Close()
	hooks, err := c.Hooks(dir)
	if err != nil {
		return Result{}, err
	}
	res := Result{Pending: Pending(hooks)}
	for _, h := range hooks {
		if h.IsSloprail() {
			res.Sloprail++
		}
	}
	if checkOnly || len(res.Pending) == 0 {
		return res, nil
	}
	if err := c.Trust(res.Pending); err != nil {
		return res, err
	}
	res.Trusted = true
	return res, nil
}
