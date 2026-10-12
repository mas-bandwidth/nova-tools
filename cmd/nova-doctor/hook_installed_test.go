package main

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/doctor"
	"github.com/mas-bandwidth/nova-tools/pkg/buildinfo"
)

// hookEnv is a machine whose PATH holds the named nova-* tools, each an
// executable that answers `version` from versions. Nothing real runs and no
// file outside the test's own directories is read.
type hookEnv struct {
	path     string
	home     string
	versions map[string]string
}

func (e hookEnv) Getenv(k string) string {
	switch k {
	case "PATH":
		return e.path
	case "HOME":
		return e.home
	}
	return ""
}

func (hookEnv) Now() time.Time                            { return time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC) }
func (hookEnv) ReadFile(string) ([]byte, error)           { return nil, fs.ErrNotExist }
func (e hookEnv) ReadDir(p string) ([]fs.DirEntry, error) { return os.ReadDir(p) }
func (hookEnv) Dial(context.Context, string, string) error {
	return errors.New("the self check opens no socket")
}

func (e hookEnv) Exec(_ context.Context, name string, args ...string) (string, error) {
	if len(args) != 1 || args[0] != "version" {
		return "", errors.New("the self check asks `version` and nothing else")
	}
	v, ok := e.versions[filepath.Base(name)]
	if !ok {
		return "", os.ErrNotExist
	}
	return buildinfo.Line(filepath.Base(name), v), nil
}

// hookMachine is a PATH of one directory holding an executable per name and
// the version each answers.
func hookMachine(t *testing.T, versions map[string]string) hookEnv {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "bin")
	require.NoError(t, os.MkdirAll(bin, 0o755))
	for n := range versions {
		require.NoError(t, os.WriteFile(filepath.Join(bin, n), []byte("#!/bin/sh\n"), 0o755))
	}
	return hookEnv{path: bin, home: t.TempDir(), versions: versions}
}

// runSelf runs only the self check over env, as the binary runs it.
func runSelf(t *testing.T, env doctor.Env) (int, string, string) {
	t.Helper()
	var out, errs bytes.Buffer
	code := doctor.Main(doctor.Default, env, "", []string{"run", "--check", "self"}, strings.NewReader(""), &out, &errs)
	return code, out.String(), errs.String()
}

// A machine whose nova tools answer one release but that has no nova-hook on
// PATH is a fail (not a warn), naming nova-hook and the command that installs
// it: without the one way in, no bus, friend or sprint tool can run.
func TestDoctorFailsWhenNovaHookIsNotInstalled(t *testing.T) {
	t.Parallel()
	code, out, errs := runSelf(t, hookMachine(t, map[string]string{"nova-bus": "v1.2.0", "nova-cairn": "v1.2.0"}))
	assert.Equal(t, 2, code, out+errs)
	assert.Contains(t, out, "DOCTOR self fail")
	assert.Contains(t, out, "nova-hook is not on PATH")
	assert.Contains(t, out, "fix: nova-update apply --file <manifest> nova-hook --version v1.2.0")
}

// With nova-hook on PATH and every tool on the one release, the check is ok.
func TestDoctorPassesWhenNovaHookIsInstalled(t *testing.T) {
	t.Parallel()
	code, out, errs := runSelf(t, hookMachine(t, map[string]string{"nova-hook": "v1.2.0", "nova-bus": "v1.2.0", "nova-cairn": "v1.2.0"}))
	assert.Equal(t, 0, code, out+errs)
	assert.Contains(t, out, "DOCTOR self ok")
}

// A nova-hook whose version differs from the release the other tools report is
// a fail naming the skew and the command that brings it to the release.
func TestDoctorFailsWhenNovaHookIsAnotherRelease(t *testing.T) {
	t.Parallel()
	code, out, errs := runSelf(t, hookMachine(t, map[string]string{"nova-hook": "v1.1.0", "nova-bus": "v1.2.0", "nova-cairn": "v1.2.0"}))
	assert.Equal(t, 2, code, out+errs)
	assert.Contains(t, out, "DOCTOR self fail")
	assert.Contains(t, out, "nova-hook=v1.1.0")
	assert.Contains(t, out, "fix: nova-update apply --file <manifest> nova-hook --version v1.2.0")
}
