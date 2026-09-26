//go:build functional

package main

// Rowan's failure-guidance audit (2026-09-26): every nova-sprint verb path in
// helpCases run as a child process against four stores -- dead (nothing
// listens), nofn (live, function library not loaded), empty (library loaded,
// no records) and acl (library loaded, the seat's ACL user may run nothing).
// git, gh, ssh, ansible-playbook, launchctl, redis-cli, go and curl on PATH
// are stubs that log their argv and exit 97, so no probe reaches a remote.
//
// TestRowanAuditSweep logs one row per path and mode: exit, whether stderr
// names the cause, and which stubs ran. It fails on the silent rows: exit 0
// against a dead store, or a non-zero exit with an empty stderr.
//
//   ROWAN_AUDIT_TSV=/path/out.tsv go test -tags functional ./cmd/nova-sprint -run TestRowanAuditSweep -count=1

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/disposition"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// TestRowanAuditChild is the re-exec target: it runs nova-sprint's run with
// the argv in ROWAN_AUDIT_ARGV and exits with its code.
func TestRowanAuditChild(t *testing.T) {
	t.Parallel()
	raw := os.Getenv("ROWAN_AUDIT_ARGV")
	if raw == "" {
		t.Skip("child only")
	}
	var argv []string
	if err := json.Unmarshal([]byte(raw), &argv); err != nil {
		os.Exit(111)
	}
	os.Exit(run(argv, os.Stdout, os.Stderr))
}

type auditRow struct {
	Path, Mode     string
	Code           int
	Hang           bool
	Stdout, Stderr string
	Stubs          string
}

func rowanStubDir(t *testing.T) (dir, log string) {
	t.Helper()
	dir = t.TempDir()
	log = filepath.Join(dir, "stubs.log")
	for _, name := range []string{"git", "gh", "ssh", "scp", "ansible-playbook", "launchctl", "redis-cli", "go", "curl", "nova-card", "nova-swarm", "nova-secrets", "rsync"} {
		body := fmt.Sprintf("#!/bin/sh\necho \"%s $*\" >> %q\necho \"stub %s refused\" >&2\nexit 97\n", name, log, name)
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir, log
}

// rowanFill gives every flag the path defines a plausible value.
func rowanFill(fs *flag.FlagSet, addr string) []string {
	vals := map[string]string{
		"redis": addr, "store": addr, "addr": addr,
		"sprint": "audit", "repo": "nova-tools", "stream": "s1", "n": "7", "pr": "7",
		"id": "audit-card", "label": "audit-card", "card": "audit-card", "task": "audit-card",
		"as": "friend:audit", "by": "rowan-audit", "actor": "rowan-audit", "who": "rowan-audit",
		"friend": "audit", "bench": "b1", "base": "dev", "head": "0123456789abcdef0123456789abcdef01234567",
		"to": "friend:audit", "copy": "audit-card.c1", "unit": "nova-tools#7",
	}
	var out []string
	if fs == nil {
		return []string{"--redis", addr, "--sprint", "audit"}
	}
	fs.VisitAll(func(f *flag.Flag) {
		if v, ok := vals[f.Name]; ok {
			out = append(out, "--"+f.Name, v)
		}
	})
	return out
}

func rowanRun(t *testing.T, argv []string, env []string, log string) auditRow {
	raw, _ := json.Marshal(argv)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRowanAuditChild$")
	cmd.Env = append(env, "ROWAN_AUDIT_ARGV="+string(raw))
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	_ = os.Remove(log)
	err := cmd.Run()
	r := auditRow{Path: strings.Join(argv, " "), Stdout: so.String(), Stderr: se.String()}
	if ctx.Err() != nil {
		r.Hang = true
	}
	if ee, ok := err.(*exec.ExitError); ok {
		r.Code = ee.ExitCode()
	} else if err != nil {
		r.Code = -1
	}
	if b, err := os.ReadFile(log); err == nil {
		r.Stubs = strings.ReplaceAll(strings.TrimSpace(string(b)), "\n", " | ")
	}
	// go test's own PASS/ok trailer is not the verb's.
	r.Stdout = strings.TrimSuffix(strings.TrimSpace(r.Stdout), "PASS")
	return r
}

// rowanReset gives each run its own fixture: nofn has no data and no
// library, empty and acl hold the library and no data.
func rowanReset(t *testing.T, mode, addr string) {
	t.Helper()
	if mode == "dead" {
		return
	}
	ctx := context.Background()
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	if err := c.FlushAll(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	if mode == "nofn" {
		if err := c.FunctionFlush(ctx).Err(); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
}

func TestRowanAuditSweep(t *testing.T) {
	t.Parallel()
	stubs, log := rowanStubDir(t)
	home := t.TempDir()
	base := []string{"PATH=" + stubs + ":/usr/bin:/bin", "HOME=" + home, "XDG_CONFIG_HOME=" + filepath.Join(home, ".config"), "TMPDIR=" + t.TempDir()}

	ctx := context.Background()
	nofn := testutil.Start(t)
	empty := testutil.Start(t)
	acl := testutil.Start(t)
	for _, addr := range []string{empty, acl} {
		c := redis.NewClient(&redis.Options{Addr: addr})
		if err := fn.Load(ctx, c); err != nil {
			t.Fatal(err)
		}
		if addr == acl {
			if err := c.Do(ctx, "ACL", "SETUSER", "audit", "on", ">auditpw", "~*", "+hello", "+auth", "+ping").Err(); err != nil {
				t.Fatal(err)
			}
		}
		_ = c.Close()
	}
	modes := []struct {
		name, addr string
		env        []string
	}{
		{"dead", "127.0.0.1:1", nil},
		{"nofn", nofn, nil},
		{"empty", empty, nil},
		{"acl", acl, []string{"NOVA_SPRINT_REDIS_USER=audit", "NOVA_REDIS_BENCH_PASSWORD=auditpw"}},
	}
	skip := map[string]bool{"self update": true, "redis-cli": true, "refresh": true}
	var rows []auditRow
	for _, path := range helpCases {
		p := strings.Join(path, " ")
		if skip[p] {
			continue
		}
		fs := helpFlagSet(path)
		for _, m := range modes {
			rowanReset(t, m.name, m.addr)
			argv := append(append([]string{}, path...), rowanFill(fs, m.addr)...)
			r := rowanRun(t, argv, append(append([]string{}, base...), m.env...), log)
			r.Path, r.Mode = p, m.name
			rows = append(rows, r)
			all := r.Stdout + r.Stderr
			switch {
			case (m.name == "dead" || m.name == "acl") && r.Code == 0 && !r.Hang && p != "result contract":
				t.Errorf("SILENT %s [%s]: exit 0 on a store it cannot use: stdout=%q stderr=%q", p, m.name, r.Stdout, r.Stderr)
			case r.Code != 0 && !r.Hang && strings.TrimSpace(all) == "":
				t.Errorf("SILENT %s [%s]: exit %d with no output at all", p, m.name, r.Code)
			case m.name == "nofn" && strings.Contains(all, "Function not found") && !strings.Contains(all, "fn load"):
				t.Errorf("NO-REMEDY %s [nofn]: missing library without `nova-sprint fn load`: %q", p, strings.TrimSpace(r.Stderr+r.Stdout))
			case m.name == "dead" && r.Code != 0 && strings.HasSuffix(strings.TrimSpace(r.Stderr), "run: nova-sprint help") && strings.Contains(all, "127.0.0.1:1"):
				t.Errorf("NO-REMEDY %s [dead]: store down answered with only the help door: %q", p, rowanLast(r.Stderr))
			case m.name == "acl" && strings.Contains(all, "NOPERM") && !strings.Contains(all, "seat") && !strings.Contains(all, "ACL"):
				t.Errorf("NO-REMEDY %s [acl]: NOPERM without the seat or ACL row to fix: %q", p, rowanLast(r.Stderr+r.Stdout))
			}
		}
	}
	if out := os.Getenv("ROWAN_AUDIT_TSV"); out != "" {
		var b strings.Builder
		for _, r := range rows {
			fmt.Fprintf(&b, "%s\t%s\t%d\t%v\t%s\t%s\t%s\n", r.Path, r.Mode, r.Code, r.Hang,
				strings.ReplaceAll(r.Stdout, "\n", "\\n"), strings.ReplaceAll(strings.TrimSpace(r.Stderr), "\n", "\\n"), r.Stubs)
		}
		if err := os.WriteFile(out, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func rowanStore(t *testing.T) string {
	t.Helper()
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	if err := fn.Load(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return addr
}

// TestRowanAuditTaskDoneNotFoundIsNamed: ns_task_done answers NOTFOUND for a
// task that does not exist, but task.Done (internal/nsprint/task/take.go:
// 499-509) has no case for it, so the verb says `unexpected status
// "NOTFOUND"; run: nova-sprint help` and exits 2 (could not run). Beat and
// cancel name NOTFOUND. A FENCED done names neither the holder nor the state.
func TestRowanAuditTaskDoneNotFoundIsNamed(t *testing.T) {
	t.Setenv("NOVA_FRIEND", "rowan")
	addr := rowanStore(t)
	rc := redis.NewClient(&redis.Options{Addr: addr})
	defer rc.Close()
	if err := rc.SAdd(context.Background(), "friends", "rowan").Err(); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runSprint("task", "done", "--redis", addr, "--sprint", "audit", "--id", "ghost", "--token", "t", "--evidence", "x")
	all := out + errOut
	if strings.Contains(all, "unexpected status") || !strings.Contains(all, "NOTFOUND") || !strings.Contains(all, "task list") {
		t.Errorf("task done on a missing task: exit=%d stdout=%q stderr=%q; want DONE NOTFOUND id=ghost sprint=audit and an inspect verb", code, out, errOut)
	}
	if code, out, errOut := runSprint("task", "push", "--redis", addr, "--sprint", "audit", "--id", "t1", "--title", "x", "--kind", "work", "--author", "rowan"); code != 0 {
		t.Fatalf("push: %d %q %q", code, out, errOut)
	}
	code, out, errOut = runSprint("task", "done", "--redis", addr, "--sprint", "audit", "--id", "t1", "--token", "stale", "--evidence", "x")
	if code != 3 || !strings.Contains(out+errOut, "state=") {
		t.Errorf("stale-token done: exit=%d stdout=%q stderr=%q; want FENCED with the task's state and holder and the verb that shows it", code, out, errOut)
	}
}

// TestRowanAuditMissingFunctionNamesFnLoad: with no nova_sprint library on
// the store, some forty verbs print `ERR Function not found; run: nova-sprint
// help`; only census names the library. The remedy is `nova-sprint fn load
// --redis <addr>` (or doctor).
func TestRowanAuditMissingFunctionNamesFnLoad(t *testing.T) {
	t.Parallel()
	addr := testutil.Start(t) // no library
	for _, argv := range [][]string{
		{"task", "done", "--redis", addr, "--sprint", "audit", "--id", "t1", "--token", "t", "--evidence", "x"},
		{"sprint", "status", "--redis", addr, "--sprint", "audit"},
		{"pitstop", "set", "--redis", addr, "--sprint", "audit", "--by", "rowan", "--why", "audit"},
		{"worker", "show", "--redis", addr, "friend:audit"},
		{"stream", "ls", "--redis", addr},
	} {
		code, out, errOut := runSprint(argv...)
		if code == 0 || !strings.Contains(out+errOut, "fn load") {
			t.Errorf("%s with no library: exit=%d stdout=%q stderr=%q; want the missing function named and `nova-sprint fn load --redis %s`",
				strings.Join(argv[:2], " "), code, out, errOut, addr)
		}
	}
}

// TestRowanAuditHoldReleaseNoUnitNamesTheUnit: hold release on a unit with no
// record prints `REFUSED no-unit`, exit 2, with no unit, sprint, or next verb.
func TestRowanAuditHoldReleaseNoUnitNamesTheUnit(t *testing.T) {
	t.Parallel()
	addr := rowanStore(t)
	code, out, errOut := runSprint("hold", "release", "--as", "rowan", "--sprint", "audit", "--redis", addr,
		"nova-tools#9", "stella", "--head", "0123456789abcdef0123456789abcdef01234567", "--evidence", "evidence-1")
	if !strings.Contains(out+errOut, "nova-tools#9") || !strings.Contains(out+errOut, "why ") && !strings.Contains(out+errOut, "hold show") {
		t.Errorf("hold release on a missing unit: exit=%d stdout=%q stderr=%q; want the unit and sprint named and an inspect verb (why / hold show)", code, out, errOut)
	}
}

// TestRowanAuditHoldShowMalformedParkIsNamed: hold show drops json.Unmarshal's
// error on a parked record (hold.go:350-352) and prints the zero Park:
// `parked x id= role= reason= age=<epoch>s`, a row that looks real.
func TestRowanAuditHoldShowMalformedParkIsNamed(t *testing.T) {
	t.Parallel()
	addr := rowanStore(t)
	ctx := context.Background()
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	if err := c.Set(ctx, "s:audit:prunit:nova-tools:7", "nova-tools#7", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.HSet(ctx, disposition.ParkKey("audit"), "nova-tools:7:reader", "{not json").Err(); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runSprint("hold", "show", "--redis", addr, "--sprint", "audit", "nova-tools#7")
	if strings.Contains(out, "parked reader id= ") || !strings.Contains(out+errOut, "malformed") && !strings.Contains(out+errOut, "invalid") {
		t.Errorf("a malformed parked record prints as a real one: exit=%d stdout=%q stderr=%q; want it named malformed with its key and field", code, out, errOut)
	}
}

func rowanLast(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}
