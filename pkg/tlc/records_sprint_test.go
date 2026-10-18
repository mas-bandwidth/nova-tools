package tlc

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The cases nova-sprint's copy of this package tested and this one did not, kept when
// that copy was deleted and nova-sprint imported this package (2026-10-18): the code
// was the same, so these hold behaviour its callers already relied on.

func TestReadRecordsFileOfAMissingFileIsANotExistError(t *testing.T) {
	t.Parallel()
	_, err := ReadRecordsFile(filepath.Join(t.TempDir(), "nope.tsv"))
	require.Error(t, err, "a missing records file was read")
	assert.True(t, errors.Is(err, os.ErrNotExist), "the error is not a not-exist error, which nova-tla groups --stale reads as no records: %v", err)
}

func TestStaleGroupsNamesTheGroupOfACaseWithNoRecord(t *testing.T) {
	t.Parallel()
	src, cases, recs := mergeTree(t)
	all := []Record{recs["MCA.cfg"], recs["MCB.cfg"], recs["MCC.cfg"]}
	got, err := StaleGroups(src, cases, all)
	require.NoError(t, err)
	assert.Empty(t, got, "every case has its current record")
	got, err = StaleGroups(src, cases, all[:2])
	require.NoError(t, err)
	assert.Equal(t, []string{"gamma"}, got, "MCC.cfg has no record")
	stale := recs["MCB.cfg"]
	stale.InputSHA256 = strings.Repeat("0", 64)
	got, err = StaleGroups(src, cases, []Record{all[0], stale})
	require.NoError(t, err)
	assert.Equal(t, []string{"beta", "gamma"}, got, "MCB.cfg is stale and MCC.cfg has no record")
}

func TestMergeNamesJarsOfEqualCountInNameOrder(t *testing.T) {
	t.Parallel()
	src, cases, recs := mergeTree(t)
	var all []Record
	for i, config := range []string{"MCA.cfg", "MCB.cfg", "MCC.cfg"} {
		r := recs[config]
		r.JarSHA256 = strings.Repeat([]string{"d", "b", "c"}[i], 64)
		all = append(all, r)
	}
	_, err := Merge(src, cases, all)
	require.Error(t, err, "three jars were merged")
	assert.Contains(t, err.Error(), "the records hold 3 jars: bbbbbbbbbbbb (1 records), cccccccccccc (1 records), dddddddddddd (1 records)", "a tie is broken by the jar's name")
}

func TestRunnerDigestNamesTheResultFilesAndNothingElse(t *testing.T) {
	t.Parallel()
	files, err := RunnerFiles()
	require.NoError(t, err)
	d := RunnerDigest(files)
	assert.Regexp(t, `^[0-9a-f]{12}$`, d)
	assert.Equal(t, d, RunnerDigest(files), "one runner, one digest")
	other := map[string][]byte{}
	for p, raw := range files {
		other[p] = raw
	}
	other["pkg/tlc/doc.go"] = []byte("bookkeeping\n")
	assert.Equal(t, d, RunnerDigest(other), "a bookkeeping file is not the runner")
	other[RunnerDir+"/run.go"] = append([]byte{}, files[RunnerDir+"/run.go"]...)
	other[RunnerDir+"/run.go"] = append(other[RunnerDir+"/run.go"], '\n')
	assert.NotEqual(t, d, RunnerDigest(other), "an edited result file is another runner")
}
