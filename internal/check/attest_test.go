package check

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
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

// Issue #30: attest.go's package comment said "four record-layer checks" while
// SPEC.md defines six. A test that only greps for the word "six" would agree
// with the comment by construction and go stale the same way on a seventh, so
// this derives the count from SPEC.md -- the authority the comment points at.
//
// Reviewer (#66, finding 2): deriving from SPEC alone fails on SPEC/comment
// drift only, not on the CODE direction -- a seventh check wired into the
// binary with no SPEC section would leave the comment stale and this test
// green, and any non-check "### " subsection added under "## nova-check" would
// redden it falsely. So the check NAMES are compared, not just counted: the
// name in each SPEC subsection heading must be a verb the binary dispatches,
// and every check verb the binary dispatches must have a SPEC subsection. The
// numeral in attest.go's comment is then the size of that agreed set.
//
// It reads three files in the repo rather than a t.TempDir() tree because the
// property under test IS those files agreeing; nothing is written.
// dispatchRE matches one verb of run()'s switch: a case whose body hands the
// remaining args to a cmd* handler. The flag-name cases elsewhere in main.go do
// not, so they do not match.
var dispatchRE = regexp.MustCompile(`(?m)^\s*case "([a-z-]+)":\s*\n\s*return cmd`)

func TestRecordLayerCheckCountMatchesSPEC(t *testing.T) {
	const specPath = "../../docs/SPEC.md"
	spec, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("cannot read %s: %v", specPath, err)
	}
	// The checks are the "### " subsections between "## nova-check" and the
	// next tool's "## " heading; each heading opens with the check's name,
	// which is also the verb the binary dispatches ("### links -- ...").
	inTool := false
	specNames := map[string]bool{}
	for _, line := range strings.Split(string(spec), "\n") {
		switch {
		case strings.HasPrefix(line, "## nova-check"):
			inTool = true
		case inTool && strings.HasPrefix(line, "## "):
			inTool = false
		case inTool && strings.HasPrefix(line, "### "):
			name, _, _ := strings.Cut(strings.TrimPrefix(line, "### "), " ")
			specNames[strings.TrimSpace(name)] = true
		}
	}

	// The code side: the verbs run() dispatches to a cmd* handler. quickstart
	// is the door to the first run, not a record-layer check, so it is the one
	// verb excluded here -- SPEC.md documents it outside the "### " sections.
	const mainPath = "../../cmd/nova-check/main.go"
	mainSrc, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatalf("cannot read %s: %v", mainPath, err)
	}
	verbNames := map[string]bool{}
	for _, m := range dispatchRE.FindAllStringSubmatch(string(mainSrc), -1) {
		if m[1] != "quickstart" {
			verbNames[m[1]] = true
		}
	}

	for name := range specNames {
		if !verbNames[name] {
			t.Errorf("SPEC.md has a nova-check subsection %q that %s does not dispatch", brief(name), mainPath)
		}
	}
	for name := range verbNames {
		if !specNames[name] {
			t.Errorf("%s dispatches check verb %q with no SPEC.md subsection", mainPath, brief(name))
		}
	}
	checks := len(specNames)
	words := []string{"zero", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten"}
	if checks <= 0 || checks >= len(words) {
		t.Fatalf("SPEC.md names %d nova-check subsections; expected 1..%d", checks, len(words)-1)
	}
	want := words[checks] + " record-layer checks"

	const attestPath = "attest.go"
	attest, err := os.ReadFile(attestPath)
	if err != nil {
		t.Fatalf("cannot read %s: %v", attestPath, err)
	}
	first, _, _ := strings.Cut(string(attest), "\n")
	if !strings.Contains(first, want) {
		t.Errorf("%s says %s; SPEC.md defines %d checks, so it should say %q", attestPath, brief(first), checks, want)
	}
}
