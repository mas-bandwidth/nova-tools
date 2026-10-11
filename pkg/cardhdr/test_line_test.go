package cardhdr_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
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
		assert.Empty(t, why, "ParseTest(%q) = %+v %q, want %+v", v, got, why, want)
		assert.Equal(t, want, got, "ParseTest(%q) = %+v %q, want %+v", v, got, why, want)
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
		assert.Empty(t, got, "ParseTest(%q) = %+v %q, want a refusal with %q and the remedy", v, got, why, wantWhy)
		assert.Contains(t, why, wantWhy, "ParseTest(%q) = %+v %q, want a refusal with %q and the remedy", v, got, why, wantWhy)
		assert.Contains(t, why, "TEST: none <why", "ParseTest(%q) = %+v %q, want a refusal with %q and the remedy", v, got, why, wantWhy)
	}
}

// TestParseTestRefusesAPackagePattern is the nova-tools#4401 read's item 2:
// a TEST over `./...` ran every package it matches, so a package that failed
// to build at base read as the named test's red while the base row showed
// `--- PASS: TestAdd`. A TEST names one package; a pattern is refused on one
// line that says so, with the remedy.
func TestParseTestRefusesAPackagePattern(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"./... TestAdd", "./internal/... TestAdd", "internal/x/... TestAdd", "-tags functional ./... TestAdd", "... TestAdd", "./x... TestAdd"} {
		got, why := cardhdr.ParseTest(v)
		assert.Empty(t, got, "ParseTest(%q) = %+v %q, want a one-line refusal naming the pattern", v, got, why)
		assert.Contains(t, why, "not one package", "ParseTest(%q) = %+v %q, want a one-line refusal naming the pattern", v, got, why)
		assert.Contains(t, why, "TEST: none <why", "ParseTest(%q) = %+v %q, want a one-line refusal naming the pattern", v, got, why)
		assert.NotContains(t, why, "\n", "ParseTest(%q) = %+v %q, want a one-line refusal naming the pattern", v, got, why)
	}
	got, why := cardhdr.ParseTest("./x TestAdd")
	assert.Empty(t, why, "one package: %+v %q", got, why)
	assert.Equal(t, "./x", got.Package, "one package: %+v %q", got, why)
}
