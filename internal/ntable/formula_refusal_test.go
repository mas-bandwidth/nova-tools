package ntable

import "testing"

// TestFormulaRefusalRemedies keeps each raw formula refusal paired with its
// specific recovery command.
func TestFormulaRefusalRemedies(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		wire []any
		want string
	}{
		{"missing source", []any{"REFUSED", "FORMULA", "okpct", "nope", "missing"}, "nova-table col add 'fleet' 'nope'"},
		{"retyped source", []any{"REFUSED", "FORMULA", "okpct", "note", "text"}, "nova-table show 'fleet'"},
		{"delete source with dependent formula", []any{"REFUSED", "DEPENDS", "ok", "okpct"}, "nova-table col del 'fleet' 'okpct'"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := (operation{table: "fleet"}).refused(tc.wire)
			r, ok := err.(*Refusal)
			if !ok {
				t.Fatalf("raw refusal: %T %v", err, err)
			}
			if r.Next != tc.want {
				t.Errorf("next = %q, want %q", r.Next, tc.want)
			}
		})
	}
}
