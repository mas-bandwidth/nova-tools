//go:build functional

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// TestCardRunEndToEnd runs the verb against a throwaway redis-server with a
// card and its body, and `true` as the runner: the START line names the
// picked route and key, native rc=0 leaves no RESULT.md, so the verb exits
// 1 with the FAILED line, the way the bash harness did.
func TestCardRunEndToEnd(t *testing.T) {
	runner, err := exec.LookPath("true")
	if err != nil {
		t.Skip("no true on PATH")
	}
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	defer client.Close()
	ctx := context.Background()
	body := []byte("RESULT: e2e sha=0\nBASE: main\n\nbody")
	sum := sha256.Sum256(body)
	sha := hex.EncodeToString(sum[:])
	if err := client.HSet(ctx, card.CardKey("e2e", "e2e"), "payload_sha", sha, "route", "pro").Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, card.BodyKey("e2e", sha), body, 0).Err(); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	env := cardRunEnv(map[string]string{
		"HOME": filepath.Join(root, "home"), "NOVA_CARD_REDIS": addr, card.RunnerEnv: runner,
		"NOVA_CARD_OUT": filepath.Join(root, "job", "out"), "NOVA_CARD_JOB": filepath.Join(root, "job"),
	})
	t.Setenv("OPENROUTER_API_KEY", "or-val")
	t.Setenv("OPENCODE_API_KEY", "oc-val")
	t.Setenv("DEEPSEEK_API_KEY", "ds-val")
	t.Setenv("INCEPTION_API_KEY", "in-val")
	var out, errOut bytes.Buffer
	code := runCardRunEnv(ctx, []string{"--sprint", "e2e", "--ids", "e2e", "--attempt", "1"}, &out, &errOut, env)
	if code != card.RunExitNoResult || !strings.HasPrefix(out.String(), "CARD RUN e2e/e2e/1 code=1 tier=pro route=") || !strings.Contains(out.String(), " rc=0 wall_s=") {
		t.Fatalf("code %d stdout %q stderr %q", code, out.String(), errOut.String())
	}
	log, err := os.ReadFile(filepath.Join(root, "job", "out", "harness.log"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"START e2e/e2e/1 bench=bench-a tier=pro route=", " key=", " sha=" + sha[:12], "END native rc=0 wall_s=", "FAILED native rc=0 but no RESULT.md"} {
		if !strings.Contains(string(log), want) {
			t.Fatalf("harness.log lacks %q:\n%s", want, log)
		}
	}
	for _, v := range []string{"or-val", "oc-val", "ds-val", "in-val"} {
		if strings.Contains(string(log), v) || strings.Contains(out.String(), v) {
			t.Fatalf("a key value reached the output: %s", v)
		}
	}
	if data, err := os.ReadFile(filepath.Join(root, "job", "card.md")); err != nil || !bytes.Equal(data, body) {
		t.Fatalf("card.md %v %q", err, data)
	}
}
