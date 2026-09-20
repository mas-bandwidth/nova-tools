package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIssue1519Repro pins docs/SPEC-WORK.md's per-harness table to record which
// harnesses can be woken by a process — Grok yes, Codex yes, OpenCode yes-in-principle,
// Antigravity no (IDE with no CLI), Gemini CLI refused (#1519).
func TestIssue1519Repro(t *testing.T) {
	t.Parallel()

	specPath := filepath.Join("..", "..", "docs", "SPEC-WORK.md")
	body, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read %s: %v", specPath, err)
	}

	content := string(body)

	var missing []string

	// Grok Build: non-interactive run verb `grok -p <prompt>` must appear
	if !strings.Contains(content, "grok -p") {
		missing = append(missing, "Grok Build non-interactive verb (grok -p)")
	}
	// Codex: non-interactive run verb `codex exec <prompt>` must appear
	if !strings.Contains(content, "codex exec") {
		missing = append(missing, "Codex non-interactive verb (codex exec)")
	}
	// OpenCode: non-interactive run verb `opencode run <message>` must appear
	if !strings.Contains(content, "opencode run") {
		missing = append(missing, "OpenCode non-interactive verb (opencode run)")
	}
	// Antigravity: must record it cannot be woken (no CLI)
	if !strings.Contains(content, "Antigravity") || !strings.Contains(content, "no CLI") {
		missing = append(missing, "Antigravity (no CLI / not wakeable)")
	}
	// Gemini CLI: must record it is now refused
	if !strings.Contains(content, "Gemini CLI") {
		missing = append(missing, "Gemini CLI (refused)")
	}

	// The issue asks for a per-harness table; verify at least one row with
	// "wakeable" or "wake" appears near a harness name.
	if !strings.Contains(content, "wakeable by a process") {
		missing = append(missing, "per-harness column header 'wakeable by a process'")
	}

	if len(missing) > 0 {
		t.Errorf("per-harness table lacks wakeable-by-process entries: %s; "+
			"#1519 requires documenting which harnesses a process can start for a bounded run",
			strings.Join(missing, ", "))
	}
}
