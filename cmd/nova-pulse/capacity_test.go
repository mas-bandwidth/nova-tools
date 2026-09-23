package main

import (
	"bytes"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"
)

// CARD-9316: `nova-pulse capacity` is the allowed-cards formula every card launcher on the
// Studio reads over ssh today -- fill-loop.sh's cap() and flash-native-bench.sh's capacity
// check -- as one local verb: allowed = min(cores*1.5 - load1 - cores/8, (free_gb-25)/2,
// memfree_gb/2), floored at 0, printed as one CAPACITY line. The inputs come from flags so a
// test never reads /proc or opens an ssh.
func TestCapacityAllowedFormula(t *testing.T) {
	cases := []struct {
		name    string
		cores   int
		load1   int
		freeGB  int
		memGB   int
		allowed int
	}{
		{"studio", 8, 2, 200, 100, 9},        // a1 = 12-2-1 = 9, a2 = 87, a3 = 50
		{"roomy", 16, 0, 100, 64, 22},        // a1 = 24-0-2 = 22, a2 = 37, a3 = 32
		{"squeezed", 4, 5, 200, 100, 1},      // a1 = 6-5-0 = 1, a2 = 87, a3 = 50
		{"diskbound", 16, 0, 30, 100, 2},     // a1 = 22, a2 = 2, a3 = 50
		{"membound", 16, 0, 200, 8, 4},       // a1 = 22, a2 = 87, a3 = 4
		{"odd-cores", 5, 0, 200, 100, 7},     // a1 = 15/2-0-0 = 7 (cap()'s integer math)
		{"red-negative", 8, 20, 200, 100, 0}, // a1 = 12-20-1 = -9, floored at 0
		{"disk-negative", 8, 0, 24, 100, 0},  // a2 = (24-25)/2 = 0, floored
		{"mem-negative", 8, 0, 200, 1, 0},    // a3 = 0, floored
		{"all-negative", 2, 9, 10, 1, 0},     // every arm is negative, floors at 0
		{"zero-cores", 0, 0, 200, 100, 0},    // a1 = 0-0-0 = 0
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			args := []string{
				"capacity", "--bench", c.name,
				"--cores", fmt.Sprint(c.cores),
				"--load1", fmt.Sprint(c.load1),
				"--free-gb", fmt.Sprint(c.freeGB),
				"--memfree-gb", fmt.Sprint(c.memGB),
			}
			var out, errb bytes.Buffer
			if code := run(args, &out, &errb, time.Now().UTC()); code != 0 {
				t.Fatalf("capacity exit = %d, want 0; stderr=%q", code, errb.String())
			}
			want := fmt.Sprintf("CAPACITY bench=%s cores=%d load=%d free=%dG memfree=%dG allowed=%d\n",
				c.name, c.cores, c.load1, c.freeGB, c.memGB, c.allowed)
			if got := out.String(); got != want {
				t.Fatalf("capacity line = %q, want %q", got, want)
			}
		})
	}
}

// A bare `nova-pulse capacity` reads this host's own cores, load, free disk and free
// memory, so it prints a CAPACITY line even with no bench and no numeric flags. The
// value is whatever this bench is; only the shape is the contract.
//
// The local read is /proc and `df -BG`, which is a LINUX fact. On a bench without
// them the verb REFUSES AND NAMES THE FLAG to pass rather than inventing a number:
// a capacity line built out of guesses is the one line a launcher must be able to
// trust. Both arms are held here, because the fleet is linux, darwin and windows and
// a test that only ran on linux is how this reached CI red.
func TestCapacityReadsItsOwnHost(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"capacity"}, &out, &errb, time.Now().UTC())
	if runtime.GOOS != "linux" {
		if code != 2 {
			t.Fatalf("capacity exit = %d on %s, want 2: the host read is linux-only", code, runtime.GOOS)
		}
		// The two /proc reads are the ones that cannot answer off linux whatever else
		// is on the bench; --free-gb is not asserted because `df` is a program and a
		// windows runner with git-bash on PATH has a GNU df that takes -BG fine.
		for _, want := range []string{"--load1", "--memfree-gb"} {
			if !strings.Contains(errb.String(), want) {
				t.Errorf("the refusal does not name %s, so a caller cannot fix it:\n%s", want, errb.String())
			}
		}
		if out.Len() != 0 {
			t.Errorf("a refused run printed a CAPACITY line: %q", out.String())
		}
		return
	}
	if code != 0 {
		t.Fatalf("capacity exit = %d, want 0; stderr=%q", code, errb.String())
	}
	line := strings.TrimSpace(out.String())
	if !strings.HasPrefix(line, "CAPACITY bench=") {
		t.Fatalf("capacity line = %q, want it to start CAPACITY bench=", line)
	}
	if !strings.Contains(line, "cores=") || !strings.Contains(line, "load=") ||
		!strings.Contains(line, "free=") || !strings.Contains(line, "memfree=") ||
		!strings.Contains(line, "allowed=") {
		t.Fatalf("capacity line = %q, want every field named", line)
	}
}

// help lists capacity now that it runs, the way issue #515 demands of a shipped verb.
func TestHelpListsCapacity(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"help"}, &out, &errb, time.Now().UTC()); code != 0 {
		t.Fatalf("help exit = %d; stderr=%q", code, errb.String())
	}
	found := false
	for _, line := range strings.Split(out.String(), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "nova-pulse" && fields[1] == "capacity" {
			found = true
			if strings.Contains(line, "not yet implemented") {
				t.Fatalf("help marks capacity not yet implemented: %q", line)
			}
		}
	}
	if !found {
		t.Fatalf("help does not list capacity:\n%s", out.String())
	}
}
