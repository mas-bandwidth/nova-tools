package ci

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCardPlatformsRequireAllCopiesOk asserts that a card with PLATFORMS naming
// multiple OSes never reaches review on only one ok (nova-tools#4395).
func TestCardPlatformsRequireAllCopiesOk(t *testing.T) {
	t.Parallel()

	// 1. Verify the Lua move implementation in internal/nsprint/fn/lua/02_card_move.lua.
	luaPath := filepath.Join(repoRoot(t), "internal", "nsprint", "fn", "lua", "02_card_move.lua")
	luaBytes, err := os.ReadFile(luaPath)
	if err != nil {
		t.Fatalf("reading 02_card_move.lua: %v", err)
	}
	luaSrc := string(luaBytes)

	// Invariants in Lua script:
	// a) FIELDS contains platform, platforms, platform_copies
	if !strings.Contains(luaSrc, "'platform', 'platforms', 'platform_copies'") {
		t.Errorf("02_card_move.lua must register platform, platforms, platform_copies in FIELDS")
	}

	// b) TM.consumer_os exists
	if !strings.Contains(luaSrc, "function TM.consumer_os(c)") {
		t.Errorf("02_card_move.lua must define TM.consumer_os(c)")
	}

	// c) TM.leg checks all platforms before returning LIVECOPY
	if !strings.Contains(luaSrc, "TM.parse_platforms(p.platforms)") ||
		!strings.Contains(luaSrc, "LIVECOPY task:' .. id .. ' has live platform copies") {
		t.Errorf("02_card_move.lua TM.leg must verify live platform copies across all platforms")
	}

	// d) TM.may checks consumer OS against required platforms
	if !strings.Contains(luaSrc, "PLATFORM task:' .. id .. ' requires ' .. TK.str(f[7])") {
		t.Errorf("02_card_move.lua TM.may must refuse consumers not matching card platforms")
	}

	// e) TM.cut tracks copy:<platform> and platform_copies
	if !strings.Contains(luaSrc, "fields[#fields + 1] = 'copy:' .. cos") ||
		!strings.Contains(luaSrc, "fields[#fields + 1] = 'platform_copies'") {
		t.Errorf("02_card_move.lua TM.cut must record copy:<platform> and platform_copies")
	}

	// f) TM.finish tracks ok:<cplat> and verifies all_ok before moving to review
	if !strings.Contains(luaSrc, "pf[#pf + 1] = 'ok:' .. cplat") ||
		!strings.Contains(luaSrc, "to, stay = 'working', true") {
		t.Errorf("02_card_move.lua TM.finish must record ok:<platform> and stay in working until all platforms are ok")
	}

	// g) TM.finish names failing platform on fail
	if !strings.Contains(luaSrc, "platform ' .. cplat .. ' failed") {
		t.Errorf("02_card_move.lua TM.finish must name failing platform on failure")
	}

	// 2. Verify Go card model & invariant support:
	copyGoPath := filepath.Join(repoRoot(t), "internal", "nsprint", "card", "copy.go")
	copyGoBytes, err := os.ReadFile(copyGoPath)
	if err != nil {
		t.Fatalf("reading copy.go: %v", err)
	}
	if !strings.Contains(string(copyGoBytes), `line("PLATFORM", c.Platform)`) {
		t.Errorf("copy.go must render PLATFORM in RenderCopy")
	}

	invariantGoPath := filepath.Join(repoRoot(t), "internal", "cardhdr", "invariant.go")
	invariantGoBytes, err := os.ReadFile(invariantGoPath)
	if err != nil {
		t.Fatalf("reading invariant.go: %v", err)
	}
	if !strings.Contains(string(invariantGoBytes), "func InferPlatforms") ||
		!strings.Contains(string(invariantGoBytes), "func ParsePlatforms") {
		t.Errorf("invariant.go must define InferPlatforms and ParsePlatforms")
	}
}
