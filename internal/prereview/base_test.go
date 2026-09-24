package prereview_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/prereview"
)

func TestBaseGateFromGH(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mergeable string
		status    string
		want      prereview.BaseGate
	}{
		{"clean", "MERGEABLE", "CLEAN", prereview.BaseOK},
		{"behind", "MERGEABLE", "BEHIND", prereview.BaseBehind},
		{"conflicting", "CONFLICTING", "DIRTY", prereview.BaseConflict},
		{"conflicting without status", "CONFLICTING", "", prereview.BaseConflict},
		{"dirty without mergeable", "", "DIRTY", prereview.BaseConflict},
		{"behind without mergeable", "", "BEHIND", prereview.BaseBehind},
		{"empty defaults to ok", "", "", prereview.BaseOK},
		{"unknown defaults to ok", "UNKNOWN", "UNKNOWN", prereview.BaseOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := prereview.BaseGateFromGH(tc.mergeable, tc.status)
			if got != tc.want {
				t.Errorf("BaseGateFromGH(%q, %q) = %q, want %q", tc.mergeable, tc.status, got, tc.want)
			}
		})
	}
}

func TestBaseGateFromGit(t *testing.T) {
	// Create a real temp git repo to exercise BaseGateFromGit
	dir := t.TempDir()

	git := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
		return string(out)
	}

	git("init", "-b", "main")
	git("config", "user.name", "Test")
	git("config", "user.email", "test@mas-bandwidth.com")

	f := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(f, []byte("initial\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "file.txt")
	git("commit", "-m", "initial")

	// Create feature branch
	git("checkout", "-b", "feature")
	f2 := filepath.Join(dir, "feature.txt")
	if err := os.WriteFile(f2, []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "feature.txt")
	git("commit", "-m", "feature commit")

	ctx := context.Background()

	// 1. Feature is ahead of main (main is ancestor of feature) -> ok
	gate, err := prereview.BaseGateFromGit(ctx, nil, dir, "main", "feature")
	if err != nil {
		t.Fatalf("BaseGateFromGit failed: %v", err)
	}
	if gate != prereview.BaseOK {
		t.Errorf("gate = %q, want ok", gate)
	}

	// 2. Main advances with independent commit -> feature is behind main, but merges cleanly
	git("checkout", "main")
	f3 := filepath.Join(dir, "main.txt")
	if err := os.WriteFile(f3, []byte("main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "main.txt")
	git("commit", "-m", "main commit")

	gate, err = prereview.BaseGateFromGit(ctx, nil, dir, "main", "feature")
	if err != nil {
		t.Fatalf("BaseGateFromGit failed: %v", err)
	}
	if gate != prereview.BaseBehind {
		t.Errorf("gate = %q, want behind", gate)
	}

	// 3. Main edits file.txt, and feature edits file.txt differently -> conflict
	if err := os.WriteFile(f, []byte("main change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "file.txt")
	git("commit", "-m", "conflict on main")

	git("checkout", "feature")
	if err := os.WriteFile(f, []byte("feature change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "file.txt")
	git("commit", "-m", "conflict on feature")

	gate, err = prereview.BaseGateFromGit(ctx, nil, dir, "main", "feature")
	if err != nil {
		t.Fatalf("BaseGateFromGit failed: %v", err)
	}
	if gate != prereview.BaseConflict {
		t.Errorf("gate = %q, want conflict", gate)
	}
}
