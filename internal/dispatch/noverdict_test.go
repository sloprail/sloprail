package dispatch

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/declaration"
)

// A script check that could not run, or said {"error": true}, refuses (fail-closed) but is typed
// NoVerdict, so a caller that caches never stores it; its own refusal is a verdict.
func TestRunScript_NoVerdictTypesAnErrorNotARefusal(t *testing.T) {
	cases := []struct {
		name, body string
		mode       os.FileMode
		noVerdict  bool
	}{
		{"cannot run", "#!/bin/sh\nexit 0\n", 0o644, true},
		{"says error", "#!/bin/sh\ncat >/dev/null\necho '{\"reason\":\"sr-test could not run\",\"error\":true}'\nexit 1\n", 0o755, true},
		{"error false", "#!/bin/sh\ncat >/dev/null\necho '{\"reason\":\"no\",\"error\":false}'\nexit 1\n", 0o755, false},
		{"plain refusal", "#!/bin/sh\ncat >/dev/null\necho '{\"reason\":\"forbidden words\"}'\nexit 1\n", 0o755, false},
		{"bare exit 1", "#!/bin/sh\ncat >/dev/null\nexit 1\n", 0o755, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "x.sh")
			require.NoError(t, os.WriteFile(p, []byte(c.body), c.mode))
			require.NoError(t, os.Chmod(p, c.mode))
			req := gateReq(nil, nil)
			req.Dir = dir
			v, err := Runner{}.withDefaults().RunScript(req, declaration.Check{Script: "./x.sh"}, Prepared{})
			require.NoError(t, err)
			require.True(t, v.Refused, "every one of these refuses")
			assert.Equal(t, c.noVerdict, v.NoVerdict)
		})
	}
}

// A prepare that cannot run is no verdict either; a prepare that refused in words is.
func TestRunPrepare_NoVerdictOnlyWhenItCouldNotRun(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string, mode os.FileMode) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), mode))
		require.NoError(t, os.Chmod(filepath.Join(dir, name), mode))
	}
	write("broken.sh", "#!/bin/sh\nexit 0\n", 0o644)
	write("says.sh", "#!/bin/sh\ncat >/dev/null\necho '{\"reason\":\"no standard\"}'\nexit 1\n", 0o755)
	req := gateReq(nil, nil)
	req.Dir = dir
	r := Runner{}.withDefaults()

	_, v, err := r.runPrepare(req, "./broken.sh")
	require.NoError(t, err)
	assert.True(t, v.Refused && v.NoVerdict)
	_, v, err = r.runPrepare(req, "./says.sh")
	require.NoError(t, err)
	assert.True(t, v.Refused)
	assert.False(t, v.NoVerdict)
}
