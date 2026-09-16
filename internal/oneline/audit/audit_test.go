package audit

import (
	"reflect"
	"testing"
)

// verbsOf must model Go's explicit argument indexes: %[2]s reads the second
// argument and formats it with %s, so the verb at argument position 1 is 's'
// and the verb at position 0 is what formats the first argument, however the
// directives are ordered. Before this modeling, the '[' itself was read as the
// verb and a correctly-indexed format was reported as a raw print that needed
// escaping -- the wrong defect.
func TestVerbsOfModelsIndexedArguments(t *testing.T) {
	cases := []struct {
		format string
		want   []byte
	}{
		{"%d %s", []byte{'d', 's'}},
		{"%[2]s %[1]d", []byte{'d', 's'}},
		{"%[1]q", []byte{'q'}},
		{"%[2]d", []byte{0, 'd'}},
	}
	for _, tc := range cases {
		got, _ := verbsOf(t, tc.format)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("verbsOf(%q) = %q, want %q", tc.format, got, tc.want)
		}
	}
}
