package sessionstate

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The guard: a migration file is never edited once shipped (a database in the field has already
// run its text), and a NEW migration needs a fixture test that opens a store of the previous
// layout. testdata/migrations.lock lists every migration as `<file> <sha256> <fixture test>`;
// the first five predate the guard and are marked `legacy`. Adding a migration without a lock
// line, editing a shipped one, or naming a fixture test that does not exist fails here.
func TestSchemaGuard_MigrationsAreImmutableAndNewOnesHaveAFixture(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "migrations.lock"))
	require.NoError(t, err)
	locked := map[string][2]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		f := strings.Fields(line)
		require.Len(t, f, 3, "migrations.lock line: %q", line)
		locked[f[0]] = [2]string{f[1], f[2]}
	}
	files, err := migrationFiles()
	require.NoError(t, err)
	require.Len(t, locked, len(files), "every migration is in testdata/migrations.lock, and only those: add the line (file, sha256, fixture test) with the migration")
	for _, name := range files {
		body, err := migrationFS.ReadFile("migrations/" + name)
		require.NoError(t, err)
		sum := sha256.Sum256(body)
		want, ok := locked[name]
		require.True(t, ok, "%s is not in testdata/migrations.lock", name)
		require.Equal(t, want[0], hex.EncodeToString(sum[:]), "%s was edited after it shipped: write a new migration instead", name)
		if want[1] == "legacy" {
			continue
		}
		out, _ := exec.Command("grep", "-rl", "--include=*_test.go", "func "+want[1]+"(", "../..").Output()
		require.NotEmpty(t, strings.TrimSpace(string(out)), "the fixture test %s named for %s does not exist", want[1], name)
	}
}

func TestMigrations_AStoreOfTheFirstLayoutOpensAndKeepsItsData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path)
	require.NoError(t, err)
	require.NoError(t, s.SetMeta("k", "v"))
	require.NoError(t, s.Close())
	s, err = Open(path)
	require.NoError(t, err)
	defer s.Close()
	v, ok, err := s.Meta("k")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "v", v)
}
