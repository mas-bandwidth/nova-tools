//go:build functional

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/seatcred/seattest"
	"github.com/redis/go-redis/v9"
)

// TestSeatRowDrivesVerbsWithNoWrapper is #4330's DONE-WHEN on a throwaway
// fleet-shaped Redis (default user off). The built nova-sprint runs with an
// environment of PATH, HOME and XDG_CONFIG_HOME only -- no address, user or
// password, no wrapper: `--seat coordinator table`, and `redis <cmd>` under
// NOVA_SPRINT_SEAT with no address on the line, take the login (and the
// address) from the seat's row in seats.tsv and the password from the seat's
// file. A row for a user the ACL restricts prints Redis's refusal, and a seat
// with no row is refused naming seats.tsv.
func TestSeatRowDrivesVerbsWithNoWrapper(t *testing.T) {
	t.Parallel()

	const pw, rpw = "row-coord-test-pw-4330", "row-reader-test-pw-4330"
	home := seattest.Home(t, "studio", map[string]string{"NOVA_REDIS_COORDINATOR_PASSWORD": pw, "NOVA_REDIS_READER_PASSWORD": rpw})
	addr := testutil.Start(t, "--user", "default", "off",
		"--user", "coordinator", "on", ">"+pw, "~*", "&*", "+@all",
		"--user", "reader", "on", ">"+rpw, "~*", "+get", "+ping")
	c := redis.NewClient(&redis.Options{Addr: addr, Username: "coordinator", Password: pw})
	defer c.Close()
	if err := fn.Load(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	xdg := filepath.Join(home, "xdg")
	row := func(name, user string) string {
		return name + "\t" + addr + "\t" + user + "\tNOVA_REDIS_" + strings.ToUpper(user) + "_PASSWORD\t~/nova-bench/secrets\t~/.config/nova-secrets/studio.key\n"
	}
	if err := os.MkdirAll(filepath.Join(xdg, "nova-sprint"), 0o700); err != nil {
		t.Fatal(err)
	}
	seats := filepath.Join(xdg, "nova-sprint", "seats.tsv")
	if err := os.WriteFile(seats, []byte(row("coordinator", "coordinator")+row("reader", "reader")), 0o600); err != nil {
		t.Fatal(err)
	}

	bin := filepath.Join(t.TempDir(), "nova-sprint")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Env = goenv.Clean(os.Environ())
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/nova-sprint: %v\n%s", err, out)
	}
	ns := func(extra []string, args ...string) (int, string, string) {
		cmd := exec.Command(bin, args...)
		cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "XDG_CONFIG_HOME=" + xdg}, extra...)
		var out, errOut bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errOut
		err := cmd.Run()
		code := 0
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		for _, s := range []string{out.String(), errOut.String()} {
			if strings.Contains(s, pw) || strings.Contains(s, rpw) {
				t.Fatal("a password was printed")
			}
		}
		return code, out.String(), errOut.String()
	}

	if code, out, errOut := ns(nil, "--seat", "coordinator", "table", "--redis", addr, "--once"); code != 0 || strings.TrimSpace(out) == "" {
		t.Fatalf("--seat coordinator table: exit %d stderr %s", code, errOut)
	}
	if code, _, errOut := ns(nil, "table", "--redis", addr, "--once"); code == 0 || !strings.Contains(errOut, "NOAUTH") {
		t.Fatalf("table with no seat: exit %d stderr %q; want NOAUTH, so the seat is what logged in", code, errOut)
	}
	seatEnv := []string{SprintSeatEnv + "=coordinator"}
	if code, out, errOut := ns(seatEnv, "redis", "SET", "seat:probe", "v1"); code != 0 || out != "OK\n" || !strings.Contains(errOut, "REDIS seat=coordinator user=coordinator") {
		t.Fatalf("NOVA_SPRINT_SEAT redis SET: exit %d stdout %q stderr %q", code, out, errOut)
	}
	if code, out, errOut := ns(seatEnv, "redis", "--", "HMGET", "nokey", "a"); code != 0 || out != "\n" {
		t.Fatalf("redis HMGET: exit %d stdout %q stderr %q", code, out, errOut)
	}
	if code, out, errOut := ns(nil, "--seat", "reader", "redis", "GET", "seat:probe"); code != 0 || out != "v1\n" {
		t.Fatalf("reader GET: exit %d stdout %q stderr %q", code, out, errOut)
	}
	if code, _, errOut := ns(nil, "--seat", "reader", "redis", "SET", "seat:probe", "v2"); code != 1 || !strings.Contains(errOut, "REDIS REFUSED seat=reader") || !strings.Contains(errOut, "NOPERM") {
		t.Fatalf("reader SET: exit %d stderr %q; want 1 and the NOPERM refusal printed", code, errOut)
	}
	if code, _, errOut := ns(nil, "--seat", "ghost", "redis", "--redis", addr, "PING"); code != 2 || !strings.Contains(errOut, seats) {
		t.Fatalf("ghost seat: exit %d stderr %q; want 2 naming %s", code, errOut, seats)
	}
}
