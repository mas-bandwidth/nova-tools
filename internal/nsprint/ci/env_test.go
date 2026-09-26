package ci_test

// The 2026-09-25 Studio defect: ci run ran its checks with the bench seat's
// NOVA_SPRINT_REDIS_* environment, so every redis-backed test in the checked
// repo authenticated as the seat against its own throwaway server and failed
// WRONGPASS. A check that prints its environment must see none of the seat's
// Redis or secrets variables, and the run names what it dropped, never a
// value.

import (
	"reflect"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ci"
)

func TestCheckEnvDropsTheSeatVariables(t *testing.T) {
	t.Parallel()

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
