package main

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMachineTagsAndResources(t *testing.T) {
	t.Parallel()
	h := newHarness()
	h.ok(t, "machine", "add", "bench-a", "--user", "nova", "--os", "linux", "--arch", "amd64",
		"--cpu-units", "16", "--memory-gb", "64", "--tags", "gpu,metal", "--resources", "ps5=1", "--actor", "ops")

	out := h.ok(t, "machine", "show", "bench-a")
	assert.Contains(t, out, " cpu_units=16 ")
	assert.Contains(t, out, " memory_gb=64 ")
	assert.Contains(t, out, " tags=gpu,metal ")
	assert.Contains(t, out, ` resources={"ps5":1} `)

	// JSON output
	jOut := h.ok(t, "machine", "show", "bench-a", "--json")
	assert.Contains(t, jOut, `"cpu_units":16`)
	assert.Contains(t, jOut, `"memory_gb":64`)
	assert.Contains(t, jOut, `"tags":"gpu,metal"`)
	assert.Contains(t, jOut, `"resources":{"ps5":1}`)

	// Update with set and flag aliases
	h.ok(t, "machine", "set", "bench-a", "--cpu_units", "32", "--memory_gb", "128", "--tags", "cuda", "--resources", "ps5=2,fpga=1", "--actor", "ops")
	out = h.ok(t, "machine", "show", "bench-a")
	assert.Contains(t, out, " cpu_units=32 ")
	assert.Contains(t, out, " memory_gb=128 ")
	assert.Contains(t, out, " tags=cuda ")
	assert.Contains(t, out, ` resources={"fpga":1,"ps5":2} `)

	// Clearing fields
	h.ok(t, "machine", "set", "bench-a", "--cpu-units", "default", "--memory-gb", "default", "--tags", "default", "--resources", "default", "--actor", "ops")
	out = h.ok(t, "machine", "show", "bench-a")
	assert.Contains(t, out, " cpu_units=- ")
	assert.Contains(t, out, " memory_gb=- ")
	assert.Contains(t, out, " tags=- ")
	assert.Contains(t, out, " resources={} ")
}

func TestMachineTagsAndResourcesRefusals(t *testing.T) {
	t.Parallel()
	h := newHarness()
	h.ok(t, "machine", "add", "bench-a", "--user", "nova", "--os", "linux", "--arch", "amd64", "--actor", "ops")

	// CPU and Memory >= 1
	code, _, errs := h.run(t, "machine", "set", "bench-a", "--cpu-units", "0", "--actor", "ops")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "--cpu-units \"0\": want an integer >= 1")

	code, _, errs = h.run(t, "machine", "set", "bench-a", "--memory-gb", "-1", "--actor", "ops")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "--memory-gb \"-1\": want an integer >= 1")

	// cpu or memory in tags
	code, _, errs = h.run(t, "machine", "set", "bench-a", "--tags", "cpu,gpu", "--actor", "ops")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "--tags: cpu is not a tag; use --cpu-units")

	code, _, errs = h.run(t, "machine", "set", "bench-a", "--tags", "memory", "--actor", "ops")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "--tags: memory is not a tag; use --memory-gb")

	// cpu or memory in resources
	code, _, errs = h.run(t, "machine", "set", "bench-a", "--resources", "cpu=4", "--actor", "ops")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "--resources: cpu is not a custom resource; use --cpu-units")

	code, _, errs = h.run(t, "machine", "set", "bench-a", "--resources", "memory=8", "--actor", "ops")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "--resources: memory is not a custom resource; use --memory-gb")

	// Conflicting OS tags
	code, _, errs = h.run(t, "machine", "set", "bench-a", "--tags", "windows,gpu", "--actor", "ops")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, `machine bench-a: tag "windows" conflicts with machine os "linux"; run: nova-config machine set bench-a --tags "gpu"`)

	// Conflicting Arch tags
	code, _, errs = h.run(t, "machine", "set", "bench-a", "--tags", "arm64", "--actor", "ops")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, `machine bench-a: tag "arm64" conflicts with machine arch "amd64"; run: nova-config machine set bench-a --tags ""`)

	// Same machine tag and resource collision (R5)
	code, _, errs = h.run(t, "machine", "set", "bench-a", "--tags", "gpu", "--resources", "gpu=1", "--actor", "ops")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "cannot be both a tag and a resource")

	// Fleet-wide R5 collision between different machines
	h.ok(t, "machine", "add", "bench-b", "--user", "nova", "--os", "linux", "--arch", "amd64", "--resources", "ps5=1", "--actor", "ops")
	code, _, errs = h.run(t, "machine", "set", "bench-a", "--tags", "ps5", "--actor", "ops")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "is a tag on bench-a and a resource on bench-b; a name is a tag or a resource everywhere")
}

func TestResourcesVerb(t *testing.T) {
	t.Parallel()
	h := newHarness()
	h.ok(t, "machine", "add", "bench-a", "--user", "nova", "--os", "linux", "--arch", "amd64",
		"--cpu-units", "16", "--memory-gb", "64", "--tags", "gpu,metal", "--resources", "ps5=1", "--actor", "ops")
	h.ok(t, "machine", "add", "bench-b", "--user", "nova", "--os", "darwin", "--arch", "arm64",
		"--cpu-units", "8", "--memory-gb", "32", "--tags", "metal", "--actor", "ops")

	out := h.ok(t, "resources")
	assert.Contains(t, out, "MACHINE name=bench-a tags=amd64,gpu,linux,metal resources=cpu=16,memory=64,ps5=1")
	assert.Contains(t, out, "MACHINE name=bench-b tags=arm64,mac,metal resources=cpu=8,memory=32")
	assert.Contains(t, out, "TAG name=metal machines=bench-a,bench-b")
	assert.Contains(t, out, "RESOURCE name=cpu machines=bench-a,bench-b")
	assert.Contains(t, out, "RESOURCE name=ps5 machines=bench-a")
	assert.Contains(t, out, "CONFIG RESOURCES machines=2 tags=6 resources=3")

	jOut := h.ok(t, "resources", "--json")
	var view struct {
		Machines  map[string]machineResources `json:"machines"`
		Tags      map[string][]string         `json:"tags"`
		Resources map[string][]string         `json:"resources"`
	}
	require.NoError(t, json.Unmarshal([]byte(jOut), &view))
	assert.Equal(t, []string{"amd64", "gpu", "linux", "metal"}, view.Machines["bench-a"].Tags)
	assert.Equal(t, map[string]int{"cpu": 16, "memory": 64, "ps5": 1}, view.Machines["bench-a"].Resources)
	assert.Equal(t, []string{"bench-a", "bench-b"}, view.Tags["metal"])
	assert.Equal(t, []string{"bench-a"}, view.Resources["ps5"])
}

func TestInventoryWithTagsAndResources(t *testing.T) {
	t.Parallel()
	h := newHarness()
	h.ok(t, "machine", "add", "bench-a", "--user", "nova", "--os", "linux", "--arch", "amd64",
		"--cpu-units", "16", "--memory-gb", "64", "--tags", "gpu,metal", "--resources", "ps5=1", "--actor", "ops")

	_, out, _ := h.run(t, "inventory", "--host", "bench-a")
	var hv map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &hv))
	assert.Equal(t, float64(16), hv["cpu_units"])
	assert.Equal(t, float64(64), hv["memory_gb"])
	assert.Equal(t, []any{"gpu", "metal"}, hv["nova_tags"])
	assert.Equal(t, map[string]any{"ps5": float64(1)}, hv["nova_resources"])
}
