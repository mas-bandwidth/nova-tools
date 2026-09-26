//go:build functional

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
)

// These probes exercise coordinator-visible failures against throwaway Redis.
func TestStellaExplicitInvalidPolicyIsReported(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"missing", "unreadable"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			c, _ := sdStore(t)
			path := filepath.Join(t.TempDir(), "policy.yml")
			if mode == "unreadable" {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := land.LoadPolicy(path); err == nil {
				t.Fatal("fixture must be an invalid policy")
			}
			var out, errOut bytes.Buffer
			code := runLandEval(context.Background(), []string{"--redis", c.Options().Addr, "--sprint", sdSprint, "--repo", "nova-tools", "--policy", path, "--mirror", "none"}, &out, &errOut)
			if code == 0 || !strings.Contains(errOut.String(), path) {
				t.Fatalf("explicit %s policy silently ignored: exit=%d stdout=%q stderr=%q", mode, code, out.String(), errOut.String())
			}
		})
	}
}

func TestStellaBenchLaunchFailureNamesCopyAndCause(t *testing.T) {
	t.Parallel()
	c, st := sdStore(t)
	ctx := context.Background()
	bench, err := taskcard.ParseConsumer("bench:b")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SAdd(ctx, "benches", "b").Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.HSet(ctx, bench.DesiredKey(), "slots", "1").Err(); err != nil {
		t.Fatal(err)
	}
	if err := taskcard.Enroll(ctx, c, bench, true); err != nil {
		t.Fatal(err)
	}
	if err := sdCard(t, c, "launch-audit", "audit", ""); err != nil {
		t.Fatal(err)
	}
	dealt, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: bench, N: 1, By: "audit"})
	if err != nil || len(dealt) != 1 {
		t.Fatalf("deal %v %v", dealt, err)
	}
	wrapper := filepath.Join(t.TempDir(), "missing-nova-card")
	var out, errOut bytes.Buffer
	benchCopySession(st, "b", true, wrapper, nil, &out, &errOut)(ctx)
	combined := out.String() + errOut.String()
	if !strings.Contains(combined, "launch-audit.c1") || !strings.Contains(combined, wrapper) {
		t.Fatalf("launch failure hides copy or cause: stdout=%q stderr=%q", out.String(), errOut.String())
	}
}

func TestStellaHoldLookupPreservesStoreError(t *testing.T) {
	t.Parallel()
	c, _ := sdStore(t)
	ctx := context.Background()
	key := "s:" + sdSprint + ":prunit:nova-tools:7"
	if err := c.HSet(ctx, key, "wrong", "type").Err(); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := runHoldShow(ctx, []string{"--redis", c.Options().Addr, "--sprint", sdSprint, "nova-tools#7"}, &out, &errOut)
	if code == 0 || !strings.Contains(errOut.String(), "WRONGTYPE") {
		t.Fatalf("store error rewritten as missing unit: exit=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
	}
}
