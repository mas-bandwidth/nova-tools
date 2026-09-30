// Command sprintsize is nova-sprint's size run: the sprint at 10x the
// largest real one (three streams of 30,000 in stops of 1,000, and two of
// 10,000) on a local store, every operation timed against a limit written
// down before the run. An operation over its limit fails the run as a wrong
// result does. It is the measuring instrument of the speed work: every change
// to how the sprint reads and writes is judged by its table.
//
// It runs the nova-sprint binary it is given, as a person does, against the
// store and prefix given; it refuses any prefix that does not start with
// dev-, and it tears the sprint down at the end unless --keep.
//
//	example:
//	  go build -o /tmp/nova-sprint ./cmd/nova-sprint
//	  go run ./tools/sprintsize --bin /tmp/nova-sprint --redis 127.0.0.1:6401 --prefix dev-
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// step is one operation of the run and its limit (0: none).
type step struct {
	what, size string
	limit      time.Duration
	args       []string
	okCodes    []int
}

// run is the size run: its steps in order.
func run() []step {
	s := []step{
		{"teardown", "-", 0, []string{"teardown", "--confirm", "PREFIX"}, []int{0, 1, 2}},
		{"init", "-", time.Second, []string{"init", "--readers", "reader-a,reader-b,reader-c,reader-d", "--members", "m1,m2,m3,m4,m5,m6,m7,m8"}, nil},
		{"add 3 streams --sentinel-every 1000", "3 x 30,000", 3 * time.Second, []string{"add", "--stream", "a,b,c", "--count", "30000", "--sentinel-every", "1000"}, nil},
		{"add --count", "10,000 onto 90,000", time.Second, []string{"add", "--stream", "d", "--count", "10000"}, nil},
		{"add --sentinel at the end", "10,000 ahead", time.Second, []string{"add", "--stream", "d", "--sentinel", "d-end"}, nil},
		{"add --count", "10,000 onto 100,000", time.Second, []string{"add", "--stream", "e", "--count", "10000"}, nil},
		{"add --sentinel --after the middle", "10,000 ready", time.Second, []string{"add", "--stream", "e", "--sentinel", "e-mid", "--after", "e-5000"}, nil},
	}
	for _, m := range []string{"m1", "m2", "m3", "m4", "m5", "m6", "m7", "m8"} {
		s = append(s, step{"fleet beat", m, 20 * time.Millisecond, []string{"fleet", "beat", m}, nil})
	}
	return append(s,
		step{"where", "110,000", 300 * time.Millisecond, []string{"where"}, nil},
		step{"inbox", "110,000", time.Second, []string{"inbox"}, nil},
		step{"start", "-", time.Second, []string{"start"}, nil},
		step{"tick (first: deals)", "110,000; 3,000 ready", time.Second, []string{"tick"}, nil},
		step{"tick (second)", "110,000", time.Second, []string{"tick"}, nil},
		step{"check", "110,000", time.Second, []string{"check"}, []int{0, 1}},
		step{"drop --stream --col waiting", "e: 5,001 waiting", time.Second, []string{"drop", "--stream", "e", "--col", "waiting", "--reason", "size run"}, nil},
		step{"stop", "-", time.Second, []string{"stop"}, nil},
	)
}

func main() {
	bin := flag.String("bin", "", "the nova-sprint binary to measure")
	redis := flag.String("redis", "127.0.0.1:6401", "the store, host:port (a local one: the limits are for a local store)")
	prefix := flag.String("prefix", "dev-", "the sprint's prefix; only one starting with dev- is accepted")
	keep := flag.Bool("keep", false, "leave the sprint on the store for a look")
	tsetOwned := flag.Bool("tset-owned-container", false, "opt in to the L1 size run on a disposable Redis container you own")
	tsetRedis := flag.String("tset-redis", "", "explicit direct Redis host:port for the L1 size run; no default")
	tsetProxy := flag.String("tset-proxy-redis", "", "optional ~128ms-per-trip proxy host:port for the same owned Redis container")
	tsetSpace := flag.String("tset-space", "", "fresh dev- namespace ending in ':' for the L1 size run")
	tsetMemoryOnly := flag.Bool("tset-memory-only", false, "separate L1 memory diagnostic; loads a test-only GC function, never times gates")
	tsetMemoryCards := flag.Int("tset-memory-cards", 0, "required with --tset-memory-only: exactly 100000 or 1000000 cards on a fresh owned container")
	flag.Parse()
	if *tsetOwned || *tsetRedis != "" || *tsetProxy != "" || *tsetSpace != "" || *tsetMemoryOnly || *tsetMemoryCards != 0 {
		cfg := tsetSizeConfig{redisAddr: *tsetRedis, proxyAddr: *tsetProxy, space: *tsetSpace, owned: *tsetOwned}
		if err := cfg.validate(); err != nil {
			fmt.Fprintln(os.Stderr, "sprintsize:", err)
			os.Exit(2)
		}
		if *tsetMemoryOnly {
			if err := tsetMemoryCLI(context.Background(), cfg, *tsetMemoryCards, os.Stdout); err != nil {
				fmt.Fprintln(os.Stderr, "sprintsize:", err)
				os.Exit(1)
			}
			return
		}
		if *tsetMemoryCards != 0 {
			fmt.Fprintln(os.Stderr, "sprintsize: --tset-memory-cards requires --tset-memory-only")
			os.Exit(2)
		}
		if err := tsetSizeCLI(context.Background(), cfg, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "sprintsize:", err)
			os.Exit(1)
		}
		return
	}
	if *bin == "" || !strings.HasPrefix(*prefix, "dev-") {
		fmt.Fprintln(os.Stderr, "sprintsize: wants --bin <nova-sprint> and a --prefix that starts with dev-")
		os.Exit(2)
	}
	env := append(os.Environ(), "NOVA_SPRINT_REDIS="+*redis, "NOVA_SPRINT_PREFIX="+*prefix, "NOVA_SPRINT_ACTOR=sizerun")
	steps := run()
	if !*keep {
		steps = append(steps, step{"teardown", "110,000", 0, []string{"teardown", "--confirm", "PREFIX"}, nil})
	}
	var out bytes.Buffer
	failed := false
	fmt.Fprintf(&out, "| operation | size | time | limit | result |\n|---|---|---|---|---|\n")
	for _, st := range steps {
		args := make([]string, len(st.args))
		for i, a := range st.args {
			args[i] = strings.ReplaceAll(a, "PREFIX", *prefix)
		}
		cmd := exec.Command(*bin, args...)
		cmd.Env = env
		var o, e bytes.Buffer
		cmd.Stdout, cmd.Stderr = &o, &e
		start := time.Now()
		err := cmd.Run()
		took := time.Since(start)
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			code = -1
		}
		result := "ok"
		if !okCode(code, st.okCodes) {
			result, failed = fmt.Sprintf("EXIT %d", code), true
			fmt.Fprintf(os.Stderr, "sprintsize: %s: %s\n", st.what, strings.TrimSpace(e.String()+o.String()))
		}
		limit := "-"
		if st.limit > 0 {
			limit = st.limit.String()
			if took > st.limit {
				result, failed = result+", OVER", true
			}
		}
		fmt.Fprintf(&out, "| %s | %s | %.2f s | %s | %s |\n", st.what, st.size, took.Seconds(), limit, result)
	}
	fmt.Print(out.String())
	if failed {
		os.Exit(1)
	}
}

func okCode(code int, ok []int) bool {
	if len(ok) == 0 {
		return code == 0
	}
	for _, c := range ok {
		if c == code {
			return true
		}
	}
	return false
}
