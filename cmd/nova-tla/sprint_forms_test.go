package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/tlc"
)

// The forms nova-sprint's own runner had, kept when its copy became this one
// (nova-sprint runs this command now): the case of inputs as one argument, and a
// checkout with no tla/RUNS.tsv reading as one that has measured nothing.

func TestInputsTakesTheCaseAsItsOneArgument(t *testing.T) {
	t.Parallel()
	root, _ := checkout(t)
	flagged := call(t, nil, "inputs", "--root", root, "--case", "MCA.cfg")
	require.Equal(t, 0, flagged.code, "%+v", flagged)
	for _, name := range []string{"MCA.cfg", "MCA"} {
		r := call(t, nil, "inputs", "--root", root, name)
		assert.Equal(t, 0, r.code, "inputs %s: %+v", name, r)
		assert.Equal(t, flagged.stdout, r.stdout, name)
		assert.Empty(t, r.stderr, name)
	}
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"a case not in the plan": {[]string{"inputs", "--root", root, "MCGhost.cfg"}, `no case "MCGhost.cfg"`},
		"two arguments":          {[]string{"inputs", "--root", root, "MCA.cfg", "MCAStale.cfg"}, "unexpected argument"},
		"an argument and --case": {[]string{"inputs", "--root", root, "--case", "MCA.cfg", "MCAStale.cfg"}, "unexpected argument"},
	} {
		r := call(t, nil, tc.args...)
		assert.Equal(t, 2, r.code, "%s: %+v", name, r)
		assert.Empty(t, r.stdout, name)
		assert.Contains(t, r.stderr, tc.want, name)
	}
}

func TestGroupsStaleWithNoRecordsFileIsEveryRequiredGroup(t *testing.T) {
	t.Parallel()
	root, _ := checkout(t)
	assert.NoFileExists(t, filepath.Join(root, "tla", tlc.RunsFile), "the fixture has no records file")
	recs, err := readRuns(root)
	require.NoError(t, err, "readRuns of a checkout with no records")
	assert.Nil(t, recs, "readRuns of a checkout with no records")
	r := call(t, nil, "groups", "--root", root, "--stale")
	require.Equal(t, 0, r.code, "groups --stale with no records: %+v", r)
	assert.Empty(t, r.stderr)
	var groups []string
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &groups))
	assert.Equal(t, []string{"alpha", "beta", "gamma"}, groups, "every group with a case, bench-gated ones too")
	// A records file that is there but cannot be read is still refused.
	require.NoError(t, os.Mkdir(filepath.Join(root, "tla", tlc.RunsFile), 0o755))
	r = call(t, nil, "groups", "--root", root, "--stale")
	assert.Equal(t, 2, r.code, "an unreadable records file: %+v", r)
	assert.Empty(t, r.stdout)
	assert.Contains(t, r.stderr, "cannot read the records")
}
