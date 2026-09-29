//go:build functional

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

func TestFleetRollOverRealRedis(t *testing.T) {
	t.Parallel()

	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const testSha = "418d464b1ba25ffe7a1de9dd8dec0305378b29be"
	const wantVersion = "v0.16.0-dev.418d464b"

	// Seed registered benches
	if err := client.SAdd(ctx, "benches", "b1", "b2").Err(); err != nil {
		t.Fatal(err)
	}

	// Seed b1 with target build line
	if err := client.HSet(ctx, "bench:b1:beat", "build", "nova-sprint "+wantVersion+" linux/amd64 go1.26.6").Err(); err != nil {
		t.Fatal(err)
	}
	// b2 has no beat yet

	var stdout, stderr bytes.Buffer
	code := runFleet(ctx, []string{"roll", "--to", testSha, "--redis", addr}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("code=%d; want 1 for b2 behind (stdout=%s, stderr=%s)", code, stdout.String(), stderr.String())
	}

	outStr := stdout.String()
	if !strings.Contains(outStr, "b1\t"+wantVersion+"\t"+wantVersion+"\tok\n") {
		t.Errorf("missing b1 ok row in stdout:\n%s", outStr)
	}
	if !strings.Contains(outStr, "b2\t"+wantVersion+"\tnone\tbehind\n") {
		t.Errorf("missing b2 behind row in stdout:\n%s", outStr)
	}
	if !strings.Contains(outStr, "FLEET ROLL BEHIND version="+wantVersion+" behind=b2\n") {
		t.Errorf("missing final behind line in stdout:\n%s", outStr)
	}

	// Verify Redis release keys were written
	v, err := client.HGet(ctx, "fleet:release", "version").Result()
	if err != nil || v != wantVersion {
		t.Errorf("fleet:release version = %q; want %q", v, wantVersion)
	}
	s, err := client.Get(ctx, "fleet:release:sha").Result()
	if err != nil || s != testSha {
		t.Errorf("fleet:release:sha = %q; want %q", s, testSha)
	}

	// Now update b2 beat with sha
	if err := client.HSet(ctx, "bench:b2:beat", "sha", testSha).Err(); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	code = runFleet(ctx, []string{"roll", "--to", testSha, "--redis", addr}, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}

	outStr2 := stdout.String()
	if !strings.Contains(outStr2, "b1\t"+wantVersion+"\t"+wantVersion+"\tok\n") {
		t.Errorf("missing b1 ok row in stdout:\n%s", outStr2)
	}
	if !strings.Contains(outStr2, "b2\t"+wantVersion+"\t"+testSha+"\tok\n") {
		t.Errorf("missing b2 ok row in stdout:\n%s", outStr2)
	}
	if !strings.Contains(outStr2, "FLEET ROLL OK version="+wantVersion+" benches=2\n") {
		t.Errorf("missing final ok line in stdout:\n%s", outStr2)
	}
}
