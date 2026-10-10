package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/testbin"
)

// THE ENVIRONMENT SEAM, AT THE UNIT: the reads one native run and one worker check make of
// the environment go through a getenv func (and, for the whole environment a child is
// handed, an environ slice) the run or the check holds, so the value is a field on the
// value under test and not a t.Setenv that changes the whole process and panics under
// t.Parallel. Production hands os.Getenv and os.Environ; these tests hand their own.

// TestEnvSeamBackoffComesFromTheRunNotTheProcess pins NOVA_SWARM_PROVIDER_BACKOFF on the
// run: both the failed-start wait and the provider retry read the run's pin, and it is not
// the process's. The process's own pin is TestMain's 0s, so a test that read the process
// would get 0s and this is red.
func TestEnvSeamBackoffComesFromTheRunNotTheProcess(t *testing.T) {
	t.Parallel()

	cfg := nativeRunConfig{getenv: func(name string) string {
		if name == "NOVA_SWARM_PROVIDER_BACKOFF" {
			return "250ms"
		}
		return ""
	}}
	require.Equal(t, 250*time.Millisecond, startWait(cfg.env, time.Second),
		"the failed-start wait is the run's pin, not the schedule entry")
	require.Equal(t, 250*time.Millisecond, providerRetryWait(cfg.env, 1),
		"the provider retry reads the same run pin")
	require.NotEqual(t, startWait(cfg.env, time.Second), startWait(os.Getenv, time.Second),
		"the run's pin and the process's differ, so the run's was the one read")
}

// TestEnvSeamShimPathComesFromTheRun pins PATH on the run: the bench's go and the bench's
// toolchain directories are resolved from the run's PATH, not the process's. A fake go
// sits in a directory the process's PATH does not name, so a read of the process would
// resolve no go and this is red.
func TestEnvSeamShimPathComesFromTheRun(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, testbin.WriteExecutable(filepath.Join(dir, "go"), []byte("#!/bin/sh\nexit 0\n"), 0o755))
	cfg := nativeRunConfig{benchHome: t.TempDir(), getenv: func(name string) string {
		if name == "PATH" {
			return dir
		}
		return ""
	}}
	require.Equal(t, dir, nativeGoBin(cfg), "the bench go is resolved on the run's PATH, not the process's")
	require.Contains(t, nativeToolPath(cfg), dir, "the toolchain path is built from the run's PATH")
	require.NotContains(t, filepath.SplitList(os.Getenv("PATH")), dir,
		"the process PATH does not name the run's directory, so the run's was the one read")
}

// TestEnvSeamTwoChecksWithDifferentSecretsDoNotSeeEachOther pins each worker check's own
// environment: two checks built with different getenv funcs each answer their own secret,
// and a check handed the other's environment names the secret it cannot see. No process
// environment is touched, so the two checks run side by side.
func TestEnvSeamTwoChecksWithDifferentSecretsDoNotSeeEachOther(t *testing.T) {
	t.Parallel()

	pathA := workerCheckFixture(t, func(d map[string]any) {
		delete(d, "key_file")
		d["secret"] = "NOVA_ENV_SEAM_SECRET_A"
	})
	pathB := workerCheckFixture(t, func(d map[string]any) {
		delete(d, "key_file")
		d["secret"] = "NOVA_ENV_SEAM_SECRET_B"
	})
	envA := func(name string) string {
		if name == "NOVA_ENV_SEAM_SECRET_A" {
			return "sk-" + "a"
		}
		return ""
	}
	envB := func(name string) string {
		if name == "NOVA_ENV_SEAM_SECRET_B" {
			return "sk-" + "b"
		}
		return ""
	}
	_, driftsA := checkWorkerDescription(pathA, true, envA)
	_, driftsB := checkWorkerDescription(pathB, true, envB)
	require.Empty(t, driftsA, "check A answers A's secret")
	require.Empty(t, driftsB, "check B answers B's secret")
	_, cross := checkWorkerDescription(pathA, true, envB)
	require.NotEmpty(t, cross, "check A does not answer B's secret")
	require.Contains(t, cross[0].String(), "secret", "the drift names the secret field")
}
