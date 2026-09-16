package main

import (
	"strings"
	"testing"
)

// The two-minute rule is an assertion now, not a record someone might forget to
// update. A `go test -json` stream with one package at 61 s fails the step; one
// at 59 s passes. The printed lines are checked byte for byte so a reformat of
// the verdict cannot silently change what CI goes red on.
func TestRun(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		want   string
		failed bool
	}{
		{
			name:   "one package over the minute bar",
			in:     `{"Action":"pass","Package":"example.com/slow","Elapsed":61}` + "\n",
			want:   "TESTDUR FAIL pkg=example.com/slow s=61 bar=60\n",
			failed: true,
		},
		{
			name:   "one package under the minute bar",
			in:     `{"Action":"pass","Package":"example.com/fast","Elapsed":59}` + "\n",
			want:   "TESTDUR OK pkg=example.com/fast s=59\n",
			failed: false,
		},
		{
			name: "two packages, the step total crosses two minutes",
			in: `{"Action":"pass","Package":"example.com/a","Elapsed":70}` + "\n" +
				`{"Action":"pass","Package":"example.com/b","Elapsed":51}` + "\n",
			want: "TESTDUR FAIL pkg=example.com/a s=70 bar=60\n" +
				"TESTDUR FAIL pkg=<total> s=121 bar=120\n",
			failed: true,
		},
		{
			name: "OK names the slowest package",
			in: `{"Action":"pass","Package":"example.com/b","Elapsed":40}` + "\n" +
				`{"Action":"pass","Package":"example.com/a","Elapsed":20}` + "\n",
			want:   "TESTDUR OK pkg=example.com/b s=40\n",
			failed: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var sb strings.Builder
			failed := run(strings.NewReader(tc.in), &sb)
			if sb.String() != tc.want {
				t.Errorf("output mismatch\n got: %q\nwant: %q", sb.String(), tc.want)
			}
			if failed != tc.failed {
				t.Errorf("failed flag = %v, want %v", failed, tc.failed)
			}
		})
	}
}
