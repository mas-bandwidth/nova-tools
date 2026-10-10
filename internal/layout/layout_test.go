package layout

import (
	"os"
	"path/filepath"
	"testing"
)

// TestEveryDefaultPathDerivesFromTheNovaRoot verifies that all default paths
// derive from a single root (ai_root). With no ai_root, defaults are ~/ai.
func TestEveryDefaultPathDerivesFromTheNovaRoot(t *testing.T) {
	t.Parallel()

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	// Default root (no ai_root set)
	root := ResolveRoot("")

	// Verify bench derives from ai
	if root.Bench != filepath.Join(root.AI, "bench") {
		t.Errorf("bench = %q, want %q", root.Bench, filepath.Join(root.AI, "bench"))
	}

	// Verify secrets derives from bench
	if root.Secrets != filepath.Join(root.Bench, "secrets") {
		t.Errorf("secrets = %q, want %q", root.Secrets, filepath.Join(root.Bench, "secrets"))
	}

	// Verify loop logs derives from bench
	if root.LoopLogs != filepath.Join(root.Bench, "loops") {
		t.Errorf("loop logs = %q, want %q", root.LoopLogs, filepath.Join(root.Bench, "loops"))
	}

	// Verify mirrors derives from bench
	if root.Mirrors != filepath.Join(root.Bench, "mirror") {
		t.Errorf("mirrors = %q, want %q", root.Mirrors, filepath.Join(root.Bench, "mirror"))
	}

	// Verify friend working derives from ai
	friend := ResolveFriend(root.AI, "bob", "")
	wantWorking := filepath.Join(root.AI, "bob", "working")
	if friend.Working != wantWorking {
		t.Errorf("friend working = %q, want %q", friend.Working, wantWorking)
	}

	// Verify default ai root is ~/ai
	if root.AI != filepath.Join(home, "ai") {
		t.Errorf("default ai root = %q, want %q", root.AI, filepath.Join(home, "ai"))
	}
}

func TestResolveRoot(t *testing.T) {
	t.Parallel()

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		aiRoot string
		want   Root
	}{
		{
			name:   "default",
			aiRoot: "",
			want: Root{
				AI:      filepath.Join(home, "ai"),
				Bench:   filepath.Join(home, "ai", "bench"),
				Secrets: filepath.Join(home, "ai", "bench", "secrets"),
				LoopLogs: filepath.Join(home, "ai", "bench", "loops"),
				Mirrors: filepath.Join(home, "ai", "bench", "mirror"),
			},
		},
		{
			name:   "custom ai root",
			aiRoot: "/Volumes/nova",
			want: Root{
				AI:      "/Volumes/nova",
				Bench:   "/Volumes/nova/bench",
				Secrets: "/Volumes/nova/bench/secrets",
				LoopLogs: "/Volumes/nova/bench/loops",
				Mirrors: "/Volumes/nova/bench/mirror",
			},
		},
		{
			name:   "tilde expands",
			aiRoot: "~/nova",
			want: Root{
				AI:      filepath.Join(home, "nova"),
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
			got := ResolveRoot(tt.aiRoot)
			if got.AI != tt.want.AI {
				t.Errorf("AI = %q, want %q", got.AI, tt.want.AI)
			}
			if got.Bench != tt.want.Bench {
				t.Errorf("Bench = %q, want %q", got.Bench, tt.want.Bench)
			}
			if got.Secrets != tt.want.Secrets {
				t.Errorf("Secrets = %q, want %q", got.Secrets, tt.want.Secrets)
			}
			if got.LoopLogs != tt.want.LoopLogs {
				t.Errorf("LoopLogs = %q, want %q", got.LoopLogs, tt.want.LoopLogs)
			}
			if got.Mirrors != tt.want.Mirrors {
				t.Errorf("Mirrors = %q, want %q", got.Mirrors, tt.want.Mirrors)
			}
		})
	}
}

func TestResolveFriend(t *testing.T) {
	t.Parallel()

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		aiRoot string
		friend string
		dir    string
		want   Friend
	}{
		{
			name:   "default",
			aiRoot: "~/ai",
			friend: "bob",
			dir:    "",
			want: Friend{
				Working: filepath.Join(home, "ai", "bob", "working"),
			},
		},
		{
			name:   "explicit dir",
			aiRoot: "~/ai",
			friend: "bob",
			dir:    "/home/bob/my-work",
			want: Friend{
				Working: "/home/bob/my-work",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ResolveFriend(tt.aiRoot, tt.friend, tt.dir)
			if got.Working != tt.want.Working {
				t.Errorf("Working = %q, want %q", got.Working, tt.want.Working)
			}
		})
	}
}
