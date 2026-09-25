package fleet_test

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fleet"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

func setBeat(t *testing.T, ctx context.Context, c *redis.Client, bench, build string) {
	t.Helper()
	err := c.HSet(ctx, "bench:"+bench+":beat",
		"host", bench+".local",
		"user", "nova",
		"load1", "0.5",
		"ssh", "ok",
		"probe", "ok",
		"launcher", "local",
		"live", "0",
		"why", "testing",
		"build", build,
		"at", "1000",
	).Err()
	if err != nil {
		t.Fatalf("set beat: %v", err)
	}
}

// TestFleetThresholdsFromConfig runs every row of testdata/thresholds.tsv.
func TestFleetThresholdsFromConfig(t *testing.T) {
	t.Setenv(testutil.CIEnv, "1")
	file, err := os.Open(filepath.Join("testdata", "thresholds.tsv"))
	if err != nil {
		t.Fatalf("open thresholds.tsv: %v", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 4 {
			t.Fatalf("thresholds.tsv line %d: expected 4 tab-separated columns, got %d", lineNum, len(parts))
		}
		downAfter, err := strconv.Atoi(parts[0])
		if err != nil {
			t.Fatalf("thresholds.tsv line %d: bad down_after: %v", lineNum, err)
		}
		upAfter, err := strconv.Atoi(parts[1])
		if err != nil {
			t.Fatalf("thresholds.tsv line %d: bad up_after: %v", lineNum, err)
		}
		script := strings.Fields(parts[2])
		expected := strings.Fields(parts[3])
		if len(script) != len(expected) {
			t.Fatalf("thresholds.tsv line %d: script has %d steps, expected has %d", lineNum, len(script), len(expected))
		}

		t.Run(fmt.Sprintf("down%d_up%d_line%d", downAfter, upAfter, lineNum), func(t *testing.T) {
			addr := testutil.Start(t)
			c := redis.NewClient(&redis.Options{Addr: addr})
			t.Cleanup(func() { _ = c.Close() })
			ctx := context.Background()
			if err := fn.Load(ctx, c); err != nil {
				t.Fatalf("load fn: %v", err)
			}
			const bench = "bench-1"
			if err := c.SAdd(ctx, "benches", bench).Err(); err != nil {
				t.Fatalf("sadd benches: %v", err)
			}
			if err := fleet.SetConfig(ctx, c, downAfter, upAfter); err != nil {
				t.Fatalf("set config: %v", err)
			}

			for i, op := range script {
				switch op {
				case "B":
					setBeat(t, ctx, c, bench, "v1")
					if err := fleet.Step(ctx, c, bench); err != nil {
						t.Fatalf("step %d (B): %v", i, err)
					}
				case "_":
					if err := c.Del(ctx, "bench:"+bench+":beat").Err(); err != nil {
						t.Fatalf("del beat %d (_): %v", i, err)
					}
					if err := fleet.Step(ctx, c, bench); err != nil {
						t.Fatalf("step %d (_): %v", i, err)
					}
				case "H":
					if err := fleet.Hold(ctx, c, bench, "hold-test", "tester"); err != nil {
						t.Fatalf("hold %d: %v", i, err)
					}
				case "R":
					if err := fleet.Release(ctx, c, bench); err != nil {
						t.Fatalf("release %d: %v", i, err)
					}
				default:
					t.Fatalf("unknown script op %q", op)
				}

				rows, err := fleet.Read(ctx, c, bench)
				if err != nil {
					t.Fatalf("read after op %d (%s): %v", i, op, err)
				}
				if len(rows) != 1 {
					t.Fatalf("expected 1 row, got %d", len(rows))
				}
				got := rows[0].State
				want := expected[i]
				if got != want {
					t.Fatalf("step %d (%s): got state %q, want %q (down_after=%d, up_after=%d)",
						i, op, got, want, downAfter, upAfter)
				}
			}
		})
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan thresholds.tsv: %v", err)
	}
}

// TestFleetUpAfterOneFromDown is the control for DOWN recovery.
func TestFleetUpAfterOneFromDown(t *testing.T) {
	t.Setenv(testutil.CIEnv, "1")
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatalf("load fn: %v", err)
	}
	const bench = "bench-dn"
	if err := c.SAdd(ctx, "benches", bench).Err(); err != nil {
		t.Fatalf("sadd benches: %v", err)
	}
	if err := fleet.SetConfig(ctx, c, 1, 1); err != nil {
		t.Fatalf("set config: %v", err)
	}

	script := []string{"_", "B", "_", "B"}
	wantStates := []string{"DOWN", "UP", "DOWN", "UP"}
	for i, op := range script {
		switch op {
		case "_":
			_ = c.Del(ctx, "bench:"+bench+":beat").Err()
			if err := fleet.Step(ctx, c, bench); err != nil {
				t.Fatalf("step %d: %v", i, err)
			}
		case "B":
			setBeat(t, ctx, c, bench, "v1")
			if err := fleet.Step(ctx, c, bench); err != nil {
				t.Fatalf("step %d: %v", i, err)
			}
		}
		rows, err := fleet.Read(ctx, c, bench)
		if err != nil || len(rows) != 1 {
			t.Fatalf("read %d: %v", i, err)
		}
		if rows[0].State != wantStates[i] {
			t.Fatalf("step %d (%s) got state %q, want %q", i, op, rows[0].State, wantStates[i])
		}
	}

	// cap:log must show DOWN->UP with no PROBING in between.
	entries, err := c.XRange(ctx, "cap:log", "-", "+").Result()
	if err != nil {
		t.Fatalf("xrange cap:log: %v", err)
	}
	var reasons []string
	for _, e := range entries {
		k, _ := e.Values["kind"].(string)
		s, _ := e.Values["subject"].(string)
		r, _ := e.Values["reason"].(string)
		if k == "fleet-state" && s == bench {
			reasons = append(reasons, r)
			if strings.Contains(r, "PROBING") {
				t.Fatalf("cap:log reason %q contains PROBING; want no PROBING in between", r)
			}
		}
	}
	if len(reasons) == 0 {
		t.Fatal("cap:log has no fleet-state entries for bench")
	}
	foundDownUp := false
	for _, r := range reasons {
		if strings.HasPrefix(r, "DOWN->UP") {
			foundDownUp = true
			break
		}
	}
	if !foundDownUp {
		t.Fatalf("cap:log did not contain DOWN->UP: %v", reasons)
	}
}

// TestFleetUpAfterOneFromHeld is the control for HELD recovery.
func TestFleetUpAfterOneFromHeld(t *testing.T) {
	t.Setenv(testutil.CIEnv, "1")
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatalf("load fn: %v", err)
	}
	const bench = "bench-held"
	if err := c.SAdd(ctx, "benches", bench).Err(); err != nil {
		t.Fatalf("sadd benches: %v", err)
	}
	if err := fleet.SetConfig(ctx, c, 3, 1); err != nil {
		t.Fatalf("set config: %v", err)
	}

	// script=B B H B R B -> DOWN UP HELD HELD PROBING UP
	script := []string{"B", "B", "H", "B", "R", "B"}
	wantStates := []string{"DOWN", "UP", "HELD", "HELD", "PROBING", "UP"}

	for i, op := range script {
		switch op {
		case "B":
			setBeat(t, ctx, c, bench, "v1")
			if err := fleet.Step(ctx, c, bench); err != nil {
				t.Fatalf("step %d: %v", i, err)
			}
		case "H":
			if err := fleet.Hold(ctx, c, bench, "held-test", "tester"); err != nil {
				t.Fatalf("hold %d: %v", i, err)
			}
		case "R":
			if err := fleet.Release(ctx, c, bench); err != nil {
				t.Fatalf("release %d: %v", i, err)
			}
			// The state is PROBING straight after R returns, even though the beat is present.
			rows, err := fleet.Read(ctx, c, bench)
			if err != nil || len(rows) != 1 {
				t.Fatalf("read after R: %v", err)
			}
			if rows[0].State != "PROBING" {
				t.Fatalf("immediately after release got state %q, want PROBING", rows[0].State)
			}
			if c.Exists(ctx, "bench:"+bench+":beat").Val() != 1 {
				t.Fatalf("beat should still be present after release")
			}
		}

		rows, err := fleet.Read(ctx, c, bench)
		if err != nil || len(rows) != 1 {
			t.Fatalf("read %d: %v", i, err)
		}
		if rows[0].State != wantStates[i] {
			t.Fatalf("step %d (%s) got state %q, want %q", i, op, rows[0].State, wantStates[i])
		}
	}
}

// TestFleetReleaseNeverUp: at up_after 1, 2 and 10, a release with the beat present leaves PROBING,
// and cap:log holds no ->UP entry written by the release call.
func TestFleetReleaseNeverUp(t *testing.T) {
	t.Setenv(testutil.CIEnv, "1")
	for _, upAfter := range []int{1, 2, 10} {
		t.Run(fmt.Sprintf("up_after_%d", upAfter), func(t *testing.T) {
			addr := testutil.Start(t)
			c := redis.NewClient(&redis.Options{Addr: addr})
			t.Cleanup(func() { _ = c.Close() })
			ctx := context.Background()
			if err := fn.Load(ctx, c); err != nil {
				t.Fatalf("load fn: %v", err)
			}
			const bench = "bench-rel"
			if err := c.SAdd(ctx, "benches", bench).Err(); err != nil {
				t.Fatalf("sadd benches: %v", err)
			}
			if err := fleet.SetConfig(ctx, c, 3, upAfter); err != nil {
				t.Fatalf("set config: %v", err)
			}

			setBeat(t, ctx, c, bench, "v1")
			if err := fleet.Hold(ctx, c, bench, "testing", "tester"); err != nil {
				t.Fatalf("hold: %v", err)
			}

			// Ensure beat is present
			setBeat(t, ctx, c, bench, "v1")

			lastID := "$"
			lastEntries, err := c.XRevRangeN(ctx, "cap:log", "+", "-", 1).Result()
			if err == nil && len(lastEntries) > 0 {
				lastID = lastEntries[0].ID
			}

			if err := fleet.Release(ctx, c, bench); err != nil {
				t.Fatalf("release: %v", err)
			}

			rows, err := fleet.Read(ctx, c, bench)
			if err != nil || len(rows) != 1 {
				t.Fatalf("read: %v", err)
			}
			if rows[0].State != "PROBING" {
				t.Fatalf("after release state is %q, want PROBING", rows[0].State)
			}

			// cap:log holds no ->UP entry written by the release call.
			entries, err := c.XRange(ctx, "cap:log", "("+lastID, "+").Result()
			if err != nil {
				t.Fatalf("xrange: %v", err)
			}
			for _, e := range entries {
				r, _ := e.Values["reason"].(string)
				if strings.Contains(r, "->UP") {
					t.Fatalf("release call wrote ->UP entry in cap:log: %q", r)
				}
			}
		})
	}
}

// TestFleetStateCarriesBuild covers the adopt contract: build follows the beat, and DOWN keeps the last build seen.
func TestFleetStateCarriesBuild(t *testing.T) {
	t.Setenv(testutil.CIEnv, "1")
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatalf("load fn: %v", err)
	}
	const bench = "bench-bld"
	if err := c.SAdd(ctx, "benches", bench).Err(); err != nil {
		t.Fatalf("sadd benches: %v", err)
	}
	if err := fleet.SetConfig(ctx, c, 2, 1); err != nil {
		t.Fatalf("set config: %v", err)
	}

	setBeat(t, ctx, c, bench, "build-1")
	_ = fleet.Step(ctx, c, bench) // first sight -> DOWN
	setBeat(t, ctx, c, bench, "build-1")
	_ = fleet.Step(ctx, c, bench) // OK -> UP

	rows, err := fleet.Read(ctx, c, bench)
	if err != nil || len(rows) != 1 {
		t.Fatalf("read: %v", err)
	}
	if rows[0].Build != "build-1" {
		t.Fatalf("got build %q, want build-1", rows[0].Build)
	}

	// Update beat with new build
	setBeat(t, ctx, c, bench, "build-2")
	_ = fleet.Step(ctx, c, bench)
	rows, err = fleet.Read(ctx, c, bench)
	if err != nil || len(rows) != 1 {
		t.Fatalf("read: %v", err)
	}
	if rows[0].Build != "build-2" {
		t.Fatalf("got build %q, want build-2", rows[0].Build)
	}

	// Misses until DOWN
	_ = c.Del(ctx, "bench:"+bench+":beat").Err()
	_ = fleet.Step(ctx, c, bench) // miss 1
	_ = fleet.Step(ctx, c, bench) // miss 2 -> DOWN

	rows, err = fleet.Read(ctx, c, bench)
	if err != nil || len(rows) != 1 {
		t.Fatalf("read: %v", err)
	}
	if rows[0].State != "DOWN" {
		t.Fatalf("expected state DOWN, got %q", rows[0].State)
	}
	if rows[0].Build != "build-2" {
		t.Fatalf("DOWN row lost last seen build; got %q, want build-2", rows[0].Build)
	}
}

// TestFleetStateOneRoundTrip reads 64 benches in one call.
func TestFleetStateOneRoundTrip(t *testing.T) {
	t.Setenv(testutil.CIEnv, "1")
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatalf("load fn: %v", err)
	}

	for i := 1; i <= 64; i++ {
		name := fmt.Sprintf("bench-%02d", i)
		c.SAdd(ctx, "benches", name)
	}

	rows, err := fleet.Read(ctx, c, "")
	if err != nil {
		t.Fatalf("read 64 benches: %v", err)
	}
	if len(rows) != 64 {
		t.Fatalf("read %d benches, want 64", len(rows))
	}
	for i, r := range rows {
		wantName := fmt.Sprintf("bench-%02d", i+1)
		if r.Bench != wantName {
			t.Fatalf("row %d bench is %q, want %q", i, r.Bench, wantName)
		}
		if r.State != "DOWN" {
			t.Fatalf("row %d state is %q, want DOWN (absent)", i, r.State)
		}
	}
}

// TestFleetConfigDefaults verifies that when cfg:fleet is absent, down_after=30 and up_after=10 apply.
func TestFleetConfigDefaults(t *testing.T) {
	t.Setenv(testutil.CIEnv, "1")
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatalf("load fn: %v", err)
	}

	down, up, err := fleet.Config(ctx, c)
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if down != 30 || up != 10 {
		t.Fatalf("default config: down_after=%d, up_after=%d; want 30, 10", down, up)
	}
}

// TestFleetConfigRefusesZero verifies that N < 1 is refused.
func TestFleetConfigRefusesZero(t *testing.T) {
	t.Setenv(testutil.CIEnv, "1")
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatalf("load fn: %v", err)
	}

	if err := fleet.SetConfig(ctx, c, 0, 10); err == nil {
		t.Fatal("expected error for down_after=0")
	}
	if err := fleet.SetConfig(ctx, c, 10, 0); err == nil {
		t.Fatal("expected error for up_after=0")
	}
	if err := fleet.SetConfig(ctx, c, -1, 10); err == nil {
		t.Fatal("expected error for down_after=-1")
	}
	if err := fleet.SetConfig(ctx, c, 10, -2); err == nil {
		t.Fatal("expected error for up_after=-2")
	}
}

// TestFleetOneWriter is a grep class test: bench:*:state and bench:*:hold are written only in fleet.lua,
// and cfg:fleet only by ns_fleet_config.
func TestFleetOneWriter(t *testing.T) {
	source, err := fn.Source()
	if err != nil {
		t.Fatalf("fn.Source(): %v", err)
	}
	sections := strings.Split(source, "\n-- lua/")
	writes := regexp.MustCompile(`redis\.call\('(HSET|HSETNX|HDEL|DEL|UNLINK|HINCRBY|HINCRBYFLOAT|EXPIRE|PEXPIRE|SET|RENAME|HMSET)',\s*([^,)]+)`)

	stateWrites := 0
	holdWrites := 0
	cfgWrites := 0

	binds := regexp.MustCompile(`local\s+(\w+)\s*=\s*'bench:'\s*\.\.\s*\w+\s*\.\.\s*':state'`)

	for _, sec := range sections[1:] {
		name := sec[:strings.Index(sec, "\n")]
		isFleetLua := name == "fleet.lua"

		inFleetConfig := false
		stateVars := map[string]bool{}
		for i, line := range strings.Split(sec, "\n") {
			if strings.HasPrefix(line, "local function ") || strings.HasPrefix(line, "function ") {
				inFleetConfig = strings.HasPrefix(line, "local function fleet_config")
				stateVars = map[string]bool{}
			}
			if m := binds.FindStringSubmatch(line); m != nil {
				stateVars[m[1]] = true
			}

			m := writes.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			target := strings.TrimSpace(m[2])

			if (strings.Contains(target, "'bench:'") && strings.Contains(target, "':state'")) || stateVars[target] {
				stateWrites++
				if !isFleetLua {
					t.Errorf("lua/%s line %d writes bench:*:state (%s) outside fleet.lua", name, i+1, strings.TrimSpace(line))
				}
			}

			if strings.Contains(target, "':hold'") {
				holdWrites++
				if !isFleetLua {
					t.Errorf("lua/%s line %d writes bench:*:hold (%s) outside fleet.lua", name, i+1, strings.TrimSpace(line))
				}
			}

			if strings.Contains(target, "'cfg:fleet'") {
				cfgWrites++
				if !isFleetLua || !inFleetConfig {
					t.Errorf("lua/%s line %d writes cfg:fleet (%s) outside fleet_config", name, i+1, strings.TrimSpace(line))
				}
			}
		}
	}

	if stateWrites == 0 {
		t.Fatal("found no write to bench:*:state; check regex")
	}
	if holdWrites == 0 {
		t.Fatal("found no write to bench:*:hold; check regex")
	}
	if cfgWrites == 0 {
		t.Fatal("found no write to cfg:fleet; check regex")
	}
}

// TestFleetStepHoldsBenchAfterSSHTimeouts is nova-tools #3322 on the fleet
// duty: the deal pass counts a bench's consecutive ssh timeouts on its own
// cell (bench:<b>:ssh timeouts); the step holds an UP bench (PROBING, the
// reason on it, one fleet-state receipt) once the count since the last hold
// reaches cfg:fleet ssh_fail_after (3 by default, set through ns_fleet_config
// and read back by SessionFailAfter), remembers the count in ssh_held so the same
// count never holds twice, lets up_after beats earn UP again, and holds again
// only after ssh_fail_after fresh timeouts; a count cleared by an ok session
// starts over.
func TestFleetStepHoldsBenchAfterSSHTimeouts(t *testing.T) {
	t.Setenv(testutil.CIEnv, "1")
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatalf("load fn: %v", err)
	}
	const bench = "bench-ssh"
	if err := c.SAdd(ctx, "benches", bench).Err(); err != nil {
		t.Fatal(err)
	}
	if n, err := fleet.SessionFailAfter(ctx, c); err != nil || n != 3 {
		t.Fatalf("default ssh_fail_after = %d (%v), want 3", n, err)
	}
	if err := fleet.SetConfig(ctx, c, 30, 2); err != nil {
		t.Fatal(err)
	}
	if n, err := fleet.SessionFailAfter(ctx, c); err != nil || n != 3 {
		t.Fatalf("ssh_fail_after after SetConfig = %d (%v), want still 3", n, err)
	}
	if err := fleet.SetSessionFailAfter(ctx, c, 0); err == nil {
		t.Fatal("SetSessionFailAfter(0) was not refused")
	}
	// The bench comes up: DOWN, then two beats (up_after 2) earn UP.
	setBeat(t, ctx, c, bench, "v1")
	for i := 0; i < 3; i++ {
		if err := fleet.Step(ctx, c, bench); err != nil {
			t.Fatal(err)
		}
	}
	state := func() (string, string, string) {
		t.Helper()
		v, err := c.HMGet(ctx, "bench:"+bench+":state", "state", "reason", "ssh_held").Result()
		if err != nil {
			t.Fatal(err)
		}
		get := func(i int) string { s, _ := v[i].(string); return s }
		return get(0), get(1), get(2)
	}
	if st, _, _ := state(); st != "UP" {
		t.Fatalf("state %q, want UP before any timeout", st)
	}
	// The deal pass's cell: two timeouts hold nothing, three do.
	cell := func(timeouts int) {
		t.Helper()
		if err := c.HSet(ctx, "bench:"+bench+":ssh", "state", "timeout", "why", "ssh killed at the deadline", "at", "2000", "timeouts", strconv.Itoa(timeouts)).Err(); err != nil {
			t.Fatal(err)
		}
	}
	cell(2)
	if err := fleet.Step(ctx, c, bench); err != nil {
		t.Fatal(err)
	}
	if st, _, _ := state(); st != "UP" {
		t.Fatalf("state %q after 2 timeouts, want UP", st)
	}
	cell(3)
	if err := fleet.Step(ctx, c, bench); err != nil {
		t.Fatal(err)
	}
	st, reason, held := state()
	if st != "PROBING" || !strings.HasPrefix(reason, "ssh timeout 3 of 3: ssh killed") || held != "3" {
		t.Fatalf("after 3 timeouts: state %q reason %q ssh_held %q, want PROBING, the count and why, 3", st, reason, held)
	}
	flips := func() []string {
		t.Helper()
		entries, err := c.XRange(ctx, "cap:log", "-", "+").Result()
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, e := range entries {
			k, _ := e.Values["kind"].(string)
			r, _ := e.Values["reason"].(string)
			if k == "fleet-state" && strings.Contains(r, "PROBING ssh") {
				out = append(out, r)
			}
		}
		return out
	}
	if got := flips(); len(got) != 1 || !strings.HasPrefix(got[0], "UP->PROBING ssh timeout 3 of 3") {
		t.Fatalf("fleet-state receipts = %v, want one UP->PROBING with the count", got)
	}
	// Beats earn UP again (up_after 2); the same count does not hold twice.
	for i := 0; i < 3; i++ {
		if err := fleet.Step(ctx, c, bench); err != nil {
			t.Fatal(err)
		}
	}
	if st, _, held := state(); st != "UP" || held != "3" {
		t.Fatalf("after the probe: state %q ssh_held %q, want UP with the hold remembered at 3", st, held)
	}
	// Two fresh timeouts (5) do not hold; the third fresh one (6) does.
	cell(5)
	if err := fleet.Step(ctx, c, bench); err != nil {
		t.Fatal(err)
	}
	if st, _, _ := state(); st != "UP" {
		t.Fatalf("state %q at 5 timeouts (2 since the hold), want UP", st)
	}
	cell(6)
	if err := fleet.Step(ctx, c, bench); err != nil {
		t.Fatal(err)
	}
	if st, _, held := state(); st != "PROBING" || held != "6" {
		t.Fatalf("state %q ssh_held %q at 6 timeouts, want PROBING held at 6", st, held)
	}
	if got := flips(); len(got) != 2 {
		t.Fatalf("fleet-state receipts = %v, want two", got)
	}
	// An ok session cleared the count (0 < ssh_held): the next hold needs
	// ssh_fail_after from zero; with ssh_fail_after 1 one timeout holds.
	for i := 0; i < 3; i++ {
		if err := fleet.Step(ctx, c, bench); err != nil {
			t.Fatal(err)
		}
	}
	if err := fleet.SetSessionFailAfter(ctx, c, 1); err != nil {
		t.Fatal(err)
	}
	if n, err := fleet.SessionFailAfter(ctx, c); err != nil || n != 1 {
		t.Fatalf("ssh_fail_after = %d (%v), want 1", n, err)
	}
	cell(0)
	if err := fleet.Step(ctx, c, bench); err != nil {
		t.Fatal(err)
	}
	if st, _, held := state(); st != "UP" || held != "0" {
		t.Fatalf("state %q ssh_held %q after the count cleared, want UP and 0", st, held)
	}
	cell(1)
	if err := fleet.Step(ctx, c, bench); err != nil {
		t.Fatal(err)
	}
	if st, _, held := state(); st != "PROBING" || held != "1" {
		t.Fatalf("state %q ssh_held %q at 1 timeout with ssh_fail_after 1, want PROBING held at 1", st, held)
	}
}
