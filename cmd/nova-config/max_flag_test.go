package main

import (
	"fmt"
	"strings"
	"testing"
)

func countPrefixLines(output, prefix string) int {
	count := 0
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if strings.HasPrefix(line, prefix) {
			count++
		}
	}
	return count
}

func TestMachineListMaxFlag(t *testing.T) {
	t.Parallel()
	h := newHarness()
	h.env["NOVA_PG_DSN"] = dsn
	h.env["NOVA_FRIEND"] = "operator"

	// Insert 25 machines: bench-01 to bench-25
	for i := 1; i <= 25; i++ {
		name := fmt.Sprintf("bench-%02d", i)
		code, _, errs := h.run(t, "machine", "add", name, "--user", "user", "--seat", name, "--slots", "10")
		if code != 0 {
			t.Fatalf("machine add %s failed: %s", name, errs)
		}
	}

	// 1. Default --max (bounded.Default = 20)
	code, out, errs := h.run(t, "machine", "list")
	if code != 0 {
		t.Fatalf("machine list default failed: %s", errs)
	}
	if got := countPrefixLines(out, "MACHINE "); got != 20 {
		t.Errorf("default machine list: got %d lines, want 20", got)
	}
	if !strings.Contains(out, "... and 5 more (use --max 0 to see all)\n") {
		t.Errorf("default machine list missing summary line; out:\n%s", out)
	}
	if !strings.HasSuffix(strings.TrimSpace(out), "CONFIG LIST kind=machine rows=25") {
		t.Errorf("default machine list missing CONFIG LIST count; out:\n%s", out)
	}

	// 2. Custom --max 5
	code, out, errs = h.run(t, "machine", "list", "--max", "5")
	if code != 0 {
		t.Fatalf("machine list --max 5 failed: %s", errs)
	}
	if got := countPrefixLines(out, "MACHINE "); got != 5 {
		t.Errorf("machine list --max 5: got %d lines, want 5", got)
	}
	if !strings.Contains(out, "... and 20 more (use --max 0 to see all)\n") {
		t.Errorf("machine list --max 5 missing summary line; out:\n%s", out)
	}
	if !strings.HasSuffix(strings.TrimSpace(out), "CONFIG LIST kind=machine rows=25") {
		t.Errorf("machine list --max 5 missing CONFIG LIST count; out:\n%s", out)
	}

	// 3. Unlimited --max 0
	code, out, errs = h.run(t, "machine", "list", "--max", "0")
	if code != 0 {
		t.Fatalf("machine list --max 0 failed: %s", errs)
	}
	if got := countPrefixLines(out, "MACHINE "); got != 25 {
		t.Errorf("machine list --max 0: got %d lines, want 25", got)
	}
	if strings.Contains(out, "... and") {
		t.Errorf("machine list --max 0 should not contain '... and'; out:\n%s", out)
	}
	if !strings.HasSuffix(strings.TrimSpace(out), "CONFIG LIST kind=machine rows=25") {
		t.Errorf("machine list --max 0 missing CONFIG LIST count; out:\n%s", out)
	}

	// 4. Negative --max refused
	code, _, errs = h.run(t, "machine", "list", "--max", "-1")
	if code != 2 {
		t.Errorf("machine list --max -1 code = %d, want 2", code)
	}
	if !strings.Contains(errs, "--max must be zero or more") {
		t.Errorf("machine list --max -1 error = %q, want mention of zero or more", errs)
	}
}

func TestFriendListMaxFlag(t *testing.T) {
	t.Parallel()
	h := newHarness()
	h.env["NOVA_PG_DSN"] = dsn
	h.env["NOVA_FRIEND"] = "operator"

	// Insert 5 friends
	friends := []string{"friend-alpha", "friend-beta", "friend-gamma", "friend-delta", "friend-epsilon"}
	for _, name := range friends {
		code, _, errs := h.run(t, "friend", "add", name, "--slots", "10", "--tiers", "frontier")
		if code != 0 {
			t.Fatalf("friend add %s failed: %s", name, errs)
		}
	}

	// 1. --max 2
	code, out, errs := h.run(t, "friend", "list", "--max", "2")
	if code != 0 {
		t.Fatalf("friend list --max 2 failed: %s", errs)
	}
	if got := countPrefixLines(out, "FRIEND "); got != 2 {
		t.Errorf("friend list --max 2: got %d lines, want 2", got)
	}
	if !strings.Contains(out, "... and 3 more (use --max 0 to see all)\n") {
		t.Errorf("friend list --max 2 missing summary line; out:\n%s", out)
	}
	if !strings.HasSuffix(strings.TrimSpace(out), "CONFIG LIST kind=friend rows=5") {
		t.Errorf("friend list --max 2 missing CONFIG LIST count; out:\n%s", out)
	}

	// 2. Default --max (5 <= 20, no more line)
	code, out, errs = h.run(t, "friend", "list")
	if code != 0 {
		t.Fatalf("friend list default failed: %s", errs)
	}
	if got := countPrefixLines(out, "FRIEND "); got != 5 {
		t.Errorf("friend list default: got %d lines, want 5", got)
	}
	if strings.Contains(out, "... and") {
		t.Errorf("friend list default should not contain '... and'; out:\n%s", out)
	}
	if !strings.HasSuffix(strings.TrimSpace(out), "CONFIG LIST kind=friend rows=5") {
		t.Errorf("friend list default missing CONFIG LIST count; out:\n%s", out)
	}

	// 3. Unlimited --max 0
	code, out, errs = h.run(t, "friend", "list", "--max", "0")
	if code != 0 {
		t.Fatalf("friend list --max 0 failed: %s", errs)
	}
	if got := countPrefixLines(out, "FRIEND "); got != 5 {
		t.Errorf("friend list --max 0: got %d lines, want 5", got)
	}
	if strings.Contains(out, "... and") {
		t.Errorf("friend list --max 0 should not contain '... and'; out:\n%s", out)
	}

	// 4. Negative --max refused
	code, _, errs = h.run(t, "friend", "list", "--max", "-5")
	if code != 2 {
		t.Errorf("friend list --max -5 code = %d, want 2", code)
	}
	if !strings.Contains(errs, "--max must be zero or more") {
		t.Errorf("friend list --max -5 error = %q, want mention of zero or more", errs)
	}
}

func TestHistoryMaxFlag(t *testing.T) {
	t.Parallel()
	h := newHarness()
	h.env["NOVA_PG_DSN"] = dsn
	h.env["NOVA_FRIEND"] = "operator"

	// Insert friend and update 24 times (total 25 changes: 1 add + 24 sets)
	code, _, errs := h.run(t, "friend", "add", "friend-alpha", "--slots", "1", "--tiers", "frontier")
	if code != 0 {
		t.Fatalf("friend add failed: %s", errs)
	}
	for i := 2; i <= 25; i++ {
		code, _, errs = h.run(t, "friend", "set", "friend-alpha", "--slots", fmt.Sprintf("%d", i))
		if code != 0 {
			t.Fatalf("friend set %d failed: %s", i, errs)
		}
	}

	// 1. Default --max (20 shown, 5 elided)
	code, out, errs := h.run(t, "friend", "history", "friend-alpha")
	if code != 0 {
		t.Fatalf("friend history default failed: %s", errs)
	}
	if got := countPrefixLines(out, "HISTORY "); got != 20 {
		t.Errorf("friend history default: got %d lines, want 20", got)
	}
	if !strings.Contains(out, "... and 5 more (use --max 0 to see all)\n") {
		t.Errorf("friend history default missing summary line; out:\n%s", out)
	}
	if !strings.HasSuffix(strings.TrimSpace(out), "CONFIG HISTORY kind=friend name=friend-alpha changes=25") {
		t.Errorf("friend history default missing CONFIG HISTORY count; out:\n%s", out)
	}

	// 2. Custom --max 3
	code, out, errs = h.run(t, "friend", "history", "friend-alpha", "--max", "3")
	if code != 0 {
		t.Fatalf("friend history --max 3 failed: %s", errs)
	}
	if got := countPrefixLines(out, "HISTORY "); got != 3 {
		t.Errorf("friend history --max 3: got %d lines, want 3", got)
	}
	if !strings.Contains(out, "... and 22 more (use --max 0 to see all)\n") {
		t.Errorf("friend history --max 3 missing summary line; out:\n%s", out)
	}
	if !strings.HasSuffix(strings.TrimSpace(out), "CONFIG HISTORY kind=friend name=friend-alpha changes=25") {
		t.Errorf("friend history --max 3 missing CONFIG HISTORY count; out:\n%s", out)
	}

	// 3. Flags before name: --max 2 friend-alpha
	code, out, errs = h.run(t, "friend", "history", "--max", "2", "friend-alpha")
	if code != 0 {
		t.Fatalf("friend history --max 2 friend-alpha failed: %s", errs)
	}
	if got := countPrefixLines(out, "HISTORY "); got != 2 {
		t.Errorf("friend history flags before name: got %d lines, want 2", got)
	}
	if !strings.Contains(out, "... and 23 more (use --max 0 to see all)\n") {
		t.Errorf("friend history flags before name missing summary line; out:\n%s", out)
	}

	// 4. Unlimited --max 0
	code, out, errs = h.run(t, "friend", "history", "friend-alpha", "--max", "0")
	if code != 0 {
		t.Fatalf("friend history --max 0 failed: %s", errs)
	}
	if got := countPrefixLines(out, "HISTORY "); got != 25 {
		t.Errorf("friend history --max 0: got %d lines, want 25", got)
	}
	if strings.Contains(out, "... and") {
		t.Errorf("friend history --max 0 should not contain '... and'; out:\n%s", out)
	}
	if !strings.HasSuffix(strings.TrimSpace(out), "CONFIG HISTORY kind=friend name=friend-alpha changes=25") {
		t.Errorf("friend history --max 0 missing CONFIG HISTORY count; out:\n%s", out)
	}

	// 5. Top-level history dispatch: history friend friend-alpha --max 4
	code, out, errs = h.run(t, "history", "friend", "friend-alpha", "--max", "4")
	if code != 0 {
		t.Fatalf("top-level history friend friend-alpha failed: %s", errs)
	}
	if got := countPrefixLines(out, "HISTORY "); got != 4 {
		t.Errorf("top-level history: got %d lines, want 4", got)
	}
	if !strings.Contains(out, "... and 21 more (use --max 0 to see all)\n") {
		t.Errorf("top-level history missing summary line; out:\n%s", out)
	}

	// 6. Top-level history dispatch with row name: history friend-alpha --max 2
	code, out, errs = h.run(t, "history", "friend-alpha", "--max", "2")
	if code != 0 {
		t.Fatalf("top-level history friend-alpha failed: %s", errs)
	}
	if got := countPrefixLines(out, "HISTORY "); got != 2 {
		t.Errorf("top-level history with name: got %d lines, want 2", got)
	}

	// 7. Singleton fleet history: fleet history --max 2
	// Add machine first so fleet set can reference it
	h.run(t, "machine", "add", "bench-alpha", "--user", "user", "--seat", "bench-alpha", "--slots", "10")
	for i := 1; i <= 5; i++ {
		h.run(t, "fleet", "set", "--coordinator", "bench-alpha")
	}
	code, out, errs = h.run(t, "fleet", "history", "--max", "2")
	if code != 0 {
		t.Fatalf("fleet history --max 2 failed: %s", errs)
	}
	if got := countPrefixLines(out, "HISTORY "); got != 2 {
		t.Errorf("fleet history --max 2: got %d lines, want 2", got)
	}
	if !strings.Contains(out, "... and 3 more (use --max 0 to see all)\n") {
		t.Errorf("fleet history --max 2 missing summary line; out:\n%s", out)
	}
	if !strings.HasSuffix(strings.TrimSpace(out), "CONFIG HISTORY kind=fleet name=fleet changes=5") {
		t.Errorf("fleet history --max 2 missing CONFIG HISTORY count; out:\n%s", out)
	}

	// 8. Negative --max refused
	code, _, errs = h.run(t, "friend", "history", "friend-alpha", "--max", "-1")
	if code != 2 {
		t.Errorf("friend history --max -1 code = %d, want 2", code)
	}
	if !strings.Contains(errs, "--max must be zero or more") {
		t.Errorf("friend history --max -1 error = %q, want mention of zero or more", errs)
	}
}
