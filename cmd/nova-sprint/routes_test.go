package main

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/route"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// TestRoutesCheckReadsFold is nova-tools #3178 rev 4: `nova-sprint routes
// --check --type <t>` reads routes:<t> (the fold's record) and answers with
// CheckFold, through the built binary, so flag parsing, the NOVA_SPRINT_REDIS
// default and store.Open are exercised. The binary is built once at the top
// of the test and every subtest execs it with an explicit environment that
// carries no NOVA_SPRINT_REDIS* unless the case sets it. Redis is the
// throwaway server testutil.Start runs; the fold is seeded with HSET of
// (*route.Fold).Fields(), standing in for the fold's record script.
func TestRoutesCheckReadsFold(t *testing.T) {
	t.Parallel()

	tab, err := route.Load()
	if err != nil {
		t.Fatal(err)
	}
	// Precondition (rowan C8): the controls rely on ocglm53 and ordspro being
	// dropped pro routes that are not dead, so the static answer refuses both
	// and CheckFold reaches the fold for both.
	rows := map[string]route.Row{}
	for _, r := range tab.Rows() {
		rows[r.Route] = r
	}
	for _, r := range []string{"ocglm53", "ordspro"} {
		row := rows[r]
		err := tab.Check(route.Card{Rung: "pro", Type: "fix", Route: r})
		if err == nil || !strings.Contains(err.Error(), r+" is dropped:") || row.Flag == route.FlagDead {
			t.Fatalf("precondition: routes.yaml changed: %s is %s/%s, want dropped and not dead; re-pick the test routes", r, row.State, dash(row.Flag))
		}
	}

	bin := buildRoutesBinary(t)
	foldAddr := testutil.Start(t)
	emptyAddr := testutil.Start(t)
	badAddr := testutil.Start(t)
	closed := closedAddr(t)

	fold := &route.Fold{
		Type: "fix", Kind: route.KindPR, Metric: route.MetricLanded, Sprint: "s1",
		FoldSHA: "0123456789abcdef", At: "2026-09-23T21:00:00Z",
		ProbationShare: "0.1", RouteMinLanded: 3, RouteBenchAfter: 15,
		Routes: []route.FoldRoute{
			{Route: "ordspro", Status: route.StatusIn, Metric: "5", Cards: 3, PRs: 3, Landed: 3, Useful: 3, USD: "15", Reason: "landed 3 of 3"},
			{Route: "ocglm53", Status: route.StatusBenched, Metric: "-", Cards: 40, PRs: 40, USD: "0.4", Reason: "landed 0 of 40"},
		},
	}
	seedFold(t, foldAddr, fold.Fields())
	var bad []string
	kv := fold.Fields()
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i] != "fold_sha" {
			bad = append(bad, kv[i], kv[i+1])
		}
	}
	seedFold(t, badAddr, bad)

	static := func(r, typ string) (int, string) {
		if err := tab.Check(route.Card{Rung: "pro", Type: typ, Route: r}); err != nil {
			return 1, err.Error() + "\n"
		}
		return 0, "OK route=" + r + " rung=pro type=" + dash(typ) + "\n"
	}
	const needs = "--check needs --redis"

	t.Run("benched_refused", func(t *testing.T) {
		code, out, errOut := runRoutes(t, bin, "", "--check", "ocglm53", "--rung", "pro", "--type", "fix", "--redis", foldAddr)
		if code != 1 || !strings.Contains(out, "landed 0 of 40") {
			t.Fatalf("exit %d stdout %q stderr %q; want exit 1 and the fold's reason landed 0 of 40", code, out, errOut)
		}
	})
	t.Run("ordspro_ok", func(t *testing.T) {
		code, out, errOut := runRoutes(t, bin, "", "--check", "ordspro", "--rung", "pro", "--type", "fix", "--redis", foldAddr)
		if code != 0 || out != "OK route=ordspro rung=pro type=fix\n" {
			t.Fatalf("exit %d stdout %q stderr %q; want exit 0 and OK route=ordspro rung=pro type=fix", code, out, errOut)
		}
	})
	t.Run("env_form", func(t *testing.T) {
		code, out, errOut := runRoutes(t, bin, foldAddr, "--check", "ocglm53", "--rung", "pro", "--type", "fix")
		if code != 1 || !strings.Contains(out, "landed 0 of 40") {
			t.Fatalf("NOVA_SPRINT_REDIS form: exit %d stdout %q stderr %q; want exit 1 and landed 0 of 40", code, out, errOut)
		}
	})
	t.Run("no_fold_equals_static", func(t *testing.T) {
		for _, r := range []string{"ocglm53", "ordspro"} {
			wantCode, wantOut := static(r, "fix")
			code, out, errOut := runRoutes(t, bin, "", "--check", r, "--rung", "pro", "--type", "fix", "--redis", emptyAddr)
			if code != wantCode || out != wantOut {
				t.Errorf("%s with no routes:fix: exit %d stdout %q stderr %q; want the static exit %d stdout %q", r, code, out, errOut, wantCode, wantOut)
			}
		}
	})
	t.Run("no_type_static_no_read", func(t *testing.T) {
		wantCode, wantOut := static("ocglm53", "")
		for name, args := range map[string][]string{
			"closed --redis":       {"--check", "ocglm53", "--rung", "pro", "--redis", closed},
			"neither flag nor env": {"--check", "ocglm53", "--rung", "pro"},
		} {
			code, out, errOut := runRoutes(t, bin, "", args...)
			if code != wantCode || out != wantOut || strings.Contains(errOut, needs) {
				t.Errorf("%s: exit %d stdout %q stderr %q; want the static exit %d stdout %q and no store opened", name, code, out, errOut, wantCode, wantOut)
			}
		}
	})
	t.Run("no_addr_exit2", func(t *testing.T) {
		code, out, errOut := runRoutes(t, bin, "", "--check", "ocglm53", "--rung", "pro", "--type", "fix")
		if code != 2 || out != "" || !strings.Contains(errOut, "routes: "+needs+" <host:port> (or NOVA_SPRINT_REDIS)") {
			t.Fatalf("exit %d stdout %q stderr %q; want exit 2, empty stdout and the needs --redis line", code, out, errOut)
		}
	})
	t.Run("unreachable_exit2", func(t *testing.T) {
		code, out, errOut := runRoutes(t, bin, "", "--check", "ocglm53", "--rung", "pro", "--type", "fix", "--redis", closed)
		if code != 2 || out != "" || !strings.Contains(errOut, closed) {
			t.Fatalf("exit %d stdout %q stderr %q; want exit 2, empty stdout (no static answer) and the address %s on stderr", code, out, errOut, closed)
		}
	})
	t.Run("malformed_fold_exit2", func(t *testing.T) {
		code, out, errOut := runRoutes(t, bin, "", "--check", "ocglm53", "--rung", "pro", "--type", "fix", "--redis", badAddr)
		if code != 2 || out != "" || !strings.Contains(errOut, "no fold_sha") {
			t.Fatalf("exit %d stdout %q stderr %q; want exit 2, empty stdout (no static answer) and no fold_sha on stderr", code, out, errOut)
		}
	})
	t.Run("no_check_table", func(t *testing.T) {
		code, out, errOut := runRoutes(t, bin, "")
		if code != 0 || out != tab.Render() {
			t.Errorf("routes: exit %d stderr %q, stdout equal to tab.Render() %v; want exit 0 and the table", code, errOut, out == tab.Render())
		}
		want := "ALLOWED pro fix " + strings.Join(tab.Allowed("pro", "fix"), " ") + "\n"
		code, out, errOut = runRoutes(t, bin, "", "--rung", "pro", "--type", "fix", "--redis", closed)
		if code != 0 || out != want {
			t.Errorf("routes --rung pro --type fix --redis <closed>: exit %d stdout %q stderr %q; want exit 0 and %q (no store opened)", code, out, errOut, want)
		}
	})
}

// buildRoutesBinary builds ./cmd/nova-sprint once for the test's subtests.
func buildRoutesBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "nova-sprint")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Env = goenv.Clean(os.Environ())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/nova-sprint: %v\n%s", err, out)
	}
	return bin
}

// runRoutes execs `nova-sprint routes <args>` with the caller's environment
// minus every NOVA_SPRINT_REDIS* variable; env, when set, is the case's
// NOVA_SPRINT_REDIS.
func runRoutes(t *testing.T, bin, env string, args ...string) (int, string, string) {
	t.Helper()
	var keep []string
	for _, e := range os.Environ() {
		name, _, _ := strings.Cut(e, "=")
		if name == "NOVA_SPRINT_REDIS" || name == store.UserEnv || name == store.PasswordEnvEnv {
			continue
		}
		keep = append(keep, e)
	}
	if env != "" {
		keep = append(keep, "NOVA_SPRINT_REDIS="+env)
	}
	cmd := exec.Command(bin, append([]string{"routes"}, args...)...)
	cmd.Env = keep
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0, out.String(), errOut.String()
	case errors.As(err, &exit):
		return exit.ExitCode(), out.String(), errOut.String()
	}
	t.Fatalf("exec %s routes %v: %v", bin, args, err)
	return -1, "", ""
}

// seedFold writes routes:fix the way the fold's record script does: one HSET.
func seedFold(t *testing.T, addr string, kv []string) {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: addr})
	defer client.Close()
	args := make([]any, len(kv))
	for i, v := range kv {
		args[i] = v
	}
	if err := client.HSet(context.Background(), route.FoldKey("fix"), args...).Err(); err != nil {
		t.Fatalf("seed %s at %s: %v", route.FoldKey("fix"), addr, err)
	}
}

// closedAddr is a loopback port that was listening and is closed: a dial is
// refused at once and no real host is dialled.
func closedAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

// TestRoutesPreamblePrintsTheParagraph is #2498 S8: `nova-sprint routes
// --preamble <route>` prints the route's one paragraph for a card front to
// inline, and refuses (exit 1, nothing on stdout) a route that carries none.
func TestRoutesPreamblePrintsTheParagraph(t *testing.T) {
	t.Parallel()

	tab, err := route.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range tab.Reachable("pro") {
		want, err := tab.Preamble(r)
		if err != nil {
			t.Fatalf("%s: %v", r, err)
		}
		var out, errb bytes.Buffer
		if code := cmdRoutes(context.Background(), []string{"--preamble", r}, &out, &errb); code != 0 {
			t.Fatalf("--preamble %s: exit %d, stderr %q", r, code, errb.String())
		}
		if out.String() != want+"\n" {
			t.Errorf("--preamble %s printed %q, want %q", r, out.String(), want+"\n")
		}
	}
	for _, r := range []string{"ormuse", "no-such-route"} {
		var out, errb bytes.Buffer
		if code := cmdRoutes(context.Background(), []string{"--preamble", r}, &out, &errb); code != 1 || out.Len() != 0 || !strings.Contains(errb.String(), r) {
			t.Errorf("--preamble %s: exit %d stdout %q stderr %q, want exit 1, empty stdout, a refusal naming the route", r, code, out.String(), errb.String())
		}
	}
	var out, errb bytes.Buffer
	if code := cmdRoutes(context.Background(), []string{"--preamble", "ordspro", "--rung", "pro"}, &out, &errb); code != 2 {
		t.Errorf("--preamble with --rung: exit %d, want 2 (it takes no other flag)", code)
	}
}
