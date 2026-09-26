//go:build functional

package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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
// password, no GH_TOKEN, no wrapper, and no --redis on any line:
// `--seat coordinator table --once`, census, digest, fn load and fn check,
// and `redis <cmd>` under NOVA_SPRINT_SEAT take the login and the address
// from the seat's row in seats.tsv and the password from the seat's file; the
// row's seventh column hands `land pr` the seat's GitHub token, and a
// six-column row says once that it reads GH_TOKEN instead. A row for a user
// the ACL restricts prints Redis's refusal, and a seat with no row is refused
// naming seats.tsv.
func TestSeatRowDrivesVerbsWithNoWrapper(t *testing.T) {
	t.Parallel()

	const pw, rpw, tok = "row-coord-test-pw-4330", "row-reader-test-pw-4330", "row-gh-test-token-4330"
	home := seattest.Home(t, "studio", map[string]string{"NOVA_REDIS_COORDINATOR_PASSWORD": pw, "NOVA_REDIS_READER_PASSWORD": rpw, "GH_GATE_TOKEN": tok})
	addr := testutil.Start(t, "--user", "default", "off",
		"--user", "coordinator", "on", ">"+pw, "~*", "&*", "+@all",
		"--user", "reader", "on", ">"+rpw, "~*", "+get", "+ping")
	c := redis.NewClient(&redis.Options{Addr: addr, Username: "coordinator", Password: pw})
	defer c.Close()
	if err := fn.Load(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	xdg := filepath.Join(home, "xdg")
	row := func(name, user, gh string) string {
		r := name + "\t" + addr + "\t" + user + "\tNOVA_REDIS_" + strings.ToUpper(user) + "_PASSWORD\t~/nova-bench/secrets\t~/.config/nova-secrets/studio.key"
		if gh != "" {
			r += "\t" + gh
		}
		return r + "\n"
	}
	if err := os.MkdirAll(filepath.Join(xdg, "nova-sprint"), 0o700); err != nil {
		t.Fatal(err)
	}
	seats := filepath.Join(xdg, "nova-sprint", "seats.tsv")
	if err := os.WriteFile(seats, []byte(row("coordinator", "coordinator", "GH_GATE_TOKEN")+row("reader", "reader", "")), 0o600); err != nil {
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
			if strings.Contains(s, pw) || strings.Contains(s, rpw) || strings.Contains(s, tok) {
				t.Fatal("a secret was printed")
			}
		}
		return code, out.String(), errOut.String()
	}

	// No --redis on any line: every verb dials the seat row's address.
	if code, out, errOut := ns(nil, "--seat", "coordinator", "table", "--once"); code != 0 || strings.TrimSpace(out) == "" {
		t.Fatalf("--seat coordinator table --once: exit %d stderr %s", code, errOut)
	}
	for _, verb := range [][]string{
		{"census", "--sprint", "seat-probe"},
		{"digest", "--since", "2026-01-01T00:00:00Z"},
		{"fn", "load"},
		{"fn", "check"},
	} {
		if code, _, errOut := ns(nil, append([]string{"--seat", "coordinator"}, verb...)...); code != 0 {
			t.Fatalf("--seat coordinator %s with no --redis: exit %d stderr %q", strings.Join(verb, " "), code, errOut)
		}
	}
	noSeatAddr := []string{"NOVA_SPRINT_REDIS=" + addr}
	if code, _, errOut := ns(noSeatAddr, "redis", "PING"); code == 0 || !strings.Contains(errOut, "NOAUTH") {
		t.Fatalf("redis with no seat: exit %d stderr %q; want NOAUTH, so the seat is what logged in", code, errOut)
	}

	// The row's seventh column is the GitHub token land pr sends; no
	// GH_TOKEN is in the environment.
	var auth []string
	var mu sync.Mutex
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auth = append(auth, r.Header.Get("Authorization"))
		mu.Unlock()
		http.NotFound(w, r)
	}))
	defer api.Close()
	emptyGH := []string{"GH_CONFIG_DIR=" + t.TempDir()}
	if _, _, errOut := ns(emptyGH, "--seat", "coordinator", "land", "pr", "--pr", "1", "--repo", "o/r", "--api", api.URL); strings.Contains(errOut, "no GitHub token") {
		t.Fatalf("land pr under a seven-column row: %q; want the seat's token sent", errOut)
	}
	mu.Lock()
	sent := append([]string(nil), auth...)
	mu.Unlock()
	if len(sent) == 0 || sent[0] != "Bearer "+tok {
		t.Fatalf("land pr sent %d request(s); want the first to carry the seat's token", len(sent))
	}
	if _, _, errOut := ns(emptyGH, "--seat", "reader", "land", "pr", "--pr", "1", "--repo", "o/r", "--api", api.URL); strings.Count(errOut, "names no GitHub token env") != 1 {
		t.Fatalf("land pr under a six-column row: stderr %q; want the old-behaviour line once", errOut)
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
	if code, _, errOut := ns(noSeatAddr, "--seat", "ghost", "redis", "PING"); code != 2 || !strings.Contains(errOut, seats) {
		t.Fatalf("ghost seat: exit %d stderr %q; want 2 naming %s", code, errOut, seats)
	}
}
