package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNovaSprintGcCoverDetail(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		line string
		want bool
	}{
		{"GC REMOVED class=jobs", true},
		{"GC WOULD-REMOVE class=jobs", true},
		{"GC KEPT class=jobs", true},
		{"GC OK freed=0 volume=3%", false},
		{"GC jobs count=1 bytes=2", false},
		{"", false},
	} {
		t.Run(tc.line, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, gcDetail(tc.line))
		})
	}
}

func TestNovaSprintGcCoverRunnerFailures(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		code int
		err  error
		want string
	}{
		{"runner does not start", 0, errors.New("no runner"), "GC FAILED machine=bench-a: the fleet runner did not start"},
		{"runner exits one", 1, nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var stderr bytes.Buffer
			code := gcOn(context.Background(), func(context.Context, string, string, io.Writer, io.Writer) (int, error) {
				return tc.code, tc.err
			}, "bench-a", false, "2d", "", io.Discard, &stderr)
			assert.Equal(t, 1, code)
			assert.Contains(t, stderr.String(), tc.want)
		})
	}
}

func TestNovaSprintGcCoverLocalVolume(t *testing.T) {
	t.Parallel()
	a, home, _, _ := gcApp(t)
	line := a.gcLocalVolume()
	assert.Regexp(t, regexp.MustCompile(`^GC OK freed=0 volume=[0-9]+%$`), line)
	_, ok := sprint.GCVolumeIn(line)
	assert.True(t, ok)
	a.getenv = func(string) string { return filepath.Join(home, "missing") }
	assert.Equal(t, "", a.gcLocalVolume())
}

func TestNovaSprintGcCoverIsLocal(t *testing.T) {
	t.Parallel()
	host, err := os.Hostname()
	require.NoError(t, err)
	short, _, _ := strings.Cut(host, ".")
	for _, tc := range []struct {
		name string
		want bool
	}{{"localhost", true}, {host, true}, {short, true}, {"no-such-host.invalid", false}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, gcIsLocal(tc.name))
		})
	}
}

func TestNovaSprintGcCoverJSONAndLocalMachine(t *testing.T) {
	t.Parallel()
	a, _, _, _ := gcApp(t)
	code, out, stderr := gcRun(a, "--json", "--dry-run")
	require.Equal(t, 0, code, stderr)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	for _, key := range []string{"freed", "volume", "dry_run"} {
		assert.Contains(t, out, `"`+key+`"`)
	}
	assert.Contains(t, out, `"dry_run":true`)

	code, out, stderr = gcRun(a, "--machine", "localhost")
	assert.Equal(t, 0, code, stderr)
	assert.Contains(t, out, "GC OK freed=0 volume=")
}
