package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/doctor"
	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/mas-bandwidth/nova-tools/internal/secrets"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// doctorServeEnv makes only the connection absent. All other Env methods are
// blocked behind that first dependency (SPEC-DOCTOR, Jobs).
type doctorServeEnv struct {
	doctor.OSEnv
	home     string
	password string
}

func (e doctorServeEnv) Getenv(k string) string {
	if k == PasswordEnv {
		return e.password
	}
	if k == "HOME" {
		return e.home
	}
	return ""
}
func (doctorServeEnv) Now() time.Time { return time.Unix(1, 0) }
func (doctorServeEnv) Dial(context.Context, string, string) error {
	return errors.New("connection refused")
}

// Drive the real serve entry point with the doctor's printed repair, rather than
// a fake shell that accepts missing authentication (SPEC-DOCTOR, Jobs).
func TestDoctorRedisRepairIsAcceptedByRealServe(t *testing.T) {
	t.Parallel()
	h := newServeHarness(t, "")
	l := secrets.Login{Store: "/secrets/work trees", As: "store-seat", Key: "/keys/store", Sops: "/bin/sops", Name: PasswordEnv}
	rep, err := doctor.RunJob(context.Background(), doctorServeEnv{home: t.TempDir()}, doctor.JobInput{Job: "local-notes", Redis: "127.0.0.1:6390", RedisLogin: l}, false)
	require.NoError(t, err)
	assert.Equal(t, "redis-reachable", rep.FirstMissing)
	words, err := onboarding.SplitShell(rep.Next)
	require.NoError(t, err)
	require.Equal(t, "nova-redis", words[0])
	reads := 0
	h.d.readLogin = func(got secrets.Login) (secrets.Secret, error) {
		reads++
		assert.Equal(t, l, got)
		return fixtureSecret(t, "Q!7#x@9%p^2!"), nil
	}
	code, out, stderr := h.run(words[1:]...)
	assert.Equal(t, 0, code, stderr)
	assert.Contains(t, out, "SERVE STOP")
	assert.Equal(t, 1, reads)
	assert.Len(t, h.launches, 1, "only the fake launch seam runs; no server starts")
}

// The existing injected-password path stays valid without new login metadata;
// the doctor never prints that password (SPEC-DOCTOR, Jobs).
func TestDoctorRedisRepairKeepsInjectedAuthentication(t *testing.T) {
	t.Parallel()
	password := "Q!7#x@9%p^2!"
	h := newServeHarness(t, password)
	rep, err := doctor.RunJob(context.Background(), doctorServeEnv{home: t.TempDir(), password: password}, doctor.JobInput{Job: "local-notes", Redis: "127.0.0.1:6390"}, false)
	require.NoError(t, err)
	assert.NotContains(t, rep.Next, password)
	assert.NotContains(t, rep.Next, "--secrets")
	words, err := onboarding.SplitShell(rep.Next)
	require.NoError(t, err)
	code, _, stderr := h.run(words[1:]...)
	assert.Equal(t, 0, code, stderr)
	assert.Len(t, h.launches, 1)
}
