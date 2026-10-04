package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func shipped(h *harness, stamp string) []string {
	var out []string
	for _, t := range h.e.targets {
		for _, n := range []string{"nova-bus", "nova-tokens"} {
			out = append(out, artifactName(n, stamp, t))
		}
	}
	return out
}

func fillDist(t *testing.T, h *harness, stamp string) string {
	t.Helper()
	h.tools("nova-bus", "nova-tokens")
	for _, n := range shipped(h, stamp) {
		h.write("dist/"+n, "bytes of "+n, 0o755)
	}
	return filepath.Join(h.dir, "dist")
}

func TestSumsWritesAndVerifiesOverExactlyTheShippedSet(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.e.targets = []target{{"linux", "amd64"}, {"windows", "amd64"}}
	dist := fillDist(t, h, "v1.0.0")
	h.wantRC(h.do("sums", "v1.0.0", "dist"), 0)

	raw, err := os.ReadFile(filepath.Join(dist, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 4 {
		t.Fatalf("SHA256SUMS has %d lines, want 4 (two tools x two platforms):\n%s", len(lines), raw)
	}
	for _, l := range lines {
		sum, name, _ := strings.Cut(l, "  ")
		b, _ := os.ReadFile(filepath.Join(dist, name))
		want := sha256.Sum256(b)
		if sum != hex.EncodeToString(want[:]) {
			t.Errorf("%s: sum %s is not the sha256 of the bytes on disk", name, sum)
		}
		if name == "SHA256SUMS" {
			t.Error("SHA256SUMS lists itself")
		}
	}
	if !strings.Contains(string(raw), "nova-bus_v1.0.0_windows_amd64.exe") {
		t.Errorf("the windows artifact is not listed with .exe:\n%s", raw)
	}
	if fi, _ := os.Stat(filepath.Join(dist, "SHA256SUMS")); fi.Mode().Perm() != 0o644 {
		t.Errorf("SHA256SUMS mode %v, want 0644", fi.Mode().Perm())
	}
	h.mustContain("nova-bus_v1.0.0_linux_amd64: OK\n")
	h.mustContain("SHA256SUMS over 4 artifacts:\n")
}

func TestSumsRefusesADirectoryThatIsNotTheShippedSetNamingTheDifference(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	dist := fillDist(t, h, "v1.0.0")
	if err := os.Remove(filepath.Join(dist, "nova-tokens_v1.0.0_linux_amd64")); err != nil {
		t.Fatal(err)
	}
	h.write("dist/stray.log", "a runner left this", 0o644)
	h.wantRC(h.do("sums", "v1.0.0", "dist"), 1)
	h.mustContain("-nova-tokens_v1.0.0_linux_amd64\n")
	h.mustContain("+stray.log\n")
	h.mustContain("is not the shipped set (- missing, + not shipped)")
	if _, err := os.Stat(filepath.Join(dist, "SHA256SUMS")); err == nil {
		t.Fatal("a checksum file was written over a set that is not the shipped set")
	}
}

func TestSumsRefusesAStampThatDoesNotNameTheArtifacts(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	fillDist(t, h, "v1.0.0")
	h.wantRC(h.do("sums", "v2.0.0", "dist"), 1)
	h.mustContain("-nova-bus_v2.0.0_linux_amd64")
	h.mustContain("+nova-bus_v1.0.0_linux_amd64")
}

func TestSumsRefusesWhatIsNotADirectoryOrAlreadyHoldsSums(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.tools("nova-bus")
	h.wantRC(h.do("sums", "v1.0.0", "nowhere"), 1)
	h.mustContain("refusing: nowhere is not a directory")

	h = newHarness(t)
	dist := fillDist(t, h, "v1.0.0")
	h.write("dist/SHA256SUMS", "an earlier one\n", 0o644)
	h.wantRC(h.do("sums", "v1.0.0", "dist"), 1)
	h.mustContain("already holds a SHA256SUMS")
	if b, _ := os.ReadFile(filepath.Join(dist, "SHA256SUMS")); string(b) != "an earlier one\n" {
		t.Fatal("an existing SHA256SUMS was overwritten")
	}
}

func TestSumsRefusesAnEmptyShippedSet(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.write("dist/x", "", 0o644)
	h.wantRC(h.do("sums", "v1.0.0", "dist"), 1)
	h.mustContain("refusing: the shipped set is empty (cmd/*/ x release-targets)")
}

func TestSumsWrongArgumentCountIsAUsageError(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"sums"}, {"sums", "v1"}, {"sums", "v1", "dist", "x"}} {
		h := newHarness(t)
		h.wantRC(h.do(args...), 2)
		h.mustContain("usage:")
	}
}

func TestVerifySumsNamesAFileWhoseBytesChanged(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	dist := fillDist(t, h, "v1.0.0")
	h.wantRC(h.do("sums", "v1.0.0", "dist"), 0)
	if err := os.WriteFile(filepath.Join(dist, "nova-bus_v1.0.0_linux_amd64"), []byte("tampered"), 0o755); err != nil {
		t.Fatal(err)
	}
	h2 := newHarness(t)
	if verifySums(h2.out, h2.errb, dist, filepath.Join(dist, "SHA256SUMS")) {
		t.Fatal("a tampered file verified")
	}
	h2.mustContain("nova-bus_v1.0.0_linux_amd64: FAILED")
	h2.mustContain("nova-tokens_v1.0.0_linux_amd64: OK")
}

func TestDiffSortedFindsBothHalves(t *testing.T) {
	t.Parallel()
	missing, extra := diffSorted([]string{"a", "b", "d"}, []string{"b", "c", "e"})
	if strings.Join(missing, ",") != "a,d" || strings.Join(extra, ",") != "c,e" {
		t.Fatalf("missing %v extra %v", missing, extra)
	}
}
