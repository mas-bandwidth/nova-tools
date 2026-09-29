package main

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// Regression tests for Tool Efficiency Audit: nova-swarm slots list --max flag.
func TestSlotsListMaxFlag(t *testing.T) {
	t.Parallel()

	// Set up a store with 25 leases.
	store := slotShares(t, "capacity\t30\nreserve\t0\nalice\t30\n")
	until := time.Now().UTC().Add(time.Hour)
	pid := os.Getpid()
	for i := 0; i < 25; i++ {
		id := fmt.Sprintf("lease-%02d", i)
		label := fmt.Sprintf("card-%02d", i)
		if err := swarm.MakeSlotLease(store, id, "alice", pid, label, until); err != nil {
			t.Fatalf("MakeSlotLease %s: %v", id, err)
		}
	}

	t.Run("default max 20", func(t *testing.T) {
		var out, errb bytes.Buffer
		rc := run([]string{"slots", "list", "--store", store}, strings.NewReader(""), &out, &errb, time.Now())
		if rc != 0 {
			t.Fatalf("slots list default max: %d, stderr: %s", rc, errb.String())
		}
		lines := strings.Split(strings.TrimSpace(out.String()), "\n")
		var slotLines []string
		for _, l := range lines {
			if strings.HasPrefix(l, "SLOT ") {
				slotLines = append(slotLines, l)
			}
		}
		if len(slotLines) != 20 {
			t.Fatalf("expected 20 slot lines with default max, got %d:\n%s", len(slotLines), out.String())
		}
		wantSummary := "... and 5 more (use --max 0 to see all)"
		if !strings.Contains(out.String(), wantSummary) {
			t.Fatalf("expected summary line %q, got:\n%s", wantSummary, out.String())
		}
	})

	t.Run("custom max 5", func(t *testing.T) {
		var out, errb bytes.Buffer
		rc := run([]string{"slots", "list", "--store", store, "--max", "5"}, strings.NewReader(""), &out, &errb, time.Now())
		if rc != 0 {
			t.Fatalf("slots list --max 5: %d, stderr: %s", rc, errb.String())
		}
		lines := strings.Split(strings.TrimSpace(out.String()), "\n")
		var slotLines []string
		for _, l := range lines {
			if strings.HasPrefix(l, "SLOT ") {
				slotLines = append(slotLines, l)
			}
		}
		if len(slotLines) != 5 {
			t.Fatalf("expected 5 slot lines with --max 5, got %d:\n%s", len(slotLines), out.String())
		}
		wantSummary := "... and 20 more (use --max 0 to see all)"
		if !strings.Contains(out.String(), wantSummary) {
			t.Fatalf("expected summary line %q, got:\n%s", wantSummary, out.String())
		}
	})

	t.Run("max 0 shows all", func(t *testing.T) {
		var out, errb bytes.Buffer
		rc := run([]string{"slots", "list", "--store", store, "--max", "0"}, strings.NewReader(""), &out, &errb, time.Now())
		if rc != 0 {
			t.Fatalf("slots list --max 0: %d, stderr: %s", rc, errb.String())
		}
		lines := strings.Split(strings.TrimSpace(out.String()), "\n")
		var slotLines []string
		for _, l := range lines {
			if strings.HasPrefix(l, "SLOT ") {
				slotLines = append(slotLines, l)
			}
		}
		if len(slotLines) != 25 {
			t.Fatalf("expected all 25 slot lines with --max 0, got %d:\n%s", len(slotLines), out.String())
		}
		if strings.Contains(out.String(), "... and") || strings.Contains(out.String(), "more (use --max 0 to see all)") {
			t.Fatalf("expected no summary line with --max 0, got:\n%s", out.String())
		}
	})

	t.Run("fewer items than max prints no summary", func(t *testing.T) {
		var out, errb bytes.Buffer
		rc := run([]string{"slots", "list", "--store", store, "--max", "50"}, strings.NewReader(""), &out, &errb, time.Now())
		if rc != 0 {
			t.Fatalf("slots list --max 50: %d, stderr: %s", rc, errb.String())
		}
		lines := strings.Split(strings.TrimSpace(out.String()), "\n")
		var slotLines []string
		for _, l := range lines {
			if strings.HasPrefix(l, "SLOT ") {
				slotLines = append(slotLines, l)
			}
		}
		if len(slotLines) != 25 {
			t.Fatalf("expected all 25 slot lines with --max 50, got %d:\n%s", len(slotLines), out.String())
		}
		if strings.Contains(out.String(), "... and") || strings.Contains(out.String(), "more (use --max 0 to see all)") {
			t.Fatalf("expected no summary line with --max 50, got:\n%s", out.String())
		}
	})

	t.Run("negative max is refused", func(t *testing.T) {
		var out, errb bytes.Buffer
		rc := run([]string{"slots", "list", "--store", store, "--max", "-1"}, strings.NewReader(""), &out, &errb, time.Now())
		if rc != 2 {
			t.Fatalf("expected exit 2 for negative max, got %d", rc)
		}
		if !strings.Contains(errb.String(), "--max is 0 or more, got -1; 0 already means all") {
			t.Fatalf("expected negative max refusal on stderr, got:\n%s", errb.String())
		}
	})
}
