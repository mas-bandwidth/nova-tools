// Command sprintsize is nova-sprint's size run: the sprint at 10x the
// largest real one (three streams of 30,000 in stops of 1,000, and two of
// 10,000) on a local store, every operation timed against a limit written
// down before the run. An operation over its limit fails the run as a wrong
// result does. It is the measuring instrument of the speed work: every change
// to how the sprint reads and writes is judged by its table.
//
// It runs the nova-sprint binary it is given, as a person does, against the
// store given, which is the sprint's alone: it tears the sprint down first and
// at the end unless --keep, so it refuses a store that is not local, and a store that already holds a
// sprint unless --replace.
//
//	example:
//	  go build -o /tmp/nova-sprint ./cmd/nova-sprint
//	  go run ./tools/sprintsize --bin /tmp/nova-sprint --redis 127.0.0.1:6401
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"net"
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
		{"teardown", "-", 0, []string{"teardown", "--confirm", "sprint"}, []int{0, 1, 2}},
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

// sprintExists says the store already holds a sprint: nova-sprint where reads
// it (exit 0) and refuses when there is none. Any non-zero exit counts as no
// sprint, so a store that cannot be read fails open here; the guard is for a
// local store only (localStore), and the teardown that follows fails loudly on
// a store that cannot be reached.
func sprintExists(bin string, env []string) bool {
	cmd := exec.Command(bin, "where")
	cmd.Env = env
	return cmd.Run() == nil
}

// localStore says the address is on this machine.
func localStore(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	return err == nil && (host == "127.0.0.1" || host == "localhost" || host == "::1")
}

func main() {
	bin := flag.String("bin", "", "the nova-sprint binary to measure")
	redis := flag.String("redis", "127.0.0.1:6401", "the store, host:port (a local one: the limits are for a local store)")
	replace := flag.Bool("replace", false, "tear down a sprint that already exists on the store, which the run does first")
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
	if *bin == "" || !localStore(*redis) {
		fmt.Fprintln(os.Stderr, "sprintsize: wants --bin <nova-sprint> and a local --redis (127.0.0.1, localhost or [::1]): the run tears the sprint on it down")
		os.Exit(2)
	}
	env := append(os.Environ(), "NOVA_SPRINT_REDIS="+*redis, "NOVA_SPRINT_ACTOR=sizerun")
	if !*replace && sprintExists(*bin, env) {
		fmt.Fprintln(os.Stderr, "sprintsize: a sprint already exists on "+*redis+"; the run tears it down first. Pass --replace to tear it down")
		os.Exit(2)
	}
	steps := run()
	if !*keep {
		steps = append(steps, step{"teardown", "110,000", 0, []string{"teardown", "--confirm", "sprint"}, nil})
	}
	var out bytes.Buffer
	failed := false
	fmt.Fprintf(&out, "| operation | size | time | limit | result |\n|---|---|---|---|---|\n")
	for _, st := range steps {
		cmd := exec.Command(*bin, st.args...)
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
