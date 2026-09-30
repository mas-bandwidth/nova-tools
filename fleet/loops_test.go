package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoopsPlistTemplateSetsAbandonProcessGroup verifies that templates/nova-loop.plist.j2
// sets AbandonProcessGroup true and fleet/loops.yml renders it.
func TestLoopsPlistTemplateSetsAbandonProcessGroup(t *testing.T) {
	t.Parallel()

	path := filepath.Join("templates", "nova-loop.plist.j2")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v; fleet/loops.yml renders this template for every loop unit", path, err)
	}
	content := string(raw)
	if !strings.Contains(content, "<key>AbandonProcessGroup</key>") || !strings.Contains(content, "<true/>") {
		t.Fatalf("%s does not set AbandonProcessGroup true; a kickstart -k then SIGTERMs the unit's process group", path)
	}
	play, err := os.ReadFile("loops.yml")
	if err != nil {
		t.Fatalf("reading fleet/loops.yml: %v", err)
	}
	if !strings.Contains(string(play), "templates/nova-loop.plist.j2") {
		t.Fatal("fleet/loops.yml does not render templates/nova-loop.plist.j2")
	}
}

// TestLoopsTableAndMemberRow verifies that fleet/loops.tsv declares nova-swarm-member
// with the exact nova-secrets exec line and nova-swarm member arguments, and that
// fleet/loops.yml implements the required substitutions and preflight checks.
func TestLoopsTableAndMemberRow(t *testing.T) {
	t.Parallel()

	tsvPath := "loops.tsv"
	raw, err := os.ReadFile(tsvPath)
	if err != nil {
		t.Fatalf("reading %s: %v", tsvPath, err)
	}

	lines := strings.Split(string(raw), "\n")
	foundMember := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 5 {
			continue
		}
		name, where, cmd := parts[0], parts[1], parts[2]
		if name == "nova-swarm-member" {
			foundMember = true
			if where != "every-bench" {
				t.Fatalf("nova-swarm-member where = %q, want 'every-bench'", where)
			}
			for _, required := range []string{
				"nova-secrets exec",
				"--store $HOME/nova-bench/secrets",
				"--as $SEAT",
				"--key $HOME/.config/nova-secrets/$SEAT.key",
				"--sops $SOPS",
				"--only NOVA_REDIS_BENCH_PASSWORD,GH_PUSH_TOKEN,DEEPSEEK_API_KEY,INCEPTION_API_KEY,OPENCODE_API_KEY,OPENROUTER_API_KEY",
				"--require NOVA_REDIS_BENCH_PASSWORD",
				"/usr/bin/env",
				"NOVA_SPRINT_REDIS=$STORE",
				"NOVA_SPRINT_REDIS_USER=ns-bench",
				"NOVA_SPRINT_REDIS_PASSWORD_ENV=NOVA_REDIS_BENCH_PASSWORD",
				"nova-swarm member",
				"--as $HOST",
				"--width $WIDTH",
				"--harness $HARNESS",
				"--model $MODEL",
				"--root $HOME/nova-bench",
				"--deadline 20m",
				"--tokens 200000",
			} {
				if !strings.Contains(cmd, required) {
					t.Errorf("nova-swarm-member command missing required fragment %q:\n%s", required, cmd)
				}
			}
		}
	}
	if !foundMember {
		t.Fatalf("%s does not declare a nova-swarm-member row", tsvPath)
	}

	playPath := "loops.yml"
	play, err := os.ReadFile(playPath)
	if err != nil {
		t.Fatalf("reading %s: %v", playPath, err)
	}
	playStr := string(play)
	for _, required := range []string{
		"loops.tsv",
		"nova-config machine self",
		"nova-config machine width",
		"templates/nova-loop.plist.j2",
		"templates/nova-loop.service.j2",
		"ansible_check_mode",
	} {
		if !strings.Contains(playStr, required) {
			t.Errorf("%s missing required element %q", playPath, required)
		}
	}
}
