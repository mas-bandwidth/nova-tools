package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeAgePub is the public key the stand-in age-keygen below writes into the key file
// it produces. The shape of keygen's success output is all this test asserts, so the
// key never needs to be a real X25519 key and the test never needs a real age-keygen.
const fakeAgePub = "age1fake000000000000000000000000000000000000000000000000000000000"

// writeFakeAgeKeygen writes a tiny stand-in for age-keygen: it answers --version and,
// for -o <path>, writes a key file whose "# public key:" comment carries fakeAgePub.
func writeFakeAgeKeygen(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "age-keygen")
	script := `#!/bin/sh
case "$1" in
  --version|-version) echo "v1.3.2"; exit 0 ;;
esac
out=""
prev=""
for a in "$@"; do
  [ "$prev" = "-o" ] && out="$a"
  prev="$a"
done
cat > "$out" <<'EOF'
# created: 2026-09-18
# public key: age1fake000000000000000000000000000000000000000000000000000000000
AGE-SECRET-KEY-1FAKE
EOF
`
	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	return path
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// TestKeygenSuccessReadsAsSuccess runs keygen with no --store and pins the human-facing
// shape of a green run: the last line is a plain closing line saying the key was made,
// where it is, and the next step; and the NOTE about the placeholder cannot be read as
// a refusal (nova-tools#1393).
func TestKeygenSuccessReadsAsSuccess(t *testing.T) {
	bin := buildNovaSecrets(t)
	ageKeygen := writeFakeAgeKeygen(t)

	keyDir := filepath.Join(t.TempDir(), "keys")
	if err := os.MkdirAll(keyDir, 0700); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(keyDir, "air.key")

	out, errOut, code := runNovaSecrets(bin, "keygen", "--as", "air", "--key", keyPath, "--age-keygen", ageKeygen)
	if code != 0 {
		t.Fatalf("keygen failed: code %d, err: %s", code, errOut)
	}

	lines := nonEmptyLines(out)
	if len(lines) < 2 {
		t.Fatalf("keygen printed fewer than two lines:\n%s", out)
	}

	last := lines[len(lines)-1]
	wantLast := "Next: send this public key to whoever seals your seat: " + fakeAgePub
	if last != wantLast {
		t.Errorf("last line is not the plain closing line\n got: %q\nwant: %q\nfull:\n%s", last, wantLast, out)
	}

	wantDone := "Done. Your new key is at " + keyPath + ". Nothing failed."
	if lines[len(lines)-2] != wantDone {
		t.Errorf("second-to-last line does not say it worked and where the key is\n got: %q\nwant: %q\nfull:\n%s", lines[len(lines)-2], wantDone, out)
	}

	for _, l := range lines {
		if strings.Contains(l, "unfilled") {
			t.Errorf("the NOTE still reads as a failure: %q", l)
		}
	}
}
