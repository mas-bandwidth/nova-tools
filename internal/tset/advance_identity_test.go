package tset

import (
	"errors"
	"testing"
)

func TestAdvanceRequiresStableIdentityGo(t *testing.T) {
	t.Parallel()
	request := func(entries ...Entry) Step {
		return Step{Epoch: "0", Space: "s", Entries: entries}
	}
	wantRequest := func(t *testing.T, label string, err error) {
		t.Helper()
		var refusal *Refusal
		if !errors.As(err, &refusal) || refusal.Code != "REQUEST" {
			t.Errorf("%s: got %v, want REQUEST", label, err)
		}
	}
	for _, tc := range []struct {
		name    string
		entries []Entry
	}{
		{"generic", []Entry{{Kind: "advance", AdvanceFrom: "0"}}},
		{"guarded", []Entry{{Kind: "rowset", Table: "work", Rows: []RowRank{}}, {Kind: "advance", AdvanceFrom: "0"}}},
		{"restoring", []Entry{{Kind: "advance", AdvanceFrom: "0"}, {Kind: "rows", Table: "work", Add: []string{"r"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			step := request(tc.entries...)
			wantRequest(t, "ValidateStep", ValidateStep(step))
			_, err := EncodeStep(step)
			wantRequest(t, "EncodeStep", err)
			_, err = encodeStep(step, true) // builder intermediate cannot erase advance identity
			wantRequest(t, "builder encode", err)
			// The raw decoder is a separate public admission path.
			var raw []byte
			switch tc.name {
			case "generic":
				raw = []byte(`{"epoch":"0","space":"s","entries":[{"kind":"advance","from":"0"}]}`)
			case "guarded":
				raw = []byte(`{"epoch":"0","space":"s","entries":[{"kind":"rowset","t":"work","rows":[]},{"kind":"advance","from":"0"}]}`)
			case "restoring":
				raw = []byte(`{"epoch":"0","space":"s","entries":[{"kind":"advance","from":"0"},{"kind":"rows","t":"work","add":["r"]}]}`)
			}
			_, err = DecodeStep(raw)
			wantRequest(t, "DecodeStep", err)
			op, intent := "clear-0", "stable clear intent"
			step.Op, step.Intent = &op, &intent
			wire, err := EncodeStep(step)
			if err != nil {
				t.Fatalf("named advance encode: %v", err)
			}
			if _, err := DecodeStep(wire); err != nil {
				t.Fatalf("named advance decode: %v", err)
			}
		})
	}
	if _, err := EncodeStep(request(Entry{Kind: "rows", Table: "work", Add: []string{"r"}})); err != nil {
		t.Fatalf("ordinary anonymous rows step lost admission: %v", err)
	}
}
