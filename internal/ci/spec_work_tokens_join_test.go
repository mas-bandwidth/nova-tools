package ci

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// spec_work_tokens_join_test.go pins the cross-spec join that the two documents
// make by name and by nothing else: SPEC-WORK's `:attempt` `:usage` field is a
// `usage:<receipt-id>` pointer to a SPEC-TOKENS usage receipt, and SPEC-TOKENS
// rule 32 names the same shape. The two grammars must agree and each must hold
// the exact spelling the other points at (NEXT-TOOLS.md's cross-spec ask in
// SPEC-TOKENS draft 2, #240: nova-tokens never reads a work set and nova-work
// never reads a receipt, so two strings name each other).
//
// The receipt-id is pinned here as 32 lowercase hexadecimal characters: the
// drawn id a receipt carries is 32-hex (NEXT-TOOLS.md: a receipt is "under a
// drawn 32-hex id"), so the pointer grammar is checked against that spelling
// and refused when it strays from it.

var usagePointerRe = regexp.MustCompile(`^usage:[0-9a-f]{32}$`)

func TestWorkAttemptUsagePointerSchema(t *testing.T) {
	root := repoRoot(t)
	work := readFile(t, filepath.Join(root, "docs", "SPEC-WORK.md"))
	tokens := readFile(t, filepath.Join(root, "docs", "SPEC-TOKENS.md"))

	// SPEC-WORK.md: the `:attempt` event kind's `:usage` field is the pointer
	// definition. It must name the `usage:<receipt-id>` form and its receipt-id
	// spelling.
	attempt := specWorkAttemptKind(t, work)
	if !strings.Contains(attempt, "usage:<receipt-id>") {
		t.Errorf("SPEC-WORK.md `:attempt` `:usage` definition does not include \"usage:<receipt-id>\"")
	}
	if !strings.Contains(attempt, "32-character") {
		t.Errorf("SPEC-WORK.md `:attempt` `:usage` definition does not pin receipt-id as 32-character hexadecimal")
	}

	// SPEC-TOKENS.md: rule 32 is where the usage receipt specification names the
	// pointer. It must define the same `usage:<receipt-id>` form.
	rule32 := specTokensRule32(t, tokens)
	if !strings.Contains(rule32, "usage:<receipt-id>") {
		t.Errorf("SPEC-TOKENS.md rule 32 does not define \"usage:<receipt-id>\"")
	}

	// Grammar of `usage:<receipt-id>`: exactly the scheme, a colon, and a
	// 32-character lowercase hexadecimal id.
	valid := []string{
		"usage:0123456789abcdef0123456789abcdef",
		"usage:deadbeef0123456789abcdef01234567",
		"usage:00000000000000000000000000000000",
	}
	for _, p := range valid {
		if !usagePointerRe.MatchString(p) {
			t.Errorf("valid usage pointer %q failed the grammar", p)
		}
	}

	invalid := []string{
		"usage:",       // no id
		"usage:abcdef", // too short
		"usage:0123456789abcdef0123456789abcdef0",      // too long
		"usage:0123456789ABCDEF0123456789ABCDEF",       // uppercase hex is not this spelling
		"usage:0123456789abcdef0123456789abcdeg",       // non-hex character
		"commit:0123456789abcdef0123456789abcdef",      // another scheme
		" receipt:0123456789abcdef0123456789abcdef",    // not the usage scheme
		"usage:0123456789abcdef0123456789abcdef:extra", // trailing field
	}
	for _, p := range invalid {
		if usagePointerRe.MatchString(p) {
			t.Errorf("invalid usage pointer %q unexpectedly matched the grammar", p)
		}
	}
}

// specWorkAttemptKind returns the `:attempt` event kind's definition block:
// from the `  - `:attempt` ` bullet to the next `  - ` bullet.
func specWorkAttemptKind(t *testing.T, src string) string {
	t.Helper()
	lines := strings.Split(src, "\n")
	start := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "  - `:attempt`") {
			start = i
			break
		}
	}
	if start == -1 {
		t.Fatal("SPEC-WORK.md has no `  - `:attempt` ` bullet")
	}
	var block []string
	for _, line := range lines[start+1:] {
		if strings.HasPrefix(line, "  - `") {
			break
		}
		block = append(block, line)
	}
	return strings.Join(block, "\n")
}

// specTokensRule32 returns rule 32's block: from the `32. ` line to the next
// numbered rule or the section's end.
func specTokensRule32(t *testing.T, src string) string {
	t.Helper()
	lines := strings.Split(src, "\n")
	start := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "32. ") {
			start = i
			break
		}
	}
	if start == -1 {
		t.Fatal("SPEC-TOKENS.md has no rule 32")
	}
	var block []string
	for _, line := range lines[start+1:] {
		if strings.HasPrefix(line, "33. ") {
			break
		}
		block = append(block, line)
	}
	return strings.Join(block, "\n")
}
