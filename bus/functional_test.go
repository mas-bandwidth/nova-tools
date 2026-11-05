//go:build functional

package main

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestAgainstRealRedis sends to two inboxes, hooks one in, kills the hook,
// sends again and hooks back in: the second hook never prints an acked message.
func TestAgainstRealRedis(t *testing.T) {
	server, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("redis-server not on PATH")
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	redis := exec.Command(server, "--port", strconv.Itoa(port), "--save", "", "--appendonly", "no", "--dir", t.TempDir())
	if err := redis.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { redis.Process.Kill(); redis.Wait() })
	addr := "127.0.0.1:" + strconv.Itoa(port)
	for i := 0; ; i++ {
		if c, err := net.Dial("tcp", addr); err == nil {
			c.Close()
			break
		} else if i > 100 {
			t.Fatal(err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	env := map[string]string{"NOVA_BUS_REDIS": addr, "NOVA_BUS_AS": "alpha"}
	send := func(subject, body string) {
		var out, errOut bytes.Buffer
		code := run(context.Background(), []string{"send", "--to", "beta,gamma", subject, body}, func(k string) string { return env[k] }, nil, &out, &errOut, dialRedis)
		if code != 0 || strings.Count(out.String(), "\n") != 2 {
			t.Fatalf("send: %d %q %q", code, out.String(), errOut.String())
		}
	}
	// hookUntil hooks beta in until a line ends with last, then kills it.
	hookUntil := func(last string) []string {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		r, w := io.Pipe()
		done := make(chan struct{})
		go func() {
			run(ctx, []string{"hook", "--as", "beta"}, func(k string) string { return env[k] }, nil, w, io.Discard, dialRedis)
			w.Close()
			close(done)
		}()
		var got []string
		sc := bufio.NewScanner(r)
		for sc.Scan() {
			if got = append(got, sc.Text()); strings.HasSuffix(sc.Text(), last) {
				break
			}
		}
		cancel()
		go io.Copy(io.Discard, r)
		<-done
		return got
	}
	send("one", "first\nline")
	send("two", "second")
	first := hookUntil("\tsecond")
	if len(first) != 2 || !strings.HasSuffix(first[0], "\talpha\t-\tone\tfirst\\nline") || !strings.HasSuffix(first[1], "\ttwo\tsecond") {
		t.Fatalf("first hook: %q", first)
	}
	send("three", "third")
	// The kill may land between the last print and its ack, so "two" may be
	// printed again (at-least-once); "one" was acked before "two" was printed.
	second := hookUntil("\tthird")
	if strings.Contains(strings.Join(second, "\n"), "\tone\t") || !strings.HasSuffix(second[len(second)-1], "\tthree\tthird") {
		t.Fatalf("second hook: %q", second)
	}
}
