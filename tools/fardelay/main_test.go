package main

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/delayproxy"
)

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
		if code != 2 || !strings.Contains(stderr.String(), c.want) || !strings.HasSuffix(stderr.String(), "; run: fardelay -h\n") || stdout.Len() != 0 {
			t.Errorf("%s: exit %d, stdout %q, stderr %q; want exit 2, nothing on stdout, and one line naming %q with the hint", name, code, stdout.String(), stderr.String(), c.want)
		}
		if lines := strings.Count(stderr.String(), "\n"); lines != 1 {
			t.Errorf("%s: stderr is %d lines; want one", name, lines)
		}
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
		if code := run(context.Background(), []string{flag}, &stdout, &stderr); code != 0 {
			t.Fatalf("%s: exit %d; want 0", flag, code)
		}
		if stdout.String() != usage || stderr.Len() != 0 {
			t.Fatalf("%s: stdout is not the usage, or stderr = %q", flag, stderr.String())
		}
	}
	// Each flag has an entry of its own, and the entry says what a reader needs of
	// that flag: the synopsis and the other flags' entries cannot stand in for it.
	for flag, need := range map[string][]string{
		"--target": {"HOST:PORT", "Required", "dialled"},
		"--delay":  {"DURATION", "Required", "round trip", delayproxy.MaxDelay.String()},
		"--listen": {"HOST:PORT", "loopback", "Default"},
	} {
		entry, found := flagEntry(usage, flag)
		if !found {
			t.Errorf("the help has no entry for %s in its flags section", flag)
			continue
		}
		for _, want := range need {
			if !strings.Contains(entry, want) {
				t.Errorf("the entry for %s does not say %q:\n%s", flag, want, entry)
			}
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
		if !strings.Contains(usage, need) {
			t.Errorf("the help does not say %q", need)
		}
	}
}


