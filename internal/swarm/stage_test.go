package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseCardBase(t *testing.T) {
	t.Parallel()

	card := []byte("RESULT test-1 sha=1234\nbase-repo: https://example.com/mas-bandwidth/nova-tools.git\nbase-sha: 09fbedc9052145b20677501a1dbcb5f5ba9c87d4\nKIND: fix\n")
	repo, sha, ok := ParseCardBase(card)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if repo != "https://example.com/mas-bandwidth/nova-tools.git" {
		t.Fatalf("unexpected repo: %s", repo)
	}
	if sha != "09fbedc9052145b20677501a1dbcb5f5ba9c87d4" {
		t.Fatalf("unexpected sha: %s", sha)
	}

	cardFallback := []byte("RESULT test-2\nBASE: dev@09fbedc9052145b20677501a1dbcb5f5ba9c87d4\nSTEP 1. " + forgeClone("mas-bandwidth/schema") + " repo\n")
	repo, sha, ok = ParseCardBase(cardFallback)
	if !ok {
		t.Fatal("expected ok=true for fallback")
	}
	if repo != defaultProbeBase+"/mas-bandwidth/schema.git" {
		t.Fatalf("unexpected repo: %s", repo)
	}
	if sha != "09fbedc9052145b20677501a1dbcb5f5ba9c87d4" {
		t.Fatalf("unexpected sha: %s", sha)
	}
}

func TestFindBenchMirror(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	mirrorDir := filepath.Join(home, "nova-bench", "mirror", "nova-tools.git")
	if err := os.MkdirAll(mirrorDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mirrorDir, "HEAD"), []byte("ref: refs/heads/dev\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	found := FindBenchMirror(home, "https://example.com/mas-bandwidth/nova-tools.git")
	if found != mirrorDir {
		t.Fatalf("expected %s, got %s", mirrorDir, found)
	}

	notFound := FindBenchMirror(home, "https://example.com/mas-bandwidth/nonexistent.git")
	if notFound != "" {
		t.Fatalf("expected empty string, got %s", notFound)
	}
}

func TestStageCardFailsWithoutMirror(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	target := filepath.Join(root, "jobs", "card-1", "repo")
	jobDir := filepath.Join(root, "jobs", "card-1")

	card := []byte("base-repo: https://example.com/mas-bandwidth/missing-mirror.git\nbase-sha: 09fbedc9052145b20677501a1dbcb5f5ba9c87d4\n")
	res, err := StageCard(StageOptions{
		Card:      card,
		TargetDir: target,
		JobDir:    jobDir,
		BenchHome: filepath.Join(root, "home"),
		BenchName: "testhost",
		Timeout:   30 * time.Second,
	})
	if err == nil {
		t.Fatal("expected StageCard to fail when cloning directly without mirror")
	}
	if res.Staged {
		t.Fatal("expected Staged=false")
	}
	if !strings.Contains(err.Error(), "no bench mirror") {
		t.Fatalf("expected error mentioning no bench mirror, got: %v", err)
	}
}
