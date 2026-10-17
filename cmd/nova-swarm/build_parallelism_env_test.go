package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestTheChildStartsWithTheBuildParallelism: every card's child carries the default build
// parallelism exactly once (nativeBuildParallelism), whatever the caller exported, and
// GOMAXPROCS is not set by it.
func TestTheChildStartsWithTheBuildParallelism(t *testing.T) {
	t.Parallel()
	for _, environ := range [][]string{
		nil,
		{"GOFLAGS=-p=64", "MAKEFLAGS=-j64", "CMAKE_BUILD_PARALLEL_LEVEL=64"},
	} {
		env := nativeChildEnvFrom(environ, "data", "job", "tmp", "", "", "", "", nil)
		got := map[string][]string{}
		for _, kv := range env {
			name, val, _ := strings.Cut(kv, "=")
			got[name] = append(got[name], val)
		}
		assert.Equal(t, []string{"-p=4"}, got["GOFLAGS"], "from %v", environ)
		assert.Equal(t, []string{"-j8"}, got["MAKEFLAGS"], "from %v", environ)
		assert.Equal(t, []string{"8"}, got["CMAKE_BUILD_PARALLEL_LEVEL"], "from %v", environ)
		assert.Empty(t, got["GOMAXPROCS"], "GOMAXPROCS is left alone")
	}
}
