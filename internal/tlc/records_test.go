package tlc

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func rec(config, inputs string) Record {
	return Record{Config: config, Module: "MCA.tla", InputSHA256: inputs, InputFiles: 3, JarSHA256: strings.Repeat("a", 64),
		Host: "bench", StartedUTC: "2026-01-01T00:00:00.000000+00:00", Generated: "10", Distinct: "5",
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
	if !strings.HasPrefix(b.String(), "config\tmodule\tinput_sha256\tinput_files\tjar_sha256\thost\tstarted_utc\tgenerated\tdistinct\tseconds\texit\tresult\texpected\tproperty\tbudget\tmode\n") {
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
		"a wrong header":       "config\tmodule\n",
		"a short row":          good + "MCA.cfg\tMCA.tla\n",
		"a bad count of files": good + strings.Join([]string{"MCA.cfg", "MCA.tla", "x", "none", "j", "h", "t", "1", "1", "1", "0", "PASS", "pass", "-", "110", "bounded"}, "\t") + "\n",
		"no files":             good + strings.Join([]string{"MCA.cfg", "MCA.tla", "x", "0", "j", "h", "t", "1", "1", "1", "0", "PASS", "pass", "-", "110", "bounded"}, "\t") + "\n",
		"a bad exit":           good + strings.Join([]string{"MCA.cfg", "MCA.tla", "x", "3", "j", "h", "t", "1", "1", "1", "abc", "PASS", "pass", "-", "110", "bounded"}, "\t") + "\n",
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
