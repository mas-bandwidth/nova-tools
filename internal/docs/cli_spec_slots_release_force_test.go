package docs

import (
	"os"
	"strings"
	"testing"
)

// THE SPEC THAT STILL SAID "FREES THEM" (#1902, Johnny's hold on PR #1943).
//
// The binary keeps a lease whose holder is still running and only `--force` frees it, but
// `docs/SPEC-SWARM.md` went on saying `slots release … frees them` and neither reference
// named the flag. A spec that describes the behaviour the code was fixed out of is worse
// than no spec: it is the version a reader trusts.
//
// This holds both documents to the keep, to `--force`, and to the two things about
// `--force` that make it safe to document at all — that it oversubscribes the bench, and
// that it is a person's act rather than anything a card or a manager passes by default.
func TestTheReferencesNameTheLiveKeepAndForceOnSlotsRelease(t *testing.T) {
	t.Parallel()

	for _, doc := range []string{"../../docs/SPEC-SWARM.md", "../../docs/CLI.md"} {
		raw, err := os.ReadFile(doc)
		if err != nil {
			t.Fatalf("%s: %v; it is where a person reads what `slots release` does", doc, err)
		}
		text := string(raw)
		for _, want := range []string{
			// the usage line carries the flag, optional.
			"nova-swarm slots release --store <dir> --owner <o> (--label <text> | --all) [--force]",
			// the keep, by its printed evidence.
			"SLOTS KEPT",
			"live=",
			// what --force costs.
			"--force",
			"oversubscribe",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("%s does not name %q; the default keeps a live holder and only --force frees it, and a reference that omits either teaches the old behaviour", doc, want)
			}
		}
		// The oversubscribing flag is a person's, not a default. Each document says so in
		// its own words, so this asks only that one of them is there.
		if !strings.Contains(text, "operator's act") {
			t.Errorf("%s does not say --force is an operator's act; a flag that oversubscribes a bench must not read like something a card or a manager may pass", doc)
		}
	}
}
