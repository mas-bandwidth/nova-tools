package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// withEnv replaces a variable the environment holds and adds one it does not, the rest
// in place; gateRuns is the build and the vet, then the tree tests the clone holds when
// asked; treeTested is a document or a test file; gateWhy is one line of the run, how it
// ended and its output.
func TestTreeGateWords(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []string{"GOFLAGS=-mod=readonly", "PATH=/bin", "NOVA_CI_UPDATE=1"},
		withEnv([]string{"GOFLAGS=-mod=mod", "PATH=/bin"}, "GOFLAGS=-mod=readonly", "NOVA_CI_UPDATE=1"))
	assert.Equal(t, []string{"GOFLAGS=-mod=readonly", "PATH=/bin"},
		withEnv([]string{"GOFLAGS=-mod=vendor", "PATH=/bin", "GOFLAGS=-mod=mod"}, "GOFLAGS=-mod=readonly"))
	assert.Equal(t, []string{"GOFLAGS=-mod=readonly"}, withEnv(nil, "GOFLAGS=-mod=readonly"))
	assert.Equal(t, "GOFLAGS=-mod=readonly", readonlyGoFlags(nil))
	assert.Equal(t, "GOFLAGS=-tags=custom -mod=readonly", readonlyGoFlags([]string{"GOFLAGS=-tags=custom -mod=mod"}))
	assert.Equal(t, "GOFLAGS=-tags=custom -count=1 -mod=readonly",
		readonlyGoFlags([]string{"GOFLAGS=-tags=custom", "PATH=/bin", "GOFLAGS=-count=1 -mod=vendor"}))
	assert.Equal(t, [][]string{{"go", "build", "./..."}, {"go", "vet", "./..."}}, gateRuns(false, []string{"internal/docs"}))
	assert.Equal(t, [][]string{{"go", "build", "./..."}, {"go", "vet", "./..."}}, gateRuns(true, nil))
	assert.Equal(t, [][]string{{"go", "build", "./..."}, {"go", "vet", "./..."}, {"go", "test", "./internal/docs/", "./internal/ci/"}},
		gateRuns(true, []string{"internal/docs", "internal/ci"}))
	for p, want := range map[string]bool{
		"docs/CLI.md":            true,
		"a/b_test.go":            true,
		"a/b.go":                 true,
		"a/test.go":              true,
		"internal/ci/testdata/x": true,
		"testdata/cases.txt":     true,
		"README":                 false,
		"x.mdx":                  false,
		"notes.txt":              false,
	} {
		assert.Equal(t, want, treeTested(p), p)
	}
	assert.Equal(t, "go vet ./...: exit status 1: # example.com/m | ./bad.go:5:2: Printf format %d has arg \"s\" of wrong type string",
		gateWhy([]string{"go", "vet", "./..."}, errors.New("exit status 1"), "# example.com/m\n./bad.go:5:2: Printf format %d has arg \"s\" of wrong type string\n\n"))
	long := gateWhy([]string{"go", "build", "./..."}, errors.New("exit status 2"), strings.Repeat("x", 2000))
	assert.Less(t, len(long), 1600, "the output is capped")
}
