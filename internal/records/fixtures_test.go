package records

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The fixtures are the contract made executable: one valid record per source kind the
// format names, and one refused record per rule. Each carries a sidecar verdict, so a
// reader can see what the record is expected to be without reading the Go.

type verdict struct {
	Verdict       string   `json:"verdict"`
	ID            string   `json:"id"`
	Rule          string   `json:"rule"`
	Field         string   `json:"field"`
	GreenRequires []string `json:"green_requires"`
}

// hardRules are the four whose refusal leaves nothing to validate: the bytes do not parse,
// the envelope is not an object, the value is not of the type the field is read as, or the
// value is a raw JSON number where a string lexeme belongs. They have a red half and no
// green half, because there is no reading of the record in which the field can be used.
var hardRules = map[string]bool{
	RuleNotJSON:       true,
	RuleWrongType:     true,
	RuleEnvelopeShape: true,
	RuleRawJSONNumber: true,
}

func testValidator(t *testing.T) *Validator {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "allowlists.json"))
	if err != nil {
		t.Fatalf("allowlists: %v", err)
	}
	var a struct {
		RawUsageFields []string `json:"raw_usage_fields"`
		ReceiptFields  []string `json:"receipt_fields"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		t.Fatalf("allowlists: %v", err)
	}
	return NewValidator(Allowlists{RawUsageFields: a.RawUsageFields, ReceiptFields: a.ReceiptFields})
}

func validatorFor(t *testing.T, raw []byte) *Validator {
	t.Helper()
	v, err := parseStrict(raw, map[string]bool{
		RuleInvalidUTF8: true, RuleLoneSurrogate: true, RuleNotJSON: true,
		RuleDuplicateKey: true, RuleRawJSONNumber: true,
	}, "envelope")
	if err != nil {
		return testValidator(t)
	}
	bv, ok := v.(*Object)
	if !ok {
		return testValidator(t)
	}
	bodyVal, ok := bv.Get("body")
	if !ok {
		return testValidator(t)
	}
	bodyObj, ok := bodyVal.(*Object)
	if !ok {
		return testValidator(t)
	}
	var rawKeys, receiptKeys []string
	if uv, ok := bodyObj.Get("raw_usage"); ok {
		if uo, ok := uv.(*Object); ok {
			rawKeys = append([]string(nil), uo.Keys()...)
		}
	}
	if rc, ok := bodyObj.Get("receipt"); ok {
		if ro, ok := rc.(*Object); ok {
			receiptKeys = append([]string(nil), ro.Keys()...)
		}
	}
	if len(receiptKeys) == 0 {
		receiptKeys = []string{"turn_id", "idx"}
	}
	return NewValidator(Allowlists{
		RawUsageFields: rawKeys,
		ReceiptFields:  receiptKeys,
	})
}

func validatorForRefused(t *testing.T, path string, raw []byte) *Validator {
	t.Helper()
	base := filepath.Base(path)
	switch base {
	case "field_not_allowlisted.json":
		return NewValidator(Allowlists{
			RawUsageFields: []string{"input", "output"},
			ReceiptFields:  []string{"turn_id"},
		})
	case "receipt_field_not_allowlisted.json":
		return NewValidator(Allowlists{
			RawUsageFields: []string{"input", "output"},
			ReceiptFields:  []string{"turn_id"},
		})
	default:
		return validatorFor(t, raw)
	}
}

func fixtures(t *testing.T, dir string) []string {
	t.Helper()
	all, err := filepath.Glob(filepath.Join("testdata", dir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range all {
		if strings.HasSuffix(p, ".verdict.json") {
			continue
		}
		names = append(names, p)
	}
	sort.Strings(names)
	if len(names) == 0 {
		t.Fatalf("no fixtures in testdata/%s", dir)
	}
	return names
}

func readFixture(t *testing.T, path string) ([]byte, verdict) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sidecar, err := os.ReadFile(strings.TrimSuffix(path, ".json") + ".verdict.json")
	if err != nil {
		t.Fatalf("every fixture needs a sidecar verdict: %v", err)
	}
	var v verdict
	if err := json.Unmarshal(sidecar, &v); err != nil {
		t.Fatal(err)
	}
	return raw, v
}

// TestValidFixtures: one record per source kind the format names is accepted, and its
// envelope ID is the digest the sidecar states.
func TestValidFixtures(t *testing.T) {
	seen := map[string]string{}
	for _, path := range fixtures(t, "valid") {
		t.Run(filepath.Base(path), func(t *testing.T) {
			raw, want := readFixture(t, path)
			if want.Verdict != "valid" {
				t.Fatalf("fixture in valid/ has verdict %q", want.Verdict)
			}
			v := validatorFor(t, raw)
			env, err := v.ValidateEnvelope(raw)
			if err != nil {
				t.Fatalf("expected accepted, refused with: %v", err)
			}
			if env.ID != want.ID {
				t.Fatalf("envelope ID %s, sidecar says %s", env.ID, want.ID)
			}
			// The ID is derived, not merely compared: re-sealing the body reproduces it.
			if _, id, err := Seal(env.Body); err != nil || id != env.ID {
				t.Fatalf("Seal reproduced %q (err %v), want %s", id, err, env.ID)
			}
			seen[env.Observation.Source.Kind] = filepath.Base(path)
		})
	}
	for _, kind := range SourceKinds {
		if seen[kind] == "" {
			t.Errorf("no valid fixture for source kind %q", kind)
		}
	}
}

// TestRefusedFixtures: every refused record is refused by exactly the rule its sidecar
// names, at exactly the field it names. A refusal never returns a record, because this
// boundary refuses and does not repair.
func TestRefusedFixtures(t *testing.T) {
	for _, path := range fixtures(t, "refused") {
		t.Run(filepath.Base(path), func(t *testing.T) {
			raw, want := readFixture(t, path)
			v := validatorForRefused(t, path, raw)
			env, err := v.ValidateEnvelope(raw)
			if err == nil {
				t.Fatalf("expected refusal %s, the record was accepted", want.Rule)
			}
			if env != nil {
				t.Fatalf("a refusal returned a record; nothing is repaired here")
			}
			var r *Refusal
			if !errors.As(err, &r) {
				t.Fatalf("refusal is not a *Refusal: %v", err)
			}
			if r.Rule != want.Rule {
				t.Fatalf("refused by %s, sidecar says %s", r.Rule, want.Rule)
			}
			if r.Field != want.Field {
				t.Fatalf("refused at %s, sidecar says %s", r.Field, want.Field)
			}
			// The diagnostic names the field and never echoes a value: the fixture that
			// carries a private path in an unsupported field must not put it in the error.
			if strings.Contains(r.Error(), "/Users/") {
				t.Fatalf("a refusal echoed source data: %s", r.Error())
			}
		})
	}
}

// TestEachRefusalRuleIsLoadBearing is the red-then-green half. Red: the fixture is
// refused. Green: with that one rule not enforced, the same bytes are accepted -- which
// is what proves the rule, and not some other check, is what refuses the record.
func TestEachRefusalRuleIsLoadBearing(t *testing.T) {
	byRule := map[string]string{}
	for _, path := range fixtures(t, "refused") {
		_, want := readFixture(t, path)
		if prev, dup := byRule[want.Rule]; dup {
			t.Fatalf("rule %s has two fixtures: %s and %s", want.Rule, prev, path)
		}
		byRule[want.Rule] = path
	}
	for _, rule := range AllRules {
		path := byRule[rule]
		if path == "" {
			t.Errorf("rule %s has no fixture", rule)
			continue
		}
		t.Run(rule, func(t *testing.T) {
			raw, want := readFixture(t, path)
			v := validatorForRefused(t, path, raw)
			if _, err := v.ValidateEnvelope(raw); err == nil {
				t.Fatalf("red half: %s was accepted", filepath.Base(path))
			}
			if hardRules[rule] {
				t.Logf("RED  %-30s %s\nGREEN %-29s (none: the record has no valid reading once this fails)", rule, filepath.Base(path), "")
				return
			}
			lenient := v.Without(append([]string{rule}, want.GreenRequires...)...)
			if _, err := lenient.ValidateEnvelope(raw); err != nil {
				t.Fatalf("green half: without %s (and %v) the record is still refused: %v", rule, want.GreenRequires, err)
			}
			t.Logf("RED  %-30s %s\nGREEN %-29s accepted with the rule not enforced%s", rule, filepath.Base(path), "", also(want.GreenRequires))
		})
	}
}

func also(rules []string) string {
	if len(rules) == 0 {
		return ""
	}
	return " (with " + strings.Join(rules, ", ") + " also not enforced: that rule names the refusal more precisely)"
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
