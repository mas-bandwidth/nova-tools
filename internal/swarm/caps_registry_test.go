package swarm

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ISSUE #917 (the registry half): the per-provider and per-model ceiling is mechanical. A
// `caps.tsv` names, per provider and per model (or `*`), the in-flight cap and the 8-hex key
// fingerprint it is scoped to; a model row overrides the provider's `*` row; a route with no
// row is refused by name; and the key's fingerprint is the only form of the key that reaches
// a line or a file.

// writeCapsFile writes a temp caps.tsv with one row per line.
func writeCapsFile(t *testing.T, rows ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "caps.tsv")
	if err := os.WriteFile(path, []byte(strings.Join(rows, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// RED: a model row overrides the provider's `*` row.
func TestCapsModelRowOverridesProviderRow(t *testing.T) {
	fp := KeyFingerprint("sk-the-key")
	caps, err := LoadCaps(writeCapsFile(t,
		"muse\t*\t2\t"+fp,
		"muse\tzen\t5\t"+fp,
	))
	if err != nil {
		t.Fatalf("the registry loads: %v", err)
	}
	if c, ok := caps.Lookup("muse", "zen", fp); !ok || c != 5 {
		t.Errorf("a model row overrides the provider * row: got cap=%d found=%t, want cap=5", c, ok)
	}
	if c, ok := caps.Lookup("muse", "other", fp); !ok || c != 2 {
		t.Errorf("a model with no row of its own takes the provider * row: got cap=%d found=%t, want cap=2", c, ok)
	}
}

// RED: the count is per fingerprint -- two workers with different fingerprints do not share
// a cap.
func TestCapsCountIsPerFingerprint(t *testing.T) {
	dir := t.TempDir()
	p, w := recoveryPool(t, dir)
	fpA, fpB := KeyFingerprint("key-a"), KeyFingerprint("key-b")
	if fpA == fpB {
		t.Fatal("two different keys must not share a fingerprint")
	}
	for _, c := range []struct{ id, fp string }{{"a", fpA}, {"b", fpA}, {"c", fpB}} {
		sc := Sidecar{ID: c.id, Provider: w.Provider, Model: w.Model, KeyFP: c.fp, Files: 1, Tokens: 1, RC: -1}
		if err := p.Add([]byte("task "+c.id), sc); err != nil {
			t.Fatal(err)
		}
		if err := p.Claim(c.id, Pending, Running); err != nil {
			t.Fatal(err)
		}
	}
	if n := CapsInflight(p, "", w.Provider, w.Model, fpA); n != 2 {
		t.Errorf("two tasks under fingerprint A want inflight=2, got %d", n)
	}
	if n := CapsInflight(p, "", w.Provider, w.Model, fpB); n != 1 {
		t.Errorf("one task under fingerprint B wants inflight=1, got %d", n)
	}
}

// RED: a bench slot store's leases carry route= and key=<fp>, and are counted per
// fingerprint.
func TestCapsCountsLeasesByRouteAndKey(t *testing.T) {
	store := t.TempDir()
	fpA, fpB := KeyFingerprint("key-a"), KeyFingerprint("key-b")
	lease := func(name, route, key string) {
		if err := os.WriteFile(filepath.Join(store, name), []byte(`{"route":"`+route+`","key":"`+key+`"}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lease("1.json", "muse/zen", fpA)
	lease("2.json", "muse/zen", fpA)
	lease("3.json", "muse/zen", fpB)
	lease("4.json", "other/model", fpA)
	if n := CapsInflight(nil, store, "muse", "zen", fpA); n != 2 {
		t.Errorf("two leases under (muse/zen, A) want inflight=2, got %d", n)
	}
	if n := CapsInflight(nil, store, "muse", "zen", fpB); n != 1 {
		t.Errorf("one lease under (muse/zen, B) wants inflight=1, got %d", n)
	}
}

// RED: a route with no cap row is refused by name, before any launch.
func TestRunRefusesRouteWithoutCapRow(t *testing.T) {
	dir := t.TempDir()
	p, w := recoveryPool(t, dir)
	sc := Sidecar{ID: "no-cap-task", Files: 1, Tokens: 1, RC: -1}
	if err := p.Add([]byte("a task on a route with no row"), sc); err != nil {
		t.Fatal(err)
	}
	caps, err := LoadCaps(writeCapsFile(t, "other\t*\t4\t"+KeyFingerprint("sk-the-key")))
	if err != nil {
		t.Fatal(err)
	}
	var out, errb strings.Builder
	code := Run(RunInput{Pool: p, Worker: w, Key: "sk-the-key", Caps: caps,
		Workers: 1, Hours: 0.0001, NoSandbox: true, Stdout: &out, Stderr: &errb,
		Now: func() time.Time { return time.Now().UTC() }})
	want := "RUN REFUSED reason=no_cap route=fake/fake-model"
	if code != 2 || !strings.Contains(errb.String(), want) {
		t.Fatalf("a route without a row is refused by name: exit=%d want 2, stderr=%q want it to hold %q\nstdout:\n%s",
			code, errb.String(), want, out.String())
	}
}

// RED: the fingerprint never appears in usage.tsv, reports or logs except as the 8-hex
// prefix, and never as the key.
func TestCapsFingerprintNeverLeaksIntoUsage(t *testing.T) {
	key := "sk-live-supersecret-0123456789"
	fp := KeyFingerprint(key)
	if len(fp) != 8 || strings.Trim(fp, "0123456789abcdef") != "" {
		t.Fatalf("the fingerprint wants 8 lowercase hex characters, got %q", fp)
	}
	sum := sha256.Sum256([]byte(key))
	full := hex.EncodeToString(sum[:])
	if full == fp || !strings.HasPrefix(full, fp) {
		t.Fatalf("the fingerprint is the sha256 prefix of the key value: fp=%q full=%q", fp, full)
	}
	for _, c := range UsageColumns {
		low := strings.ToLower(c)
		if strings.Contains(low, "key") || strings.Contains(low, "fingerprint") || strings.Contains(low, "secret") {
			t.Errorf("usage.tsv carries no key column, got %q", c)
		}
	}
	dir := t.TempDir()
	p, _ := recoveryPool(t, dir)
	path, _, err := p.WriteUsage("job", UsageRow{"job": "job", "provider": "muse", "model": "zen"})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), key) || strings.Contains(string(raw), full) {
		t.Errorf("the key and its full digest never reach usage.tsv: %q", string(raw))
	}
}
