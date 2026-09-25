package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadCardFilesDir: --dir reads every regular non-dot file in name order,
// each named by its path, so one batch carries the whole directory (#3266).
func TestReadCardFilesDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for name, body := range map[string]string{"b.md": "B", "a.md": "A", ".hidden": "H", "c.md": "C"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	files, err := readCardFiles(false, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range files {
		got = append(got, filepath.Base(f.Name)+"="+string(f.Body))
	}
	if strings.Join(got, ",") != "a.md=A,b.md=B,c.md=C" {
		t.Fatalf("read %v, want a.md=A,b.md=B,c.md=C", got)
	}
	if _, err := readCardFiles(false, t.TempDir(), nil); err == nil || !strings.Contains(err.Error(), "holds no card file") {
		t.Fatalf("empty --dir: %v, want a refusal", err)
	}
}

// TestCardPushOneSource: files, --dir and --stdin are exclusive, and one is required.
func TestCardPushOneSource(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"--sprint", "s", "--redis", "127.0.0.1:1", "--dir", "x", "a.md"},
		{"--sprint", "s", "--redis", "127.0.0.1:1", "--stdin", "--dir", "x"},
		{"--sprint", "s", "--redis", "127.0.0.1:1"},
	} {
		var out, errb bytes.Buffer
		if code := cmdCardPush(context.Background(), args, &out, &errb); code != 2 || out.Len() != 0 {
			t.Fatalf("%v: exit %d stdout %q stderr %q, want exit 2", args, code, out.String(), errb.String())
		}
	}
}
