package gitrepo

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeRemote(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		// SSH variants
		{
			name:     "SSH with .git suffix",
			input:    "git@github.com:org/repo.git",
			expected: "github.com/org/repo",
		},
		{
			name:     "SSH without .git suffix",
			input:    "git@github.com:org/repo",
			expected: "github.com/org/repo",
		},
		{
			name:     "SSH with uppercase",
			input:    "git@GitHub.com:Org/Repo.git",
			expected: "github.com/org/repo",
		},

		// HTTPS variants
		{
			name:     "HTTPS with .git suffix",
			input:    "https://github.com/org/repo.git",
			expected: "github.com/org/repo",
		},
		{
			name:     "HTTPS without .git suffix",
			input:    "https://github.com/org/repo",
			expected: "github.com/org/repo",
		},
		{
			name:     "HTTPS with trailing slash",
			input:    "https://github.com/org/repo/",
			expected: "github.com/org/repo",
		},
		{
			name:     "HTTPS with .git and trailing slash",
			input:    "https://github.com/org/repo.git/",
			expected: "github.com/org/repo",
		},
		{
			name:     "HTTPS with uppercase",
			input:    "https://github.com/Org/Repo.git",
			expected: "github.com/org/repo",
		},

		// ssh:// scheme variants
		{
			name:     "ssh:// with user and .git suffix",
			input:    "ssh://git@github.com/org/repo.git",
			expected: "github.com/org/repo",
		},
		{
			name:     "ssh:// without user",
			input:    "ssh://github.com/org/repo.git",
			expected: "github.com/org/repo",
		},
		{
			name:     "ssh:// with non-standard port (not 22)",
			input:    "ssh://git@github.com:2222/org/repo.git",
			expected: "github.com/org/repo",
		},
		{
			name:     "ssh:// with default port 22",
			input:    "ssh://git@github.com:22/org/repo.git",
			expected: "github.com/org/repo",
		},

		// Embedded credentials
		{
			name:     "HTTPS with username and token",
			input:    "https://user:token@github.com/org/repo.git",
			expected: "github.com/org/repo",
		},
		{
			name:     "HTTPS with username only",
			input:    "https://user@github.com/org/repo.git",
			expected: "github.com/org/repo",
		},

		// Default ports
		{
			name:     "HTTPS with default port 443",
			input:    "https://github.com:443/org/repo.git",
			expected: "github.com/org/repo",
		},
		{
			name:     "HTTP with default port 80",
			input:    "http://github.com:80/org/repo.git",
			expected: "github.com/org/repo",
		},

		// Whitespace handling
		{
			name:     "Leading and trailing whitespace",
			input:    "  https://github.com/org/repo.git  ",
			expected: "github.com/org/repo",
		},

		// GitLab / other hosts
		{
			name:     "GitLab SSH",
			input:    "git@gitlab.com:group/subgroup/repo.git",
			expected: "gitlab.com/group/subgroup/repo",
		},
		{
			name:     "GitLab HTTPS",
			input:    "https://gitlab.com/group/subgroup/repo.git",
			expected: "gitlab.com/group/subgroup/repo",
		},

		// Bitbucket
		{
			name:     "Bitbucket SSH",
			input:    "git@bitbucket.org:team/repo.git",
			expected: "bitbucket.org/team/repo",
		},
		{
			name:     "Bitbucket HTTPS",
			input:    "https://bitbucket.org/team/repo.git",
			expected: "bitbucket.org/team/repo",
		},

		// Self-hosted / custom domains
		{
			name:     "Self-hosted with custom port",
			input:    "https://git.example.com:8443/org/repo.git",
			expected: "git.example.com/org/repo",
		},
		{
			name:     "Self-hosted SSH with custom port",
			input:    "ssh://git@git.example.com:2222/org/repo.git",
			expected: "git.example.com/org/repo",
		},

		// Edge cases
		{
			name:     "Only host (no path)",
			input:    "https://github.com",
			expected: "github.com",
		},
		{
			name:     "Only host with trailing slash",
			input:    "https://github.com/",
			expected: "github.com",
		},
		{
			name:     "Deep path structure",
			input:    "https://github.com/org/team/subteam/repo.git",
			expected: "github.com/org/team/subteam/repo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NormalizeRemote(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestNormalizeRemote_LocalRepo covers file:// and bare-path remotes. Two
// spellings of the SAME local repo must normalize equal — the .git suffix is
// stripped, and a symlinked path (macOS /var → /private/var) resolves to one
// canonical form so a file:// resource URL matches a /private/var cwd origin.
func TestNormalizeRemote_LocalRepo(t *testing.T) {
	dir := t.TempDir() // real dir so EvalSymlinks resolves it

	// file:// with and without .git must agree.
	assert.Equal(t, NormalizeRemote("file://"+dir), NormalizeRemote("file://"+dir+".git"))
	// file:// and the bare absolute path must agree.
	assert.Equal(t, NormalizeRemote("file://"+dir), NormalizeRemote(dir))
	// Idempotent: normalizing the canonical form again is stable.
	once := NormalizeRemote("file://" + dir)
	assert.Equal(t, once, NormalizeRemote(once))
	// A real remote URL is NOT treated as a local path.
	assert.Equal(t, "github.com/org/repo", NormalizeRemote("https://github.com/org/repo.git"))
}
