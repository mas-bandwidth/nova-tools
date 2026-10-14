package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/delayproxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		"no delay, the bottom of the range the help documents": {
			[]string{"--target", "127.0.0.1:7000", "--delay", "0s"},
			config{"127.0.0.1:0", "127.0.0.1:7000", 0},
		},
		"the longest delay, the top of that range": {
			[]string{"--target", "127.0.0.1:7000", "--delay", delayproxy.MaxDelay.String()},
			config{"127.0.0.1:0", "127.0.0.1:7000", delayproxy.MaxDelay},
		},
		"a target off the machine, which only a person running the tool may ask for": {
			[]string{"--target", "store.example:6379", "--delay", "128ms"},
			config{"127.0.0.1:0", "store.example:6379", 128 * time.Millisecond},
		},
	} {
		got, err := parse(c.args)
		require.NoError(t, err, "%s: parse(%q)", name, c.args)
		assert.Equal(t, c.want, got, "%s: parse(%q)", name, c.args)
	}
}

func TestARunWithNoTargetIsRefusedBeforeItListens(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--delay", "64ms"}, &stdout, &stderr)
	assert.Equal(t, 2, code, "could not run")
	assert.Equal(t, "fardelay: missing required flag --target; run: fardelay -h\n", stderr.String())
	assert.Empty(t, stdout.String(), "nothing was started")

	stdout.Reset()
	stderr.Reset()
	code = run(context.Background(), nil, &stdout, &stderr)
	assert.Equal(t, 2, code, "no flags")
	assert.Contains(t, stderr.String(), "--target")
	assert.Contains(t, stderr.String(), "--delay")
}

func TestARunThatCannotBeServedIsRefusedAndNamesWhy(t *testing.T) {
	t.Parallel()

	for name, c := range map[string]struct {
		args []string
		want string
	}{
		"a listen address off the loopback":     {[]string{"--listen", "0.0.0.0:7001", "--target", "127.0.0.1:7000", "--delay", "1ms"}, "not a loopback address"},
		"a listen address with no port":         {[]string{"--listen", "127.0.0.1", "--target", "127.0.0.1:7000", "--delay", "1ms"}, "--listen"},
		"a target with no port":                 {[]string{"--target", "127.0.0.1", "--delay", "1ms"}, "target"},
		"a delay with no unit":                  {[]string{"--target", "127.0.0.1:7000", "--delay", "64"}, "delay"},
		"a negative delay":                      {[]string{"--target", "127.0.0.1:7000", "--delay", "-1ms"}, "--delay"},
		"a delay of an hour":                    {[]string{"--target", "127.0.0.1:7000", "--delay", "1h"}, "--delay"},
		"a delay a nanosecond past the longest": {[]string{"--target", "127.0.0.1:7000", "--delay", (delayproxy.MaxDelay + time.Nanosecond).String()}, "--delay"},
		"a flag it does not have":               {[]string{"--target", "127.0.0.1:7000", "--delay", "1ms", "--jitter", "3ms"}, "jitter"},
		"a word that is no flag":                {[]string{"--target", "127.0.0.1:7000", "--delay", "1ms", "serve"}, `"serve"`},
	} {
		var stdout, stderr bytes.Buffer
		code := run(context.Background(), c.args, &stdout, &stderr)
		assert.Equal(t, 2, code, "%s", name)
		assert.Contains(t, stderr.String(), c.want, "%s", name)
		assert.True(t, strings.HasSuffix(stderr.String(), "; run: fardelay -h\n"), "%s", name)
		assert.Empty(t, stdout.String(), "%s: stdout", name)
		assert.Equal(t, 1, strings.Count(stderr.String(), "\n"), "%s: stderr lines", name)
	}
}

// flagEntry is one flag's own entry in the help's flags section: the line that
// starts with the flag and the lines indented under it, up to the next flag. A
// flag the synopsis or the prose names has no entry of its own.
func flagEntry(help, name string) (string, bool) {
	_, section, ok := strings.Cut(help, "\nflags:\n")
	if !ok {
		return "", false
	}
	var entry []string
	in, found := false, false
	for _, line := range strings.Split(section, "\n") {
		if line == "" {
			break // the section ends at its first blank line
		}
		if strings.HasPrefix(line, "  --") {
			in = strings.HasPrefix(line, "  "+name+" ")
			found = found || in
		}
		if in {
			entry = append(entry, line)
		}
	}
	return strings.Join(entry, "\n"), found
}

// The help is what a reader who has never seen the tool has: what it does, each
// flag and what is said of it in its own entry, what is held and what is not,
// the two lines it prints and the exit codes.
func TestHelpIsEnoughToUseTheToolCold(t *testing.T) {
	t.Parallel()

	for _, flag := range []string{"-h", "--help"} {
		var stdout, stderr bytes.Buffer
		require.Equal(t, 0, run(context.Background(), []string{flag}, &stdout, &stderr), "%s: exit", flag)
		require.Equal(t, usage, stdout.String(), "%s: stdout is not the usage", flag)
		require.Empty(t, stderr.String(), "%s: stderr", flag)
	}
	// Each flag has an entry of its own, and the entry says what a reader needs of
	// that flag: the synopsis and the other flags' entries cannot stand in for it.
	for flag, need := range map[string][]string{
		"--target": {"HOST:PORT", "Required", "dialled"},
		"--delay":  {"DURATION", "Required", "round trip", delayproxy.MaxDelay.String()},
		"--listen": {"HOST:PORT", "loopback", "Default"},
	} {
		entry, found := flagEntry(usage, flag)
		require.True(t, found, "the help has no entry for %s in its flags section", flag)
		for _, want := range need {
			assert.Contains(t, entry, want, "the entry for %s", flag)
		}
	}
	for _, need := range []string{
		"proxy", "any TCP server", // what it is
		"pipeline", "one after the other", // what is paid once and what is paid again
		fmt.Sprintf("%d MiB", delayproxy.WindowBytes>>20), // and where a pipeline starts to pay again
		"LISTEN OK addr=", "STOP OK writes=", // what it prints
		"redis-cli", // a client to try it with
		"exit:",     // the exit codes
	} {
		assert.Contains(t, usage, need, "the help")
	}
}

// lines reads what the tool writes to stdout, a line at a time.
func lines(r io.Reader) *bufio.Reader { return bufio.NewReader(r) }

// The tool listens where it says, forwards to its target and stops when asked,
// and says what it held. The target is an echo server the test owns, and the
// delay is zero: what is held, and for how long, is pkg/delayproxy's.
func TestTheToolListensForwardsAndStopsWhenAsked(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
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
	require.NoError(t, err, "reading the first line")
	fields := strings.Fields(first)
	require.True(t, len(fields) == listenFields && fields[0] == "LISTEN" && fields[1] == "OK" && fields[3] == "target="+ln.Addr().String() && fields[4] == "delay=0s" && strings.HasPrefix(fields[2], "addr=127.0.0.1:"),
		"first line %q; want LISTEN OK addr=127.0.0.1:<port> target=<the target> delay=0s", first)
	addr := strings.TrimPrefix(fields[2], "addr=")
	require.False(t, strings.HasSuffix(addr, ":0"), "the tool reports the port it was asked for, %s, and not the one it got", addr)

	c, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	defer func() { _ = c.Close() }() // ignored: the test is done with the connection; no one reads the close error
	require.NoError(t, c.SetDeadline(time.Now().Add(farCeiling)))
	_, err = io.WriteString(c, "through the tool")
	require.NoError(t, err)
	got := make([]byte, len("through the tool"))
	_, err = io.ReadFull(c, got)
	require.True(t, err == nil && string(got) == "through the tool", "what came back was %q, %v", got, err)

	stop()
	last, err := said.ReadString('\n')
	require.NoError(t, err, "reading the last line")
	require.True(t, strings.HasPrefix(last, "STOP OK writes=1 shortest="), "last line %q; want STOP OK writes=1 shortest=<duration>", last)
	require.Equal(t, 0, <-exit, "stopped as asked")
	require.Empty(t, stderr.String())
}

func TestARunThatCannotListenIsRefused(t *testing.T) {
	t.Parallel()

	taken, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = taken.Close() }() // ignored: the listener only held the address for the refused run
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--listen", taken.Addr().String(), "--target", "127.0.0.1:7000", "--delay", "1ms"}, &stdout, &stderr)
	assert.Equal(t, 2, code)
	assert.Empty(t, stdout.String())
	assert.Contains(t, stderr.String(), "listening on")
}
