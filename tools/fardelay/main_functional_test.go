//go:build functional

package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// The tool listens where it says, forwards to its target and stops when asked,
// and says what it held. The target is an echo server the test owns, and the
// delay is zero: what is held, and for how long, is pkg/delayproxy's.
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
				defer func() { _ = c.Close() }() // ignored: the echo ends with the connection; no one reads the close error
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
	ctx, stop := context.WithCancel(t.Context())
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
	defer func() { _ = c.Close() }() // ignored: the test is done with the connection; no one reads the close error
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
	defer func() { _ = taken.Close() }() // ignored: the listener only held the address for the refused run
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--listen", taken.Addr().String(), "--target", "127.0.0.1:7000", "--delay", "1ms"}, &stdout, &stderr)
	if code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "listening on") {
		t.Fatalf("exit %d, stdout %q, stderr %q; want exit 2 and a line naming the listen that failed", code, stdout.String(), stderr.String())
	}
}
