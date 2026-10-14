package cardhdr_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
)

// TestParseTestReadsACtestLine proves the `ctest <TestName>` form: a ctest name with
// letters, digits, _, . and - parses with Runner "ctest" and no package; a Go line parses
// exactly as before (Runner "go", package and name); a ctest name with a space or an empty
// name is refused with the remedy. Carried from nova-sprint 76d71f25.
func TestParseTestReadsACtestLine(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in   string
		want cardhdr.TestLine
	}{
		{"ctest DecodeAll", cardhdr.TestLine{Runner: "ctest", Name: "DecodeAll"}},
		{"ctest rocketnet.decode_all", cardhdr.TestLine{Runner: "ctest", Name: "rocketnet.decode_all"}},
		{"ctest suite/one-two_3.4", cardhdr.TestLine{Runner: "ctest", Name: "suite/one-two_3.4"}},
		{"  ctest   Foo  ", cardhdr.TestLine{Runner: "ctest", Name: "Foo"}},
		{"CTEST Foo", cardhdr.TestLine{Runner: "ctest", Name: "Foo"}},
		// a Go line reads as before.
		{"pkg/x TestFoo", cardhdr.TestLine{Package: "pkg/x", Name: "TestFoo", Runner: "go"}},
		{"-tags functional pkg/x TestFoo", cardhdr.TestLine{Package: "pkg/x", Name: "TestFoo", Tags: "functional", Runner: "go"}},
	} {
		got, why := cardhdr.ParseTest(tc.in)
		assert.Empty(t, why, "%q", tc.in)
		assert.Equal(t, tc.want, got, "%q", tc.in)
	}
	for _, in := range []string{"ctest Foo Bar", "ctest", "ctest ", "ctest Foo*", "ctest -tags x Foo", "ctest Foo:bar"} {
		got, why := cardhdr.ParseTest(in)
		assert.Equal(t, cardhdr.TestLine{}, got, "%q", in)
		require.NotEmpty(t, why, "%q", in)
		assert.Contains(t, why, "ctest <TestName>", "%q carries the remedy", in)
	}
}
