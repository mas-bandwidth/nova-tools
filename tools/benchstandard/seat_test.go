package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	require.NoError(t, os.Remove(filepath.Join(b.home, ".config", "nova-secrets", "rows.key")))
	for _, k := range keys {
		b.write(filepath.Join(".config", "nova-secrets", k), "AGE-SECRET-KEY-FAKE\n", false)
	}
	return b
}

// TestOneSeatPerOSUser pins docs/SPEC-SECRETS.md, Additions from dogfooding,
// item 2, "an AI is a unix user with one file and one key": exactly one *.key
// under $HOME/.config/nova-secrets is what makes a bench conforming on the
// seat-keys line, and two keys on one OS user is the drift the spec says "the
// survey reports".
func TestOneSeatPerOSUser(t *testing.T) {
	t.Parallel()

	b := seatBench(t, "ada.key")
	code, output := b.standard()
	require.Equal(t, 0, code, "one seat key, want STANDARD OK:\n%s", output)
	assert.Contains(t, output, "STANDARD OK")
	assert.Contains(t, output, "seats=1")
	assert.Empty(t, driftWith(output, "seat keys="), "drifted on the seat-keys line with exactly one key")

	// Two keys on one OS user: the control that proves the green above is not the
	// witness saying yes to anything.
	lines, b2 := seatLine(t, "ada.key", "bo.key")
	assert.Len(t, lines, 1, "the witness did not drift on the seat-keys line")
	assert.Equal(t, "DRIFT seat keys=2 want=1 in "+filepath.Join(b2.home, ".config", "nova-secrets"), lines[0])
}

// seatLine runs a seated bench over keys and returns its seat-keys DRIFT lines.
func seatLine(t *testing.T, keys ...string) ([]string, *bench) {
	t.Helper()
	b := seatBench(t, keys...)
	code, output := b.standard()
	require.NotEqual(t, 0, code, "two seat keys, want non-zero:\n%s", output)
	require.Contains(t, output, "STANDARD DRIFT")
	return driftWith(output, "seat keys="), b
}

// TestBenchStandardChecksOneSeatKeyPerOwnerPrefix pins docs/SPEC-SECRETS.md,
// item 6: "The bench standard checks exactly one seat key per owner prefix". The
// owner is the part before a key name's first "-"; the standard refuses two keys
// under one owner BY OWNER, on a line naming the prefix and the seat directory.
func TestBenchStandardChecksOneSeatKeyPerOwnerPrefix(t *testing.T) {
	t.Parallel()

	t.Run("one key under one owner prefix passes", func(t *testing.T) {
		t.Parallel()
		b := seatBench(t, "ada-claude.key")
		code, output := b.standard()
		require.Equal(t, 0, code, "one key under owner ada:\n%s", output)
		assert.Empty(t, driftWith(output, "seat owner="), "drifted on the owner-prefix line with one key")
	})

	t.Run("two keys under one owner prefix drift by owner", func(t *testing.T) {
		t.Parallel()
		b := seatBench(t, "ada-claude.key", "ada-codex.key")
		code, output := b.standard()
		require.NotEqual(t, 0, code, "two keys for owner ada:\n%s", output)
		lines := driftWith(output, "seat owner=")
		require.Len(t, lines, 1, "no per-owner line for [ada-claude.key ada-codex.key]")
		assert.Equal(t, "DRIFT seat owner=ada keys=2 want=1 in "+filepath.Join(b.home, ".config", "nova-secrets"), lines[0])
		// The per-owner line comes before the flat count, and the flat count is there too.
		i, j := strings.Index(output, "DRIFT seat owner="), strings.Index(output, "DRIFT seat keys=")
		assert.True(t, i >= 0 && j >= 0 && i < j, "owner line does not precede the flat seat-keys line")
	})

	t.Run("one key under each of two prefixes names no owner", func(t *testing.T) {
		t.Parallel()
		// Still DRIFT on the flat seat-keys line (one OS user, one key), but no
		// owner is doubled, so no owner-prefix line.
		b := seatBench(t, "ada.key", "bo.key")
		_, output := b.standard()
		assert.Empty(t, driftWith(output, "seat owner="), "no prefix holds two keys in [ada.key bo.key], yet an owner was named")
		assert.Len(t, driftWith(output, "seat keys="), 1, "one OS user with two keys must still drift on the seat-keys line")
	})

	t.Run("a name with no dash is its own owner", func(t *testing.T) {
		t.Parallel()
		b := seatBench(t, "ada.key", "ada-codex.key", "ada-claude.key")
		_, output := b.standard()
		assert.Len(t, driftWith(output, "seat owner=ada keys=3 want=1"), 1, "ada.key, ada-codex.key and ada-claude.key are three keys of one owner, ada")
	})

	t.Run("owners are named in name order", func(t *testing.T) {
		t.Parallel()
		b := seatBench(t, "zed-a.key", "zed-b.key", "amy-a.key", "amy-b.key")
		_, output := b.standard()
		got := driftWith(output, "seat owner=")
		require.Len(t, got, 2)
		assert.True(t, strings.HasPrefix(got[0], "DRIFT seat owner=amy ") && strings.HasPrefix(got[1], "DRIFT seat owner=zed "), "owner lines %v", got)
	})
}

func TestNoSeatKeyAtAllDrifts(t *testing.T) {
	t.Parallel()
	b := seatBench(t)
	b.drift(t, "seat keys=0 want=1 in "+filepath.Join(b.home, ".config", "nova-secrets"))
	// Only *.key files are seats.
	b.write(".config/nova-secrets/notes.txt", "x", false)
	_, output := b.standard()
	assert.Len(t, driftWith(output, "seat keys=0"), 1, "a non-key file counted as a seat")
}

func TestSeatCheckNeedsTheSecretsTool(t *testing.T) {
	t.Parallel()
	b := conformingBench(t)
	os.Remove(filepath.Join(b.bin, "nova-secrets"))
	b.drift(t, "nova-secrets not on PATH for seat check of "+filepath.Join(b.home, ".config/nova-secrets/rows.key"))
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
	require.Equal(t, 0, code, output)
	var got []string
	for _, c := range secretsChecks(b.h) {
		got = c
	}
	key := filepath.Join(b.home, ".config", "nova-secrets", "rows.key")
	assert.Equal(t, []string{"check", "--store", store, "--as", "rows", "--key", key, "--sops", sops}, got)
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
	require.GreaterOrEqual(t, len(got), 3)
	assert.Equal(t, store, got[2], "the seat check")
}

func TestNovaSecretsStoreIsTheStoreWhenSet(t *testing.T) {
	t.Parallel()
	b := conformingBench(t)
	store := filepath.Join(filepath.Dir(b.bin), "elsewhere")
	require.NoError(t, os.MkdirAll(store, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(store, "rows.yaml"), []byte("x"), 0o644))
	b.stub("sops")
	b.setEnv("NOVA_SECRETS_STORE=" + store)
	b.standard()
	var got []string
	for _, c := range secretsChecks(b.h) {
		got = c
	}
	require.GreaterOrEqual(t, len(got), 3)
	assert.Equal(t, store, got[2], "the seat check")
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
		assert.Len(t, secretsChecks(b.h), 1, "a failed store-backed check was retried")
	})
	t.Run("the key-only probe passes", func(t *testing.T) {
		t.Parallel()
		b := conformingBench(t)
		code, output := b.standard()
		require.Equal(t, 0, code, output)
		assert.Len(t, secretsChecks(b.h), 1, "want the one key-only check")
	})
	t.Run("the key-only probe fails and the store-backed retry passes", func(t *testing.T) {
		t.Parallel()
		b := conformingBench(t)
		b.h.first(func(s runSpec) bool {
			return isSecretsCheck(s) && len(s.args) == 3 && s.args[1] == "--key"
		}, failed)
		code, output := b.standard()
		require.Equal(t, 0, code, output)
	})
	t.Run("both fail", func(t *testing.T) {
		t.Parallel()
		b := conformingBench(t)
		b.h.first(isSecretsCheck, failed)
		b.drift(t, "seat nova-secrets check failed for "+key(b))
	})
}
