package main

import (
	"bytes"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func setTestResourcesQuery(fn func() (string, error)) func() {
	resourcesSeamMu.Lock()
	old := queryReserveRaw
	queryReserveRaw = fn
	resourcesSeamMu.Unlock()
	return func() {
		resourcesSeamMu.Lock()
		queryReserveRaw = old
		resourcesSeamMu.Unlock()
	}
}

func TestResourcesFormatsSnapshotAndJSON(t *testing.T) {
	const sample = `{"op":"snapshot","reconnect_until":"","machines":[{"host":"space","online":true,"tags":["amd64","linux"],"usable_cores":32,"resources":{"cpu":24,"memory":54},"free":{"cpu":16,"memory":38},"leases":[{"paused":false,"sublease":false,"needs":{"cpu":8,"memory":16}},{"paused":true,"sublease":false,"needs":{"cpu":4,"memory":8}}],"claims":[],"queue":[{}]}]}`
	restore := setTestResourcesQuery(func() (string, error) { return sample, nil })
	defer restore()

	var out, err bytes.Buffer
	code := runResources(nil, &out, &err)
	assert.Equal(t, 0, code)
	assert.Empty(t, err.String())
	assert.Equal(t, "RESOURCE space status=ok cpu=8/24 memory=16/54 free_cpu=16 free_memory=38 leases=2 claims=0 queued=1 tags=amd64,linux\n", out.String())

	out.Reset()
	code = runResources([]string{"--json"}, &out, &err)
	assert.Equal(t, 0, code)
	assert.Equal(t, sample+"\n", out.String())
}

func TestResourcesValidationAndErrors(t *testing.T) {
	var out, err bytes.Buffer
	assert.Equal(t, 0, runResources([]string{"-h"}, &out, &err))
	assert.Contains(t, out.String(), "nova-fleet resources")

	out.Reset()
	assert.Equal(t, 2, runResources([]string{"--bogus"}, &out, &err))
	assert.Contains(t, err.String(), "unknown flag")

	err.Reset()
	restore := setTestResourcesQuery(func() (string, error) { return "", errors.New("connection refused") })
	defer restore()
	assert.Equal(t, 2, runResources(nil, &out, &err))
	assert.Contains(t, err.String(), "connection refused")
}
