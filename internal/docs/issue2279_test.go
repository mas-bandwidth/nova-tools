package docs

import (
	"os"
	"strings"
	"testing"
)

// TestIssue2279 pins the spec requirements from nova-tools#2279:
// Enforce owner and TTL on spill and make recall refuse an expired key.
// The issue quotes the following spec lines as the receipt:
//   "and a required **TTL**; ... a write with no owner or no TTL is
//    refused." (docs/SPEC-REDIS.md:63-66)
//   "Every ephemeral key carries an owner prefix and a TTL; an unbounded key is
//    a bug." (docs/SPEC-REDIS.md:86-87)
//   "`spill`/`recall` with a TTL that expires" (docs/SPEC-REDIS.md:102-103)
func TestIssue2279(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile("../../docs/SPEC-REDIS.md")
	if err != nil {
		t.Fatalf("docs/SPEC-REDIS.md: %v", err)
	}
	content := string(body)

	// Verify the quoted spec lines exist
	// Line 63-66: spill requires owner prefix and TTL, write with no owner or no TTL is refused
	if !strings.Contains(content, "`spill` writes a scratch value under an **owner prefix** and a required") {
		t.Errorf("docs/SPEC-REDIS.md missing spill owner and TTL requirement")
	}
	if !strings.Contains(content, "**TTL**") {
		t.Errorf("docs/SPEC-REDIS.md missing **TTL**")
	}
	if !strings.Contains(content, "a write with no owner or no TTL is") {
		t.Errorf("docs/SPEC-REDIS.md missing 'a write with no owner or no TTL is'")
	}
	if !strings.Contains(content, "refused") {
		t.Errorf("docs/SPEC-REDIS.md missing 'refused'")
	}

	// Line 86-87: Every ephemeral key carries owner prefix and TTL
	if !strings.Contains(content, "Every ephemeral key carries an owner prefix and a TTL") {
		t.Errorf("docs/SPEC-REDIS.md missing requirement that every ephemeral key carries owner and TTL")
	}
	if !strings.Contains(content, "an unbounded key is") {
		t.Errorf("docs/SPEC-REDIS.md missing 'an unbounded key is'")
	}
	if !strings.Contains(content, "a bug") {
		t.Errorf("docs/SPEC-REDIS.md missing 'a bug'")
	}

	// Line 102-103: spill/recall with TTL that expires
	if !strings.Contains(content, "`spill`/`recall` with a TTL that") {
		t.Errorf("docs/SPEC-REDIS.md missing '`spill`/`recall` with a TTL that'")
	}
	if !strings.Contains(content, "expires") {
		t.Errorf("docs/SPEC-REDIS.md missing 'expires'")
	}

	// Verify the "Tests this spec demands" section exists with the required test names
	if !strings.Contains(content, "## Tests this spec demands") {
		t.Errorf("docs/SPEC-REDIS.md missing '## Tests this spec demands' section")
	}
	if !strings.Contains(content, "TestSpillRefusedWithoutOwner") {
		t.Errorf("docs/SPEC-REDIS.md missing TestSpillRefusedWithoutOwner")
	}
	if !strings.Contains(content, "TestSpillRefusedWithoutTTL") {
		t.Errorf("docs/SPEC-REDIS.md missing TestSpillRefusedWithoutTTL")
	}
	if !strings.Contains(content, "TestRecallRefusesAnExpiredKey") {
		t.Errorf("docs/SPEC-REDIS.md missing TestRecallRefusesAnExpiredKey")
	}
	if !strings.Contains(content, "TestEveryEphemeralKeyCarriesOwnerAndTTL") {
		t.Errorf("docs/SPEC-REDIS.md missing TestEveryEphemeralKeyCarriesOwnerAndTTL")
	}
}
