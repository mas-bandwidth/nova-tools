//go:build functional

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/testredis"
)

// tableMainEnv, set to 1, makes TestNovaTableMain be nova-table's main().
const tableMainEnv = "NOVA_TABLE_TEST_MAIN"

// TestNovaTableMain is nova-table's main() with the arguments after "--",
// when tableMainEnv is 1; otherwise it returns at once. Only
// TestFreshStoreFirstVerbLoadsTheLibrary runs it that way, so each nova-table
// it runs is a fresh process, whose first contact has not been made.
func TestNovaTableMain(t *testing.T) {
	t.Parallel()
	if os.Getenv(tableMainEnv) != "1" {
		return
	}
	for i, a := range os.Args {
		if a == "--" {
			os.Exit(run(os.Args[i+1:], os.Stdout, os.Stderr))
		}
	}
	os.Exit(2)
}

// novaTable runs nova-table as its own process, in an environment scrubbed
// to tableMainEnv and an empty HOME, so no seat or login of the parent
// reaches it.
func novaTable(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], append([]string{"-test.run=^TestNovaTableMain$", "--"}, args...)...)
	cmd.Env = []string{tableMainEnv + "=1", "HOME=" + t.TempDir()}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0, out.String(), errb.String()
	case errors.As(err, &exit):
		return exit.ExitCode(), out.String(), errb.String()
	}
	t.Fatalf("nova-table %q: %v", args, err)
	return 0, "", ""
}

// TestFreshStoreFirstVerbLoadsTheLibrary: on a redis-server that holds no
// function library, the first nova-table verb puts this build's nova_sprint
// on the store (LoadMissing) and succeeds; its trips= counts the miss, the
// FUNCTION LIST, the FUNCTION LOAD and the verb sent again. The next process's
// verb is one round trip. A store whose nova_sprint is an older build that
// lacks the verb's function is never replaced: the verb is refused in one
// line, exit 1, naming the deployer's remedy.
func TestFreshStoreFirstVerbLoadsTheLibrary(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	addr := testredis.Start(t)
	admin := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = admin.Close() })
	if libs, err := admin.FunctionList(ctx, redis.FunctionListQuery{}).Result(); err != nil || len(libs) != 0 {
		t.Fatalf("the fresh store holds %v (%v); want no library", libs, err)
	}

	code, out, errOut := novaTable(t, "create", "demo", "--columns", "ready,working,done", "--redis", addr)
	if code != 0 || out != "TABLE CREATE table=demo columns=3 trips=4\n" || errOut != "" {
		t.Fatalf("create on a fresh store: exit %d stdout %q stderr %q; want TABLE CREATE with trips=4", code, out, errOut)
	}
	libs, err := admin.FunctionList(ctx, redis.FunctionListQuery{LibraryNamePattern: "nova_sprint", WithCode: true}).Result()
	if err != nil || len(libs) != 1 {
		t.Fatalf("FUNCTION LIST after the first verb: %v %v", libs, err)
	}
	want, err := tableLibrary().Source()
	if err != nil {
		t.Fatal(err)
	}
	if libs[0].Code != want {
		t.Fatalf("the store holds %d bytes of nova_sprint; want this build's %d", len(libs[0].Code), len(want))
	}

	code, out, errOut = novaTable(t, "row", "add", "demo", "build", "--redis", addr)
	if code != 0 || out != "TABLE ROW ADD table=demo row=build cols=3 bound=0 trips=1\n" || errOut != "" {
		t.Fatalf("the next process on the loaded store: exit %d stdout %q stderr %q; want one round trip", code, out, errOut)
	}

	older := testredis.Start(t)
	oc := redis.NewClient(&redis.Options{Addr: older})
	t.Cleanup(func() { _ = oc.Close() })
	const old = "#!lua name=nova_sprint\nredis.register_function('ns_ping', function() return 'PONG' end)\n"
	if err := oc.FunctionLoad(ctx, old).Err(); err != nil {
		t.Fatal(err)
	}
	code, out, errOut = novaTable(t, "create", "demo", "--columns", "ready,done", "--redis", older)
	if code != 1 || out != "" || strings.Count(errOut, "\n") != 1 ||
		!strings.Contains(errOut, "ERR Function not found: function ns_table_create") ||
		!strings.Contains(errOut, "older than this nova-table") || !strings.Contains(errOut, "run: nova-redis fn load --addr <host:port>") {
		t.Fatalf("create on a store with an older nova_sprint: exit %d stdout %q stderr %q; want one refusal naming the deployer's remedy", code, out, errOut)
	}
	libs, err = oc.FunctionList(ctx, redis.FunctionListQuery{LibraryNamePattern: "nova_sprint", WithCode: true}).Result()
	if err != nil || len(libs) != 1 || libs[0].Code != old {
		t.Fatalf("the older library after the refusal: %v %v; want it as it was, never replaced", libs, err)
	}
}
