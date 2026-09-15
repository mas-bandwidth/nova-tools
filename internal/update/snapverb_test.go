package update

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSnapshotWritesRuleTwoManifest(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0755); err != nil {
		t.Fatal(err)
	}
	stub := func(name, line string) {
		p := filepath.Join(bin, name)
		if err := os.WriteFile(p, []byte("#!/bin/sh\nprintf '%s\\n' '"+line+"'\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	stub("nova-bus", "nova-bus v0.12.1-0.20260912-0459069+dirty darwin/arm64")
	stub("nova-check", "v7.8.9")
	stub("nova-wake", "nova-wake 2.3.4")
	stub("nova-bad", "hello world")
	if err := os.WriteFile(filepath.Join(bin, "README"), []byte("not a tool"), 0644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.tsv")
	var o, e bytes.Buffer
	code := Run("nova-version", []string{"snapshot", "--bin", bin, "--out", out, "--owner", "rowan"}, "", &o, &e, Environment{})
	if code != 0 {
		t.Fatalf("exit %d out=%q err=%q", code, o.String(), e.String())
	}
	need(t, o.String(), "SNAPSHOT OK tools=3 unreadable=1 out="+field(out))
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	want := Header + "\n" +
		"nova-bus\tbinary\t0.12.1-0.20260912-0459069+dirty\t-\t-\trowan\n" +
		"nova-check\tbinary\t7.8.9\t-\t-\trowan\n" +
		"nova-wake\tbinary\t2.3.4\t-\t-\trowan\n"
	if string(b) != want {
		t.Fatalf("manifest mismatch:\ngot:\n%s\nwant:\n%s", b, want)
	}

	// Default owner is "-", never guessed.
	o.Reset()
	code = Run("nova-version", []string{"snapshot", "--bin", bin, "--out", out}, "", &o, &e, Environment{})
	if code != 0 {
		t.Fatalf("exit %d out=%q err=%q", code, o.String(), e.String())
	}
	b, _ = os.ReadFile(out)
	if !strings.Contains(string(b), "nova-check\tbinary\t7.8.9\t-\t-\t-\n") {
		t.Fatalf("default owner not '-':\n%s", b)
	}
}
