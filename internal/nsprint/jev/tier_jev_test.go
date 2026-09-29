package jev_test

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/jev"
)

func TestTierJevFormatAndString(t *testing.T) {
	t.Parallel()

	tj := jev.TierJev{
		Tier:       "pro",
		Type:       "verb",
		Confidence: 0.85,
		Why:        "command-line verb or flag with tests",
	}

	wantFmt := "tier=pro type=verb conf=0.85 why=command-line verb or flag with tests"
	if tj.Format() != wantFmt {
		t.Errorf("Format() = %q, want %q", tj.Format(), wantFmt)
	}

	wantStr := "TIER-JEV tier=pro type=verb conf=0.85 why=command-line verb or flag with tests"
	if tj.String() != wantStr {
		t.Errorf("String() = %q, want %q", tj.String(), wantStr)
	}
}

func TestParseTierJev(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in     string
		want   jev.TierJev
		wantOK bool
	}{
		{
			in: "TIER-JEV: tier=pro type=verb conf=0.85 why=command-line verb or flag with tests",
			want: jev.TierJev{
				Tier:       "pro",
				Type:       "verb",
				Confidence: 0.85,
				Why:        "command-line verb or flag with tests",
			},
			wantOK: true,
		},
		{
			in: "TIER-JEV tier=flash type=docs conf=0.95 why=documentation only",
			want: jev.TierJev{
				Tier:       "flash",
				Type:       "docs",
				Confidence: 0.95,
				Why:        "documentation only",
			},
			wantOK: true,
		},
		{
			in: "tier=frontier type=spec conf=0.90 why=specification across packages",
			want: jev.TierJev{
				Tier:       "frontier",
				Type:       "spec",
				Confidence: 0.90,
				Why:        "specification across packages",
			},
			wantOK: true,
		},
		{
			in: "TIER-JEV: tier=pro type=lua conf=0.90 why=Redis Function (Lua) and caller",
			want: jev.TierJev{
				Tier:       "pro",
				Type:       "lua",
				Confidence: 0.90,
				Why:        "Redis Function (Lua) and caller",
			},
			wantOK: true,
		},
		{
			in:     "",
			wantOK: false,
		},
		{
			in:     "TIER-JEV: tier=unknown type=verb conf=0.80 why=test",
			wantOK: false,
		},
		{
			in:     "TIER-JEV: tier=pro type=invalidtype conf=0.80 why=test",
			wantOK: false,
		},
	}

	for _, c := range cases {
		got, ok := jev.ParseTierJev(c.in)
		if ok != c.wantOK {
			t.Errorf("ParseTierJev(%q) ok = %v, want %v", c.in, ok, c.wantOK)
			continue
		}
		if ok {
			if got.Tier != c.want.Tier || got.Type != c.want.Type ||
				got.Confidence != c.want.Confidence || got.Why != c.want.Why {
				t.Errorf("ParseTierJev(%q) = %+v, want %+v", c.in, got, c.want)
			}
		}
	}
}

func TestClassifyCut(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		title    string
		paths    string
		doneWhen string
		body     string
		wantType string
		wantTier string
	}{
		{
			name:     "docs card",
			title:    "update contributing guide",
			paths:    "docs/CONTRIBUTING.md, docs/GUIDE.md",
			doneWhen: "markdown lint passes",
			body:     "Documentation update only.",
			wantType: "docs",
			wantTier: "flash",
		},
		{
			name:     "spec card",
			title:    "SPEC-sandbox: container design",
			paths:    "docs/SPEC-sandbox.md",
			doneWhen: "spec approved",
			body:     "Specification for sandboxing.",
			wantType: "spec",
			wantTier: "frontier",
		},
		{
			name:     "fixture card",
			title:    "add testdata for pr record",
			paths:    "internal/nsprint/land/testdata/pr.json",
			doneWhen: "tests read fixture",
			body:     "Test fixtures only.",
			wantType: "fixture",
			wantTier: "flash",
		},
		{
			name:     "lua card",
			title:    "update card pool moves in lua",
			paths:    "internal/nsprint/fn/lua/card_pool.lua",
			doneWhen: "go test ./internal/nsprint/card/... passes",
			body:     "Update Redis Function (Lua).",
			wantType: "lua",
			wantTier: "pro",
		},
		{
			name:     "read card",
			title:    "read: first pass audit of land stream",
			paths:    "internal/nsprint/land/stream.go",
			doneWhen: "audit complete",
			body:     "Judge existing implementation.",
			wantType: "read",
			wantTier: "flash",
		},
		{
			name:     "test fix card",
			title:    "fix flaky TestTaskCardsHaveOneWriter",
			paths:    "internal/ci/taskwriter_class_test.go",
			doneWhen: "go test ./internal/ci passes 100 times",
			body:     "Repair flaky test.",
			wantType: "test-fix",
			wantTier: "flash",
		},
		{
			name:     "refactor multi-package card",
			title:    "refactor store interface",
			paths:    "internal/nsprint/store/store.go, internal/nsprint/card/cut.go, internal/nsprint/jev/sync.go, internal/nsprint/ws/ws.go",
			doneWhen: "make vet passes",
			body:     "Clean up store across packages.",
			wantType: "refactor",
			wantTier: "frontier",
		},
		{
			name:     "verb card",
			title:    "add nova-sprint jev report",
			paths:    "cmd/nova-sprint/jev.go, internal/nsprint/jev/report.go",
			doneWhen: "nova-sprint jev report prints table",
			body:     "Add new CLI verb.",
			wantType: "verb",
			wantTier: "pro",
		},
		{
			name:     "explicit TYPE and ROUTE in body",
			title:    "misc changes",
			paths:    "cmd/x.go",
			doneWhen: "done",
			body:     "STREAM: s\nTYPE: refactor\nROUTE: frontier\nPATHS: cmd/x.go\n",
			wantType: "refactor",
			wantTier: "frontier",
		},
	}

	for _, c := range cases {
		tj := jev.ClassifyCut(c.title, c.paths, c.doneWhen, c.body)
		if tj.Type != c.wantType || tj.Tier != c.wantTier {
			t.Errorf("%s: ClassifyCut = type:%s tier:%s (%+v), want type:%s tier:%s",
				c.name, tj.Type, tj.Tier, tj, c.wantType, c.wantTier)
		}
		if tj.Confidence <= 0 || tj.Why == "" {
			t.Errorf("%s: ClassifyCut invalid conf: %v or why: %q", c.name, tj.Confidence, tj.Why)
		}
	}
}
