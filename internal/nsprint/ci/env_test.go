package ci_test

// The 2026-09-25 Studio defect: ci run ran its checks with the bench seat's
// NOVA_SPRINT_REDIS_* environment, so every redis-backed test in the checked
// repo authenticated as the seat against its own throwaway server and failed
// WRONGPASS. A check that prints its environment must see none of the seat's
// Redis or secrets variables, and the run names what it dropped, never a
// value.

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ci"
)

func TestCheckEnvDropsTheSeatVariables(t *testing.T) {
	in := []string{
		"PATH=/usr/bin", "HOME=/h", "GOCACHE=/c", "GOFLAGS=-mod=mod", "TMPDIR=/t", "LANG=C", "USER=u",
		"NOVA_SPRINT_REDIS_USER=bench", "NOVA_SPRINT_REDIS_PASSWORD_ENV=SEAT_PW", "SEAT_PW=v1",
		"NOVA_REDIS_BENCH_PASSWORD=v2", "NOVA_SECRETS_FILE=v3", "REDISCLI_AUTH=v4", "NOVA_SEAT=studio", "NOVA_CI=1",
	}
	env, dropped := ci.CheckEnv(in)
	want := []string{"PATH=/usr/bin", "HOME=/h", "GOCACHE=/c", "GOFLAGS=-mod=mod", "TMPDIR=/t", "LANG=C", "USER=u", "NOVA_CI=1"}
	if !reflect.DeepEqual(env, want) {
		t.Fatalf("env = %v\nwant %v", env, want)
	}
	wantDropped := []string{"NOVA_REDIS_BENCH_PASSWORD", "NOVA_SEAT", "NOVA_SECRETS_FILE", "NOVA_SPRINT_REDIS_PASSWORD_ENV", "NOVA_SPRINT_REDIS_USER", "REDISCLI_AUTH", "SEAT_PW"}
	if !reflect.DeepEqual(dropped, wantDropped) {
		t.Fatalf("dropped = %v\nwant %v", dropped, wantDropped)
	}
}

func TestCIRunCheckDoesNotSeeTheSeatEnvironment(t *testing.T) {
	f := newRunFixture(t)
	// The fixture's client connects directly, so the seat variables below
	// reach only the runner's own environment, as they do on a bench.
	t.Setenv("NOVA_SPRINT_REDIS_USER", "bench")
	t.Setenv("NOVA_SPRINT_REDIS_PASSWORD_ENV", "SEAT_PW_FOR_TEST")
	t.Setenv("SEAT_PW_FOR_TEST", "value-one-never-printed")
	t.Setenv("NOVA_REDIS_BENCH_PASSWORD", "value-two-never-printed")
	t.Setenv("NOVA_SECRETS_ONLY", "value-three-never-printed")
	t.Setenv("REDISCLI_AUTH", "value-four-never-printed")
	t.Setenv("CI_ENV_KEEP_ME", "kept")
	f.client.HSet(f.ctx, ci.ConfigKey(runRepo), "checks", "env", "check:env", "env")

	if _, err := ci.Request(f.ctx, f.st, ci.RequestRequest{Repo: runRepo, SHA: f.sha, URL: f.url}); err != nil {
		t.Fatalf("request: %v", err)
	}
	res, out, err := f.run(t, "b1")
	if err != nil || res.Summary != ci.SummaryGreen || len(res.Checks) != 1 {
		t.Fatalf("run = %+v, %v\n%s", res, err, out)
	}
	logBody, err := os.ReadFile(res.Checks[0].Log)
	if err != nil {
		t.Fatal(err)
	}
	got := string(logBody)
	for _, name := range []string{"NOVA_SPRINT_", "NOVA_REDIS_", "NOVA_SECRETS_", "REDISCLI_AUTH=", "SEAT_PW_FOR_TEST=", "never-printed"} {
		if strings.Contains(got, name) {
			t.Fatalf("check environment has %q:\n%s", name, got)
		}
	}
	for _, kept := range []string{"CI_ENV_KEEP_ME=kept", "PATH=", "CI=1", "GIT_TERMINAL_PROMPT=0"} {
		if !strings.Contains(got, kept) {
			t.Fatalf("check environment lacks %q:\n%s", kept, got)
		}
	}
	if strings.Count(out, "ENV scrubbed=") != 1 {
		t.Fatalf("run output has no single ENV line:\n%s", out)
	}
	_, line, _ := strings.Cut(out, "ENV scrubbed=")
	line, _, _ = strings.Cut(line, "\n")
	names := map[string]bool{}
	for _, n := range strings.Split(line, ",") {
		names[n] = true
	}
	for _, n := range []string{"NOVA_REDIS_BENCH_PASSWORD", "NOVA_SECRETS_ONLY", "NOVA_SPRINT_REDIS_PASSWORD_ENV", "NOVA_SPRINT_REDIS_USER", "REDISCLI_AUTH", "SEAT_PW_FOR_TEST"} {
		if !names[n] {
			t.Fatalf("ENV line %q does not name %s", line, n)
		}
	}
	if strings.Contains(out, "never-printed") {
		t.Fatalf("run output prints a value:\n%s", out)
	}
}
