package cardhdr_test

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/cardhdr"
)

// TestParseTestIsTheOneGrammar is nova-tools#4313's TEST line: build tags
// if the test needs them, a package (./-relative or repository-relative) and
// a Go test name, or none with a why; everything else is refused on one line
// with the remedy, and a bare none is refused because the reader must see why.
func TestParseTestIsTheOneGrammar(t *testing.T) {
	t.Parallel()

	for v, want := range map[string]cardhdr.TestLine{
		"./internal/x TestY":            {Package: "./internal/x", Name: "TestY"},
		"  ./internal/x/  ExampleZ ":    {Package: "./internal/x/", Name: "ExampleZ"},
		". FuzzQ":                       {Package: ".", Name: "FuzzQ"},
		"internal/x TestY":              {Package: "internal/x", Name: "TestY"},
		"-tags functional ./x TestY":    {Package: "./x", Name: "TestY", Tags: "functional"},
		"-tags=slow,functional . TestY": {Package: ".", Name: "TestY", Tags: "slow,functional"},
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
		"":                     "no TEST line",
		"none":                 "TEST: none says no why",
		"none   ":              "TEST: none says no why",
		"rm -rf /":             "is not `[-tags <tags>] <package> <TestName>` or `none <why>`",
		"-tags ./x TestY":      "is not",
		"-tags a;b ./x TestY":  "is not",
		"-run TestY ./x TestY": "is not",
		"/abs TestY":           "is not",
		"C:/x TestY":           "is not",
		"../x TestY":           "is not",
		"./a/../b TestY":       "is not",
		"./x NotATest":         "is not",
		"./x TestY; echo":      "is not",
	} {
		got, why := cardhdr.ParseTest(v)
		if got != (cardhdr.TestLine{}) || !strings.Contains(why, wantWhy) || !strings.Contains(why, "TEST: none <why") {
			t.Errorf("ParseTest(%q) = %+v %q, want a refusal with %q and the remedy", v, got, why, wantWhy)
		}
	}
	if s := (cardhdr.TestLine{Package: "./x", Name: "TestY"}).String(); s != "./x TestY" {
		t.Errorf("String() = %q", s)
	}
	if s := (cardhdr.TestLine{Package: "./x", Name: "TestY", Tags: "functional"}).String(); s != "-tags functional ./x TestY" {
		t.Errorf("String() = %q", s)
	}
	for pkg, want := range map[string]string{"internal/x": "./internal/x", "./x": "./x", ".": "."} {
		if got := (cardhdr.TestLine{Package: pkg}).GoPackage(); got != want {
			t.Errorf("GoPackage(%q) = %q, want %q", pkg, got, want)
		}
	}
	if s := (cardhdr.TestLine{None: true, Why: "why"}).String(); s != "none why" {
		t.Errorf("String() = %q", s)
	}
}
