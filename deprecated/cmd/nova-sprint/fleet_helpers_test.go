package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The release fixtures every fleet test shares (the release tests run on a
// real Redis since 2026-09-27; these stay in every tier).
const (
	verbSha     = "c8178673f5e19ffbfe841e11826e611b74b6900d"
	verbVersion = "v0.16.0-dev.c8178673"
	verbPW      = "pw-seat-4356"
)

func releaseRegistry(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "machines.tsv")
	body := "space\tspace\tlinux/x64\tbench,services\nhulk\thulk\tlinux/x64\tbench\nstudio\tlocalhost\tdarwin/arm64\tcoordination\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func releasePlayDir(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	for _, f := range []string{"inventory.py", "tools.yml"} {
		if err := os.WriteFile(filepath.Join(d, f), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return d
}
