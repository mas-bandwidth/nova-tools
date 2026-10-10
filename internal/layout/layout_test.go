package layout

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEveryDefaultPathDerivesFromTheNovaRoot verifies that all default paths
// derive from a single root (nova_root). With no nova_root, defaults are ~/nova.
func TestEveryDefaultPathDerivesFromTheNovaRoot(t *testing.T) {
	t.Parallel()

	home, err := os.UserHomeDir()
	require.NoError(t, err)

	// Default root (no nova_root set)
	root := ResolveRoot("")

	// Verify ai derives from nova
	assert.Equal(t, filepath.Join(filepath.Join(home, "nova"), "ai"), root.AI)

	// Verify bench derives from nova
	assert.Equal(t, filepath.Join(filepath.Join(home, "nova"), "bench"), root.Bench)

	// Verify secrets derives from bench
	assert.Equal(t, filepath.Join(root.Bench, "secrets"), root.Secrets)

	// Verify loop logs derives from bench
	assert.Equal(t, filepath.Join(root.Bench, "loops"), root.LoopLogs)

	// Verify mirrors derives from bench
	assert.Equal(t, filepath.Join(root.Bench, "mirror"), root.Mirrors)

	// Verify friend working derives from ai
	friend := ResolveFriend(filepath.Join(home, "nova"), "bob", "")
	wantWorking := filepath.Join(filepath.Join(home, "nova"), "ai", "bob", "working")
	assert.Equal(t, wantWorking, friend.Working)

	// Verify default nova root is ~/nova
	assert.Equal(t, filepath.Join(filepath.Join(home, "nova"), "ai"), root.AI)
}

func TestResolveRoot(t *testing.T) {
	t.Parallel()

	home, err := os.UserHomeDir()
	require.NoError(t, err)

	tests := []struct {
		name     string
		novaRoot string
		want     Root
	}{
		{
			name:     "default",
			novaRoot: "",
			want: Root{
				AI:      filepath.Join(home, "nova", "ai"),
				Bench:   filepath.Join(home, "nova", "bench"),
				Secrets: filepath.Join(home, "nova", "bench", "secrets"),
				LoopLogs: filepath.Join(home, "nova", "bench", "loops"),
				Mirrors: filepath.Join(home, "nova", "bench", "mirror"),
			},
		},
		{
			name:     "custom nova root",
			novaRoot: "/Volumes/nova",
			want: Root{
				AI:      "/Volumes/nova/ai",
				Bench:   "/Volumes/nova/bench",
				Secrets: "/Volumes/nova/bench/secrets",
				LoopLogs: "/Volumes/nova/bench/loops",
				Mirrors: "/Volumes/nova/bench/mirror",
			},
		},
		{
			name:     "tilde expands",
			novaRoot: "~/nova",
			want: Root{
				AI:      filepath.Join(home, "nova", "ai"),
				Bench:   filepath.Join(home, "nova", "bench"),
				Secrets: filepath.Join(home, "nova", "bench", "secrets"),
				LoopLogs: filepath.Join(home, "nova", "bench", "loops"),
				Mirrors: filepath.Join(home, "nova", "bench", "mirror"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ResolveRoot(tt.novaRoot)
			assert.Equal(t, tt.want.AI, got.AI)
			assert.Equal(t, tt.want.Bench, got.Bench)
			assert.Equal(t, tt.want.Secrets, got.Secrets)
			assert.Equal(t, tt.want.LoopLogs, got.LoopLogs)
			assert.Equal(t, tt.want.Mirrors, got.Mirrors)
		})
	}
}

func TestResolveFriend(t *testing.T) {
	t.Parallel()

	home, err := os.UserHomeDir()
	require.NoError(t, err)

	tests := []struct {
		name     string
		novaRoot string
		friend   string
		dir      string
		want     Friend
	}{
		{
			name:     "default",
			novaRoot: "~/nova",
			friend:   "bob",
			dir:      "",
			want: Friend{
				Working: filepath.Join(home, "nova", "ai", "bob", "working"),
			},
		},
		{
			name:     "explicit dir",
			novaRoot: "~/nova",
			friend:   "bob",
			dir:      "/home/bob/my-work",
			want: Friend{
				Working: "/home/bob/my-work",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ResolveFriend(tt.novaRoot, tt.friend, tt.dir)
			assert.Equal(t, tt.want.Working, got.Working)
		})
	}
}

func TestValidNovaRoot(t *testing.T) {
	t.Parallel()
	tmpDir, err := os.MkdirTemp("", "nova-root-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	tests := []struct {
		name string
		root string
		want bool
	}{
		{
			name: "empty ok",
			root: "",
			want: true,
		},
		{
			name: "real directory ok",
			root: tmpDir,
			want: true,
		},
		{
			name: "missing directory fails",
			root: filepath.Join(tmpDir, "nonexistent"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidNovaRoot(tt.root)
			got := err == nil
			assert.Equal(t, tt.want, got, "ValidNovaRoot(%q)", tt.root)
		})
	}

	// Test symlink rejection
	symlinkPath := filepath.Join(tmpDir, "symlink")
	require.NoError(t, os.Symlink(tmpDir, symlinkPath))
	err = ValidNovaRoot(symlinkPath)
	assert.Error(t, err, "ValidNovaRoot(%q)", symlinkPath)
}
