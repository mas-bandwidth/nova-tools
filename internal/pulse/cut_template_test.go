package pulse

import (
	"strings"
	"testing"
)

func TestCutTemplateFormatDependsOn(t *testing.T) {
	tests := []struct {
		name      string
		deps      []string
		wantLine  string
		wantValue string
	}{
		{
			name:      "nil slice",
			deps:      nil,
			wantLine:  "DEPENDS-ON: -",
			wantValue: "-",
		},
		{
			name:      "empty slice",
			deps:      []string{},
			wantLine:  "DEPENDS-ON: -",
			wantValue: "-",
		},
		{
			name:      "slice with empty string",
			deps:      []string{""},
			wantLine:  "DEPENDS-ON: -",
			wantValue: "-",
		},
		{
			name:      "slice with single dash",
			deps:      []string{"-"},
			wantLine:  "DEPENDS-ON: -",
			wantValue: "-",
		},
		{
			name:      "slice with single dash and spaces",
			deps:      []string{"  -  "},
			wantLine:  "DEPENDS-ON: -",
			wantValue: "-",
		},
		{
			name:      "single dependency",
			deps:      []string{"tools-01"},
			wantLine:  "DEPENDS-ON: tools-01",
			wantValue: "tools-01",
		},
		{
			name:      "multiple dependencies in slice",
			deps:      []string{"tools-01", "tools-04"},
			wantLine:  "DEPENDS-ON: tools-01, tools-04",
			wantValue: "tools-01, tools-04",
		},
		{
			name:      "comma-separated dependencies in single element",
			deps:      []string{"tools-01, tools-04"},
			wantLine:  "DEPENDS-ON: tools-01, tools-04",
			wantValue: "tools-01, tools-04",
		},
		{
			name:      "comma-separated dependencies without space",
			deps:      []string{"tools-01,tools-04"},
			wantLine:  "DEPENDS-ON: tools-01, tools-04",
			wantValue: "tools-01, tools-04",
		},
		{
			name:      "elements with leading and trailing spaces",
			deps:      []string{"  tools-01  ", "  tools-04  "},
			wantLine:  "DEPENDS-ON: tools-01, tools-04",
			wantValue: "tools-01, tools-04",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotLine := FormatDependsOn(tt.deps)
			if gotLine != tt.wantLine {
				t.Errorf("FormatDependsOn(%v) = %q, want %q", tt.deps, gotLine, tt.wantLine)
			}
			gotVal := FormatDependsOnValue(tt.deps)
			if gotVal != tt.wantValue {
				t.Errorf("FormatDependsOnValue(%v) = %q, want %q", tt.deps, gotVal, tt.wantValue)
			}
		})
	}
}

func TestCutTemplateParseDependsOn(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{input: "", want: []string{"-"}},
		{input: "  ", want: []string{"-"}},
		{input: "-", want: []string{"-"}},
		{input: " - ", want: []string{"-"}},
		{input: "tools-01", want: []string{"tools-01"}},
		{input: "tools-01, tools-04", want: []string{"tools-01", "tools-04"}},
		{input: "tools-01,tools-04", want: []string{"tools-01", "tools-04"}},
		{input: " tools-01 , tools-04 ", want: []string{"tools-01", "tools-04"}},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ParseDependsOn(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("ParseDependsOn(%q) len = %d, want %d (%v vs %v)", tt.input, len(got), len(tt.want), got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("ParseDependsOn(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestCutTemplateApplyDependsOn(t *testing.T) {
	tests := []struct {
		name string
		tmpl string
		deps []string
		want string
	}{
		{
			name: "insert after PATHS when independent",
			tmpl: "KIND: fix\nPATHS: internal/pulse/cut.go\nFILES: 1\nTEST: ./internal/pulse/ TestCut",
			deps: nil,
			want: "KIND: fix\nPATHS: internal/pulse/cut.go\nDEPENDS-ON: -\nFILES: 1\nTEST: ./internal/pulse/ TestCut",
		},
		{
			name: "insert after PATHS when dash provided",
			tmpl: "KIND: fix\nPATHS: internal/pulse/cut.go\nFILES: 1\nTEST: ./internal/pulse/ TestCut",
			deps: []string{"-"},
			want: "KIND: fix\nPATHS: internal/pulse/cut.go\nDEPENDS-ON: -\nFILES: 1\nTEST: ./internal/pulse/ TestCut",
		},
		{
			name: "insert after PATHS when dependencies provided",
			tmpl: "KIND: fix\nPATHS: internal/pulse/cut.go\nFILES: 1\nTEST: ./internal/pulse/ TestCut",
			deps: []string{"tools-01", "tools-04"},
			want: "KIND: fix\nPATHS: internal/pulse/cut.go\nDEPENDS-ON: tools-01, tools-04\nFILES: 1\nTEST: ./internal/pulse/ TestCut",
		},
		{
			name: "insert after TEST when PATHS is absent",
			tmpl: "KIND: report\nSCHEMA: v2\nTEST: ./internal/pulse/ TestCut\nRUN: go test ./...",
			deps: []string{"tools-01"},
			want: "KIND: report\nSCHEMA: v2\nTEST: ./internal/pulse/ TestCut\nDEPENDS-ON: tools-01\nRUN: go test ./...",
		},
		{
			name: "update existing DEPENDS-ON in place",
			tmpl: "KIND: fix\nPATHS: internal/pulse/cut.go\nDEPENDS-ON: -\nFILES: 1\nTEST: ./internal/pulse/ TestCut",
			deps: []string{"tools-01", "tools-04"},
			want: "KIND: fix\nPATHS: internal/pulse/cut.go\nDEPENDS-ON: tools-01, tools-04\nFILES: 1\nTEST: ./internal/pulse/ TestCut",
		},
		{
			name: "replace placeholder <depends-on>",
			tmpl: "KIND: fix\nPATHS: internal/pulse/cut.go\nDEPENDS-ON: <depends-on>\nFILES: 1\nTEST: ./internal/pulse/ TestCut",
			deps: []string{"tools-01", "tools-04"},
			want: "KIND: fix\nPATHS: internal/pulse/cut.go\nDEPENDS-ON: tools-01, tools-04\nFILES: 1\nTEST: ./internal/pulse/ TestCut",
		},
		{
			name: "legacy template with neither PATHS nor TEST inserts after line 1",
			tmpl: "RESULT label sha=123456789012\nYou are a worker.\nSTEP 1. mkdir -p scratch && git clone -q https://example.com/o/r.git . && git checkout -b b\n",
			deps: []string{"tools-01"},
			want: "RESULT label sha=123456789012\nDEPENDS-ON: tools-01\nYou are a worker.\nSTEP 1. mkdir -p scratch && git clone -q https://example.com/o/r.git . && git checkout -b b\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ApplyDependsOn(tt.tmpl, tt.deps)
			if got != tt.want {
				t.Errorf("ApplyDependsOn:\ngot:\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}

func TestCutTemplateRoundtripAndValidationControls(t *testing.T) {
	// Fixture P (accept.md shape from docs/SPEC-CARD.md clause 9 & 10)
	fixtureAccept := strings.Join([]string{
		"RESULT: tools-03 sha=000000000000",
		"KIND: fix",
		"SCHEMA: v2",
		"ATTEMPT: 1",
		"DEADLINE: 2700",
		"LEG: go",
		"REPO: mas-bandwidth/nova-tools",
		"BASE: dev",
		"base-repo: https://example.com/mas-bandwidth/nova-tools.git",
		"base-sha: af6a9fccf33199b4edfc586ab3c7f0c5e1a2d71d",
		"PATHS: internal/pulse/harveststale.go, internal/pulse/harveststale_test.go",
		"DEPENDS-ON: -",
		"FILES: 2",
		"TEST: ./internal/pulse/ TestStaleBaseAcceptsBranchWhoseParentIsTargetTip",
	}, "\n")

	// Fixture D (refuse-depends-on-missing.md: accept.md without line 12)
	fixtureMissing := strings.Join([]string{
		"RESULT: tools-03 sha=000000000000",
		"KIND: fix",
		"SCHEMA: v2",
		"ATTEMPT: 1",
		"DEADLINE: 2700",
		"LEG: go",
		"REPO: mas-bandwidth/nova-tools",
		"BASE: dev",
		"base-repo: https://example.com/mas-bandwidth/nova-tools.git",
		"base-sha: af6a9fccf33199b4edfc586ab3c7f0c5e1a2d71d",
		"PATHS: internal/pulse/harveststale.go, internal/pulse/harveststale_test.go",
		"FILES: 2",
		"TEST: ./internal/pulse/ TestStaleBaseAcceptsBranchWhoseParentIsTargetTip",
	}, "\n")

	// Control 1: Rendering missing fixture with independent dependencies yields fixtureAccept byte-for-byte.
	t.Run("missing to accept byte-for-byte", func(t *testing.T) {
		got := ApplyDependsOn(fixtureMissing, nil)
		if got != fixtureAccept {
			t.Fatalf("ApplyDependsOn(missing, nil) did not yield accept.md:\ngot:\n%s\nwant:\n%s", got, fixtureAccept)
		}
	})

	// Control 2: Roundtrip on accept.md with independent dependencies is idempotent.
	t.Run("accept roundtrip idempotent", func(t *testing.T) {
		got := ApplyDependsOn(fixtureAccept, nil)
		if got != fixtureAccept {
			t.Fatalf("ApplyDependsOn(accept, nil) changed accept.md:\ngot:\n%s\nwant:\n%s", got, fixtureAccept)
		}
		gotDash := ApplyDependsOn(fixtureAccept, []string{"-"})
		if gotDash != fixtureAccept {
			t.Fatalf("ApplyDependsOn(accept, [-]) changed accept.md:\ngot:\n%s\nwant:\n%s", gotDash, fixtureAccept)
		}
	})

	// Control 3: Providing dependencies to missing fixture renders DEPENDS-ON: a, b at line 12 immediately after PATHS.
	t.Run("missing to dependencies provided", func(t *testing.T) {
		got := ApplyDependsOn(fixtureMissing, []string{"tools-01", "tools-04"})
		lines := strings.Split(got, "\n")
		if len(lines) < 14 {
			t.Fatalf("unexpected line count: %d", len(lines))
		}
		if lines[10] != "PATHS: internal/pulse/harveststale.go, internal/pulse/harveststale_test.go" {
			t.Errorf("line 11 is %q, want PATHS: ...", lines[10])
		}
		if lines[11] != "DEPENDS-ON: tools-01, tools-04" {
			t.Errorf("line 12 is %q, want DEPENDS-ON: tools-01, tools-04", lines[11])
		}
		if lines[12] != "FILES: 2" {
			t.Errorf("line 13 is %q, want FILES: 2", lines[12])
		}

		// Control 4: Re-applying dependencies to already rendered output is idempotent.
		roundtrip := ApplyDependsOn(got, []string{"tools-01", "tools-04"})
		if roundtrip != got {
			t.Fatalf("roundtrip with dependencies not idempotent:\ngot:\n%s\nwant:\n%s", roundtrip, got)
		}
	})
}

func TestCutTemplatePreservesFencedPriorCardEvidence(t *testing.T) {
	tmpl := strings.Join([]string{
		"RESULT: tools-03 sha=000000000000",
		"KIND: fix",
		"PATHS: internal/pulse/harveststale.go",
		"FILES: 1",
		"TEST: ./internal/pulse/ TestStaleBaseAcceptsBranchWhoseParentIsTargetTip",
		"",
		"## Prior attempt",
		"",
		"```",
		"RESULT: tools-02 sha=111111111111",
		"KIND: fix",
		"PATHS: internal/pulse/harveststale.go",
		"DEPENDS-ON: old-card",
		"FILES: 1",
		"TEST: ./internal/pulse/ TestStaleBaseAcceptsBranchWhoseParentIsTargetTip",
		"```",
	}, "\n")

	got := ApplyDependsOn(tmpl, []string{"tools-01", "tools-04"})

	want := strings.Join([]string{
		"RESULT: tools-03 sha=000000000000",
		"KIND: fix",
		"PATHS: internal/pulse/harveststale.go",
		"DEPENDS-ON: tools-01, tools-04",
		"FILES: 1",
		"TEST: ./internal/pulse/ TestStaleBaseAcceptsBranchWhoseParentIsTargetTip",
		"",
		"## Prior attempt",
		"",
		"```",
		"RESULT: tools-02 sha=111111111111",
		"KIND: fix",
		"PATHS: internal/pulse/harveststale.go",
		"DEPENDS-ON: old-card",
		"FILES: 1",
		"TEST: ./internal/pulse/ TestStaleBaseAcceptsBranchWhoseParentIsTargetTip",
		"```",
	}, "\n")

	if got != want {
		t.Fatalf("ApplyDependsOn did not preserve fenced prior card evidence:\ngot:\n%s\nwant:\n%s", got, want)
	}

	const fencedEvidence = "DEPENDS-ON: old-card"
	if !strings.Contains(got, fencedEvidence) {
		t.Fatalf("fenced evidence %q was not preserved in output:\n%s", fencedEvidence, got)
	}
}

func TestCutTemplateInsertsDependsOnWhenNoPathsOrTest(t *testing.T) {
	tmpl := strings.Join([]string{
		"RESULT: tools-05 sha=000000000000",
		"KIND: fix",
		"FILES: 1",
		"",
		"## Details",
		"Some details here.",
	}, "\n")

	got := ApplyDependsOn(tmpl, []string{"tools-01", "tools-04"})

	want := strings.Join([]string{
		"RESULT: tools-05 sha=000000000000",
		"KIND: fix",
		"FILES: 1",
		"DEPENDS-ON: tools-01, tools-04",
		"",
		"## Details",
		"Some details here.",
	}, "\n")

	if got != want {
		t.Fatalf("ApplyDependsOn did not insert DEPENDS-ON at end of header block:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

