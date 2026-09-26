//go:build functional

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// TestCensusSprintVerb runs `census --sprint` end to end as a user that may
// not SORT: TSV header, one row per card, the CENSUS receipt with
// round_trips=1, exit 0; flag misuse exits 2 before any Redis read; a
// wrong-type index exits 1 REFUSED.
func TestCensusSprintVerb(t *testing.T) {
	ctx := context.Background()
	for name, args := range map[string][]string{
		"keys without sprint":  {"--redis", "127.0.0.1:1", "--keys", "queued"},
		"sprint with fields":   {"--redis", "127.0.0.1:1", "--sprint", "s1", "--fields", "state"},
		"sprint with set":      {"--redis", "127.0.0.1:1", "--sprint", "s1", "--set", "benches"},
		"sprint with a glob":   {"--redis", "127.0.0.1:1", "--sprint", "s*"},
		"sprint without redis": {"--sprint", "s1"},
	} {
		var out, errOut bytes.Buffer
		if code := runCensus(ctx, args, &out, &errOut); code != 2 || out.Len() != 0 {
			t.Errorf("%s: exit %d, stdout %q; want 2 and nothing", name, code, out.String())
		}
	}

	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	defer client.Close()
	if err := fn.Load(ctx, client); err != nil {
		t.Fatalf("load nova_sprint library: %v", err)
	}
	// The verb runs as the coordinator seat without SORT (#3620): +@all minus
	// @dangerous. A census that sent SORT would exit 1 NOPERM here.
	const user, pass = "coordinator", "ctl-3037"
	if err := client.Do(ctx, "ACL", "SETUSER", user, "reset", "on", ">"+pass,
		"~*", "&*", "+@all", "-@dangerous").Err(); err != nil {
		t.Fatalf("ACL SETUSER: %v", err)
	}
	if v, err := client.Do(ctx, "ACL", "DRYRUN", user, "SORT", "k").Text(); err != nil || v == "OK" {
		t.Fatalf("the control user may SORT (%q, %v); the control needs the seat without it", v, err)
	}
	t.Setenv(store.UserEnv, user)
	t.Setenv(store.PasswordEnvEnv, "NOVA_CENSUS_TEST_PASSWORD")
	t.Setenv("NOVA_CENSUS_TEST_PASSWORD", pass)
	pipe := client.Pipeline()
	pipe.HSet(ctx, "s:v1:card:a1", "state", "running", "bench", "b1", "route", "pro", "attempt", "2")
	pipe.SAdd(ctx, "s:v1:idx:card:running", "a1")
	pipe.HSet(ctx, "s:v1:card:q1", "state", "queued")
	pipe.SAdd(ctx, "s:v1:idx:card:queued", "q1")
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := runCensus(ctx, []string{"--redis", addr, "--sprint", "v1"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	want := []string{"label\tstate\tbench\troute\tattempt\tage", "q1\tqueued\t-\t-\t-\t-", "a1\trunning\tb1\tpro\t2\t-"}
	if len(lines) != 4 || strings.Join(lines[:3], "\n") != strings.Join(want, "\n") ||
		!strings.HasPrefix(lines[3], "CENSUS sprint=v1 sets=12 cards=2 missing=0 dup=0 drift=0 round_trips=1 ms=") {
		t.Fatalf("census --sprint v1 printed %q", out.String())
	}

	if err := client.Set(ctx, "s:v1:idx:card:landed", "not-a-set", 0).Err(); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errOut.Reset()
	if code := runCensus(ctx, []string{"--redis", addr, "--sprint", "v1"}, &out, &errOut); code != 1 || out.Len() != 0 ||
		!strings.Contains(errOut.String(), "REFUSED") {
		t.Fatalf("a wrong-type index: exit %d, stdout %q, stderr %q; want 1, nothing, REFUSED", code, out.String(), errOut.String())
	}
}
