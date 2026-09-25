package buildinfo_test

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
)

// TestParseTakesEveryToolsLineApart holds the ONE grammar from the reading side.
//
// The hurt (#1297): `nova-version snapshot --bin ~/.local/bin` refused a whole
// install because nova-merge prints a fifth `build=<hex>` token, and the reader
// demanded exactly four. Every reader in this tree had its own four-token
// parser, so a tool that says one more true thing about itself broke them one
// at a time. The grammar is four mandatory tokens and then any number of
// `key=value` extras, and this is the only place it is spelled out.
func TestParseTakesEveryToolsLineApart(t *testing.T) {
	const stamp = "v0.15.3-0.20260918044559-d576bf6bbabb"
	for _, tc := range []struct {
		name     string
		line     string
		tool     string
		version  string
		platform string
		extras   int
	}{
		{
			name:     "the four-token line nineteen tools print",
			line:     "nova-bus " + stamp + " darwin/arm64 go1.27.1",
			tool:     "nova-bus",
			version:  stamp,
			platform: "darwin/arm64",
		},
		{
			name:     "nova-merge's own file digest as a fifth token",
			line:     "nova-merge " + stamp + " darwin/arm64 go1.27.1 build=9c1885748f57",
			tool:     "nova-merge",
			version:  stamp,
			platform: "darwin/arm64",
			extras:   1,
		},
		{
			name:     "nova-sandbox's backend and platform as extras",
			line:     "nova-sandbox " + stamp + " darwin/arm64 go1.27.1 backend=sandbox-exec platform=darwin",
			tool:     "nova-sandbox",
			version:  stamp,
			platform: "darwin/arm64",
			extras:   2,
		},
		{
			name:     "a devel build with no stamp is still the grammar",
			line:     "nova-work devel linux/amd64 go1.27.1",
			tool:     "nova-work",
			version:  "devel",
			platform: "linux/amd64",
		},
		{
			name:     "trailing newline and carriage return are not tokens",
			line:     "nova-ci " + stamp + " windows/amd64 go1.27.1\r\nsomething else\n",
			tool:     "nova-ci",
			version:  stamp,
			platform: "windows/amd64",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := buildinfo.Parse(tc.line)
			if !ok {
				t.Fatalf("Parse refused a line that obeys the grammar:\n%s", tc.line)
			}
			if got.Tool != tc.tool || got.Version != tc.version || got.Platform != tc.platform {
				t.Errorf("Parse read tool=%q version=%q platform=%q, want %q %q %q",
					got.Tool, got.Version, got.Platform, tc.tool, tc.version, tc.platform)
			}
			if got.GoVersion != "go1.27.1" {
				t.Errorf("Parse read go version %q, want go1.27.1", got.GoVersion)
			}
			if len(got.Extras) != tc.extras {
				t.Errorf("Parse read %d extras, want %d: %v", len(got.Extras), tc.extras, got.Extras)
			}
		})
	}
}

// TestParseRefusesWhatIsNotAVersionLine keeps the grammar from being a synonym
// for "any line at all": a refusal that accepts everything cannot tell a caller
// that the binary it just ran is broken.
func TestParseRefusesWhatIsNotAVersionLine(t *testing.T) {
	for _, tc := range []struct{ name, line string }{
		{"nothing at all", ""},
		{"a usage refusal, which is what a nova tool prints with no verb", "nova-swarm: no verb given; run: nova-swarm help"},
		{"three tokens: the platform is missing", "nova-bus v1 go1.27.1"},
		{"field three carries no goos/goarch slash", "nova-bus v1 darwin go1.27.1"},
		{"a fifth token that is not key=value", "nova-bus v1 darwin/arm64 go1.27.1 9c1885748f57"},
		{"an extra with an empty value says nothing", "nova-bus v1 darwin/arm64 go1.27.1 build="},
		{"an extra with an empty key names nothing", "nova-bus v1 darwin/arm64 go1.27.1 =9c18"},
		{"the sandbox's old shape, which no reader could take apart", "SANDBOX VERSION tool=nova-sandbox version=v1 backend=sandbox-exec platform=darwin"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, ok := buildinfo.Parse(tc.line); ok {
				t.Errorf("Parse accepted %q as a version line: %+v", tc.line, got)
			}
		})
	}
}

// TestExtraFindsWhatTheToolSaidAboutItself is why extras are key=value rather
// than free tokens: a reader asks for the fact it wants by name and is never
// holding a position.
func TestExtraFindsWhatTheToolSaidAboutItself(t *testing.T) {
	f, ok := buildinfo.Parse("nova-merge v1 darwin/arm64 go1.27.1 build=9c1885748f57")
	if !ok {
		t.Fatal("Parse refused nova-merge's line")
	}
	if v, ok := f.Extra("build"); !ok || v != "9c1885748f57" {
		t.Errorf("Extra(build) = %q %v, want 9c1885748f57 true", v, ok)
	}
	if _, ok := f.Extra("backend"); ok {
		t.Error("Extra(backend) found a key the line does not carry")
	}
}

// TestLineCarriesItsExtrasThroughTheOneWriter: the writer and the reader are
// one pair, so a tool that adds a fact adds it in the shape every reader
// already takes apart -- which is what nova-merge's hand-rolled Fprintf did not
// guarantee.
func TestLineCarriesItsExtrasThroughTheOneWriter(t *testing.T) {
	line := buildinfo.Line("nova-merge", "v9.9.9", "build=9c1885748f57")
	f, ok := buildinfo.Parse(line)
	if !ok {
		t.Fatalf("the line this package writes is not one it reads:\n%s", line)
	}
	if f.Tool != "nova-merge" || f.Version != "v9.9.9" {
		t.Errorf("round trip lost the identity: %+v", f)
	}
	if v, _ := f.Extra("build"); v != "9c1885748f57" {
		t.Errorf("round trip lost the extra: %+v", f)
	}
	if plain, _ := buildinfo.Parse(buildinfo.Line("nova-bus", "v9.9.9")); len(plain.Extras) != 0 {
		t.Errorf("a tool with nothing to add printed extras: %v", plain.Extras)
	}
}

// TestLineRefusesAnExtraThatIsNotKeyValue makes the writer as strict as the
// reader: an extra that a reader would refuse must never be printable, or the
// grammar is enforced only on the day somebody runs the snapshot.
func TestLineRefusesAnExtraThatIsNotKeyValue(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("Line accepted an extra that is not key=value; the reader refuses it, so the writer must too")
		}
	}()
	_ = buildinfo.Line("nova-merge", "v9.9.9", "9c1885748f57")
}
