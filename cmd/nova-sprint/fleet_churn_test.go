package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// churnFake answers the ps sample per host with canned output and records
// every argv; no process starts and no host is reached.
type churnFake struct {
	mu    sync.Mutex
	calls []string
}

func (f *churnFake) Run(ctx context.Context, argv []string) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, strings.Join(argv, " "))
	f.mu.Unlock()
	if argv[0] == "ps" {
		return "  PID     ELAPSED  PPID COMM\n  1 12-03:00:00 0 /sbin/launchd\n 50 00:03 1 /usr/local/bin/sprint-table\n 60 00:00 61 /bin/ps\n 61 00:00 1 -zsh\n", nil
	}
	switch argv[6] {
	case "hulk":
		return "  PID ELAPSED  PPID COMMAND\n 1 900000 0 systemd\n 10 2 1 bench-row\n 11 1 1 bench-row\n 20 90000 1 ci-run\n 30 0 31 ps\n 31 0 32 bash\n 32 1 1 sshd\n", nil
	case "bat.example.com":
		return "  PID     ELAPSED  PPID COMM\n 1 1-00:00:00 0 /sbin/launchd\n 5 00:20 1 /usr/bin/thing\n", nil
	case "dead":
		return "ssh: connect to host dead port 22: Connection refused\n", errors.New("exit status 255")
	}
	return "", errors.New("unexpected " + argv[6])
}

func churnRegistry(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "machines.tsv")
	body := "# name\tssh\tos/arch\troles\nhulk\thulk\tlinux/x64\tbench\nbatman\tbat.example.com\tdarwin/x64\tbench\nstudio\tlocalhost\tdarwin/arm64\tcoordination\nwin\twin\twindows/x64\tbench\ndead\tdead\tlinux/x64\tbench\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestFleetChurnSamplesEveryMachine is #4310's DONE-WHEN: one ps per
// registered machine over the ssh column (none for localhost), per machine
// the young commands with counts and the old processes under init, the
// CHURN line last, in registry order; a machine that fails or has no ps line
// prints its FAIL line and the verb goes on, exit 1.
func TestFleetChurnSamplesEveryMachine(t *testing.T) {
	t.Parallel()
	f := &churnFake{}
	reg := churnRegistry(t)
	var out, errOut bytes.Buffer
	code := runFleetChurnWith(context.Background(), []string{"--machines", reg, "--seconds", "12"}, &out, &errOut, f, func(string) string { return "" })
	want := strings.Join([]string{
		"hulk young bench-row 2",
		"hulk old pid=20 age=1d comm=ci-run",
		"CHURN hulk young=2 old=1",
		"CHURN batman young=0 old=0",
		"studio young sprint-table 1",
		"CHURN studio young=1 old=0",
		"CHURN win FAIL no ps sample for windows/x64 (linux and darwin)",
		"CHURN dead FAIL exit status 255: ssh: connect to host dead port 22: Connection refused",
		"",
	}, "\n")
	if code != 1 || out.String() != want || errOut.String() != "" {
		t.Fatalf("code=%d\n%s\nwant:\n%s\nerr=%q", code, out.String(), want, errOut.String())
	}
	joined := strings.Join(f.calls, "\n")
	for _, c := range []string{
		"ssh -n -o BatchMode=yes -o ConnectTimeout=6 hulk ps -eo pid,etimes,ppid,comm",
		"ssh -n -o BatchMode=yes -o ConnectTimeout=6 bat.example.com ps -Ao pid,etime,ppid,comm",
		"ps -Ao pid,etime,ppid,comm",
	} {
		if !strings.Contains(joined, c) {
			t.Errorf("missing %q in\n%s", c, joined)
		}
	}
	if len(f.calls) != 4 {
		t.Errorf("%d samples, want 4: %v", len(f.calls), f.calls)
	}

	// --only narrows to the named machines; the default window is 12 s.
	f = &churnFake{}
	out.Reset()
	code = runFleetChurnWith(context.Background(), []string{"--machines", reg, "--only", "batman"}, &out, &errOut, f, func(string) string { return "" })
	if code != 0 || out.String() != "CHURN batman young=0 old=0\n" || len(f.calls) != 1 {
		t.Fatalf("only: code=%d out=%q calls=%v", code, out.String(), f.calls)
	}
	// The registry comes from the environment when --machines is not given.
	f = &churnFake{}
	out.Reset()
	code = runFleetChurnWith(context.Background(), []string{"--only", "hulk", "--seconds", "1"}, &out, &errOut, f,
		func(k string) string {
			if k == "NOVA_FLEET_MACHINES" {
				return reg
			}
			return ""
		})
	if code != 0 || !strings.HasSuffix(out.String(), "CHURN hulk young=0 old=1\n") {
		t.Fatalf("env registry: code=%d out=%q", code, out.String())
	}
}

func TestFleetChurnRefusals(t *testing.T) {
	t.Parallel()
	reg := churnRegistry(t)
	none := func(string) string { return "" }
	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, "wants the machines registry"},
		{[]string{"--machines", reg, "extra"}, "takes no positional arguments"},
		{[]string{"--machines", reg, "--seconds", "0"}, "--seconds must be >= 1"},
		{[]string{"--machines", reg, "--only", "nobody"}, "names machines the registry does not: nobody"},
		{[]string{"--machines", filepath.Join(t.TempDir(), "missing.tsv")}, "machines registry:"},
		{[]string{"--machines", reg, "--nope"}, "--nope is not a flag of nova-sprint fleet churn"},
	} {
		f := &churnFake{}
		var out, errOut bytes.Buffer
		code := runFleetChurnWith(context.Background(), tc.args, &out, &errOut, f, none)
		if code != 2 || out.Len() != 0 || !strings.Contains(errOut.String(), tc.want) || len(f.calls) != 0 {
			t.Errorf("%v: code=%d out=%q err=%q calls=%v", tc.args, code, out.String(), errOut.String(), f.calls)
		}
	}
	var out, errOut bytes.Buffer
	if code := runFleet(context.Background(), []string{"churn", "--machines", reg, "--nope"}, &out, &errOut); code != 2 {
		t.Errorf("dispatch: code=%d err=%q", code, errOut.String())
	}
}
