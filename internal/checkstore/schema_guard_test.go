package checkstore

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The guard: a change to the check-results layout (schema.sql, or the columns the open steps add)
// without a SchemaVersion bump and a migration fixture test fails here. testdata/schema.lock holds
// `<SchemaVersion> <layout hash> <fixture test>`; the test named must exist in the repository and
// is the one that opens a store of the PREVIOUS layout and shows nothing settled or owed is lost.
func layoutHash(t *testing.T) string {
	t.Helper()
	st, err := OpenFamily(filepath.Join(t.TempDir(), "checks.db"), "guard")
	require.NoError(t, err)
	defer st.Close()
	rows, err := st.(*store).db.Query(`SELECT name, sql FROM main.sqlite_master WHERE name NOT LIKE 'sqlite_%' ORDER BY name`)
	require.NoError(t, err)
	defer rows.Close()
	h := sha256.New()
	for rows.Next() {
		var name string
		var def *string
		require.NoError(t, rows.Scan(&name, &def))
		d := ""
		if def != nil {
			d = *def
		}
		fmt.Fprintf(h, "%s|%s\n", name, strings.Join(strings.Fields(d), " "))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func TestSchemaGuard_ALayoutChangeNeedsAVersionBumpAndAMigrationFixture(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "schema.lock"))
	require.NoError(t, err)
	f := strings.Fields(string(raw))
	require.Len(t, f, 3, "testdata/schema.lock is `<SchemaVersion> <layout hash> <fixture test>`")
	require.Equal(t, fmt.Sprint(SchemaVersion), f[0],
		"SchemaVersion and testdata/schema.lock disagree: a layout change bumps SchemaVersion, adds a migration step and a fixture test of the previous layout, and records all three in the lock")
	require.Equal(t, f[1], layoutHash(t),
		"the check-results layout changed without a SchemaVersion bump: bump it, add the migration step, write the fixture test (a store of the previous layout opens, nothing settled or owed is lost), then update testdata/schema.lock")
	out, _ := exec.Command("grep", "-rl", "--include=*_test.go", "func "+f[2]+"(", "../..").Output()
	require.NotEmpty(t, strings.TrimSpace(string(out)), "the fixture test %s named in testdata/schema.lock does not exist", f[2])
}

func TestMigrate_OpensAPreviousLayoutStoreAndIsRepeatable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checks.db")
	old, err := Open(path) // the layout before the repository database: no family, no folder
	require.NoError(t, err)
	passRun(t, old, "h1", "fp")
	require.NoError(t, old.Close())
	require.NoError(t, Migrate(path))
	require.NoError(t, Migrate(path))
	st, err := OpenFamily(path, "other")
	require.NoError(t, err)
	defer st.Close()
	var v int
	require.NoError(t, st.(*store).db.QueryRow(`PRAGMA user_version`).Scan(&v))
	require.Equal(t, SchemaVersion, v)
}
