package tlc

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func rec(config, inputs string) Record {
	return Record{Config: config, Module: "MCA.tla", InputSHA256: inputs, InputFiles: 3, JarSHA256: strings.Repeat("a", 64), JavaVersion: "21.0.1",
		Host: "linux-amd64", CPUs: 8, StartedUTC: "2026-01-01T00:00:00.000000+00:00", Workers: 2, Generated: "10", Distinct: "5",
		Seconds: "1.250", Exit: 0, Result: "PASS", Expected: "pass", Property: "-", Budget: "110", Mode: "bounded"}
}

func TestRecordsRoundTrip(t *testing.T) {
	t.Parallel()
	in := []Record{rec("MCA.cfg", "x"), rec("MCB.cfg", "x")}
	in[1].Exit, in[1].Result, in[1].Generated, in[1].Distinct = 124, "FAIL", "-", "-"
	var b bytes.Buffer
	require.NoError(t, WriteRecords(&b, in))
	if !strings.HasPrefix(b.String(), "config\tmodule\tinput_sha256\tinput_files\tjar_sha256\tjava_version\thost\tcpus\tstarted_utc\tworkers\tgenerated\tdistinct\tseconds\texit\tresult\texpected\tproperty\tbudget\tmode\n") {
		t.Fatalf("header: %q", strings.SplitN(b.String(), "\n", 2)[0])
	}
	out, err := ReadRecords(&b)
	require.NoError(t, err, "round trip = %v, %v", out, err)
	require.Equal(t, out, in, "round trip = %v, %v", out, err)
}

func TestRecordsRefuseWhatIsNotTheirFormat(t *testing.T) {
	t.Parallel()
	bad := rec("MCA.cfg", "x")
	bad.Host = "two\twords"
	assert.Error(t, WriteRecords(&bytes.Buffer{}, []Record{bad}), "a field with a tab was written")
	good := strings.Join(RecordsHeader, "\t") + "\n"
	// The plain forms are read, the exit's negative sign included.
	for _, exit := range []string{"0", "12", "124", "-1"} {
		_, err := ReadRecords(strings.NewReader(good + badRow("10", "2", exit)))
		assert.NoError(t, err, "exit %s refused: %v", exit, err)
	}
	for name, text := range map[string]string{
		"a wrong header":         "config\tmodule\n",
		"a short row":            good + "MCA.cfg\tMCA.tla\n",
		"a bad count of files":   good + badRow("none", "2", "0"),
		"no files":               good + badRow("0", "2", "0"),
		"no workers":             good + badRow("3", "0", "0"),
		"no cpus":                good + strings.Replace(badRow("3", "2", "0"), "\t8\t", "\t0\t", 1),
		"a bad count of workers": good + badRow("3", "two", "0"),
		"a bad exit":             good + badRow("3", "2", "abc"),
		"files with text after":  good + badRow("10x", "2", "0"),
		"files with a sign":      good + badRow("+10", "2", "0"),
		"files with a zero":      good + badRow("010", "2", "0"),
		"files with an exponent": good + badRow("1e1", "2", "0"),
		"files with a space":     good + badRow(" 10", "2", "0"),
		"workers with text":      good + badRow("3", "2x", "0"),
		"cpus with text":         good + strings.Replace(badRow("3", "2", "0"), "\t8\t", "\t8x\t", 1),
		"an exit with text":      good + badRow("3", "2", "12x"),
		"an exit with a sign":    good + badRow("3", "2", "+12"),
	} {
		_, err := ReadRecords(strings.NewReader(text))
		assert.Error(t, err, "%s was read", name)
	}
}

// mergeTree is a checkout with three cases; MCA and MCB read the shared module
// and MCC reads nothing of theirs. The records are the ones a run of each on
// these files would write.
func mergeTree(t *testing.T) (Source, []Case, map[string]Record) {
	t.Helper()
	root := tree(t, map[string]string{
		"CASES.tsv": header +
			row("MCA.cfg", "MCA.tla", "pass", "-", "check", "alpha", "required", "-") +
			row("MCB.cfg", "MCB.tla", "pass", "-", "check", "beta", "required", "-") +
			row("MCC.cfg", "MCC.tla", "pass", "-", "check", "gamma", "required", "-"),
		"MCA.tla": "EXTENDS Shared\n", "MCB.tla": "EXTENDS Shared\n", "MCC.tla": "EXTENDS Naturals\n", "Shared.tla": "shared\n",
		"MCA.cfg": "c\n", "MCB.cfg": "c\n", "MCC.cfg": "c\n",
	})
	cases, err := LoadCases(root)
	require.NoError(t, err)
	src := testSource(t, root)
	recs := map[string]Record{}
	for _, c := range cases {
		fp, n, err := src.Fingerprint(c.Config)
		require.NoError(t, err)
		r := rec(c.Config, fp)
		r.Module, r.InputFiles = c.Module, n
		recs[c.Config] = r
	}
	return src, cases, recs
}

func TestMergeOrdersByThePlanAndRefusesWhatIsWrong(t *testing.T) {
	t.Parallel()
	src, cases, recs := mergeTree(t)
	a, b, c := recs["MCA.cfg"], recs["MCB.cfg"], recs["MCC.cfg"]
	got, err := Merge(src, cases, []Record{c}, []Record{a, b})
	require.NoError(t, err, "merge = %v, %v", got, err)
	require.Equal(t, []Record{a, b, c}, got, "merge = %v, %v", got, err)
	stale := c
	stale.InputSHA256 = strings.Repeat("0", 64)
	fewer := c
	fewer.InputFiles--
	refused := []struct {
		name string
		runs [][]Record
		why  string
	}{
		{"a case recorded twice", [][]Record{{a, b, c}, {a}}, "appears twice"},
		{"a record for an undeclared case", [][]Record{{a, b, c, rec("MCD.cfg", "x")}}, "no such case"},
		{"a declared case with no record", [][]Record{{a, b}}, "no record for 1 declared cases (MCC.cfg)"},
		{"a record measured on other inputs", [][]Record{{a, b}, {stale}}, "1 records were measured on other inputs than these: MCC.cfg (recorded 000000000000 over 4 files, now "},
		{"a record over another count of files", [][]Record{{a, b}, {fewer}}, "MCC.cfg (recorded "},
	}
	for _, tc := range refused {
		if _, err := Merge(src, cases, tc.runs...); err == nil || !strings.Contains(err.Error(), tc.why) {
			t.Errorf("%s: %v, want %q", tc.name, err, tc.why)
		}
	}
}

// A record measured by another runner than the one merging (the runner's files
// are part of every fingerprint) is refused, and so is one measured on a model
// edited since: the merge catches both by the fingerprint, per case.
func TestMergeRefusesRecordsOfAnotherRunnerOrEditedModel(t *testing.T) {
	t.Parallel()
	src, cases, recs := mergeTree(t)
	all := []Record{recs["MCA.cfg"], recs["MCB.cfg"], recs["MCC.cfg"]}
	_, err := Merge(src, cases, all)
	require.NoError(t, err)
	otherRunner := src
	otherRunner.Runner = map[string][]byte{"internal/tlc/run.go": []byte("another reading of the results\n")}
	_, err = Merge(otherRunner, cases, all)
	assert.ErrorContains(t, err, "3 records were measured on other inputs", "records of another runner")
	require.NoError(t, os.WriteFile(filepath.Join(src.TLADir, "Shared.tla"), []byte("edited\n"), 0o644))
	if _, err := Merge(src, cases, all); err == nil || !strings.Contains(err.Error(), "2 records were measured on other inputs than these: MCA.cfg (") || strings.Contains(err.Error(), "MCC.cfg") {
		t.Errorf("records of a model edited since: %v", err)
	}
}

// A merge of records that were not all measured with one jar is refused: the
// kept records of an older jar beside the runs of a new one.
func TestMergeRefusesRecordsOfMoreThanOneJar(t *testing.T) {
	t.Parallel()
	src, cases, recs := mergeTree(t)
	for i := range cases {
		cases[i].Group = []string{"alpha", "beta", "gamma"}[i]
	}
	a, b, c := recs["MCA.cfg"], recs["MCB.cfg"], recs["MCC.cfg"]
	c.JarSHA256 = strings.Repeat("c", 64)
	_, err := Merge(src, cases, []Record{a, b}, []Record{c})
	want := "the records hold 2 jars: aaaaaaaaaaaa (2 records), cccccccccccc (1 records); one jar measures the whole file: run again, with the jar aaaaaaaaaaaa, the groups recorded under the other jars (gamma: MCC.cfg), or run every group with one jar and merge without --keep"
	require.EqualError(t, err, want, "got %v\nwant %s", err, want)
	c.JarSHA256 = a.JarSHA256
	_, err = Merge(src, cases, []Record{a, b, c})
	require.NoError(t, err, "one jar refused")
}

func badRow(files, workers, exit string) string {
	return strings.Join([]string{"MCA.cfg", "MCA.tla", "x", files, "j", "21.0.1", "linux-amd64", "8", "t", workers, "1", "1", "1", exit, "PASS", "pass", "-", "110", "bounded"}, "\t") + "\n"
}

// Records in another column layout are refused naming the layout found and the
// one expected, whatever their field counts.
func TestReadRecordsNamesTheLayoutItFoundAndTheOneItExpects(t *testing.T) {
	t.Parallel()
	old := "config\tmodule\tinput_sha256\tjar_sha256\thost\tstarted_utc\tgenerated\tdistinct\tseconds\texit\tresult\texpected\tproperty\tbudget\tmode\n" +
		"MCA.cfg\tMCA.tla\tx\tj\th\tt\t1\t1\t1\t0\tPASS\tpass\t-\t110\tbounded\n"
	_, err := ReadRecords(strings.NewReader(old))
	require.Error(t, err, "the old layout was read")
	for _, want := range []string{"another layout", "found 15 columns (config,module,input_sha256,jar_sha256,", "reads and writes 19 (config,module,input_sha256,input_files,jar_sha256,java_version,"} {
		assert.ErrorContains(t, err, want, "%q lacks %q", err, want)
	}
	_, err = ReadRecords(strings.NewReader(strings.Join(RecordsHeader, "\t") + "\nMCA.cfg\tMCA.tla\n"))
	assert.ErrorContains(t, err, "line 2 has 2 fields, want 19", "a short row")
}

// A kept record is dropped for one of two reasons, and each is named.
func TestCarryNamesEachRecordItDrops(t *testing.T) {
	t.Parallel()
	src, cases, recs := mergeTree(t)
	for i := range cases {
		cases[i].Group = []string{"alpha", "beta", "gamma"}[i]
	}
	stale := recs["MCB.cfg"]
	stale.InputSHA256 = strings.Repeat("0", 64)
	gone := rec("MCGone.cfg", "x")
	measured := recs["MCC.cfg"]
	measured.InputSHA256 = strings.Repeat("1", 64) // replaced by the run below, so not dropped
	kept, dropped, err := Carry(src, cases, []Record{recs["MCA.cfg"], stale, gone, measured}, []Record{recs["MCC.cfg"]})
	if err != nil || len(kept) != 1 || kept[0].Config != "MCA.cfg" {
		t.Fatalf("kept %+v, %v", kept, err)
	}
	want := []Dropped{{Config: "MCB.cfg", Group: "beta", Why: DroppedStale}, {Config: "MCGone.cfg", Why: DroppedGone}}
	require.Equal(t, want, dropped, "dropped %+v, want %+v", dropped, want)
	_, err = Merge(src, cases, kept, []Record{recs["MCC.cfg"]})
	var missing *MissingError
	require.ErrorAs(t, err, &missing, "merge error %v", err)
	require.Equal(t, []string{"MCB.cfg"}, missing.Cases, "merge error %v", err)
}

// The host column holds a platform label, never a machine name.
func TestPlatformIsTheGoosGoarchLabelOfALinuxMachine(t *testing.T) {
	t.Parallel()
	for goos, archs := range map[string][]string{"linux": {"amd64", "arm64", "riscv64"}} {
		for _, arch := range archs {
			if got, err := Platform(goos, arch); err != nil || got != goos+"-"+arch {
				t.Errorf("%s/%s: %q, %v", goos, arch, got, err)
			}
		}
	}
	for _, bad := range [][2]string{{"darwin", "arm64"}, {"windows", "amd64"}, {"linux", "sparc"}, {"", ""}, {"linux", ""}} {
		got, err := Platform(bad[0], bad[1])
		assert.Error(t, err, "%v gave %q", bad, got)
	}
	for _, name := range []string{"build-host-7.example", "bench", "linux", "linux-", "Linux-amd64", "linux-amd64 ", "-amd64", ""} {
		assert.False(t, ValidPlatform(name), "%q is a platform label", name)
	}
	assert.True(t, ValidPlatform("linux-amd64"), "linux-amd64 is not a platform label")
}

// A record's module, expected and property cells are copies of the case's
// declaration that the fingerprint does not hash (it hashes the plan's row):
// editing one leaves a valid fingerprint, and the record must still be refused
// by a merge, counted stale by StaleGroups and dropped by Carry.
func TestARecordThatNamesAnotherModuleExpectationOrPropertyIsNotCurrent(t *testing.T) {
	t.Parallel()
	src, cases, recs := mergeTree(t)
	for i := range cases {
		cases[i].Group = []string{"alpha", "beta", "gamma"}[i]
	}
	a, b, c := recs["MCA.cfg"], recs["MCB.cfg"], recs["MCC.cfg"]
	_, err := Merge(src, cases, []Record{a, b, c})
	require.NoError(t, err, "the unchanged control is refused")
	if got, err := StaleGroups(src, cases, []Record{a, b, c}); err != nil || len(got) != 0 {
		t.Fatalf("the control has stale groups %v, %v", got, err)
	}
	for _, tc := range []struct {
		name   string
		edit   func(*Record)
		phrase string
	}{
		{"module", func(r *Record) { r.Module = "MCB.tla" }, `module is "MCB.tla" and the plan declares "MCA.tla"`},
		{"expected", func(r *Record) { r.Expected = "invariant" }, `expected is "invariant" and the plan declares "pass"`},
		{"property", func(r *Record) { r.Property = "BogusGuard" }, `property is "BogusGuard" and the plan declares "-"`},
	} {
		tampered := a
		tc.edit(&tampered)
		require.Equal(t, a.InputSHA256, tampered.InputSHA256, "%s: the probe changed the fingerprint columns", tc.name)
		require.Equal(t, a.InputFiles, tampered.InputFiles, "%s: the probe changed the fingerprint columns", tc.name)
		if _, err := Merge(src, cases, []Record{tampered, b, c}); err == nil || !strings.Contains(err.Error(), "record for MCA.cfg does not match the plan") || !strings.Contains(err.Error(), tc.phrase) {
			t.Errorf("%s: merge accepted or misnamed the record: %v", tc.name, err)
		}
		if got, err := StaleGroups(src, cases, []Record{tampered, b, c}); err != nil || !reflect.DeepEqual(got, []string{"alpha"}) {
			t.Errorf("%s: StaleGroups = %v, %v, want [alpha]", tc.name, got, err)
		}
		kept, dropped, err := Carry(src, cases, []Record{tampered, b}, []Record{c})
		if err != nil || len(kept) != 1 || kept[0].Config != "MCB.cfg" || !reflect.DeepEqual(dropped, []Dropped{{Config: "MCA.cfg", Group: "alpha", Why: DroppedStale}}) {
			t.Errorf("%s: Carry kept %+v dropped %+v, %v", tc.name, kept, dropped, err)
		}
	}
}
