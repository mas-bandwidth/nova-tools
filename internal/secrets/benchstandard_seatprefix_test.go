package secrets

import (
	"strings"
	"testing"
)

// TestBenchStandardChecksOneSeatKeyPerOwnerPrefix pins docs/SPEC-SECRETS.md,
// Additions from dogfooding, item 6: "The bench standard checks exactly one
// seat key per owner prefix", because two keys for one owner is either a
// lost key still trusted or a grant nobody declared.
//
// The owner prefix is the part of a seat-key name before its first "-"
// (rowan-claude.key and rowan-codex.key are both owner rowan). The bench
// standard must refuse two keys under one owner BY OWNER: a
// `DRIFT seat owner=<prefix> keys=<n> want=1` line naming the prefix and the
// seat directory, so the reader sees whose key is doubled, not only a flat
// count.
//
// This is the recut of nova-tools#2541. Its first test also demanded that
// rowan.key + alex.key (one key under each of two prefixes) PASS, which
// contradicts TestOneSeatPerOSUser on dev (rowan.key + air.key on one OS
// user must DRIFT; item 2, "one seat per OS user"). Until Stella rules on
// ruling-2541-seatprefix-1a02e30b, both sentences hold together: one OS user
// carries one key in total (item 2), and within that the check is also made
// per owner prefix (item 6). A single key passes both; the per-prefix line
// is the new assertion.
func TestBenchStandardChecksOneSeatKeyPerOwnerPrefix(t *testing.T) {
	t.Parallel()

	t.Run("one key under one owner prefix passes", func(t *testing.T) {
		t.Parallel()
		home, bin := oneSeatBenchHome(t, "rowan-claude.key")
		out, code := runOneSeatBenchStandard(t, home, bin)
		if code != 0 || !strings.Contains(out, "STANDARD OK") {
			t.Fatalf("bench-standard.sh with one key under owner rowan exited %d, want 0 and STANDARD OK:\n%s", code, out)
		}
		if l := hasDRIFTSearch(out, "DRIFT seat owner="); l != "" {
			t.Fatalf("bench-standard.sh drifted on the owner-prefix line with one key:\n%s", l)
		}
	})

	t.Run("two keys under one owner prefix drift by owner", func(t *testing.T) {
		t.Parallel()
		home, bin := oneSeatBenchHome(t, "rowan-claude.key", "rowan-codex.key")
		out, code := runOneSeatBenchStandard(t, home, bin)
		if code == 0 {
			t.Fatalf("bench-standard.sh with two keys for owner rowan exited 0, want non-zero:\n%s", out)
		}
		l := hasDRIFTSearch(out, "DRIFT seat owner=")
		if l == "" {
			t.Fatalf("docs/SPEC-SECRETS.md item 6 demands one seat key per owner prefix; bench-standard.sh has no per-owner line for [rowan-claude.key rowan-codex.key]:\n%s", out)
		}
		for _, want := range []string{"owner=rowan ", "keys=2", "want=1", ".config/nova-secrets"} {
			if !strings.Contains(l, want) {
				t.Errorf("owner-prefix DRIFT line lacks %q:\n%s", want, l)
			}
		}
	})

	t.Run("one key under each of two prefixes names no owner", func(t *testing.T) {
		t.Parallel()
		// Still DRIFT on the flat seat-keys line (item 2, TestOneSeatPerOSUser),
		// but no owner is doubled, so no owner-prefix line.
		home, bin := oneSeatBenchHome(t, "rowan.key", "air.key")
		out, _ := runOneSeatBenchStandard(t, home, bin)
		if l := hasDRIFTSearch(out, "DRIFT seat owner="); l != "" {
			t.Fatalf("no owner prefix holds two keys in [rowan.key air.key], yet bench-standard.sh named one:\n%s", l)
		}
		if hasDRIFTSearch(out, "DRIFT seat keys=") == "" {
			t.Fatalf("one OS user with two keys must still drift on the seat-keys line (TestOneSeatPerOSUser):\n%s", out)
		}
	})
}
