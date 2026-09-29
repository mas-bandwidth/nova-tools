//go:build unix

package launch

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// TestLaunchCopyEnv verifies that LaunchCopyEnv starts the copy wrapper,
// feeds the copy line on stdin, receives the LAUNCHED acknowledgement, and
// sanitizes NOVA_CARD_HARNESS from the child's environment (#4234).
func TestLaunchCopyEnv(t *testing.T) {
	t.Parallel()

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cl := CopyLine{Copy: "work-1~1", Token: "1." + strings.Repeat("a", 32)}
	env := []string{
		"NOVA_LAUNCH_TEST_DIR=" + dir,
		"NOVA_CARD_HARNESS=/invalid/obsolete/harness",
	}
	ack, err := LaunchCopyEnv(exe, cl, DefaultBudget, env)
	if err != nil {
		t.Fatalf("LaunchCopyEnv failed: %v (ack: %q)", err, ack)
	}
	if ack != "LAUNCHED" {
		t.Fatalf("ack = %q, want LAUNCHED", ack)
	}

	copyFile := filepath.Join(dir, "copy."+cl.Copy)
	data, err := os.ReadFile(copyFile)
	if err != nil {
		t.Fatalf("read copy record: %v", err)
	}
	content := string(data)
	fields := strings.Fields(content)
	if len(fields) < 4 {
		t.Fatalf("copy file content malformed: %q", content)
	}
	pid, err := strconv.Atoi(fields[0])
	if err != nil {
		t.Fatalf("pid not a number: %v", err)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })

	if fields[1] != cl.Token {
		t.Errorf("token = %q, want %q", fields[1], cl.Token)
	}
	if !strings.Contains(content, "nova-card copy work-1~1") {
		t.Errorf("content %q lacks nova-card copy work-1~1", content)
	}
	if !strings.Contains(content, "harness=") || strings.Contains(content, "harness=/invalid") {
		t.Errorf("NOVA_CARD_HARNESS was not sanitized: %q", content)
	}
}

// TestLaunchCopyEnvWithCopyRecordNoSKeys verifies that launching a copy with a
// modern copy record at task:<copy> operates with zero s: keys in Redis,
// ensuring the crash reason 'no payload_sha on s:copies:card:<label>' cannot
// occur (#4234).
func TestLaunchCopyEnvWithCopyRecordNoSKeys(t *testing.T) {
	t.Parallel()

	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	ctx := context.Background()
	copyID := "probe-001~1"
	err := client.HSet(ctx, "task:"+copyID, map[string]any{
		"stream":   "cards",
		"sprint":   "s0",
		"repo":     "mas-bandwidth/nova-tools",
		"base":     "origin/dev",
		"base_sha": "0123456789abcdef0123456789abcdef01234567",
		"est":      "15m",
		"tier":     "pro",
		"where":    "working",
		"token":    "1." + strings.Repeat("c", 32),
		"consumer": "bench:test-bench",
		"attempt":  "1",
	}).Err()
	if err != nil {
		t.Fatal(err)
	}

	// Verify initially zero s:* keys in Redis
	sKeys, err := client.Keys(ctx, "s:*").Result()
	if err != nil {
		t.Fatal(err)
	}
	if len(sKeys) != 0 {
		t.Fatalf("expected 0 s:* keys in Redis, got %v", sKeys)
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cl := CopyLine{Copy: copyID, Token: "1." + strings.Repeat("c", 32)}
	env := []string{
		"NOVA_LAUNCH_TEST_DIR=" + dir,
		"NOVA_CARD_REDIS=" + mr.Addr(),
		"NOVA_CARD_BENCH=test-bench",
		"NOVA_CARD_HARNESS=/invalid/obsolete/harness",
	}

	ack, err := LaunchCopyEnv(exe, cl, DefaultBudget, env)
	if err != nil {
		t.Fatalf("LaunchCopyEnv failed: %v (ack: %q)", err, ack)
	}
	if ack != "LAUNCHED" {
		t.Fatalf("ack = %q, want LAUNCHED", ack)
	}

	copyFile := filepath.Join(dir, "copy."+cl.Copy)
	data, err := os.ReadFile(copyFile)
	if err != nil {
		t.Fatalf("read copy record: %v", err)
	}
	fields := strings.Fields(string(data))
	if len(fields) > 0 {
		if pid, err := strconv.Atoi(fields[0]); err == nil {
			t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
		}
	}

	// Assert zero s:* keys were created or read in Redis
	sKeys, err = client.Keys(ctx, "s:*").Result()
	if err != nil {
		t.Fatal(err)
	}
	if len(sKeys) != 0 {
		t.Fatalf("expected 0 s:* keys in Redis after launch, got %v", sKeys)
	}

	// Assert modern task record is intact
	rec, err := client.HGetAll(ctx, "task:"+copyID).Result()
	if err != nil || len(rec) == 0 {
		t.Fatalf("task:%s record missing: %v", copyID, err)
	}
}

// TestLaunchCopyEnvRefusal verifies that when the wrapper returns a refusal,
// LaunchCopyEnv returns the refusal text as an error.
func TestLaunchCopyEnvRefusal(t *testing.T) {
	t.Parallel()

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cl := CopyLine{Copy: "refuse-copy~1", Token: "1." + strings.Repeat("d", 32)}
	env := []string{
		"NOVA_LAUNCH_TEST_REFUSE_LABEL=" + cl.Copy,
	}
	ack, err := LaunchCopyEnv(exe, cl, DefaultBudget, env)
	if err == nil {
		t.Fatalf("expected error on refusal, got ack=%q", ack)
	}
	if !strings.Contains(err.Error(), "REFUSED not dealt") {
		t.Fatalf("unexpected error: %v", err)
	}
}
