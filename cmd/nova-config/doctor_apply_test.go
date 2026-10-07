package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/doctor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// doctorApplyEnv records the actual check argv and makes only applied state
// stale, stopping before any binary/supervisor probe (SPEC-DOCTOR, Jobs).
type doctorApplyEnv struct {
	doctor.OSEnv
	apply []string
}

func (*doctorApplyEnv) Getenv(string) string                       { return "" }
func (*doctorApplyEnv) Now() time.Time                             { return time.Unix(1, 0) }
func (*doctorApplyEnv) Dial(context.Context, string, string) error { return nil }
func (e *doctorApplyEnv) Exec(_ context.Context, name string, args ...string) (string, error) {
	if name == "nova-config" && len(args) > 0 && args[0] == "apply" {
		e.apply = append([]string{}, args...)
		return "CONFIG CHECK kind=friend add=0 set=0 remove=0 rev=7 applied=6\n", nil
	}
	return "CHECK OK\n", nil
}

// The doctor's check must clear the real actor/argv checks. Opening the store
// is intercepted with a sentinel; no database or Redis connection is made.
func TestDoctorAppliedStateCheckPassesRealActorValidation(t *testing.T) {
	t.Parallel()
	e := &doctorApplyEnv{}
	rep, err := doctor.RunJob(context.Background(), e, doctor.JobInput{Job: "coordinator", As: "operator with space", Redis: "127.0.0.1:6390"}, false)
	require.NoError(t, err)
	assert.Equal(t, "config-applied", rep.FirstMissing)
	require.NotEmpty(t, e.apply)
	opened := 0
	d := deps{
		getenv: func(k string) string {
			if k == "NOVA_PG_DSN" {
				return "postgres://fixture/config"
			}
			return ""
		},
		openStore: func(context.Context, string) (pgStore, error) {
			opened++
			return nil, errors.New("test-store-sentinel")
		},
	}
	var out, stderr bytes.Buffer
	code := runApply(context.Background(), e.apply[1:], &out, &stderr, d)
	assert.Equal(t, 2, code)
	assert.Equal(t, 1, opened, "missing actor refuses before the store opener")
	assert.True(t, strings.Contains(stderr.String(), "test-store-sentinel"), stderr.String())
	assert.NotContains(t, stderr.String(), "--actor is required")
}
