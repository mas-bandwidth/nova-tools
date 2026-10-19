package main

import (
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuiltModuleIsTheNovaToolsTheBinaryWasBuiltFrom(t *testing.T) {
	t.Parallel()
	main := &debug.BuildInfo{Main: debug.Module{Path: toolsModule, Version: "v1.6.1-0.20261018120000-abcdef123456"}}
	assert.Equal(t, toolsModule+"@v1.6.1-0.20261018120000-abcdef123456", builtModule("", main, true), "a build of nova-tools names the version the toolchain stamped")
	assert.Equal(t, toolsModule+"@v1.6.2", builtModule("v1.6.2", main, true), "a release names its tag")
	dep := &debug.BuildInfo{Main: debug.Module{Path: "example.com/sprint"}, Deps: []*debug.Module{
		{Path: "golang.org/x/mod", Version: "v0.41.0"}, {Path: toolsModule, Version: "v1.6.0"}}}
	assert.Equal(t, toolsModule+"@v1.6.0", builtModule("", dep, true), "go run from a tree names the nova-tools its go.mod pins")
	dep.Deps[1].Replace = &debug.Module{Path: "../nova-tools"}
	assert.Equal(t, "../nova-tools@", builtModule("", dep, true), "a replaced nova-tools names the replacement")
	assert.Empty(t, builtModule("", nil, false), "a build that records nothing")
	assert.Empty(t, builtModule("", &debug.BuildInfo{Main: debug.Module{Path: "example.com/other"}}, true), "a build without nova-tools")
}

func writeGoMod(t *testing.T, text string) string {
	t.Helper()
	root := t.TempDir()
	if text != "" {
		require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte(text), 0o644))
	}
	return root
}

func TestTreeRunnerIsDecidedByTheTree(t *testing.T) {
	t.Parallel()
	built := toolsModule + "@v1.6.0"
	sprint := writeGoMod(t, "module example.com/sprint\n\ngo 1.26\n\nrequire "+toolsModule+" v1.6.0\n")
	require.NoError(t, checkTreeRunner(sprint, built), "a binary built at the tree's pin is its runner")
	err := checkTreeRunner(sprint, toolsModule+"@v1.5.2")
	require.Error(t, err, "a binary of another nova-tools is not the tree's runner")
	for _, want := range []string{"the runner of " + built + " (its go.mod)", "this nova-tla is " + toolsModule + "@v1.5.2 (runner ", "run the tree's own from " + sprint + ": go tool nova-tla", "go run " + toolsModule + "/cmd/nova-tla"} {
		assert.Contains(t, err.Error(), want)
	}
	err = checkTreeRunner(sprint, "")
	require.Error(t, err, "a build that records no version cannot be the tree's runner")
	assert.Contains(t, err.Error(), "a build that records no nova-tools version")

	replaced := writeGoMod(t, "module example.com/tree\n\ngo 1.26\n\nrequire "+toolsModule+" v1.6.0\n\nreplace "+toolsModule+" => ../nova-tools\n")
	assert.NoError(t, checkTreeRunner(replaced, "../nova-tools@"), "go run under the tree's replace is its runner")
	assert.Error(t, checkTreeRunner(replaced, built), "the release is not the tree's replacement")

	for name, text := range map[string]string{
		"no go.mod (a bench copy of tla/)":         "",
		"a go.mod without nova-tools":              "module example.com/rocketnet\n\ngo 1.26\n",
		"nova-tools itself (pkg/tlc is the check)": "module " + toolsModule + "\n\ngo 1.26\n",
	} {
		assert.NoError(t, checkTreeRunner(writeGoMod(t, text), "anything"), name)
	}
	err = checkTreeRunner(writeGoMod(t, "module x\nrequire (\n"), built)
	require.Error(t, err, "a go.mod that does not parse")
	assert.Contains(t, err.Error(), "cannot be read to find the runner it pins")
}

func TestBenchPackageIsTheTreesRunner(t *testing.T) {
	t.Parallel()
	tools := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(tools, "cmd", "nova-tla"), 0o755))
	pkg, err := benchPackage(tools)
	require.NoError(t, err)
	assert.Equal(t, "./cmd/nova-tla", pkg, "nova-tools builds its own")
	pkg, err = benchPackage(writeGoMod(t, benchTreeGoMod))
	require.NoError(t, err)
	assert.Equal(t, toolsModule+"/cmd/nova-tla", pkg, "a tree that requires nova-tools builds nova-tools' at its pin")
	_, err = benchPackage(writeGoMod(t, "module example.com/rocketnet\n\ngo 1.26\n"))
	require.Error(t, err, "a tree with no runner of its own")
	assert.Contains(t, err.Error(), "neither holds cmd/nova-tla nor requires "+toolsModule)
}

func TestVersionNamesTheRunner(t *testing.T) {
	t.Parallel()
	r := call(t, nil, "version")
	require.Equal(t, 0, r.code, "%+v", r)
	assert.True(t, strings.HasPrefix(r.stdout, "nova-tla "), r.stdout)
	assert.Contains(t, r.stdout, " runner="+runnerDigest(), r.stdout)
	assert.Equal(t, r, call(t, nil, "--version"), "--version is version")
	r = call(t, nil, "version", "extra")
	assert.Equal(t, 2, r.code, "version with an argument: %+v", r)
	assert.Contains(t, r.stderr, "takes no positional arguments")
	r = call(t, nil, "help", "runner")
	assert.Equal(t, 0, r.code, "help runner: %+v", r)
	for _, want := range []string{"nova-tla help runner: which runner a tree's records are measured with.", "go build -o /tmp/nova-tla ./cmd/nova-tla", "go run " + toolsModule + "/cmd/nova-tla", "runner=<12 hex>", "example: nova-tla version"} {
		assert.Contains(t, r.stdout, want)
	}
}
