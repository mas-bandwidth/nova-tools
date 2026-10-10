package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNovaDeleteRefusesAnythingButOneLiteralPath(t *testing.T) {
	t.Parallel()

	// Set up a temporary root for testing
	tmpDir := t.TempDir()
	os.Setenv("NOVA_DELETE_ROOTS", tmpDir)
	defer os.Unsetenv("NOVA_DELETE_ROOTS")

	tests := []struct {
		name     string
		path     string
		wantExit int
	}{
		{"empty argument", "", 2},
		{"glob star", "/tmp/*", 2},
		{"glob question", "/tmp?", 2},
		{"glob bracket", "/tmp[", 2},
		{"backtick", "/tmp`cmd`", 2},
		{"relative path", "./tmp/file", 2},
		{"root filesystem", "/", 2},
		{"temp root itself", os.TempDir(), 2},
		{"home root itself", os.Getenv("HOME"), 2},
		{"outside allowed roots", "/etc/hosts", 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if tt.path == "" {
				// Test no argument case
				_, exit, err := Delete("")
				if exit != 2 || err == nil {
					t.Errorf("Delete() exit = %d, want 2; err = %v", exit, err)
				}
				return
			}
			_, exit, err := Delete(tt.path)
			if exit != 2 {
				t.Errorf("Delete() exit = %d, want 2", exit)
			}
			if err == nil {
				t.Error("Delete() expected error, got nil")
			}
		})
	}
}

func TestNovaDeleteMovesLiteralPath(t *testing.T) {
	t.Parallel()

	// Create temp root
	tmpRoot := t.TempDir()
	os.Setenv("NOVA_DELETE_ROOTS", tmpRoot)
	defer os.Unsetenv("NOVA_DELETE_ROOTS")

	// Test moving a file
	filePath := filepath.Join(tmpRoot, "testfile.txt")
	if err := os.WriteFile(filePath, []byte("test"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	msg, exit, err := Delete(filePath)
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if exit != 0 {
		t.Errorf("Delete() exit = %d, want 0", exit)
	}
	if len(msg) < 6 || msg[:6] != "MOVED " {
		t.Errorf("Delete() msg = %q, want to start with MOVED", msg)
	}

	// Verify file is gone from original location
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Errorf("Delete() original file still exists")
	}

	// Test moving a directory
	dirPath := filepath.Join(tmpRoot, "testdir")
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		t.Fatalf("failed to create test dir: %v", err)
	}
	testFile := filepath.Join(dirPath, "nested.txt")
	os.WriteFile(testFile, []byte("nested"), 0644)

	msg, exit, err = Delete(dirPath)
	if err != nil {
		t.Fatalf("Delete dir() error = %v", err)
	}
	if exit != 0 {
		t.Errorf("Delete dir() exit = %d, want 0", exit)
	}
	if _, err := os.Stat(dirPath); !os.IsNotExist(err) {
		t.Errorf("Delete dir() original dir still exists")
	}

	// Test missing path
	missingPath := filepath.Join(tmpRoot, "nonexistent.txt")
	msg, exit, err = Delete(missingPath)
	if err != nil {
		t.Fatalf("Delete missing() error = %v", err)
	}
	if exit != 0 {
		t.Errorf("Delete missing() exit = %d, want 0", exit)
	}
	if len(msg) < 5 || msg[:5] != "SKIP " {
		t.Errorf("Delete missing() msg = %q, want to start with SKIP", msg)
	}
}

func TestNovaDeleteSweep(t *testing.T) {
	tmpRoot := t.TempDir()
	os.Setenv("NOVA_DELETE_ROOTS", tmpRoot)
	defer os.Unsetenv("NOVA_DELETE_ROOTS")

	// Create old quarantine dir (set modification time to 30 days ago)
	oldDir := filepath.Join(tmpRoot, ".quarantine-20200101")
	os.MkdirAll(oldDir, 0755)
	os.WriteFile(filepath.Join(oldDir, "old.txt"), []byte("old"), 0644)
	oldTime := time.Now().Add(-30 * 24 * time.Hour)
	os.Chtimes(oldDir, oldTime, oldTime)

	// Create recent quarantine dir
	recentDir := filepath.Join(tmpRoot, ".quarantine-"+time.Now().Format("20060102"))
	os.MkdirAll(recentDir, 0755)
	os.WriteFile(filepath.Join(recentDir, "recent.txt"), []byte("recent"), 0644)

	swept, err := Sweep(7 * 24 * time.Hour)
	if err != nil {
		t.Fatalf("Sweep() error = %v", err)
	}

	if len(swept) != 1 {
		t.Errorf("Sweep() swept = %d entries, want 1", len(swept))
	}

	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Errorf("Sweep() old dir still exists")
	}

	if _, err := os.Stat(recentDir); err != nil {
		t.Errorf("Sweep() recent dir should still exist: %v", err)
	}
}
