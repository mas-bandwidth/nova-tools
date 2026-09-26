package cardhdr_test

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/cardhdr"
)

// TestParseTestIsTheOneGrammar is nova-tools#4313's TEST line: a package and
// a Go test name, or none with a why; everything else is refused on one line
// with the remedy, and a bare none is refused because the reader must see why.
func TestParseTestIsTheOneGrammar(t *testing.T) {
	t.Parallel()

	for v, want := range map[string]cardhdr.TestLine{
		"./internal/x TestY":         {Package: "./internal/x", Name: "TestY"},
		"  ./internal/x/  ExampleZ ": {Package: "./internal/x/", Name: "ExampleZ"},
		". FuzzQ":                    {Package: ".", Name: "FuzzQ"},
		"none the change is one docs page; the reader checks it": {None: true, Why: "the change is one docs page; the reader checks it"},
		"none: a fixture line":  {None: true, Why: "a fixture line"},
		"none - a fixture line": {None: true, Why: "a fixture line"},
	} {
		got, why := cardhdr.ParseTest(v)
		if why != "" || got != want {
			t.Errorf("ParseTest(%q) = %+v %q, want %+v", v, got, why, want)
		}
	}
	for v, wantWhy := range map[string]string{
		"":                 "no TEST line",
		"none":             "TEST: none says no why",
		"none   ":          "TEST: none says no why",
		"rm -rf /":         "is not `<package> <TestName>` or `none <why>`",
		"../x TestY":       "is not",
		"./a/../b TestY":   "is not",
		"./x NotATest":     "is not",
		"./x TestY; echo":  "is not",
		"internal/x TestY": "is not",
	} {
		got, why := cardhdr.ParseTest(v)
		if got != (cardhdr.TestLine{}) || !strings.Contains(why, wantWhy) || !strings.Contains(why, "TEST: none <why") {
			t.Errorf("ParseTest(%q) = %+v %q, want a refusal with %q and the remedy", v, got, why, wantWhy)
		}
	}
	if s := (cardhdr.TestLine{Package: "./x", Name: "TestY"}).String(); s != "./x TestY" {
		t.Errorf("String() = %q", s)
	}
	if s := (cardhdr.TestLine{None: true, Why: "why"}).String(); s != "none why" {
		t.Errorf("String() = %q", s)
	}
}
