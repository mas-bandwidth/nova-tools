package fleet_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fleet"
)

func TestUnitRetireScript(t *testing.T) {
	t.Parallel()

	script := fleet.UnitRetireScript("nova-loop-test.service")
	if !strings.Contains(script, "Darwin") || !strings.Contains(script, "launchctl bootout") {
		t.Errorf("script missing Darwin launchctl handling: %s", script)
	}
	if !strings.Contains(script, "systemctl --user stop") || !strings.Contains(script, "systemctl --user disable") {
		t.Errorf("script missing systemctl stop/disable: %s", script)
	}
	if !strings.Contains(script, "nova-loop-test.service") {
		t.Errorf("script missing service name: %s", script)
	}
}

func TestRetireArgv(t *testing.T) {
	t.Parallel()

	argv := fleet.RetireArgv("alpha", "test-unit")
	if len(argv) < 8 || argv[0] != "ansible" || argv[1] != "alpha" {
		t.Errorf("RetireArgv unexpected: %v", argv)
	}
}

func TestRetireSuccess(t *testing.T) {
	t.Parallel()

	mr := miniredis.RunT(t)
	c := testRedisClient(t, mr.Addr())

	runner := &fakeExecRunner{
		out: map[string]string{
			"ansible": "alpha | CHANGED => { \"changed\": true }\n",
		},
	}

	var out bytes.Buffer
	r := &fleet.Retire{
		Runner:  runner,
		Client:  c,
		Bench:   "alpha",
		Unit:    "unit1",
		Benches: []string{"alpha"},
		Out:     &out,
	}

	ctx := context.Background()
	res, err := r.Run(ctx)
	if err != nil {
		t.Fatalf("Retire.Run failed: %v", err)
	}
	if !res.OK() {
		t.Errorf("RetireResult.OK: got false, want true")
	}
	if res.Status != "OK" {
		t.Errorf("RetireResult.Status: got %s, want OK", res.Status)
	}

	// Output line check
	line := out.String()
	if !strings.HasPrefix(line, "FLEET RETIRE bench=alpha unit=unit1 status=OK ms=") {
		t.Errorf("unexpected output line: %s", line)
	}

	// Redis receipt check
	retireHash, err := c.HGetAll(ctx, fleet.RetireKey("alpha")).Result()
	if err != nil {
		t.Fatalf("HGetAll %s: %v", fleet.RetireKey("alpha"), err)
	}
	if retireHash["bench"] != "alpha" || retireHash["unit"] != "unit1" || retireHash["status"] != "OK" {
		t.Errorf("retire receipt hash mismatch: %v", retireHash)
	}

	// Retired units set check
	isMember, err := c.SIsMember(ctx, fleet.RetiredUnitsKey("alpha"), "unit1").Result()
	if err != nil || !isMember {
		t.Errorf("unit1 not in retired units set: err=%v isMember=%v", err, isMember)
	}
}

func TestRetireFailure(t *testing.T) {
	t.Parallel()

	mr := miniredis.RunT(t)
	c := testRedisClient(t, mr.Addr())

	runner := &fakeExecRunner{
		out: map[string]string{
			"ansible": "alpha | FAILED! => { \"msg\": \"unit not found\" }\n",
		},
		err: map[string]error{
			"ansible": errors.New("exit status 2"),
		},
	}

	var out bytes.Buffer
	r := &fleet.Retire{
		Runner:  runner,
		Client:  c,
		Bench:   "alpha",
		Unit:    "unit1",
		Benches: []string{"alpha"},
		Out:     &out,
	}

	ctx := context.Background()
	res, err := r.Run(ctx)
	if err != nil {
		t.Fatalf("Retire.Run error: %v", err)
	}
	if res.OK() {
		t.Errorf("RetireResult.OK: got true, want false")
	}
	if res.Status != "FAILED" {
		t.Errorf("RetireResult.Status: got %s, want FAILED", res.Status)
	}

	line := out.String()
	if !strings.Contains(line, "FLEET RETIRE bench=alpha unit=unit1 status=FAILED") {
		t.Errorf("unexpected output line: %s", line)
	}

	// Verify not added to retired_units
	isMember, _ := c.SIsMember(ctx, fleet.RetiredUnitsKey("alpha"), "unit1").Result()
	if isMember {
		t.Errorf("failed retirement should not be in retired_units set")
	}
}

func TestRetireDryRun(t *testing.T) {
	t.Parallel()

	mr := miniredis.RunT(t)
	c := testRedisClient(t, mr.Addr())

	runner := &fakeExecRunner{}
	var out bytes.Buffer
	r := &fleet.Retire{
		Runner: runner,
		Client: c,
		Bench:  "alpha",
		Unit:   "unit1",
		DryRun: true,
		Out:    &out,
	}

	ctx := context.Background()
	res, err := r.Run(ctx)
	if err != nil {
		t.Fatalf("Retire.Run dry-run: %v", err)
	}
	if !res.DryRun || !res.OK() {
		t.Errorf("RetireResult: %+v", res)
	}
	if !strings.Contains(res.Line(), "check=yes") {
		t.Errorf("line missing check=yes: %s", res.Line())
	}

	keys, _ := c.Keys(ctx, "bench:*").Result()
	if len(keys) != 0 {
		t.Errorf("dry-run wrote keys to redis: %v", keys)
	}
}

func TestRetireRefusals(t *testing.T) {
	t.Parallel()

	mr := miniredis.RunT(t)
	c := testRedisClient(t, mr.Addr())
	runner := &fakeExecRunner{}

	// Missing bench
	rNoBench := &fleet.Retire{Runner: runner, Client: c, Unit: "unit1"}
	if _, err := rNoBench.Run(context.Background()); !errors.Is(err, fleet.ErrRetireRefused) {
		t.Errorf("missing bench: got %v, want ErrRetireRefused", err)
	}

	// Missing unit
	rNoUnit := &fleet.Retire{Runner: runner, Client: c, Bench: "alpha"}
	if _, err := rNoUnit.Run(context.Background()); !errors.Is(err, fleet.ErrRetireRefused) {
		t.Errorf("missing unit: got %v, want ErrRetireRefused", err)
	}

	// Unknown bench in known benches
	rUnknown := &fleet.Retire{
		Runner:  runner,
		Client:  c,
		Bench:   "unknown-bench",
		Unit:    "unit1",
		Benches: []string{"alpha"},
	}
	if _, err := rUnknown.Run(context.Background()); !errors.Is(err, fleet.ErrRetireRefused) {
		t.Errorf("unknown bench: got %v, want ErrRetireRefused", err)
	}
}
