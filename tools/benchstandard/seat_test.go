package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// secretsChecks are the times the witness ran `nova-secrets check`, the seat
// check, as opposed to the version check every nova binary gets.
func secretsChecks(h *fakeHost) [][]string {
	var checks [][]string
	for _, c := range h.calls("nova-secrets") {
		if len(c.args) > 0 && c.args[0] == "check" {
			checks = append(checks, c.args)
		}
	}
	return checks
}

func isSecretsCheck(s runSpec) bool {
	return filepath.Base(s.name) == "nova-secrets" && len(s.args) > 0 && s.args[0] == "check"
}

// seatBench is a conforming bench whose seat directory holds exactly the named
// key files.
func seatBench(t *testing.T, keys ...string) *bench {
	t.Helper()
	b := conformingBench(t)
	if err := os.Remove(filepath.Join(b.home, ".config", "nova-secrets", "rows.key")); err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		b.write(filepath.Join(".config", "nova-secrets", k), "AGE-SECRET-KEY-FAKE\n", false)
	}
	return b
}

// TestOneSeatPerOSUser pins docs/SPEC-SECRETS.md, Additions from dogfooding,
// item 2, "an AI is a unix user with one file and one key": exactly one *.key
// under $HOME/.config/nova-secrets is what makes a bench conforming on the
// seat-keys line, and two keys on one OS user is the drift the spec says "the
// survey reports". The two halves of one rule: one key is accepted and the
// STANDARD OK line carries seats=1; two keys are refused by name.
func TestOneSeatPerOSUser(t *testing.T) {
	t.Parallel()

	b := seatBench(t, "rowan.key")
	code, output := b.standard()
	if code != 0 || !strings.Contains(output, "STANDARD OK") {
		t.Fatalf("one seat key exited %d, want 0 and STANDARD OK:\n%s", code, output)
	}
	if !strings.Contains(output, "seats=1") {
		t.Errorf("the STANDARD OK line does not carry `seats=1`:\n%s", output)
	}
	if got := driftWith(output, "seat keys="); len(got) != 0 {
		t.Errorf("drifted on the seat-keys line with exactly one key: %v", got)
	}

	// Two keys on one OS user: the control that proves the green above is not the
	// witness saying yes to anything.
	b2 := seatBench(t, "rowan.key", "air.key")
	code, output = b2.standard()
	if code == 0 {
		t.Fatalf("two seat keys exited 0, want non-zero:\n%s", output)
	}
	if !strings.Contains(output, "STANDARD DRIFT") {
		t.Fatalf("no STANDARD DRIFT line:\n%s", output)
	}
	lines := driftWith(output, "seat keys=")
	if len(lines) != 1 {
		t.Fatalf("the witness did not drift on the seat-keys line:\n%s", output)
	}
	want := "DRIFT seat keys=2 want=1 in " + filepath.Join(b2.home, ".config", "nova-secrets")
	if lines[0] != want {
		t.Errorf("seat-keys line\n got %s\nwant %s", lines[0], want)
	}
}

// TestBenchStandardChecksOneSeatKeyPerOwnerPrefix pins docs/SPEC-SECRETS.md,
// item 6: "The bench standard checks exactly one seat key per owner prefix", because
// two keys for one owner is either a lost key still trusted or a grant nobody
// declared. The owner prefix is the part of a seat-key name before its first
// "-": rowan-claude.key and rowan-codex.key are both owner rowan. The standard
// refuses two keys under one owner BY OWNER, on a line that names the prefix and
// the seat directory, so the reader sees whose key is doubled and not only a
// flat count.
func TestBenchStandardChecksOneSeatKeyPerOwnerPrefix(t *testing.T) {
	t.Parallel()

	t.Run("one key under one owner prefix passes", func(t *testing.T) {
		t.Parallel()
		b := seatBench(t, "rowan-claude.key")
		code, output := b.standard()
		if code != 0 || !strings.Contains(output, "STANDARD OK") {
			t.Fatalf("one key under owner rowan exited %d:\n%s", code, output)
		}
		if got := driftWith(output, "seat owner="); len(got) != 0 {
			t.Fatalf("drifted on the owner-prefix line with one key: %v", got)
		}
	})

	t.Run("two keys under one owner prefix drift by owner", func(t *testing.T) {
		t.Parallel()
		b := seatBench(t, "rowan-claude.key", "rowan-codex.key")
		code, output := b.standard()
		if code == 0 {
			t.Fatalf("two keys for owner rowan exited 0:\n%s", output)
		}
		lines := driftWith(output, "seat owner=")
		if len(lines) != 1 {
			t.Fatalf("no per-owner line for [rowan-claude.key rowan-codex.key]:\n%s", output)
		}
		want := "DRIFT seat owner=rowan keys=2 want=1 in " + filepath.Join(b.home, ".config", "nova-secrets")
		if lines[0] != want {
			t.Errorf("owner line\n got %s\nwant %s", lines[0], want)
		}
		// The per-owner line comes before the flat count, and the flat count is there too.
		if i, j := strings.Index(output, "DRIFT seat owner="), strings.Index(output, "DRIFT seat keys="); i < 0 || j < 0 || i > j {
			t.Errorf("owner line does not precede the flat seat-keys line:\n%s", output)
		}
	})

	t.Run("one key under each of two prefixes names no owner", func(t *testing.T) {
		t.Parallel()
		// Still DRIFT on the flat seat-keys line (one OS user, one key), but no
		// owner is doubled, so no owner-prefix line.
		b := seatBench(t, "rowan.key", "air.key")
		_, output := b.standard()
		if got := driftWith(output, "seat owner="); len(got) != 0 {
			t.Fatalf("no prefix holds two keys in [rowan.key air.key], yet an owner was named: %v", got)
		}
		if len(driftWith(output, "seat keys=")) != 1 {
			t.Fatalf("one OS user with two keys must still drift on the seat-keys line:\n%s", output)
		}
	})

	t.Run("a name with no dash is its own owner", func(t *testing.T) {
		t.Parallel()
		b := seatBench(t, "rowan.key", "rowan-codex.key", "rowan-claude.key")
		_, output := b.standard()
		if got := driftWith(output, "seat owner=rowan keys=3 want=1"); len(got) != 1 {
			t.Errorf("rowan.key, rowan-codex.key and rowan-claude.key are three keys of one owner, rowan:\n%s", output)
		}
	})

	t.Run("owners are named in name order", func(t *testing.T) {
		t.Parallel()
		b := seatBench(t, "zed-a.key", "zed-b.key", "amy-a.key", "amy-b.key")
		_, output := b.standard()
		got := driftWith(output, "seat owner=")
		if len(got) != 2 || !strings.HasPrefix(got[0], "DRIFT seat owner=amy ") || !strings.HasPrefix(got[1], "DRIFT seat owner=zed ") {
			t.Errorf("owner lines %v", got)
		}
	})
}

func TestNoSeatKeyAtAllDrifts(t *testing.T) {
	t.Parallel()
	b := seatBench(t)
	code, output := b.standard()
	wantOnlyDrift(t, code, output, "seat keys=0 want=1 in "+filepath.Join(b.home, ".config", "nova-secrets"))
	// Only *.key files are seats.
	b.write(".config/nova-secrets/notes.txt", "x", false)
	if _, output = b.standard(); len(driftWith(output, "seat keys=0")) != 1 {
		t.Errorf("a non-key file counted as a seat:\n%s", output)
	}
}

func TestSeatCheckNeedsTheSecretsTool(t *testing.T) {
	t.Parallel()
	b := conformingBench(t)
	os.Remove(filepath.Join(b.bin, "nova-secrets"))
	code, output := b.standard()
	wantOnlyDrift(t, code, output, "nova-secrets not on PATH for seat check of "+filepath.Join(b.home, ".config/nova-secrets/rows.key"))
}

// The seat store at $HOME/nova-bench/secrets is found without NOVA_SECRETS_STORE,
// and with sops present the check is the store-backed one.
func TestTheSeatStoreIsFoundWithoutNovaSecretsStore(t *testing.T) {
	t.Parallel()
	b := conformingBench(t)
	store := b.mkdir("nova-bench", "secrets")
	b.write("nova-bench/secrets/rows.yaml", "bench: rows\n", false)
	sops := b.stub("sops")
	code, output := b.standard()
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, output)
	}
	var got []string
	for _, c := range secretsChecks(b.h) {
		got = c
	}
	key := filepath.Join(b.home, ".config", "nova-secrets", "rows.key")
	want := []string{"check", "--store", store, "--as", "rows", "--key", key, "--sops", sops}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the seat check was %v, want %v", got, want)
	}
}

func TestTheSeatStoreFallsBackToHomeSecrets(t *testing.T) {
	t.Parallel()
	b := conformingBench(t)
	store := b.mkdir("secrets")
	b.write("secrets/rows.yaml", "bench: rows\n", false)
	b.stub("sops")
	b.standard()
	var got []string
	for _, c := range secretsChecks(b.h) {
		got = c
	}
	if len(got) < 3 || got[2] != store {
		t.Errorf("the seat check was %v, want the store %s", got, store)
	}
}

func TestNovaSecretsStoreIsTheStoreWhenSet(t *testing.T) {
	t.Parallel()
	b := conformingBench(t)
	store := filepath.Join(filepath.Dir(b.bin), "elsewhere")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "rows.yaml"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	b.stub("sops")
	b.setEnv("NOVA_SECRETS_STORE=" + store)
	b.standard()
	var got []string
	for _, c := range secretsChecks(b.h) {
		got = c
	}
	if len(got) < 3 || got[2] != store {
		t.Errorf("the seat check was %v, want the store %s", got, store)
	}
}

func TestSeatCheckFailureRows(t *testing.T) {
	t.Parallel()
	key := func(b *bench) string { return filepath.Join(b.home, ".config/nova-secrets/rows.key") }
	t.Run("a store-backed check that fails", func(t *testing.T) {
		t.Parallel()
		b := conformingBench(t)
		b.mkdir("nova-bench", "secrets")
		b.write("nova-bench/secrets/rows.yaml", "x", false)
		b.stub("sops")
		b.h.first(isSecretsCheck, failed)
		code, output := b.standard()
		wantOnlyDrift(t, code, output, "seat nova-secrets check failed for "+key(b))
		if n := len(secretsChecks(b.h)); n != 1 {
			t.Errorf("a failed store-backed check was retried %d times", n)
		}
	})
	t.Run("the key-only probe passes", func(t *testing.T) {
		t.Parallel()
		b := conformingBench(t)
		if code, output := b.standard(); code != 0 {
			t.Fatalf("exit %d:\n%s", code, output)
		}
		if n := len(secretsChecks(b.h)); n != 1 {
			t.Errorf("ran nova-secrets check %d times, want the one key-only check", n)
		}
	})
	t.Run("the key-only probe fails and the store-backed retry passes", func(t *testing.T) {
		t.Parallel()
		b := conformingBench(t)
		b.h.first(func(s runSpec) bool {
			return isSecretsCheck(s) && len(s.args) == 3 && s.args[1] == "--key"
		}, failed)
		if code, output := b.standard(); code != 0 {
			t.Errorf("exit %d:\n%s", code, output)
		}
	})
	t.Run("both fail", func(t *testing.T) {
		t.Parallel()
		b := conformingBench(t)
		b.h.first(isSecretsCheck, failed)
		code, output := b.standard()
		wantOnlyDrift(t, code, output, "seat nova-secrets check failed for "+key(b))
	})
}
