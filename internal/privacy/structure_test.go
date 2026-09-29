package privacy_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/privacy"
)

// structureConfig is a neutral set of shapes: an absolute home path and a
// record filename refuse; a tool name and a local host name warn.
const structureConfig = `refuse home-path /home/[a-z]+\b(?:/[A-Za-z0-9._+~-]+)*
refuse record-file \b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}(?:-\d+)?\.(?:md|jsonl)\b
warn tool-name \bacme-[a-z][a-z0-9-]*\b
warn host \b[a-z]+\.lan\b
allow acme-docs
`

func structureRules(t *testing.T) privacy.Rules {
	t.Helper()
	f, err := privacy.ParseConfig(structureConfig)
	if err != nil {
		t.Fatal(err)
	}
	r, err := f.Rules()
	if err != nil {
		t.Fatal(err)
	}
	return r
}

const leakSpecimen = `The evidence is at /home/ada/work/acme-logs/latest and the record
5a69bb12-003d-42b7-88a2-b777be2907a3.md carries the full transcript.`

const genericRepair = `The evidence is in the run logs of a local checkout; the record carries the full transcript.`

func TestEveryRefusingShapeFiresAndItsNeighbourDoesNot(t *testing.T) {
	t.Parallel()
	r := structureRules(t)
	for name, tc := range map[string]struct {
		payload string
		refuses bool
	}{
		"home path":            {"see /home/ada/work/specs/x.md", true},
		"bare home":            {"it lives under /home/ada somewhere", true},
		"tilde form is fine":   {"clone into ~/work as the docs say", false},
		"bare /home is fine":   {"the /home directory holds accounts", false},
		"record filename":      {"read a11d0695-e984-4e04-9c88-b777be2907a3.md first", true},
		"generation suffix":    {"the second 5a69bb12-003d-42b7-88a2-b777be2907a3-2.md is open", true},
		"transcript":           {"grep the 5a69bb12-003d-42b7-88a2-b777be2907a3.jsonl file", true},
		"bare guid is fine":    {"request id 5a69bb12-003d-42b7-88a2-b777be2907a3 failed", false},
		"short hex is fine":    {"commit b66c378 is the authority", false},
		"no shapes configured": {"ordinary words only", false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			refusing := false
			for _, h := range r.Structure(tc.payload) {
				refusing = refusing || h.Refuse
			}
			if refusing != tc.refuses {
				t.Errorf("refuses = %v, want %v for %q", refusing, tc.refuses, tc.payload)
			}
		})
	}
}

func TestDefaultRulesHaveNoStructureShapes(t *testing.T) {
	t.Parallel()
	if hits := privacy.DefaultRules().Structure(leakSpecimen); len(hits) != 0 {
		t.Errorf("hits %+v: no shape is built in", hits)
	}
}

// A refusing hit outranks an unreadable corpus, and the corpus problem is
// still spoken.
func TestARefusalSurvivesABrokenCorpus(t *testing.T) {
	t.Parallel()
	c := privacy.Corpus{
		Rules:   structureRules(t),
		Sources: []privacy.SourceLoad{{Path: "later.md", Err: errors.New("permission denied")}},
	}
	r := privacy.Judge(c, leakSpecimen)
	if r.Outcome != privacy.Flagged || len(r.StructureRefusals()) == 0 {
		t.Fatalf("outcome %s, want FLAGGED by structure", r.Outcome)
	}
	if !strings.Contains(strings.Join(r.Warnings, "\n"), "could not be read") {
		t.Errorf("the corpus problem is spoken beside the refusal: %v", r.Warnings)
	}
}

func TestAnEmptyPrivateCorpusCannotSilenceTheRefusalEither(t *testing.T) {
	t.Parallel()
	c := privacy.Corpus{Rules: structureRules(t), Sources: []privacy.SourceLoad{{Path: "later.md"}}}
	r := privacy.Judge(c, leakSpecimen)
	if r.Outcome != privacy.Flagged || len(r.StructureRefusals()) == 0 {
		t.Errorf("outcome %s, want FLAGGED by structure", r.Outcome)
	}
	if !strings.Contains(strings.Join(r.Warnings, "\n"), "verified nothing") {
		t.Errorf("warnings %v", r.Warnings)
	}
}

func TestBothFindingsTravelInOneRefusal(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	r := privacy.Screen(f.withConfig(t, structureConfig), "the zarquon engine of flibberty wumpus lives at /home/ada/notes/later.md")
	if r.Outcome != privacy.Flagged || len(r.Flags) == 0 || len(r.StructureRefusals()) == 0 {
		t.Fatalf("outcome %s flags %d refusals %d", r.Outcome, len(r.Flags), len(r.StructureRefusals()))
	}
	if !strings.Contains(r.Reason, " and ") || !strings.Contains(r.Remedy, "general terms") {
		t.Errorf("reason %q remedy %q name both kinds", r.Reason, r.Remedy)
	}
}

func TestTheGeneralRepairClears(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	r := privacy.Screen(f.withConfig(t, structureConfig), genericRepair)
	if r.Outcome != privacy.UnprovenClean || len(r.StructureRefusals()) != 0 {
		t.Errorf("outcome %s refusals %v", r.Outcome, r.StructureRefusals())
	}
}

func TestWarnShapesSpeakAndDoNotRefuse(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	r := privacy.Screen(f.withConfig(t, structureConfig), "the acme-sync wrapper runs on build.lan every hour")
	if r.Outcome != privacy.UnprovenClean {
		t.Fatalf("outcome %s: a warning shape does not refuse", r.Outcome)
	}
	classes := map[string]string{}
	for _, h := range r.Structure {
		if h.Refuse {
			t.Errorf("%+v refused", h)
		}
		classes[h.Class] = h.Specimen
	}
	if classes["tool-name"] != "acme-sync" || classes["host"] != "build.lan" {
		t.Errorf("classes %v", classes)
	}
	warned := 0
	for _, w := range r.Warnings {
		if strings.HasPrefix(w, "structure: ") {
			warned++
		}
	}
	if warned != 2 {
		t.Errorf("every warning hit is spoken: %v", r.Warnings)
	}
}

func TestAnAllowedSpecimenFiresNothing(t *testing.T) {
	t.Parallel()
	if hits := structureRules(t).Structure("the ACME-DOCS page explains it"); len(hits) != 0 {
		t.Errorf("hits %+v: an allowed specimen never fires, case-insensitively", hits)
	}
}

func TestOrdinaryProseFiresNothing(t *testing.T) {
	t.Parallel()
	if hits := structureRules(t).Structure("The genetic code has thrift. Nothing here is about any machine."); len(hits) != 0 {
		t.Errorf("hits %+v", hits)
	}
}

// A refused span is redacted before the warning shapes run, so one path
// holding a tool name is one hit.
func TestOneSpecimenIsNeverReportedTwice(t *testing.T) {
	t.Parallel()
	hits := structureRules(t).Structure("evidence: /home/ada/work/acme-logs/acme-sync.log")
	if len(hits) != 1 || hits[0].Class != "home-path" || !hits[0].Refuse {
		t.Errorf("hits %+v, want one home-path refusal", hits)
	}
}

func TestDuplicateSpecimensCollapse(t *testing.T) {
	t.Parallel()
	p := "/home/ada/work/x"
	if hits := structureRules(t).Structure(p + " then " + p + " then " + p); len(hits) != 1 {
		t.Errorf("hits %+v, want one", hits)
	}
}
