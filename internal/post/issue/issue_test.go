package issue

import (
	"testing"
)

func TestParseSection_WithResult(t *testing.T) {
	raw := `RESULT test-card sha=abcd
KIND: fix
SCHEMA: v2`

	sec, err := ParseSection(raw)
	if err != nil {
		t.Fatalf("ParseSection failed: %v", err)
	}

	if sec.Field("RESULT") != "test-card sha=abcd" {
		t.Errorf("RESULT field = %q, want %q", sec.Field("RESULT"), "test-card sha=abcd")
	}
	if sec.Field("KIND") != "fix" {
		t.Errorf("KIND field = %q, want %q", sec.Field("KIND"), "fix")
	}
	if sec.Field("SCHEMA") != "v2" {
		t.Errorf("SCHEMA field = %q, want %q", sec.Field("SCHEMA"), "v2")
	}
}

func TestParseSection_Validate(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr bool
		errMsg  string
	}{
		{
			name: "all required fields",
			raw: `RESULT test sha=1234
KIND: fix
SCHEMA: v2`,
			wantErr: false,
		},
		{
			name: "missing RESULT",
			raw: `KIND: fix
SCHEMA: v2`,
			wantErr: true,
			errMsg:  "missing-required-field",
		},
		{
			name: "missing KIND",
			raw: `RESULT test
SCHEMA: v2`,
			wantErr: true,
			errMsg:  "missing-required-field",
		},
		{
			name: "missing SCHEMA",
			raw: `RESULT test
KIND: fix`,
			wantErr: true,
			errMsg:  "missing-required-field",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sec, err := ParseSection(tt.raw)
			if err != nil {
				t.Fatalf("ParseSection failed: %v", err)
			}

			err = sec.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && tt.errMsg != "" && !contains(err.Error(), tt.errMsg) {
				t.Errorf("Validate error %q does not contain %q", err.Error(), tt.errMsg)
			}
		})
	}
}

func TestSection_Body(t *testing.T) {
	raw := `RESULT test sha=1234
KIND: fix
SCHEMA: v2

This is the body
with multiple lines`

	sec, _ := ParseSection(raw)
	body := sec.Body()

	if body != raw {
		t.Errorf("Body() returned modified text:\ngot:\n%q\nwant:\n%q", body, raw)
	}
}

func TestSection_Title(t *testing.T) {
	raw := `RESULT my-test-card sha=abcd123
KIND: fix
SCHEMA: v2`

	sec, _ := ParseSection(raw)
	title := sec.Title()

	if title != "my-test-card sha=abcd123" {
		t.Errorf("Title() = %q, want %q", title, "my-test-card sha=abcd123")
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
