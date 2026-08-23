package check

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestAttest(t *testing.T) {
	tests := []struct {
		name      string
		files     map[string]string // relative path -> content, under home
		manifest  string
		wantFiles int
		wantBytes int64
		wantFail  []string // substrings of expected failures; empty = must pass
	}{
		{
			name:      "full self attests",
			files:     map[string]string{"KERNEL.md": "kernel\n", "notes/a.md": "aaa"},
			manifest:  "KERNEL.md\nnotes/a.md\n",
			wantFiles: 2,
			wantBytes: 10,
		},
		{
			name:      "comments and blank lines ignored",
			files:     map[string]string{"KERNEL.md": "k"},
			manifest:  "# the boot order\n\nKERNEL.md\n\n",
			wantFiles: 1,
			wantBytes: 1,
		},
		{
			name:     "missing file is a named failure",
			files:    map[string]string{"KERNEL.md": "k"},
			manifest: "KERNEL.md\nMISSING.md\n",
			wantFail: []string{"MISSING.md", "does not exist"},
		},
		{
			name:     "empty file must not attest",
			files:    map[string]string{"KERNEL.md": ""},
			manifest: "KERNEL.md\n",
			wantFail: []string{"KERNEL.md", "empty"},
		},
		{
			name:     "directory entry is not a file",
			files:    map[string]string{"notes/a.md": "x"},
			manifest: "notes\n",
			wantFail: []string{"notes", "not a regular file"},
		},
		{
			name:     "absolute path refused",
			manifest: "/etc/hosts\n",
			wantFail: []string{"/etc/hosts", "absolute"},
		},
		{
			name:     "escape from home refused",
			manifest: "../outside.md\n",
			wantFail: []string{"../outside.md", "escapes"},
		},
		{
			name:     "duplicate entry refused",
			files:    map[string]string{"a.md": "x"},
			manifest: "a.md\na.md\n",
			wantFail: []string{"duplicate"},
		},
		{
			name:     "empty manifest attests nothing",
			manifest: "# nothing here\n\n",
			wantFail: []string{"attesting to nothing"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			writeTree(t, home, tt.files)
			manifest := filepath.Join(t.TempDir(), "manifest.txt")
			if err := os.WriteFile(manifest, []byte(tt.manifest), 0o644); err != nil {
				t.Fatal(err)
			}
			att, failures, err := Attest(home, manifest)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			wantFailures(t, failures, tt.wantFail)
			if len(tt.wantFail) > 0 {
				if att.SHA256 != "" || att.Files != 0 {
					t.Errorf("failing attest must not produce an attestation, got %+v", att)
				}
				return
			}
			if att.Files != tt.wantFiles || att.Bytes != tt.wantBytes {
				t.Errorf("got files=%d bytes=%d, want files=%d bytes=%d", att.Files, att.Bytes, tt.wantFiles, tt.wantBytes)
			}
			if len(att.SHA256) != 64 {
				t.Errorf("sha256 = %q, want 64 hex chars", att.SHA256)
			}
		})
	}
}

// The attestation must be deterministic, and must move when contents,
// paths, or manifest order move — that is the whole point of the hash.
func TestAttestHashBindsContentPathAndOrder(t *testing.T) {
	manifestFile := func(t *testing.T, content string) string {
		p := filepath.Join(t.TempDir(), "m.txt")
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	home := t.TempDir()
	writeTree(t, home, map[string]string{"a.md": "alpha", "b.md": "beta"})
	m := manifestFile(t, "a.md\nb.md\n")

	first, _, err := Attest(home, m)
	if err != nil {
		t.Fatal(err)
	}
	again, _, err := Attest(home, m)
	if err != nil {
		t.Fatal(err)
	}
	if first.SHA256 != again.SHA256 {
		t.Fatalf("attestation not deterministic: %s vs %s", first.SHA256, again.SHA256)
	}

	// Tampered contents move the hash.
	writeMode(t, home, "a.md", "alpha'", 0o644)
	tampered, _, err := Attest(home, m)
	if err != nil {
		t.Fatal(err)
	}
	if tampered.SHA256 == first.SHA256 {
		t.Error("content tamper did not move the hash")
	}

	// Swapped contents (same bytes, different files) move the hash.
	swapHome := t.TempDir()
	writeTree(t, swapHome, map[string]string{"a.md": "beta", "b.md": "alpha"})
	swapped, _, err := Attest(swapHome, m)
	if err != nil {
		t.Fatal(err)
	}
	if swapped.SHA256 == first.SHA256 {
		t.Error("content swap between files did not move the hash")
	}

	// Reordered manifest moves the hash.
	reordered, _, err := Attest(home, manifestFile(t, "b.md\na.md\n"))
	if err != nil {
		t.Fatal(err)
	}
	tamperedAgain, _, err := Attest(home, m)
	if err != nil {
		t.Fatal(err)
	}
	if reordered.SHA256 == tamperedAgain.SHA256 {
		t.Error("manifest reorder did not move the hash")
	}
}

// Reviewer B1: the escape check was lexical only, so a symlinked directory
// inside --home let a manifest attest files outside the home — a hash oracle
// once the OK line is pasted publicly. Symlinks must never be followed.
func TestAttestRefusesSymlinkedDirEscape(t *testing.T) {
	home := t.TempDir()
	outside := t.TempDir()
	writeTree(t, outside, map[string]string{"secret.md": "the secret\n"})
	if err := os.Symlink(outside, filepath.Join(home, "notes")); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}
	manifest := filepath.Join(t.TempDir(), "m.txt")
	if err := os.WriteFile(manifest, []byte("notes/secret.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	att, failures, err := Attest(home, manifest)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantFailures(t, failures, []string{"notes/secret.md", "symlink"})
	if att.SHA256 != "" || att.Files != 0 {
		t.Errorf("symlink escape must not produce an attestation, got %+v", att)
	}
}

// Symlinks are never followed even when they resolve inside --home, and a
// leaf that is a symlink is refused as not a regular file.
func TestAttestRefusesSymlinksInsideHome(t *testing.T) {
	home := t.TempDir()
	writeTree(t, home, map[string]string{"real/a.md": "content"})
	if err := os.Symlink(filepath.Join(home, "real"), filepath.Join(home, "alias")); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}
	if err := os.Symlink(filepath.Join(home, "real", "a.md"), filepath.Join(home, "leaf.md")); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(t.TempDir(), "m.txt")
	if err := os.WriteFile(manifest, []byte("alias/a.md\nleaf.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	att, failures, err := Attest(home, manifest)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantFailures(t, failures, []string{
		"alias/a.md", "symlinked path component",
		"leaf.md", "not a regular file",
	})
	if att.SHA256 != "" {
		t.Errorf("symlinked entries must not produce an attestation, got %+v", att)
	}
}

// Reviewer B2: entries were deduped on the raw string before cleaning, so
// "a.md" and "./a.md" both attested and double-counted bytes over one file.
// Entries must already be canonical; anything else is a named failure.
func TestAttestRejectsNonCanonicalEntries(t *testing.T) {
	tests := []struct {
		name     string
		manifest string
	}{
		{"dot-slash spelling of a duplicate", "a.md\n./a.md\n"},
		{"leading dot-slash", "./a.md\n"},
		{"doubled separator", "notes//b.md\n"},
		{"interior dot segment", "notes/./b.md\n"},
		{"interior dot-dot segment", "notes/../a.md\n"},
		{"trailing slash", "notes/\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			writeTree(t, home, map[string]string{"a.md": "x", "notes/b.md": "y"})
			manifest := filepath.Join(t.TempDir(), "m.txt")
			if err := os.WriteFile(manifest, []byte(tt.manifest), 0o644); err != nil {
				t.Fatal(err)
			}
			att, failures, err := Attest(home, manifest)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			wantFailures(t, failures, []string{"not canonical"})
			if att.SHA256 != "" || att.Files != 0 || att.Bytes != 0 {
				t.Errorf("non-canonical manifest must not attest (double-counted bytes), got %+v", att)
			}
		})
	}
}

// Reviewer S1: the old path 0x00 contents 0x00 framing collided when contents
// contained NUL: one file holding "x\x00y.md\x00z" hashed identically to the
// two files a.md="x", y.md="z". Length-prefixed framing must separate them.
func TestAttestHashInjectiveWithNULContents(t *testing.T) {
	attest := func(files map[string]string, manifest string) Attestation {
		t.Helper()
		home := t.TempDir()
		writeTree(t, home, files)
		m := filepath.Join(t.TempDir(), "m.txt")
		if err := os.WriteFile(m, []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
		att, failures, err := Attest(home, m)
		if err != nil {
			t.Fatal(err)
		}
		if len(failures) > 0 {
			t.Fatalf("unexpected failures: %v", failures)
		}
		return att
	}
	one := attest(map[string]string{"a.md": "x\x00y.md\x00z"}, "a.md\n")
	two := attest(map[string]string{"a.md": "x", "y.md": "z"}, "a.md\ny.md\n")
	if one.SHA256 == two.SHA256 {
		t.Errorf("hash framing is not injective: one NUL-bearing file and two files collide on %s", one.SHA256)
	}
}

// An unreadable manifested file is a named failure (exit 1), not a refusal.
func TestAttestUnreadableFileIsNamedFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows: chmod 0 does not refuse reads, so this property cannot be observed here")
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads through file modes")
	}
	home := t.TempDir()
	writeTree(t, home, map[string]string{"a.md": "x"})
	if err := os.Chmod(filepath.Join(home, "a.md"), 0o000); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(t.TempDir(), "m.txt")
	if err := os.WriteFile(manifest, []byte("a.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	att, failures, err := Attest(home, manifest)
	if err != nil {
		t.Fatalf("unreadable file must fail the entry, not the run: %v", err)
	}
	wantFailures(t, failures, []string{"a.md", "unreadable"})
	if att.SHA256 != "" {
		t.Errorf("unreadable entry must not produce an attestation, got %+v", att)
	}
}

func TestAttestRefusals(t *testing.T) {
	home := t.TempDir()
	m := filepath.Join(t.TempDir(), "m.txt")
	if err := os.WriteFile(m, []byte("a.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Attest(filepath.Join(home, "no-such-dir"), m); err == nil {
		t.Error("nonexistent home should be an error, not a guess")
	}
	if _, _, err := Attest(home, filepath.Join(home, "no-such-manifest")); err == nil {
		t.Error("nonexistent manifest should be an error")
	}
}
