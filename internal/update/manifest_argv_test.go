package update

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestArgv(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		input   string
		want    []string
		wantErr bool
	}{
		{"simple", "nova-update", []string{"nova-update"}, false},
		{"two args", "go version", []string{"go", "version"}, false},
		{"double spaces", "go  version", nil, true},
		{"tab", "go\tversion", nil, true},
		{"newline", "go\nversion", nil, true},
		{"empty", "", nil, true},
		{"json simple", `json:["go","version"]`, []string{"go", "version"}, false},
		{"json space in path", `json:["/Applications/My App/bin/x","--version"]`, []string{"/Applications/My App/bin/x", "--version"}, false},
		{"json empty", `json:[]`, []string{}, false},
		{"json single element", `json:["go"]`, []string{"go"}, false},
		{"json escaped quote", `json:["file\"name"]`, []string{"file\"name"}, false},
		{"json unclosed quote", `json:["go`, nil, true},
		{"json bad prefix", `json(go)`, nil, true},
		{"json trailing text", `json:["go"]x`, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := argv(tt.input)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}
