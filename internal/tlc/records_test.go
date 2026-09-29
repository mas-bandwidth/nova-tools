package tlc

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
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
	if err := WriteRecords(&b, in); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(b.String(), "config\tmodule\tinput_sha256\tinput_files\tjar_sha256\tjava_version\thost\tcpus\tstarted_utc\tworkers\tgenerated\tdistinct\tseconds\texit\tresult\texpected\tproperty\tbudget\tmode\n") {
		t.Fatalf("header: %q", strings.SplitN(b.String(), "\n", 2)[0])
	}
	out, err := ReadRecords(&b)
	if err != nil || !reflect.DeepEqual(in, out) {
		t.Fatalf("round trip = %v, %v", out, err)
	}
}

func TestRecordsRefuseWhatIsNotTheirFormat(t *testing.T) {
	t.Parallel()
	bad := rec("MCA.cfg", "x")
	bad.Host = "two\twords"
	if err := WriteRecords(&bytes.Buffer{}, []Record{bad}); err == nil {
		t.Error("a field with a tab was written")
	}
	good := strings.Join(RecordsHeader, "\t") + "\n"
	for name, text := range map[string]string{
		"a wrong header":         "config\tmodule\n",
		"a short row":            good + "MCA.cfg\tMCA.tla\n",
		"a bad count of files":   good + badRow("none", "2", "0"),
		"no files":               good + badRow("0", "2", "0"),
		"no workers":             good + badRow("3", "0", "0"),
		"no cpus":                good + strings.Replace(badRow("3", "2", "0"), "\t8\t", "\t0\t", 1),
		"a bad count of workers": good + badRow("3", "two", "0"),
		"a bad exit":             good + badRow("3", "2", "abc"),
	} {
		if _, err := ReadRecords(strings.NewReader(text)); err == nil {
			t.Errorf("%s was read", name)
		}
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
	if err != nil {
		t.Fatal(err)
	}
	src := testSource(t, root)
	recs := map[string]Record{}
	for _, c := range cases {
		fp, n, err := src.Fingerprint(c.Config)
		if err != nil {
			t.Fatal(err)
		}
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
	if err != nil || !reflect.DeepEqual(got, []Record{a, b, c}) {
		t.Fatalf("merge = %v, %v", got, err)
	}
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
	if _, err := Merge(src, cases, all); err != nil {
		t.Fatal(err)
	}
	otherRunner := src
	otherRunner.Runner = map[string][]byte{"internal/tlc/run.go": []byte("another reading of the results\n")}
	if _, err := Merge(otherRunner, cases, all); err == nil || !strings.Contains(err.Error(), "3 records were measured on other inputs") {
		t.Errorf("records of another runner: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src.TLADir, "Shared.tla"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
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
	if err == nil || err.Error() != want {
		t.Fatalf("got %v\nwant %s", err, want)
	}
	c.JarSHA256 = a.JarSHA256
	if _, err := Merge(src, cases, []Record{a, b, c}); err != nil {
		t.Fatalf("one jar refused: %v", err)
	}
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
	if err == nil {
		t.Fatal("the old layout was read")
	}
	for _, want := range []string{"another layout", "found 15 columns (config,module,input_sha256,jar_sha256,", "reads and writes 19 (config,module,input_sha256,input_files,jar_sha256,java_version,"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q lacks %q", err, want)
		}
	}
	if _, err := ReadRecords(strings.NewReader(strings.Join(RecordsHeader, "\t") + "\nMCA.cfg\tMCA.tla\n")); err == nil || !strings.Contains(err.Error(), "line 2 has 2 fields, want 19") {
		t.Errorf("a short row: %v", err)
	}
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
	if !reflect.DeepEqual(dropped, want) {
		t.Fatalf("dropped %+v, want %+v", dropped, want)
	}
	_, err = Merge(src, cases, kept, []Record{recs["MCC.cfg"]})
	var missing *MissingError
	if !errors.As(err, &missing) || !reflect.DeepEqual(missing.Cases, []string{"MCB.cfg"}) {
		t.Fatalf("merge error %v", err)
	}
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
		if got, err := Platform(bad[0], bad[1]); err == nil {
			t.Errorf("%v gave %q", bad, got)
		}
	}
	for _, name := range []string{"build-host-7.example", "bench", "linux", "linux-", "Linux-amd64", "linux-amd64 ", "-amd64", ""} {
		if ValidPlatform(name) {
			t.Errorf("%q is a platform label", name)
		}
	}
	if !ValidPlatform("linux-amd64") {
		t.Error("linux-amd64 is not a platform label")
	}
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
	if _, err := Merge(src, cases, []Record{a, b, c}); err != nil {
		t.Fatalf("the unchanged control is refused: %v", err)
	}
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
		if tampered.InputSHA256 != a.InputSHA256 || tampered.InputFiles != a.InputFiles {
			t.Fatalf("%s: the probe changed the fingerprint columns", tc.name)
		}
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
