package tlc

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func rec(config, inputs string) Record {
	return Record{Config: config, Module: "MCA.tla", InputSHA256: inputs, JarSHA256: strings.Repeat("a", 64),
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
	if !strings.HasPrefix(b.String(), "config\tmodule\tinput_sha256\tjar_sha256\thost\tstarted_utc\tgenerated\tdistinct\tseconds\texit\tresult\texpected\tproperty\tbudget\tmode\n") {
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
		"a wrong header": "config\tmodule\n",
		"a short row":    good + "MCA.cfg\tMCA.tla\n",
		"a bad exit":     good + strings.Join([]string{"MCA.cfg", "MCA.tla", "x", "j", "h", "t", "1", "1", "1", "abc", "PASS", "pass", "-", "110", "bounded"}, "\t") + "\n",
	} {
		if _, err := ReadRecords(strings.NewReader(text)); err == nil {
			t.Errorf("%s was read", name)
		}
	}
}

func TestMergeOrdersByThePlanAndRefusesWhatIsWrong(t *testing.T) {
	t.Parallel()
	cases := []Case{{Config: "MCA.cfg"}, {Config: "MCB.cfg"}, {Config: "MCC.cfg"}}
	a, b, c := rec("MCA.cfg", "in1"), rec("MCB.cfg", "in1"), rec("MCC.cfg", "in1")
	got, err := Merge(cases, []Record{c}, []Record{a, b})
	if err != nil || !reflect.DeepEqual(got, []Record{a, b, c}) {
		t.Fatalf("merge = %v, %v", got, err)
	}
	refused := []struct {
		name string
		runs [][]Record
		why  string
	}{
		{"a case recorded twice", [][]Record{{a, b, c}, {a}}, "appears twice"},
		{"a record for an undeclared case", [][]Record{{a, b, c, rec("MCD.cfg", "in1")}}, "no such case"},
		{"a declared case with no record", [][]Record{{a, b}}, "no record for 1 declared cases (MCC.cfg)"},
		{"records of different inputs", [][]Record{{a, b}, {rec("MCC.cfg", "in2")}}, "2 different inputs"},
	}
	for _, tc := range refused {
		if _, err := Merge(cases, tc.runs...); err == nil || !strings.Contains(err.Error(), tc.why) {
			t.Errorf("%s: %v, want %q", tc.name, err, tc.why)
		}
	}
}
