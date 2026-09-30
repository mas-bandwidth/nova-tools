package main

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// farCeiling bounds each read of a socket here, generously: a test that is not
// answered fails at it and does not hang.
const farCeiling = 30 * time.Second

// listenFields is the words of the LISTEN line: LISTEN, OK, addr=, target=, delay=.
const listenFields = 5

func TestFlagsParse(t *testing.T) {
	t.Parallel()

	for name, c := range map[string]struct {
		args []string
		want config
	}{
		"every flag": {
			[]string{"--listen", "127.0.0.1:7001", "--target", "127.0.0.1:7000", "--delay", "64ms"},
			config{"127.0.0.1:7001", "127.0.0.1:7000", 64 * time.Millisecond},
		},
		"the listen address defaults to a port the kernel picks": {
			[]string{"--target", "127.0.0.1:7000", "--delay", "1s"},
			config{"127.0.0.1:0", "127.0.0.1:7000", time.Second},
		},
		"a single dash and an equals sign": {
			[]string{"-target=localhost:7000", "-delay=0s", "-listen=localhost:7001"},
			config{"localhost:7001", "localhost:7000", 0},
		},
		"a target off the machine, which only a person running the tool may ask for": {
			[]string{"--target", "store.example:6379", "--delay", "128ms"},
			config{"127.0.0.1:0", "store.example:6379", 128 * time.Millisecond},
		},
	} {
		got, err := parse(c.args)
		if err != nil || got != c.want {
			t.Errorf("%s: parse(%q) = %+v, %v; want %+v", name, c.args, got, err, c.want)
		}
	}
}

func TestARunWithNoTargetIsRefusedBeforeItListens(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"--delay", "64ms"}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit %d; want 2, could not run", code)
	}
	if got, want := stderr.String(), "fardelay: missing required flag --target; run: fardelay -h\n"; got != want {
		t.Fatalf("stderr = %q; want %q", got, want)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q; want nothing, for nothing was started", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run(context.Background(), nil, &stdout, &stderr); code != 2 {
		t.Fatalf("exit %d with no flags; want 2", code)
	}
	if !strings.Contains(stderr.String(), "--target") || !strings.Contains(stderr.String(), "--delay") {
		t.Fatalf("stderr = %q; want every missing flag named at once", stderr.String())
	}
}

func TestARunThatCannotBeServedIsRefusedAndNamesWhy(t *testing.T) {
	t.Parallel()

	for name, c := range map[string]struct {
		args []string
		want string
	}{
		"a listen address off the loopback": {[]string{"--listen", "0.0.0.0:7001", "--target", "127.0.0.1:7000", "--delay", "1ms"}, "not a loopback address"},
		"a listen address with no port":     {[]string{"--listen", "127.0.0.1", "--target", "127.0.0.1:7000", "--delay", "1ms"}, "--listen"},
		"a target with no port":             {[]string{"--target", "127.0.0.1", "--delay", "1ms"}, "target"},
		"a delay with no unit":              {[]string{"--target", "127.0.0.1:7000", "--delay", "64"}, "delay"},
		"a negative delay":                  {[]string{"--target", "127.0.0.1:7000", "--delay", "-1ms"}, "--delay"},
		"a delay of an hour":                {[]string{"--target", "127.0.0.1:7000", "--delay", "1h"}, "--delay"},
		"a flag it does not have":           {[]string{"--target", "127.0.0.1:7000", "--delay", "1ms", "--jitter", "3ms"}, "jitter"},
		"a word that is no flag":            {[]string{"--target", "127.0.0.1:7000", "--delay", "1ms", "serve"}, `"serve"`},
	} {
		var stdout, stderr bytes.Buffer
		code := run(context.Background(), c.args, &stdout, &stderr)
		if code != 2 || !strings.Contains(stderr.String(), c.want) || !strings.HasSuffix(stderr.String(), "; run: fardelay -h\n") || stdout.Len() != 0 {
			t.Errorf("%s: exit %d, stdout %q, stderr %q; want exit 2, nothing on stdout, and one line naming %q with the hint", name, code, stdout.String(), stderr.String(), c.want)
		}
		if lines := strings.Count(stderr.String(), "\n"); lines != 1 {
			t.Errorf("%s: stderr is %d lines; want one", name, lines)
		}
	}
}

// The help is what a reader who has never seen the tool has: what it does, each
// flag, what is held and what is not, the two lines it prints and the exit codes.
func TestHelpIsEnoughToUseTheToolCold(t *testing.T) {
	t.Parallel()

	for _, flag := range []string{"-h", "--help"} {
		var stdout, stderr bytes.Buffer
		if code := run(context.Background(), []string{flag}, &stdout, &stderr); code != 0 {
			t.Fatalf("%s: exit %d; want 0", flag, code)
		}
		if stdout.String() != usage || stderr.Len() != 0 {
			t.Fatalf("%s: stdout is not the usage, or stderr = %q", flag, stderr.String())
		}
	}
	for _, need := range []string{
		"--target HOST:PORT", "--delay DURATION", "--listen HOST:PORT", // the flags
		"Required",                        // which are
		"round trip",                      // what a delay is as a distance
		"pipeline", "one after the other", // what is paid once and what is paid again
		"LISTEN OK addr=", "STOP OK writes=", // what it prints
		"loopback", "exit:", // the limits and the exit codes
	} {
		if !strings.Contains(usage, need) {
			t.Errorf("the help does not say %q", need)
		}
	}
}

// lines reads what the tool writes to stdout, a line at a time.
func lines(r io.Reader) *bufio.Reader { return bufio.NewReader(r) }

// The tool listens where it says, forwards to its target and stops when asked,
// and says what it held. The target is an echo server the test owns, and the
// delay is zero: what is held, and for how long, is internal/delayproxy's.
func TestTheToolListensForwardsAndStopsWhenAsked(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var serving sync.WaitGroup
	serving.Add(1)
	go func() {
		defer serving.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			serving.Add(1)
			go func() {
				defer serving.Done()
				defer c.Close()
				_, _ = io.Copy(c, c)
			}()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		serving.Wait()
	})

	out, in := io.Pipe()
	var stderr bytes.Buffer
	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	exit := make(chan int, 1)
	go func() {
		exit <- run(ctx, []string{"--target", ln.Addr().String(), "--delay", "0s"}, in, &stderr)
		_ = in.Close()
	}()
	said := lines(out)

	first, err := said.ReadString('\n')
	if err != nil {
		t.Fatalf("reading the first line: %v", err)
	}
	fields := strings.Fields(first)
	if len(fields) != listenFields || fields[0] != "LISTEN" || fields[1] != "OK" || fields[3] != "target="+ln.Addr().String() || fields[4] != "delay=0s" || !strings.HasPrefix(fields[2], "addr=127.0.0.1:") {
		t.Fatalf("first line %q; want LISTEN OK addr=127.0.0.1:<port> target=<the target> delay=0s", first)
	}
	addr := strings.TrimPrefix(fields[2], "addr=")
	if strings.HasSuffix(addr, ":0") {
		t.Fatalf("the tool reports the port it was asked for, %s, and not the one it got", addr)
	}

	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.SetDeadline(time.Now().Add(farCeiling)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(c, "through the tool"); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len("through the tool"))
	if _, err := io.ReadFull(c, got); err != nil || string(got) != "through the tool" {
		t.Fatalf("what came back was %q, %v", got, err)
	}

	stop()
	last, err := said.ReadString('\n')
	if err != nil {
		t.Fatalf("reading the last line: %v", err)
	}
	if !strings.HasPrefix(last, "STOP OK writes=1 shortest=") {
		t.Fatalf("last line %q; want STOP OK writes=1 shortest=<duration>", last)
	}
	if code := <-exit; code != 0 {
		t.Fatalf("exit %d; want 0, stopped as asked", code)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q; want nothing", stderr.String())
	}
}

func TestARunThatCannotListenIsRefused(t *testing.T) {
	t.Parallel()

	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--listen", taken.Addr().String(), "--target", "127.0.0.1:7000", "--delay", "1ms"}, &stdout, &stderr)
	if code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "listening on") {
		t.Fatalf("exit %d, stdout %q, stderr %q; want exit 2 and a line naming the listen that failed", code, stdout.String(), stderr.String())
	}
}
