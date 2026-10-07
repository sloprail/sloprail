package trust

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMain lets the test binary stand in for `codex app-server`: a script that
// speaks just enough of the protocol (initialize, hooks/list, config/batchWrite),
// with the hooks it reports and the writes it receives kept in files named by the
// environment. The shapes are the ones recorded from codex 0.160.1.
func TestMain(m *testing.M) {
	if os.Getenv("FAKE_APP_SERVER") == "1" {
		fakeAppServer()
		return
	}
	os.Exit(m.Run())
}

func fakeAppServer() {
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		var req struct {
			ID     *int            `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		json.Unmarshal(sc.Bytes(), &req)
		if req.ID == nil {
			continue
		}
		switch req.Method {
		case "initialize":
			fmt.Printf("{\"id\":%d,\"result\":{\"codexHome\":\"/h\"}}\n", *req.ID)
		case "hooks/list":
			raw, _ := os.ReadFile(os.Getenv("FAKE_HOOKS"))
			var compact bytes.Buffer // one message per line
			json.Compact(&compact, raw)
			b := compact.Bytes()
			fmt.Printf("{\"id\":%d,\"result\":{\"data\":[{\"cwd\":\"/c\",\"hooks\":%s}]}}\n", *req.ID, b)
		case "config/batchWrite":
			os.WriteFile(os.Getenv("FAKE_WRITES"), req.Params, 0o644)
			fmt.Printf("{\"id\":%d,\"result\":{\"status\":\"ok\"}}\n", *req.ID)
		}
	}
}

// fakeCodex is an executable that is this test binary acting as codex.
func fakeCodex(t *testing.T, hooks string) (bin, writes string) {
	t.Helper()
	dir := t.TempDir()
	writes = filepath.Join(dir, "writes.json")
	hooksFile := filepath.Join(dir, "hooks.json")
	require.NoError(t, os.WriteFile(hooksFile, []byte(hooks), 0o644))
	t.Setenv("FAKE_APP_SERVER", "1")
	t.Setenv("FAKE_HOOKS", hooksFile)
	t.Setenv("FAKE_WRITES", writes)
	exe, err := os.Executable()
	require.NoError(t, err)
	return exe, writes
}

const listed = `[
 {"key":"sloprail@m:hooks/hooks.json:pre_tool_use:0:0","eventName":"preToolUse","pluginId":"sloprail@m","currentHash":"sha256:a","trustStatus":"untrusted","enabled":true},
 {"key":"sloprail@m:hooks/hooks.json:stop:0:0","eventName":"stop","pluginId":"sloprail@m","currentHash":"sha256:b","trustStatus":"modified","enabled":true},
 {"key":"sloprail@m:hooks/hooks.json:start:0:0","eventName":"sessionStart","pluginId":"sloprail@m","currentHash":"sha256:c","trustStatus":"trusted","enabled":true},
 {"key":"sloprail-tasks@m:hooks/hooks.json:stop:0:0","eventName":"stop","pluginId":"sloprail-tasks@m","currentHash":"sha256:d","trustStatus":"untrusted","enabled":true},
 {"key":"other@m:hooks/hooks.json:stop:0:0","eventName":"stop","pluginId":"other@m","currentHash":"sha256:e","trustStatus":"untrusted","enabled":true},
 {"key":"/p/.codex/hooks.json:stop:0:0","eventName":"stop","pluginId":null,"currentHash":"sha256:f","trustStatus":"untrusted","enabled":true},
 {"key":"sloprail@m:hooks/hooks.json:x:0:0","eventName":"x","pluginId":"sloprail@m","currentHash":"sha256:g","trustStatus":"untrusted","enabled":false}
]`

func TestRun_TrustsSloprailsPendingHooksOnly(t *testing.T) {
	bin, writes := fakeCodex(t, listed)
	res, err := Run(context.Background(), bin, t.TempDir(), false)
	require.NoError(t, err)

	assert.True(t, res.Trusted)
	assert.Equal(t, 5, res.Sloprail, "every sloprail hook Codex lists, trusted or not")
	var keys []string
	for _, h := range res.Pending {
		keys = append(keys, h.Key)
	}
	assert.Equal(t, []string{
		"sloprail@m:hooks/hooks.json:pre_tool_use:0:0",
		"sloprail@m:hooks/hooks.json:stop:0:0",
		"sloprail-tasks@m:hooks/hooks.json:stop:0:0",
	}, keys, "untrusted and modified, enabled, sloprail's: not another plugin's, not a project's, not a disabled one")

	raw, err := os.ReadFile(writes)
	require.NoError(t, err)
	assert.JSONEq(t, `{"edits":[{"keyPath":"hooks.state","mergeStrategy":"upsert","value":{
	  "sloprail@m:hooks/hooks.json:pre_tool_use:0:0":{"trusted_hash":"sha256:a"},
	  "sloprail@m:hooks/hooks.json:stop:0:0":{"trusted_hash":"sha256:b"},
	  "sloprail-tasks@m:hooks/hooks.json:stop:0:0":{"trusted_hash":"sha256:d"}}}],
	  "reloadUserConfig":true}`, string(raw))
}

func TestRun_CheckOnlyWritesNothing(t *testing.T) {
	bin, writes := fakeCodex(t, listed)
	res, err := Run(context.Background(), bin, t.TempDir(), true)
	require.NoError(t, err)
	assert.False(t, res.Trusted)
	assert.Len(t, res.Pending, 3)
	assert.NoFileExists(t, writes)
}

func TestRun_NothingPendingWritesNothing(t *testing.T) {
	bin, writes := fakeCodex(t, `[{"key":"k","pluginId":"sloprail@m","currentHash":"h","trustStatus":"trusted","enabled":true}]`)
	res, err := Run(context.Background(), bin, t.TempDir(), false)
	require.NoError(t, err)
	assert.Empty(t, res.Pending)
	assert.False(t, res.Trusted)
	assert.NoFileExists(t, writes)
}

func TestRun_NoCodex(t *testing.T) {
	_, err := Run(context.Background(), "codex-that-is-not-installed", t.TempDir(), false)
	assert.ErrorIs(t, err, ErrNoCodex)
}
